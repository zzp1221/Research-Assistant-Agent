# 并发幂等、锁恢复与 PDF 翻译浮层修复方案

状态：已实施并完成本轮验收。版本：1.1。日期：2026-11-06。

本方案覆盖三项已复现的问题：计划创建的并发幂等失效、陈旧锁清理失败导致卡死，以及 PDF 划词翻译浮层越出阅读区域。第 2 至 4 章的根因描述保留修复前状态；实际实现、逐项验收结果及保证范围见[修复验收报告](defect-remediation-2026-11-06-results.md)。

## 1. 范围与实施顺序

| 顺序 | 问题 | 优先级 | 修复目标 |
|---|---|---|---|
| 1 | 并发幂等 | P1 | 同一用户、同一键、同一请求在处理期间只创建一个计划，后续请求复用结果 |
| 2 | 锁恢复卡死 | P1 | 陈旧锁清理失败时明确报错，所有重试路径均受超时约束 |
| 3 | PDF 翻译浮层越界 | P2 | 浮层完整落在可见阅读区域内，并在缩放、滚动和布局改变后更新 |

三个修复可分别提交，每个提交包含对应的回归用例。本轮不处理产物下载授权、预算路由、文献检索等其他审查问题。

## 2. 并发幂等修复

### 2.1 已复现的行为与根因

对运行服务先调用 `/api/me` 取得稳定身份 Cookie，再并发发送两次相同的 `POST /api/plan`，请求体和 `Idempotency-Key` 完全相同。两次均返回 200，但 `plan_graph.id` 不同。

现有代码位置：

- [idempotency.go](../research-assistant/backend/internal/api/idempotency.go)：只有已完成响应的 Lookup/Store，各自持锁。
- [plan_runtime.go](../research-assistant/backend/internal/api/plan_runtime.go)：`createPlan` 先限流、Lookup，再调用规划器和保存计划，最后 Store。
- [hardening_test.go](../research-assistant/backend/internal/api/hardening_test.go)：现有幂等用例只串行发送两次请求。
- [researchAssistantApi.ts](../research-assistant/frontend/src/services/api/researchAssistantApi.ts)：每次调用 `createPlan` 都重新生成 key。

当前竞态如下：

```text
请求 A：Lookup 未命中 ── 生成并保存计划 A ── Store
请求 B：Lookup 未命中 ── 生成并保存计划 B ── Store
```

map 的读写互斥保证了内存访问安全，没有保证“检查到创建完成”这一整段业务操作唯一。`go test -race` 本身也不能证明业务幂等成立。

另一个已复现的问题是限流位于 Lookup 前：即使成功响应已经缓存，同键重放仍会消耗创建额度，额度不足时返回 429。

### 2.2 请求契约

| 场景 | 目标行为 |
|---|---|
| 未提供 key | 保持现有创建语义，执行正常限流 |
| 首次提供合法 key | 原子取得该用户/key 的执行权，再检查创建额度并运行规划 |
| 同用户、同 key、同指纹，首请求处理中 | 等待该执行的结果，等待者不调用规划器、不消耗创建额度 |
| 同用户、同 key、同指纹，已有成功响应 | 返回首次成功响应的状态码、Content-Type 和响应体，增加 `Idempotency-Replayed: true` |
| 同用户、同 key、不同指纹 | 返回 409，`code=idempotency_key_conflict`，不执行创建 |
| 不同用户使用相同 key | 相互独立，不能读取或复用另一用户的响应 |
| 等待超过 30 秒，首请求仍运行 | 返回 503，`code=idempotency_in_progress`、`Retry-After: 1`，客户端保留原 key 重试 |
| 等待者断开连接 | 结束该等待，不取消首请求，不清除其占位 |
| 已知尚未保存计划的失败 | 本批等待者收到同一次失败；首请求退出后释放占位，后续请求可用原 key 重试 |
| 保存结果不确定 | 返回 503，`code=idempotency_commit_unknown`，保留占位和已知 plan ID，核对完成前不得重新创建 |

