# 2026-11-06 缺陷修复验收报告

状态：已完成。对应[修复方案](defect-remediation-2026-11-06.md)。

本轮完成并发幂等、陈旧锁恢复和 PDF 翻译浮层三项修复。验收使用当前工作树构建的后端和前端，未执行实验，在线创建的测试计划已通过删除接口清理。

## 实现与实际差异

计划创建改为 owner/key 原子占位，使用版本化请求的 SHA-256 指纹。等待、重放和冲突在创建限流前处理；只有执行者调用规划器、创建 revision、保存计划和发出创建事件。成功响应在保存前编码、保存后发布，保存异常或保存期间 panic 保留 `commit_unknown` 与原 plan ID；保存前失败向已加入的等待者发布同一结果，并释放该次占位。成功 TTL 从完成时计算，容量不足只拒绝新 key，不淘汰执行中或结果不确定的条目。

聊天流程保存待确认提交的原参数和 key。超时、网络错误、处理中响应后，同一输入可以复用原提交；修改输入、权限、附件或新会话会产生新 key。保存结果不确定时提示查询原计划，不自动重建。OpenAPI 已补齐重放头、409/503、预算与 review 字段和单进程保证边界。

文件锁在每轮入口检查期限，非正 timeout 只尝试创建一次；检查写入结果，清理失败立即返回包装后的原始错误。Windows 进程查询权限不足或 Unix 进程存活状态不明均按可能存活处理。恢复竞争中的 `ErrNotExist` 允许重试，所有应用层重试受期限约束。

PDF 使用当前阅读器 ref，在正文、外层裁剪与可视窗口的交集中计算固定浮层。实际 DOM 与原方案描述不同：`.rpv-default-layout__body` 包含工具栏，不能单独作为正文范围；实现进一步扣除工具栏与 padding，并与真实滚动视口相交。浏览器回归曾捕获工具栏遮挡，修正后保留了相应断言。

浮层观察阅读器、祖先容器、当前页和自身的尺寸，监听滚动、resize、visualViewport 和页层替换，通过 animation frame 合并更新。选区离屏或空间不足时隐藏但保留结果；关闭、换文档和卸载清理监听与待执行 frame，并屏蔽过期异步结果。标题、关闭和追问控件不被正文挤压，长内容在浮层内部滚动。隐藏时继续按阅读器宽度测量，避免换行控件导致显示/隐藏反复切换。

## 环境与执行结果

- Windows amd64；Go 1.27.3；Node 24；本机 Chrome，由 `PLAYWRIGHT_CHROME_PATH` 指定。
- Linux 验收使用 `golang:1.27.3-bookworm` 和真实只读 bind mount。锁文件在本轮临时目录内，未接触正在使用的 memory/wiki 数据。
- Compose 项目为 `remediation20261106`，临时数据位于 `tmp/remediation-20261106/data`。前端为当前 `dist` 的只读挂载，后端为当前源码交叉编译的 Linux 二进制。
- 本机 8080 被其他服务占用，验收改用后端 18081、前端 15174；浏览器明确设置 `E2E_BASE_URL=http://127.0.0.1:15174`。
- 最终合并执行 PDF 与无障碍用例，12 项全部通过。验收结束后已清理本轮 Compose 容器及网络，临时数据保留。

| 检查 | 结果 |
|---|---|
| `go test -count=1 ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki` | 四包通过 |
| `go test -race -count=1 ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki` | 四包通过，无数据竞争 |
| `go vet ./internal/api ./internal/lockfile ./internal/memory ./internal/wiki` | 通过 |
| Linux 三包 `go test -v -count=1 -run TestReadOnlyMount` | lockfile、memory、wiki 全部通过 |
| `npm test` | 21 项单元测试、158 项组件测试通过 |
| `npm run lint` | 通过 |
| `npm run build` | 通过 |
| `npx playwright test test/e2e/pdf.spec.ts` | 6 项通过 |
| `npm run test:a11y` | 6 项通过 |
| 在线同身份并发验收脚本 | 16 次请求共 1 个计划、1 次创建事件；原 key 重放 200，新 key 429 |
| 本轮修改的 `git diff --check` | 通过 |

