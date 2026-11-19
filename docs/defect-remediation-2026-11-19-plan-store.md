# 计划存储隔离与提交一致性修复方案

状态：已实施并完成本轮验收；实际结果、已知失败与未测范围见[修复与验收报告](defect-remediation-2026-11-19-plan-store-results.md)。版本：1.1。日期：2026-11-19。

审阅基线：`d4d2af1a5fda67fbd027f5d1e5cfe9116ace20fa`。历史证据来自[项目评分与上下文交接](project-assessment-handoff-2026-11-18.md)。本文保留实施前的根因分析、设计契约和验收矩阵；第 2 节位置对应基线，不能当作修复后源码行号。实际实现、复跑结果及设计细化以验收报告为准。

修复目标：调用方只能通过存储接口提交变化；确定失败的操作不改变已提交状态；成功返回的数据能够通过重新打开存储恢复；无法确定提交结果时明确报错并停止继续使用不可信状态。

## 1. 范围、证据与优先级

本方案优先级为 P1，覆盖 Go 后端 `MemoryPlanStore`、`FilePlanStore`、摘要投影及其直接调用链。保留按计划快照和 JSONL 事件日志的文件存储架构。

| ID | 问题 | 证据强度 | 修复范围 |
|---|---|---|---|
| PS-01 | 返回计划中的嵌套 map 与内部状态共享 | 历史临时 Go overlay 探针已复现：未调用 `UpdatePlan`，`Inputs["metrics"]["score"]` 从 1 变为 999 | 读写入口、更新回调、事件和摘要的对象所有权 |
| PS-02 | `UpdatePlan` 先替换内存，快照失败后未恢复 | 历史临时探针已复现：注入写盘错误，方法返回 error，随后读取已为新值 | 快照提交顺序、错误分类和重试 |
| PS-03 | `SavePlan`、事件追加、删除也存在先改内存再执行持久化的顺序 | 当前源码静态确认，未逐项执行故障注入 | 全部写接口使用一致提交规则 |
| PS-04 | 隔离和提交顺序缺陷可能破坏回调失败撤销、dirty check 和失败后的幂等重试 | 静态推论，尚无单独运行记录 | 形成独立候选状态，在已提交状态上判断是否变化 |
| PS-05 | 指针、切片、RawMessage、摘要投影等复制不完整 | 当前源码静态确认，尚未逐字段复现 | 完整可变字段清单与回归测试 |
| PS-06 | Windows 旧 append-only 日志迁移时原文件未关闭 | 历史既有测试 `TestFilePlanStoreMigratesLegacyAppendOnlyLog` 全包及单独复跑失败 | 本轮涉及恢复代码，必须同时修复并复跑 |

原审阅的历史探针没有提交进仓库。本轮已用临时探针对照基线，并增加仓库永久回归测试；具体证据见验收报告。下文新协议、错误类型、文件字段及测试描述保留为设计约定。

本轮不处理 Python Optimizer 的 SQLite 连接释放、沙箱安全、PDF.js、前端结果详情或真实科研效果评测。跨进程并发写、分布式事务、计划与外部副作用的整体原子提交另行设计。

## 2. 基线实现与缺陷机制

相关文件均位于 `research-assistant/backend/internal/`：

| 文件 | 基线位置与问题 |
|---|---|
| [store/memory_plan_store.go](../research-assistant/backend/internal/store/memory_plan_store.go) | 第 300 行 `cloneStringAnyMap` 仅执行 `out[k] = v`；第 71 行起 `UpdatePlan` 将回调对象直接存入内部 map |
| [store/file_plan_store.go](../research-assistant/backend/internal/store/file_plan_store.go) | 第 114 行起更新；第 132 行发布内存，第 133 行写快照，随后追加日志；第 154 行先将事件加入内存 |
| [store/summary.go](../research-assistant/backend/internal/store/summary.go) | `SummarizePlanRecord` 直接携带部分时间指针、`Usage` 和 `Review` 的引用字段 |
| [store/file_plan_store_test.go](../research-assistant/backend/internal/store/file_plan_store_test.go) | 第 54 行的复制测试仅修改节点 `Name` 字符串，未覆盖嵌套 map、切片或指针 |
| [scheduler/scheduler_events.go](../research-assistant/backend/internal/scheduler/scheduler_events.go) | `publishAndStore` 在追加事件失败后仍继续 Publish 和 touchActivity |
| [api/plan_runtime.go](../research-assistant/backend/internal/api/plan_runtime.go) | 保存失败已有保守的 `commit_unknown`；删除失败和授权读取需要区分存储异常与资源不存在 |