身份来自服务端签名 Cookie。并发复现和自动化验收必须先建立身份，不能让每个并发请求分别获得新的匿名 owner。

以上保证针对当前单后端进程。成功记录沿用 24 小时 TTL；缓存命中返回首次创建的快照，计划最新状态仍通过计划详情接口读取。多副本和进程重启后的重试需要持久化唯一约束，见 2.7。

### 2.3 增加“处理中”状态与原子占位

将缓存条目扩展为 `in_progress`、`succeeded`、`commit_unknown`。条目至少包含请求指纹、创建时间、完成时间、完成通知 channel、结果、已知 plan ID；占位返回一个只属于该次执行的 handle。

建议的内部职责如下，命名可随实现调整：

| 操作 | 职责 |
|---|---|
| `Begin(owner, key, fingerprint)` | 在同一临界区内检查已有条目并创建占位；返回执行权、等待句柄、成功结果或冲突 |
| `Wait(ctx, handle)` | 等待首请求或本次请求取消；不能持有缓存总锁等待 |
| `Complete(handle, response)` | 原子发布不可变成功结果，通知所有等待者；成功 TTL 从完成时间计算 |
| `Fail(handle, errorResponse)` | 向已有等待者发布本次失败，再移除可重试条目 |
| `MarkCommitUnknown(handle, planID)` | 保留不确定状态并通知等待者，禁止将其当作缓存未命中 |

网络请求、LLM、存储调用以及 channel 等待都在临界区外运行；不同 key 的创建不能被一个慢请求串行阻塞。完成操作要核验 handle 对应的条目仍然有效，结果与通知只能发布一次。

容量管理也要调整：现有“达到 4096 条后清空整张 map”的逻辑不能保留。先清理过期成功条目；处理中和结果不确定的条目不可因 TTL 或容量被淘汰。容量仍不足时拒绝新 key，返回 503 `idempotency_capacity_exceeded`，已有 key 的等待和重放仍可用。不要为了释放空间重新开放尚在执行的 key。

### 2.4 指纹与 handler 执行顺序

指纹使用 `SHA-256`，输入为版本化、规范序列化后的创建请求。范围包括 intent、session/project、attachments、运行权限模式及 acknowledgement、四类预算限制、budget/cost policy、require_review。JSON 空白和对象键顺序不影响指纹；附件数组保持顺序。只做与实际执行完全一致的默认值和空白规范化，不能让指纹视为相同的两个请求产生不同的执行参数。

对省略的 `session_id`，指纹保留“未显式指定”的稳定表示；首请求解析出的有效 session 固定在该次执行中，重放时复用首次响应，不因当前 Cookie 切换会话而重新创建。前端正常请求应继续显式提供 session ID。

建议调整为以下顺序：

1. 鉴权、请求体大小限制、JSON 解析和无副作用的字段校验，解析 owner；校验 key 长度。
2. 生成指纹并 `Begin`。命中、等待或冲突直接走对应分支。
3. 仅取得执行权的请求消耗创建限流额度，检查 active plan 配额并校验/确保 session。
4. 解析附件和上下文，调用规划器，完成治理与初始 revision。
5. 预先构造并序列化成功响应，避免计划保存成功后才发现响应无法编码。
6. 保存计划。确认成功后发布幂等成功结果，再返回 HTTP 响应。
7. `plan_created` 事件、记忆写入与审计等原有步骤只由首请求执行；等待者和重放请求不重复执行。

保留入口层的通用流量保护；这里跳过的只是“创建新计划”的额度。无 key 请求也继续受原有限流约束。

### 2.5 失败与取消处理

不能在所有错误路径上简单 `delete(key)`：