构建仍提示现有 pdfjs 的 eval 和大 bundle 警告；Linux wiki 测试提示 sonic 对 Go 1.27 使用 encoding/json 回退。上述提示均未阻止命令通过。

## I 组证据

核心测试位于 `backend/internal/api/idempotency_test.go`、`idempotency_handler_test.go`。测试使用 channel 暂停执行者，模拟真实签名身份 Cookie。

| ID | 证据与结果 |
|---|---|
| I-01 | `TestCreatePlanConcurrentSignedIdentity`：16 请求响应体完全一致，规划一次、保存一次、存储一个计划、创建事件一次。在线脚本也验证 16 次请求只产生一个计划和一次事件。 |
| I-02 | 同测试耗尽创建额度后仍可原 key 重放，带重放头；新 key 429。在线脚本验证相同行为。 |
| I-03 | 同测试在执行中及完成后修改 intent、附件、权限、四类预算、policy、session/project、review，均为 409。 |
| I-04 | `TestCreatePlanIndependentOwnersAndKeys` 与缓存独立键测试，验证 owner 隔离及独立创建。 |
| I-05 | `TestCreatePlanWaitCancellationAndTimeout`：等待者取消不取消执行者；后续成功且只保存一次。 |
| I-06 | `TestCreatePlanFailureRetryAndPanic`、`TestIdempotencyFailureIsPublishedToWholeBatch`：失败批次共享结果，后续请求可重新取得执行权；旧句柄不能修改新执行。 |
| I-07 | `TestCreatePlanSavedResponseDisconnect`：模拟响应写入连接断开，重放仍成功，保存计数为一。 |
| I-08 | 保存返回错误、保存 panic、规划 panic、等待超时和执行者取消均有测试；不确定保存不再次调用规划器，返回原 plan ID。等待超时返回 503 与 `Retry-After: 1`。 |
| I-09 | `TestIdempotencyCapacityAndExpiry`：过期成功结果可清理；执行中与不确定条目不被 TTL/容量淘汰；已有 key 可继续使用。 |
| I-10 | `AutoRunOnCreate.test.tsx`：超时和处理中重试沿用相同参数/key；修改输入、新会话换 key；commit_unknown 不重建。API reliability 测试验证调用方 key 原样发送。 |

在线验收脚本位于 `research-assistant/test/defect-remediation-api.mjs`。在使用创建限流 burst=1 的隔离测试服务上，从仓库根目录执行：

```sh
node research-assistant/test/defect-remediation-api.mjs
```

默认访问上述 15174 测试地址，可通过 `REMEDIATION_BASE_URL` 更改。脚本先调用 `/api/me`，复用签名 Cookie；请求显式 `require_review`，核查 `plan.review.status=pending`，不会调用执行接口，并在 finally 中删除计划。最终在线核验的测试计划 ID 为 `8eae6a45-9c39-4a27-af30-6364118b484a`，删除后计划列表为空。

## L 组证据

核心测试位于 `backend/internal/lockfile/lockfile_test.go`；真实挂载场景位于三个包各自的 `readonly_linux_test.go`。没有依赖 chmod 模拟只读，也没有全局可变 I/O 注入。