### 2.1 对象隔离失效

当前外层 map、部分节点和结构体已经复制，但内部 map、slice、指针仍可共享。例如，返回的 `Inputs` 是新 map，其中 `metrics` 仍引用原 map。修改返回对象因此能够绕过存储更新接口，且不会触发写盘。

锁只约束存储方法执行期间的访问。返回后泄漏的可变引用不受锁约束；这是潜在 race 风险，历史材料没有 race 实证。

### 2.2 提交早于持久化

现有更新顺序为：

```text
复制计划 → 执行回调 → 比较变化 → 替换内存 → 写快照 → 追加生命周期记录
```

快照失败时，新状态已在内存；日志失败时，新快照还可能已在磁盘。因此“返回 error”既可能表示未保存，也可能表示部分生效。

临时文件、Sync 和 Rename 只处理单个快照文件的写入，不能自动将内存、快照、日志合成同一事务。把内存赋值后移仍不足以定义快照成功、日志失败时的语义。

### 2.3 两类问题叠加

以下为静态推论，需新增测试确认：

- 回调修改共享的嵌套值后返回 error，原计划可能已被污染。
- 回调只改共享嵌套值时，随后序列化的原计划和工作副本可能相等，dirty check 直接成功返回而未写盘。
- 写盘失败后内存已变新；重试若只设置同一新值、没有更新时间等其他变化，可能又被 dirty check 跳过。
- 即使补全第一次复制，回调若保留 `working` 或把外部对象赋给它，直接将其存入内部 map 仍会泄漏引用。

## 3. 修复后的接口约定

| 编号 | 必须满足的约束 |
|---|---|
| C-01 | 写入输入、读取输出、事件返回值、摘要中的可变对象均与存储内部对象分离 |
| C-02 | 更新回调失败时，内存、快照和事件均保持已提交状态 |
| C-03 | 更新成功后，回调保留的对象及其赋入的外部对象不能继续影响存储 |
| C-04 | 确定未提交的持久化错误不发布新内存状态；恢复 I/O 后重试能够真正保存 |
| C-05 | 成功返回后，内存视图与重新打开存储后的 JSON 持久化内容一致，事件 Seq 可恢复；动态 Go 类型仍受 JSON 重载规则约束 |
| C-06 | 结果不确定时使用专门错误，封存该存储实例；禁止继续调度、追加或自动重试 |
| C-07 | 只有已提交事件才进入事件列表、取得对外确认的 Seq，并通过 SSE 发布 |
| C-08 | 真正的 no-op 不重写快照，也不追加生命周期日志；只改嵌套数据必须被识别为变化 |
| C-09 | 删除后重启不复活；同 ID 重建时不继承上一生命周期的事件 |

调用方必须在方法调用期间交出输入对象的独占访问权；更新回调同步执行，不得启动 goroutine 并发修改传入对象。复制保证针对正常遵守该约定的调用方，不承诺消除调用方主动制造的并发读写。

一次 `UpdatePlan` 与随后独立的 `AppendEventReturning` 仍是两次提交。计划提交后、事件追加前中断的恢复，继续由调度器的状态机和终态补偿机制负责；本文不将二者宣称为原子事务。

## 4. 对象所有权与深拷贝方案

### 4.1 完整复制，保留内存中的类型

将复制逻辑集中到可独立测试的文件，例如 `store/clone.go`。模型使用有类型的深拷贝；动态值使用统一、保留具体 Go 类型的递归复制器，必要时返回含字段路径的错误。

不得通过整图 JSON Marshal/Unmarshal 代替内存深拷贝。该方式会使 `any` 中的整数、typed map、slice、结构体发生类型变化，也可能因 `omitempty` 改变 nil/empty 区别。现有 `cloneTyped` 忽略编解码错误，应替换或显式传播错误，不能静默得到零值。