- 首请求取消且计划尚未保存时，先确认创建工作已停止，再向等待者发布取消结果并释放占位；等待者不能自行接管仍在运行的工作。
- 已成功保存，即使首个浏览器断开，也必须保留成功结果，后续同键请求取得原计划。
- `SavePlan` 返回错误并不必然意味着完全没有变化；现有 FilePlanStore 在部分步骤中先更新内存，再写快照/日志。因此一旦进入保存阶段后结果不明，保守进入 `commit_unknown`，携带已知 plan ID 供核对，禁止自动重跑。
- panic 恢复必须唤醒等待者，并根据是否已经进入保存阶段发布失败或结果不确定，不能留下无人执行的永久等待条目。
- 当前失败批次的所有等待者收到同一次结果，避免失败后每个等待者依次成为新执行者、放大重试。

### 2.6 前端重试与修改文件

在聊天流程层为一次逻辑提交创建 key，连同请求参数保存为待确认提交；API 函数接受该 key。超时或 `idempotency_in_progress` 后重新查询/重试时沿用它。新任务、修改过的输入、切换后的新会话提交使用新 key。收到 `commit_unknown` 时提示查询原计划，不自动生成新 key 重试。

| 文件 | 预期修改 |
|---|---|
| `backend/internal/api/idempotency.go` | 原子占位、等待通知、指纹冲突、完成/失败状态、容量与过期策略 |
| `backend/internal/api/plan_runtime.go` | 调整顺序、区分执行者与等待者、处理保存与取消边界 |
| `backend/internal/api/openapi.json` | 补充重放头、409 与 503 错误语义 |
| `frontend/src/services/api/researchAssistantApi.ts` | 接受并发送调用方提供的 key，兼容现有调用方式 |
| `frontend/src/app/hooks/useResearchAssistantChatFlow.ts` | 保存一次提交的 key 与原参数，明确重试/新建边界 |
| 新增 `backend/internal/api/idempotency_test.go`，扩充 `hardening_test.go` | 状态机和 handler 的并发、失败、限流测试 |

表内路径均相对 `research-assistant/`。

### 2.7 保证范围与后续持久化

本次以单进程内的原子占位解决已复现的并发重复创建。仅内存缓存无法保证进程重启后的幂等，也不能协调两个后端副本；文档、接口说明和前端提示必须保留这个边界。

若需要在重启后继续自动重试，后续应在持久化层增加 `(owner_id, key)` 唯一记录，关联 fingerprint、plan ID、响应和完成状态，并保证记录与计划提交具有可恢复的一致性。启动时先核对未完成记录，不能只丢弃记录后重新规划。该能力单独验收，不能以本次进程内测试通过代替。

### 2.8 回归用例与验收

| ID | 场景 | 必须满足 |
|---|---|---|
| I-01 | 同 owner/key/请求同时发起 16 次，首请求在测试屏障处暂停 | 规划调用一次、计划保存一次、只有一个 plan ID，全部成功响应体一致 |
| I-02 | 首请求已完成，创建限流额度已用尽 | 原 key 仍返回 200，带重放头；新 key 返回 429 |
| I-03 | 处理中和已完成时分别用同 key 修改 intent/附件/权限/预算 | 都返回 409；原任务不受影响 |
| I-04 | 不同 owner 使用同 key；同 owner 使用不同 key | 不跨用户重放；独立 key 可并发推进 |
| I-05 | 一个等待者取消，其余请求继续 | 执行者不中断，其余等待者仍取得同一计划 |
| I-06 | 规划在保存前失败 | 本批等待者收到相同失败；后续同键重试可成功，无条目泄漏 |
| I-07 | 已保存但响应连接断开 | 后续重放取得原 ID，保存计数仍为一 |
| I-08 | 保存结果不确定、panic、等待超时 | 等待者及时结束；结果不确定时不重新调用规划器 |
| I-09 | TTL 到期、缓存满、慢创建期间触发清理 | 只清理可过期成功条目；不能丢失执行占位 |
| I-10 | UI 首次提交超时后重试；修改输入后新建 | 重试 key 不变，新建 key 不同，请求参数与对应 key 一致 |