| ID | 证据与结果 |
|---|---|
| L-01 | 空闲路径写入当前 PID，释放后可以再次取得锁。 |
| L-02 | 存活 PID 的锁在 100ms 期限后返回 `ErrTimeout`，500ms 上界内结束，保留原锁。 |
| L-03 | 启动并等待子进程退出，用其 PID 创建陈旧锁；恢复成功并写入当前 PID。Windows 与 Linux 均运行通过。 |
| L-04 | 包内每次调用独立注入删除失败，保留 cause、仅调用删除一次并立即结束。 |
| L-05 | Linux 真正只读挂载、timeout=40ms：最终核验约 2.21ms 返回 `EROFS`，无后台重试。 |
| L-06 | 模拟另一进程已删除，`ErrNotExist` 后可取得锁；持续恢复竞争也受 deadline 约束。 |
| L-07 | 不可读、损坏内容、未知 PID 状态均不删除锁，执行有界等待。 |
| L-08 | 写入失败关闭并清理本次资源；零/负 timeout 对已有锁只创建一次，不读取或清理重试。 |
| L-09 | memory/wiki 的损坏锁初始化在既定 3 秒期限内失败并保留锁；Linux 真只读恢复失败分别约 2.19ms、2.00ms 向上传递 `EROFS`。 |

真实挂载验收需设置 `LOCKFILE_READONLY_TEST_PATH` 和 `STORE_READONLY_TEST_DIR`，分别指向只读目录中的陈旧 `stale.lock`、`.memory.lock`、`.wiki.lock`。普通 Windows 测试不运行带 Linux build tag 的挂载测试；这些场景已在 Linux 单独实测。

## P 组证据

核心测试位于 `frontend/test/e2e/pdf.spec.ts`、`test/components/AITranslationPopover.test.tsx`、`translationPopoverLayout.test.ts`。

| ID | 证据与结果 |
|---|---|
| P-01 | 保留 1024×500、长译文、缩小两次的原复现；正文四边及工具栏避让断言通过，允许 1px 舍入。 |
| P-02 | 放大/缩小、窗口改变和真实阅读器滚动均由浏览器验证；等待布局完成后比较边界。 |
| P-03 | 全文翻译面板展开/收起和工作台分隔线拖动后，使用新区域定位；小区域隐藏后恢复结果。 |
| P-04 | 纯几何测试覆盖小于 160×120 的区域；浏览器把容器缩至 130×100、再缩至 35px 高，验证必要时隐藏并保留结果，恢复后重新显示。 |
| P-05 | 浏览器覆盖 loading、短结果、长结果、错误和追问控件，关闭可用；组件测试覆盖内容状态切换。 |
| P-06 | 几何测试覆盖四角；浏览器先确认选中文字已滚到正文外，再断言隐藏，缩放/滚动恢复后重显。 |
| P-07 | 组件测试同时建立相同页码的两个阅读器，验证只定位触发者；文档 URL 改变时重建阅读器和插件，旧浮层卸载。 |
| P-08 | 390×844、1440×900 无水平溢出；浏览器模拟带非零 offset 的缩小 visualViewport，断言位于正文和窗口交集。 |
| P-09 | 组件测试核查每次重测前旧 observer 已断开，卸载后取消待执行 frame；祖先在没有 resize 时隐藏/恢复也会更新 Portal。浏览器反复隐藏/恢复、关闭浮层和关闭阅读器后无旧浮层。异步结果带 generation 检查。 |

## 审阅、发布与剩余边界

修改前，同键并发请求各自规划并保存；修改后，原子占位将其合并为一次执行，等待/重放不消耗创建额度。陈旧锁删除失败从无限循环变为可识别的错误；翻译浮层从窗口约束改为实际正文可视区域约束。

幂等保证限于单后端进程，成功快照保留 24 小时；不承诺跨进程重启或多副本幂等。保存结果不确定条目保留至人工核对；本轮未增加持久化幂等表或自动核对服务。服务重启后的不确定请求应先查计划库。

文件锁保持现有 PID 协议，多进程同时回收同一路径仍存在原协议的身份检查/删除竞态。本轮未引入平台原子文件锁。timeout 约束应用层循环和退避，不中断阻塞中的内核文件操作。

未改变文件格式、翻译 API，也未引入浮层依赖。本轮仅更新本地隔离验收服务；正式发布仍应按方案第 6 章排空创建请求，再分别发布验证三个修复。回退无需数据迁移，但会恢复原缺陷风险。