动态值至少覆盖实际调用方使用的 `map[string]any`、`map[string]string`、`[]any`、`[]string`、`[]map[string]any`、数值切片、字节切片、`json.RawMessage`、诊断报告结构体及 `*models.Verification`。不能只支持 JSON 解码后的通用容器。

复制器应满足：

- 标量、枚举等值类型按值复制；保留整数精度、named type 和容器的具体类型。
- map、slice、数组、指针和结构体中的可变字段递归复制；`time.Time` 等有明确值语义的类型单独处理。
- 保留 nil、非 nil 空容器、typed nil 及指针标志语义，尤其 `Enabled`、`Optional` 和 `AllowedTools`。
- 循环引用、函数、channel 及其他不支持的对象明确报错；禁止遇到未知可变类型就原样返回。
- 两种 store 采用相同的可编码输入域，候选对象在发布前完成 JSON 可编码性校验；NaN、Inf 等编码错误不能在内存发布后才暴露。

这里要求的是内存复制保持类型。磁盘重载继续受 JSON codec 的既有动态类型归一化约束，不能据此承诺 Go 动态类型在磁盘往返后完全不变。

### 4.2 必须审计的字段

下表是当前源码中的重点清单，实施时仍需逐字段复核模型，不能视为穷尽清单。

| 对象 | 必查字段 |
|---|---|
| TaskNode | `Inputs`、`Metadata`、`InputBindings`、`RuntimeActionIDs`、`Enabled`、`Optional`、`Usage`、`Verification`、各时间指针、Contract 和依赖切片 |
| PlanGraph | `ArchivedAt`、`PausedAt`、`MissingRequirements`、`EnvironmentManifest`、`Verification`、`ModelSnapshot`、`Questions`、`AgentSnapshot`、`RuntimePolicy.Grants` |
| Context / Meta | Attachments、Documents、Paper 及 Authors/Warnings、Entities、Constraints、MemoryRefs、SandboxCapabilities 及 PythonVersions |
| Usage / Review | 时间指针、BudgetWarnings、TaskUsage.Models/ModelBreakdown、Review.ReviewedAt |
| Artifact | `Payload` 的底层字节数组，以及嵌套 `Metadata` |
| 其他结构 | Acceptance、OutcomeDetails、Recovery、Failure、ReplanProposal、BlockInfo、RuntimeActions、ToolCalls 的可变子字段 |
| PlanEvent | Payload 的全部嵌套可变对象 |
| PlanSummaryRecord | FinishedAt、Usage、Review 内的指针和切片；保持轻量投影，不为列表复制整个图 |

### 4.3 各入口的对象流转

```text
SavePlan / AppendEvent 输入 ── 深拷贝和校验 ── store 自有候选对象

已提交计划 ── 深拷贝 ── callback 工作对象
                         │
                         ├─ callback 失败：丢弃
                         └─ callback 成功：再次深拷贝 ── store 自有候选对象

store 内部对象 ── 深拷贝或独立摘要投影 ── 对外返回
```

`MemoryPlanStore` 也执行回调后的第二次复制；只有 FilePlanStore 需要磁盘协议，但两者应具有相同的隔离和回调失败语义。更新不得修改 Plan ID；如果候选 ID 与参数不一致，应在提交前拒绝。

## 5. 文件提交协议：计划快照与事件分别确定提交点

### 5.1 方案选择

采用以下协议，避免为一次计划保存同时维护两个必需的磁盘提交点：

- 计划创建、更新和删除以同一计划文件中的版本化记录为权威状态。
- JSONL 仍保存业务事件，不再为新版计划追加 `plan_saved`、`plan_updated`、`plan_deleted` 生命周期记录。
- 历史生命周期记录仅用于旧数据恢复。新版权威记录必须覆盖旧日志推导的状态。
- 如需记录保存操作的诊断信息，使用普通诊断日志；其失败不能把已经提交的计划变成“保存失败”。

不引入新数据库或完整 WAL。代价是需要明确区分旧裸快照与新版记录，并同时调整删除和恢复逻辑；不能只修改 `UpdatePlan` 后就宣布提交一致性已完成。