并发测试使用 barrier/channel 控制交错，不依赖固定 sleep 碰撞。I-01 既断言响应，也检查存储数量和副作用次数；验收同时运行 `-race`。

## 3. 锁恢复卡死修复

### 3.1 已复现的行为与根因

[lockfile.go](../research-assistant/backend/internal/lockfile/lockfile.go) 的 `Acquire` 遇到陈旧锁后执行：

```go
if stale(path) {
    _ = os.Remove(path)
    continue
}
```

只读卷或目录权限使删除失败时，下一轮仍看到相同陈旧锁，再次进入该分支；deadline 检查位于分支后，始终无法执行。这既导致无法返回，也产生无退避的循环。

独立诊断中 `Acquire(path, 40*time.Millisecond)` 超过 250ms 仍未返回。本问题影响 [memory/store.go](../research-assistant/backend/internal/memory/store.go) 与 [wiki/store.go](../research-assistant/backend/internal/wiki/store.go) 等调用方，通常使用 3 秒 timeout。

### 3.2 修复算法

保留现有 `Acquire(path, timeout) (*os.File, error)` 签名。非正 timeout 定义为“只尝试一次创建，不等待、不清理重试”；空闲路径仍可成功，已有锁立即返回获取失败。

正 timeout 的处理顺序：

1. 使用带单调时钟的 deadline，每次循环开始检查是否超时；允许第一次创建尝试。
2. 以现有独占创建方式尝试创建锁文件。创建成功后写入 PID/时间，检查写入结果；写入失败应关闭并清理本次创建的文件，返回错误。
3. 若不是“文件已存在”，直接返回包含操作、路径和原始 cause 的错误。
4. 若文件已存在，检查是否陈旧。仅确认原进程不在运行时允许尝试清理。
5. 删除成功或得到 `os.ErrNotExist`，重新进入循环，下一轮仍检查 deadline。
6. 删除返回权限不足、只读文件系统或其他错误，立即返回清理失败，保留原始 cause；不要忽略错误继续循环。
7. 非陈旧锁保持有界等待。休眠取 `min(25ms, remaining)`，超时后返回获取超时，不能把每轮等待延伸到 deadline 之后。

无法读取、内容损坏、PID 存活状态不确定时，按未确认陈旧处理并有界等待，不直接删锁。调用方不得因恢复失败强行删锁或悄悄建立第二份持久化实例。

超时约束覆盖应用层的重试与退避；它不承诺中断一个由内核或网络文件系统阻塞的同步文件操作。

### 3.3 错误与并发边界

错误应能区分“锁被持有并超时”“无法清理陈旧锁”“无法创建锁”“写锁信息失败”。使用 `%w` 保留底层错误，测试可以通过 `errors.Is` 判断权限等原因；日志携带路径、等待耗时和清理结果。

不能通过“取消所有重试”解决并发恢复：另一进程可能刚好删除陈旧锁，当前删除返回 `ErrNotExist` 时应继续竞争。也不能因为 PID 检测权限受限就当作进程已死亡。

本次维持现有 PID 文件锁协议。多个进程同时回收同一陈旧路径时，文件身份检查与删除之间仍有竞态；本修复不把该协议升级为跨进程原子锁。若部署要求严格的多进程持久化互斥，后续应采用平台文件锁或等价协议，并单独测试恢复竞争。

### 3.4 修改文件与测试方法

| 文件 | 预期修改 |
|---|---|
| `backend/internal/lockfile/lockfile.go` | 循环入口 deadline、检查删除/写入错误、限制退避、明确非正 timeout |
| 新增 `backend/internal/lockfile/lockfile_test.go` | 获取成功、存活/陈旧锁、删除失败、边界时间和清理竞争 |
| `backend/internal/memory/*_test.go`、`backend/internal/wiki/*_test.go` | 检查错误向上传递，不能出现无限初始化或错误的成功结果 |

表内路径均相对 `research-assistant/`。如需模拟文件系统错误，使用包内的最小依赖注入，不引入可被并行测试随意改写的全局函数变量。

不能仅靠 `chmod` 模拟只读：容器内 root 可能绕过权限。普通单测注入删除失败；Linux 集成测试使用只读挂载，并在可写目录中放置控制/结果文件；Windows 使用适合的平台用例，不强行套用 POSIX 权限。

### 3.5 回归用例与验收

| ID | 场景 | 必须满足 |
|---|---|---|
| L-01 | 路径空闲 | 成功，锁内容完整，正常释放后可再次获取 |
| L-02 | 锁由存活进程持有，timeout=100ms | 返回超时；不删除锁；允许测试调度余量，总时长不超过 500ms |
| L-03 | 锁的 PID 已退出且允许删除 | 清理后取得锁，写入当前 PID |
| L-04 | 陈旧锁删除返回 EACCES/EROFS | 立即返回带 cause 的错误；删除调用一次，不进入忙循环 |
| L-05 | 原始只读挂载场景，timeout=40ms | 500ms 内返回明确错误，不残留后台重试 |
| L-06 | 清理时另一进程先删掉路径 | `ErrNotExist` 可重试，最终成功或在 deadline 内失败 |
| L-07 | 锁不可读、内容损坏、PID 状态无法确认 | 不擅自删锁，返回明确错误或有界超时 |
| L-08 | 锁信息写入失败；timeout 为零或负数 | 不报告成功；本次创建的资源被清理；非正 timeout 不等待 |
| L-09 | memory/wiki 初始化遇到恢复失败 | 在既定获取期限与测试余量内结束，沿用各自的失败/降级契约 |

时间断言用宽松上界防止调度抖动；“没有忙循环”主要通过注入函数的调用次数验证，不能仅观察 CPU 截图。

## 4. PDF 翻译浮层越界修复

### 4.1 已复现的行为与根因

现有 [pdf.spec.ts](../research-assistant/frontend/test/e2e/pdf.spec.ts) 已包含复现：1024×500 窗口，上传本地测试 PDF，选中文字翻译，随后连续缩小两次。实测浮层下边缘约 y=488，而阅读区域下边缘约 y=440，超出约 48px。

[AITranslationPopover.tsx](../research-assistant/frontend/src/hooks/AITranslationPopover.tsx) 当前按 `window.innerHeight` 计算上下空间，通过 Portal 挂载到 `document.body`。页面可视区域比 PDF 正文区大，因此“在窗口里”不能保证“在阅读器里”。另外，宽度至少 160px、高度至少 120px 的强制下限在更小的阅读区域也会造成越界。

现有 ResizeObserver 只观察 PDF 页层；打开全文翻译面板后，[PdfPanel.tsx](../research-assistant/frontend/src/features/pdf-viewer/PdfPanel.tsx) 将阅读器高度改为 55%，页层尺寸不一定随容器一起变化。

### 4.2 明确阅读区域与坐标系

在 PdfPanel 包裹 Viewer 的容器添加稳定 ref/标记。由 [PdfViewerSurface.tsx](../research-assistant/frontend/src/features/pdf-viewer/PdfViewerSurface.tsx) 将同一阅读器的范围解析能力传给 [useAITranslationPlugin.tsx](../research-assistant/frontend/src/hooks/useAITranslationPlugin.tsx)，再交给浮层。

实际核对发现，当前插件 3.12 的 `.rpv-default-layout__body` 包含绝对定位的工具栏，并用 `padding-top: 2.5rem` 为工具栏留位，其外接矩形仍覆盖工具栏。因此定位同时排除工具栏、正文 padding，并与 `.rpv-core__inner-pages` 的真实滚动视口取交集。所有查询限定在已确定的阅读器容器内；避免从 `document` 全局选择第一个阅读器或第一个同页码节点。