### 5.2 计划文件格式

建议继续使用现有每计划一个文件的位置，内容增加 envelope。以下字段均为拟新增的存储元数据：

```json
{
  "plan_store_format": 2,
  "plan_id": "plan-123",
  "generation": "lifecycle-uuid",
  "event_seq_floor": 0,
  "state": "active",
  "plan": { "id": "plan-123" }
}
```

`plan` 仅为示意，实际保存完整 PlanGraph。删除时以同路径的 `state: "deleted"`、`plan: null` 记录替换，保留 plan_id、generation 和事件序号边界，作为持久化 tombstone。

`generation` 标识一次计划生命周期，不是科研计划的 `CurrentRevision`，也不是执行轮次。普通保存、更新和格式升级保持生命周期不变；删除后同 ID 重建才换代。

为兼容当前接口允许保存同一 ID 的行为，采用 generation 隔离事件。新事件的外层存储记录携带 generation，公开 PlanEvent 结构可保持不变。旧数据使用明确的 legacy generation 映射，规则见第 7 节。

自动分配的 Seq 对同一 PlanID 不后退：删除时记录已提交事件的最大 Seq；同 ID 重建继承该序号下界，新事件从更大序号开始。这样保留现有数值型 SSE cursor，避免重建后从 1 开始使旧 Last-Event-ID 跳过新事件。历史事件仍按 generation 隔离，不向新生命周期重放。

### 5.3 SavePlan / UpdatePlan

持有现有进程内写锁，按以下顺序执行：

1. 检查存储健康状态，查找已提交计划或 tombstone，确定 generation 和 Seq 下界。
2. 形成独立工作对象；执行回调；再次复制为 store 自有候选对象。
3. 校验 ID、可编码性、存储元数据及其他现有约束；在原计划未被修改的前提下做 dirty check。
4. 真正无变化则返回成功；否则预先编码完整 envelope，准备发布所需的内存对象。
5. 同目录创建临时文件，完整写入，检查短写，Sync 并 Close。
6. 使用经平台验证的文件替换 helper 替换目标文件，完成该平台约定的持久化步骤。
7. 确认提交后再替换内存状态；首次创建才初始化事件视图，更新保留已有事件。
8. 返回成功。提交后的临时文件清理或诊断失败不改变提交结果。

步骤 6 是逻辑提交边界；成功确认还需满足已声明的平台刷新保证。若替换已发生但后续必要刷新失败，不能伪装成确定未提交，按第 6 节进入结果不确定。

dirty check 比较已提交计划与独立候选计划的持久化内容。旧格式首次迁移所需的 envelope 写入属于存储变化，不能因业务内容相同而漏做必要迁移。不要通过修改 UpdatedAt 强制绕过错误的无变化判断。

### 5.4 AppendEventReturning

1. 持锁确认计划存在、存储健康，校验 event.PlanID 与参数一致。
2. 深拷贝 Payload，取 `max(envelope.event_seq_floor, 当前 generation 已提交事件的最大 Seq)` 为序号上界，自动 Seq 为上界加一；显式 Seq 必须大于该上界。预编码外层 generation 和事件记录。
3. 记录日志最后一个完整有效记录及其换行分隔符之后的结束 offset；先准备好成功后需要的内存列表和独立返回副本。
4. 完整写入一条 JSONL 记录，处理短写；Sync 成功后才认为追加已确认。
5. 将事件发布到内部列表，并返回独立副本及 Seq。

Write、短写或 Sync 失败时，不发布内存事件。尝试将日志截断回步骤 3 的 offset，并再次 Sync；只有确认回退成功，才能返回“确定未提交”。回退或必要刷新失败则封存实例，返回结果不确定，禁止在损坏尾部继续追加。

文件关闭及相关错误必须被分类处理，不能用 `defer Close()` 忽略影响提交判断的错误。若已经完成规定的确认边界，后续诊断错误不能再表示普通追加失败；若不能确定，应进入不确定状态。

`MemoryPlanStore` 的事件追加也要形成独立的存储副本与返回副本。两种实现对自动 Seq、显式 Seq 的校验行为保持一致；拒绝重复或倒退的显式 Seq，回放旧记录使用独立恢复路径。