保留 `position: fixed` 和 Portal；统一使用 viewport 坐标中的 `getBoundingClientRect()` 计算位置，不混入页面 scrollTop。按以下顺序计算：

1. 得到当前阅读器的正文 rect、外层可见裁剪范围及浏览器可视窗口 rect；有 `visualViewport` 时纳入其 offset 和尺寸。
2. 取这些 rect 的交集作为可见正文区域 `R`。全文翻译面板占用的下半区与工具栏不得被纳入。
3. 在 R 内预留 8px 安全边距；空间过小时允许缩小边距，不能让边距本身产生负宽高。
4. 选区的百分比坐标继续相对当前 PDF page rect 转换。选区或页层已离开正文可视区域时隐藏浮层，保留已有翻译结果。

### 4.3 定位与高度计算

以扣除安全边距后的正文区域记为 `B`，选区记为 `S`：

```text
width = min(360px, B.width)
below = max(0, B.bottom - S.bottom - gap)
above = max(0, S.top - gap - B.top)
gap = 8px
```

优先使用能容纳期望内容的下方；下方不足而上方更合适则置于上方。两侧均不足时，将浮层收进 B 内，允许覆盖部分选区，内容内部滚动。期望最小高度只能影响位置选择，不能迫使浮层大于正文区域。

先根据内容与可用空间得到实际 width/height，再计算：

```text
left = clamp(S.left, B.left, B.right - width)
top = clamp(preferredTop, B.top, B.bottom - height)
```

渲染后测量真实 rect；翻译从 loading 切换为结果、错误或长文本时重新测量并校正。`maxHeight` 只是约束，不能拿它替代真实高度计算上方定位。

标题与关闭按钮保持可见，正文使用 `min-height: 0` 与内部滚动，避免页脚按钮挤占正文后把整层撑大。若可见区域连标题与关闭控件都放不下，则暂不显示浮层，保留选区/结果，区域恢复后重新计算；不要扩大到阅读区外，也不设置超出可用尺寸的硬最小宽高。

### 4.4 布局变化与生命周期

定位更新需要覆盖：

- 阅读器正文、外层容器、当前 page layer 和浮层自身的 ResizeObserver。
- 浏览器 resize、捕获阶段 scroll，以及可用时的 visualViewport resize/scroll。
- PDF 缩放、页层重新挂载、选区改变，以及全文翻译面板展开/收起。
- 阅读器隐藏、换文档或卸载：取消监听和待执行的 animation frame，隐藏旧浮层。

用 requestAnimationFrame 合并同一帧的布局读取，比较新旧位置后再 setState，避免 ResizeObserver/渲染相互触发。不要只监听页层大小；侧栏拖动造成的容器位移也必须更新。

### 4.5 修改文件

| 文件 | 预期修改 |
|---|---|
| `frontend/src/features/pdf-viewer/PdfPanel.tsx` | 标记实际阅读器容器，传入/挂载 ref，保证正文区域可识别 |
| `frontend/src/features/pdf-viewer/PdfViewerSurface.tsx` | 连接阅读器范围与翻译插件 |
| `frontend/src/hooks/useAITranslationPlugin.tsx` | 传递当前阅读器引用和选区生命周期 |
| `frontend/src/hooks/AITranslationPopover.tsx` | 正文与 viewport 交集、位置/尺寸约束、重测及监听清理 |
| 建议新增 `frontend/src/hooks/translationPopoverLayout.ts` | 将纯几何计算与 DOM 读取分离，便于边界测试 |
| `frontend/test/e2e/pdf.spec.ts` 与相应单元/组件测试 | 保留原失败断言，补充布局、长内容与多阅读器场景 |

表内路径均相对 `research-assistant/`。`translationPopoverLayout.ts` 是建议新增文件，当前尚不存在。

### 4.6 回归用例与验收