### 5.5 DeletePlan

删除以成功写入 tombstone 为提交点，随后才从内存删除计划和事件视图。tombstone 必须包含当前 generation，`event_seq_floor` 取原 floor 与当前 generation 已提交事件最大 Seq 的较大值；即使重建后没有追加事件，再次删除也不能降低 floor。重建继承该 floor。

不能通过先删除内存、忽略 `os.Remove` 错误、再追加删除日志实现删除。也不能提交后直接清除 tombstone，否则旧快照或旧日志可能再次恢复出计划。

旧事件在物理日志中的清理属于后续压缩工作；逻辑读取必须先被 tombstone 和 generation 阻断。需要物理数据擦除的场景应另行规定，不能把逻辑删除等同于安全擦除。

## 6. 错误、重试与调用链

### 6.1 错误分类

具体类型名可调整，但调用方必须能用 `errors.Is/As` 区分：

| 结果 | 例子 | 状态与后续行为 |
|---|---|---|
| 输入或回调失败 | 不支持的动态类型、编码失败、回调返回 error | 未提交；原内存和磁盘不变 |
| 确定未提交 | 临时文件创建失败；事件追加失败且截断、Sync 回退已确认 | 保留原状态；修复 I/O 后可按业务幂等性重试 |
| 已提交 | 快照替换及规定刷新完成；事件追加和规定确认完成 | 发布内存；返回成功；诊断失败另行记录 |
| 提交结果不确定 | 替换结果无法确认；必要刷新失败；日志回退失败 | 返回拟新增 `ErrCommitUnknown`，附 operation、plan ID、已知 generation/Seq 和底层原因 |
| 实例需要恢复 | 不确定结果之后再次访问 | 返回拟新增 `ErrStoreRecoveryRequired`，禁止继续使用该实例推进业务 |

本方案选择保守的恢复策略：发生结果不确定后，该实例的计划读、列表、摘要、事件读写均返回需要恢复错误，避免调用方将旧内存视图误当成最新状态。健康检查和不依赖计划内容的诊断仍可工作。

重新打开、校验磁盘并完成恢复后，才使用新实例继续服务；不能仅清空 unhealthy 标志，也不能在不确定状态下自动重跑更新回调。

### 6.2 必须同时检查的调用方

- `scheduler_events.go`：追加失败时不发布为可重放的正常事件，不推进 lastSeq，也不将其记为已持久化进展。错误需向执行/恢复决策传递；不能继续忽略返回结果。
- scheduler 更新路径：确定未提交时不得把状态当作已转换；不确定时停止该实例上的后续派发和状态写入。已运行的外部任务如何停止或恢复仍受原有执行租约与恢复约束。
- 计划创建幂等：保留现有 `commit_unknown` 保护，不能因存储重构就对所有错误释放 key 并重新创建。只有完成类型化错误接入和测试后，才能对确定未提交的失败采用更精确策略。
- API 读取和删除：区分 plan not found 与存储不可用，避免把恢复错误映射成 404；存储不可用返回明确的可识别 503，不触发客户端新建任务。
- 审计、sandbox 清理、revisionStore 删除、记忆等外部副作用仍在独立边界内，不宣称与计划文件同一事务。

进程可能在磁盘提交完成后、发布内存或返回响应前退出。此时重启恢复到新状态，而调用方没有收到成功，这属于确认丢失。不能承诺“所有未收到成功的操作都没有生效”，也不能仅凭重试追加相同内容来保证事件恰好一次；需要这项保证时另加稳定事件 ID 与去重协议。

## 7. 旧格式恢复、迁移与平台边界

### 7.1 恢复顺序

恢复必须在暂存结构中完成，全部校验成功后才对外开放：

1. 分别解码旧裸 PlanGraph 快照与 v2 envelope，识别格式版本，校验文件身份与 plan_id。
2. 按旧日志顺序构建 legacy 计划和事件视图，保留旧 `plan_deleted` 清除事件等语义；同时记录每个 PlanID 的历史最大 Seq 供迁移序号下界使用。不要让旧 `readSnapshot()` 将 envelope 当成裸 PlanGraph 解码。
3. 在筛选事件前，为无 generation 的旧事件建立稳定映射，例如保留值 `legacy:<PlanID>`；映射不能随重启变化，也不能只为仍有旧裸快照的计划建立。单独收集带 generation 的新版事件记录。
4. 以 v2 envelope 为权威覆盖 legacy 视图：active 只选当前 generation 的事件；deleted 屏蔽该计划及全部事件。旧 lifecycle marker 不得覆盖 v2 状态。
5. 旧计划升级为 envelope 时沿用该 legacy generation，旧事件只取旧协议回放后仍有效的那一组；已经升级且仍用 legacy generation 的 v2 计划也必须恢复这些事件。迁移 floor 保留扫描所得的序号上界，新生命周期使用新 generation。
6. 校验 Seq、日志有效边界和 tombstone，再发布内存状态；迁移中断后重新启动应得到相同结果。

未知版本、损坏 envelope、新版记录缺失或非法 generation、同一权威记录内身份自相矛盾必须明确报错，禁止静默退回旧快照并复活状态。日志中合法的历史 generation 是正常情况，按生命周期过滤，不视为损坏。发现截断尾记录时，在允许下一次写入前必须修复到最后完整边界并确认持久化；中间坏记录应拒绝启动，不能跳过。

旧日志末行若是完整合法 JSON 但缺少换行，先补充分隔符并 Sync，才能作为后续追加与回退的边界；若末行 JSON 不完整，截断到上一完整记录及其分隔符之后。不能将新记录直接拼接到缺少分隔符的末行。

legacy 迁移与新协议的恢复应独立测试。需要旋转旧日志时，先显式关闭所有输入句柄，再执行 Rename；归档失败、写入失败和中途退出均需可重入，保留原始数据供恢复。

若迁移需要重写日志，先生成并 Sync 完整的新日志，再关闭输入句柄，以单个替换步骤启用新日志；不能沿用先旋转原日志、再逐条追加新日志的顺序。替换前保留原日志可恢复，替换后归档失败仅记录诊断，不撤销已完成的迁移。跨计划的格式升级允许逐个提交，但每个中间状态都必须能由上述混合格式恢复规则重新打开。

### 7.2 Windows 与持久性声明

Go 的 `os.Rename` 在非 Unix 平台不承诺与 Unix 相同的原子性。应封装平台文件替换，验证目标已存在、文件占用、权限失败及同目录替换行为。不能使用“先删旧文件再重命名”的方式制造恢复空窗。

Linux 按需要验证文件与父目录刷新；Windows 对使用的文件替换/刷新 API 及共享模式做专门验证。若操作系统无法明确确认替换或持久化结果，应按不确定处理。

本轮保证限于已验证的 Windows/Linux 本地文件系统、单个存储写进程及明确测试过的进程中止恢复。机器断电、磁盘控制器缓存、网络文件系统、多进程同时打开同一路径均不能自动包含在保证内。

历史 Windows 旧 append-only 日志迁移失败必须消除。scheduler 全包历史另有 CRLF 内容比较和符号链接权限失败，应在验收报告单列，不得将其混写成存储修复已失败或已全部通过。

### 7.3 升级与回退

实施前保存快照目录与日志的一致备份；可停写复制，不在运行中分别复制两个不断变化的路径。

新版本需同时读旧格式与 v2。旧程序不理解 envelope 和 tombstone，开始写入 v2 后不能直接降级打开同一份数据。回退使用停写前的完整备份，或先实现并验证离线降级转换；不能只回退可执行文件。

## 8. 实施拆分与涉及文件

| 阶段 | 工作与主要文件 | 完成条件 |
|---|---|---|
| A：隔离 | 新增 `store/clone.go`；调整 memory/file store、`summary.go`；补充模型字段清单和隔离测试 | 所有入口的输入/输出隔离、回调失败及回调后引用测试通过，可独立提交 |
| B：提交与恢复 | `file_plan_store.go` 拆出快照 codec、平台替换、事件追加与回退 helper；定义错误；实现 envelope、tombstone、generation、恢复 | 保存/更新/事件/删除与 loader 一起交付；不能只替换某一个写方法 |
| C：调用链 | `scheduler_events.go` 及相关调用、`api/plan_runtime.go`、必要的健康状态和错误响应 | 不发布未确认事件、不继续使用不确定状态，404/503 与幂等语义正确 |
| D：平台与验收 | store 专项、scheduler/api 集成、`.github/workflows/ci.yml`、验收报告 | Linux 与 Windows 证据齐全，迁移和故障注入通过 |