| ID | 场景 | 必须满足 |
|---|---|---|
| P-01 | 原始 1024×500、长译文、缩小两次 | 原用例通过；浮层四边均位于可见正文区域内 |
| P-02 | 放大、缩小、阅读器滚动、改变窗口大小 | 定位随布局变化更新，不覆盖缩放/翻页工具栏 |
| P-03 | 打开/关闭全文翻译面板、拖动工作台分隔线 | 使用改变后的阅读区域，不占用全文翻译区或对话区 |
| P-04 | 正文宽度小于 160px、高度小于 120px | 不发生负尺寸或越界；必要时隐藏浮层并保留结果 |
| P-05 | 短结果、长结果、loading、错误及追问按钮组合 | 内容变化后仍受约束；关闭按钮可用，长内容可滚动 |
| P-06 | 选区靠近四角或滚动出屏幕 | 自动选择合理位置；离屏时不把浮层钉在无关区域 |
| P-07 | 两个阅读器或换文档后的相同 pageIndex | 只使用触发操作的阅读器和页层，不定位到另一个 PDF |
| P-08 | 390×844、1440×900；模拟 visualViewport 缩小 | 无水平溢出，浮层保持在两类可视范围交集内 |
| P-09 | 反复打开、关闭、换文档和卸载 | 无遗留监听、重复观察或卸载后状态更新 |

几何算法用单元测试覆盖极小区域和坐标计算；实际字体、flex 收缩、Portal、PDF 缩放必须由浏览器验证。E2E 使用 `expect.poll` 等待布局稳定后比较 boundingBox，四边允许 1px 舍入误差，不能放宽为“只要还在窗口内”。

## 5. 验证命令与交付标准

以下为实施后执行的命令，不是本次文档编写的执行结果。

后端在 `research-assistant/backend/` 使用 Go 1.27.3：

```sh
go test -count=1 ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki
go test -race -count=1 ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki
go vet ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki
```

在已启动的测试 Compose 服务上，使用稳定身份、同一 key 并发请求计划接口，验证 I-01/I-02；请求开启 `require_review`，不自动运行实验，完成后删除本轮测试计划。Linux 只读挂载验收使用临时锁文件，不操作正在使用的 memory/wiki 数据目录。Windows 另行验证锁获取与恢复。

前端在 `research-assistant/frontend/`：

```sh
npm test
npm run lint
npm run build
npx playwright test test/e2e/pdf.spec.ts
npm run test:a11y
```

浏览器默认目标为已启动的 `http://127.0.0.1:15173`；需要访问别处时显式设置 `E2E_BASE_URL`。本地 Windows 可按现有配置设置 `PLAYWRIGHT_CHROME_PATH`。UI 验收必须使用包含本次修复的前端构建，不能只测试旧容器中的页面。

交付须同时满足：

- I/L/P 三组用例有可追溯的结果；失败时附原因和复现材料，不能将未执行标为通过。
- I-01 在线验证只产生一个计划；L-05 在期限内返回；P-01 保留原正文边界断言并通过。
- 新的回归测试随代码进入版本库；临时诊断中的“PASS 表示复现缺陷”用例不能直接当作修复后门禁。
- 成功重放、冲突、等待超时、保存不确定和锁清理失败均有可区分的日志；避免在日志中写入完整研究请求、Cookie 或 token。
- PR 说明记录保证范围、验证环境、前后行为及剩余限制；完成前保持本文件状态为“待实施/验收中”。

## 6. 发布与回退

三个改动分别发布和验证；上线幂等修复前先排空正在创建的请求，避免部署重启直接切断处理中占位。当前内存记录不随进程保留，升级窗口内的结果不确定请求应先查询计划库，不能自动视为未创建。

锁修复不改变已有文件格式，回退无需迁移，但回退会恢复陈旧锁删除失败时的卡死风险。PDF 修复不改变翻译 API 或文档格式，回退仅涉及前端构建。

若实现过程中决定增加持久化幂等记录、改变文件锁协议或引入新的浮层依赖，应先在本文件追加迁移、兼容与恢复方案，并补相应验收用例。