阶段 B 与 C 应作为同一可发布单元；内部可分提交，但不得发布调用方仍忽略新错误的中间状态。Go 模型新增可变字段时同步更新复制逻辑和字段覆盖测试。

本轮不借机拆分全部大型模块；仅围绕复制、快照格式、文件操作、恢复职责做必要分离。

## 9. 回归与故障注入矩阵

下表是实施前制定的验收矩阵。实际测试名称、通过结果及未专项覆盖的细分边界见验收报告；不能将设计条目本身当作运行证据。隔离用例同时覆盖 MemoryPlanStore 和 FilePlanStore；重开检查用于 FilePlanStore。

| ID | 场景 | 必须断言 |
|---|---|---|
| ISO-01 | SavePlan 返回后修改原输入的嵌套 map/slice/指针/RawMessage | GetPlan 与磁盘内容不受影响 |
| ISO-02 | 修改 GetPlan、ListPlans 的返回对象 | 后续读取不变，覆盖第 4.2 节字段 |
| ISO-03 | callback 修改嵌套值后返回 error | 已提交内存、快照和事件全不变 |
| ISO-04 | callback 成功后修改保留的 working/child，或修改赋入的外部对象 | store 不变 |
| ISO-05 | 仅修改嵌套 map、slice、指针或字节内容 | 被识别为变化，重新打开仍保留修改 |
| ISO-06 | 修改事件输入、AppendEventReturning 返回值、ListEvents/After 输出 | 所有事件副本相互独立 |
| ISO-07 | 修改摘要 FinishedAt、Usage、Review | 计划和其他摘要不变，列表仍采用轻量投影 |
| ISO-08 | int/int64/大整数、typed map/slice、诊断结构体、typed nil、nil/empty | 内存复制不改变类型、精度或标志语义 |
| ISO-09 | 循环值、不支持类型、编码错误 | 返回明确错误，不改变状态，不静默生成零值 |
| COM-01 | 真正 no-op | 快照写入和事件日志追加次数为零 |
| COM-02 | 创建/更新在编码、创建临时文件、Write、Sync、Close、替换前失败 | 确定未提交时内存不变；重新打开无新状态 |
| COM-03 | 快照替换成功，随后发布内存/响应前进程中止 | 重新打开恢复新状态；符合确认丢失语义 |
| COM-04 | 替换或必要刷新结果不确定 | 返回专门错误，后续读写拒绝，恢复后重新核对状态 |
| COM-05 | 确定失败后解除故障，以同一目标值重试 | 真正写入成功，不被 dirty check 吞掉 |
| EVT-01 | 事件短写、Write/Sync 失败，日志回退成功 | 不发布事件，内存及恢复后事件不增加，重试取得正确 Seq |
| EVT-02 | 回退 Truncate 或恢复 Sync 失败 | 封存实例；不能继续在损坏尾部追加或发布 SSE |
| EVT-03 | 事件确认后、返回响应前中止 | 重启事件存在，Seq 可恢复；不宣称重试恰好一次 |
| EVT-04 | 自动 Seq、显式冲突 Seq、PlanID 不匹配 | 顺序与身份校验一致，错误不修改状态 |
| DEL-01 | tombstone 写入失败/成功；连续两次重新打开 | 确定未提交时保留旧计划，不确定时封存；成功后不复活旧计划或事件 |
| DEL-02 | 同 ID 删除后重建，再无事件删除并重建；旧日志含保存/删除/事件 | 新 generation 无旧事件，floor 和 Seq 不后退，SSE cursor 不跳过新事件 |
| REC-01 | legacy JSON 整体快照、旧 append-only、旧 snapshot+log；首次升级后再重开 | 计划、旧事件、删除语义和租约恢复均正确，legacy generation 映射稳定 |
| REC-02 | 旧 marker 与 v2 active/tombstone 混合 | v2 权威状态不被旧日志覆盖 |
| REC-03 | 未知版本、损坏 envelope、中间坏记录、截断尾记录 | 明确拒绝或修复；禁止静默回退、禁止带坏尾继续写 |
| REC-04 | 迁移/归档中止后重复启动 | 可恢复、无重复事件、不丢删除状态 |
| WIN-01 | 覆盖已存在文件、占用句柄、权限错误 | 平台替换结果符合错误分类，旧有效状态受保护 |
| WIN-02 | `TestFilePlanStoreMigratesLegacyAppendOnlyLog` | 输入句柄先关闭，迁移及重复打开通过 |
| INT-01 | 存储确定失败/不确定时调度及 SSE | 无未确认事件、无错误进展计数、无后续错误派发 |
| INT-02 | 创建幂等、授权读取与删除遭遇存储异常 | 保留 commit_unknown；存储不可用不伪装为 404，不自动创建新计划 |
| RACE-01 | 各 goroutine 修改各自返回副本，同时执行 store 读写 | race 检查通过，业务状态保持一致 |

故障注入使用可控文件操作接口，精确控制 Write、Sync、Close、Replace、Truncate 和必要的目录刷新。必须通过真实临时文件与子进程中止测试补充 fake I/O；fake 只能证明分支行为，不能证明 Windows 文件系统语义。

并发测试使用 channel/barrier 控制交错，不依赖 sleep 碰撞。no-op 通过 I/O 次数与快照/日志内容断言，不仅检查文件 mtime。每个失败用例至少核对方法结果、内存视图及重新打开后的视图；不确定状态则先断言拒绝读取，再重开核对允许的恢复结果。

## 10. 验证命令与 CI

在 `research-assistant/backend` 执行，工具链与 go.mod 的 Go 1.27.3 对齐。以下保留复跑命令；本轮实际执行范围、结果和日志见验收报告。

Windows 和 Linux 都运行存储专项：

```text
go test -count=1 ./internal/store
go vet ./internal/store
```

Linux 运行调用链及全量门禁：

```text
go test -count=1 ./internal/api ./internal/scheduler ./internal/recovery
go test -race -count=1 ./internal/store ./internal/scheduler ./internal/api
go test ./...
go vet ./...
```

必要时先定向运行新增用例，再进行上述完整门禁。保持现有 CI 更广的 race、安全和覆盖率检查，不因本方案缩减。

设计时 CI 虽有 Windows matrix，但 Backend tests/vet/race 实际只在 Linux 执行。本轮已新增 Windows store test/vet 专项 gate，包含旧日志迁移和平台替换测试。门禁配置已修改，本轮本机 Windows 已执行；尚无远程 CI 运行结果。

验收报告记录提交、系统/文件系统、Go 版本、命令、通过/失败/跳过数量、故障点和进程中止结果。已有历史失败必须单列其当前状态，不将环境缺口视为通过。

## 11. 完成标准与当前状态

只有在以下条件全部满足后，才将状态改为“已修复并验收”：

- PS-01/02 转化为仓库永久回归测试，修复前能失败，修复后通过。
- PS-03/04/05 的入口、所有权及故障边界均有测试，PS-06 的 Windows 迁移失败已消除。
- 新格式提交、删除、事件和恢复使用同一份明确契约，旧数据可升级，回退限制已说明。
- 调用方正确处理确定失败与不确定结果，无未确认 SSE 发布或错误自动重试。
- Linux 与 Windows 专项、相关集成和 race 证据齐备，已知失败及未测范围如实列出。
- 原 no-op 和大计划紧凑日志优化保留；新版操作不把完整 PlanGraph 反复追加进事件日志。

本轮已完成隔离、提交与恢复、调用链及 Windows 门禁修复，并将源码和构建产物复制至现有 Docker 后端容器。Linux 全后端、相关 race/vet、Windows store/API 专项及容器重启 smoke 已有运行证据；Windows scheduler 另有 5 项在原基线复现的失败。详细计数、备份回退、Docker 镜像未重建和未测范围见[修复与验收报告](defect-remediation-2026-11-19-plan-store-results.md)。本结论限于报告列出的环境和用例。
