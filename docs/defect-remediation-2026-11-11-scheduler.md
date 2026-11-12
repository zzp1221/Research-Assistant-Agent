# 调度等待态、失败传播与部分成功修复方案

状态：已实施，并通过两轮独立复核整改。版本：1.3。日期：2026-11-12。核查基线：`8623926`。

本方案修复执行中等待审批被误传播为上游失败、终态汇总竞态及阻断原因缺失，并补齐 `OutcomePartial` 的生成规则。实施证据、命令输出与逐项验收见[`defect-remediation-2026-11-11-scheduler-results.md`](defect-remediation-2026-11-11-scheduler-results.md)；文中新增字段与辅助函数已按此设计落地，命名以代码为准。

## 1. 已确认的问题与修复边界

| 编号 | 性质 / 优先级 | 已确认行为 | 目标行为 |
|---|---|---|---|
| SCH-01 | 调度 bug / P1 | 执行中返回 `awaiting_approval` 后仍进入失败处理，下游被写为 `blocked`，事件原因是 `upstream_failed` | 等待期间保留后继为 pending，不传播失败；批准后可以正常继续 |
| SCH-02 | 终态 bug / P1 | 主循环使用旧快照判断死锁，即使失败传播已结束，仍可能写入 `Status=failed、Outcome=""` | 在同一状态提交边界重新判断等待、可运行和终态；任何新写入的终态均有 Outcome 和原因 |
| SCH-03 | 可观测性缺陷 / P2 | `blockDependents` 只写状态和更新时间，节点快照缺少阻断原因 | 节点、事件及刷新后的界面均能解释阻断来源 |
| SCH-04 | 功能缺口 / P1 | `OutcomePartial` 只有定义；失败计划不进入 finalizer，finalizer 也将任一 failed 节点判为失败 | 核心目标已验收、仅可选任务损失时产生 partial，并列出损失 |
| SCH-05 | 语义清理 / P3 | `shouldBlockOnFailure(task)` 忽略参数并恒返回 true | 用明确的结果分类和依赖规则替代无实际策略内容的判断 |

主要依据：

- [policy.go](../research-assistant/backend/internal/scheduler/policy.go)：`shouldBlockOnFailure`。
- [scheduler_dispatch.go](../research-assistant/backend/internal/scheduler/scheduler_dispatch.go)：`runReadyNodes` 将非 completed 结果统一交给 `markTaskFailed`。
- [scheduler_state.go](../research-assistant/backend/internal/scheduler/scheduler_state.go)：`markTaskFailed` 同时处理失败、blocked 和等待状态。
- [scheduler_execute.go](../research-assistant/backend/internal/scheduler/scheduler_execute.go)：终态汇总、死锁分支及单任务执行入口。
- [graph.go](../research-assistant/backend/internal/scheduler/graph.go)：依赖状态与产物存在性判断。
- [scheduler_lease_approval.go](../research-assistant/backend/internal/scheduler/scheduler_lease_approval.go)：下游阻断、重试恢复与预算判断。
- [semantic_validation.go](../research-assistant/backend/internal/planner/semantic_validation.go)、[outcome.go](../research-assistant/backend/internal/models/outcome.go)：最终结果生成与 Outcome 枚举。

核查时已通过临时 Go overlay 完成七项定向复现，包括等待审批误传播、无传播时的空 Outcome、以及固定快照时序下的空 Outcome。临时复现不等于仓库已有回归测试；实施时必须将其转为正式用例。

以下边界必须保留：

1. 正常前置审批使用 `awaiting_approval`，正常结构化提问使用 `awaiting_input`，这两个入口目前已有提前返回逻辑。[runtime_gate.go](../research-assistant/backend/internal/scheduler/runtime_gate.go) 的 blocked 分支对应已拒绝，[questions.go](../research-assistant/backend/internal/scheduler/questions.go) 的相应分支对应超时无默认答案，不能把它们描述成仍在正常等待。
2. `repo_discovery` 未找到可信仓库、`repo_prepare` 数据形状不匹配，当前属于需要补条件后重跑的阻断终态。第一批保留此行为，只修正传播原因；不自动改造成可无限等待的交互流程。
3. 失败产物保留用于诊断和展示，本身不是 bug；产物存在不代表适合下游消费。
4. 尝试次数和执行时长是独立上限，不因数值不同认定配置矛盾。本方案不修改 `100 / 2100` 默认值，不把预算耗尽改成 blocked。

## 2. 修复后的状态契约

### 2.1 TaskStatus 表达执行进度

| 状态 | 本轮是否结束 | 是否满足现有硬依赖 | 对后继的影响 |
|---|---|---|---|
| pending / ready / in_progress | 否 | 否 | 等待调度或执行 |
| awaiting_approval / awaiting_input | 否 | 否 | 后继保留 pending，不写 upstream_failed |
| completed | 是 | 是 | 仍需满足输入产物契约 |
| skipped | 是 | 是，沿用现有约定 | 不能凭 skipped 伪造必需产物或核心验收通过 |
| failed | 是 | 否 | 阻断依赖它的硬后继，保留真实失败状态 |
| blocked | 是，需补条件并显式重跑 | 否 | 阻断硬后继，原因是 upstream_blocked |
| canceled | 是 | 否 | 按取消原因停止或阻断硬后继 |

`blocked` 不承担“正在等待用户回答”的含义。需要在线等待时必须有待处理的审批或问题记录，并使用对应 awaiting 状态。

### 2.2 PlanOutcome 表达结果能否使用

| 场景 | Plan.Status | Plan.Outcome |
|---|---|---|
| 尚在运行或等待 | in_progress / awaiting_* 等非终态 | 空；清除上一轮 Outcome 和 OutcomeReason |
| 核心目标及全部启用的工作已验收 | completed | completed |
| 核心目标已验收，仅可选工作损失 | completed | partial |
| 核心执行成功，但语义证据尚不能判定 | completed | needs_review |
| 核心任务真实失败 | failed | failed |
| 核心条件缺失、审批拒绝或超时终止 | failed | blocked |
| 显式取消或运行预算耗尽 | canceled | failed，原因区分 user_canceled / budget_exceeded |
| 符合现有 smoke / mock 规则 | completed | smoke / mock，保留降级性质 |

本轮沿用现有 Outcome 枚举，不增加 canceled Outcome。上表中 `Status=completed` 表示本轮流程结束，不保证结果科学有效；界面必须同时读取 Outcome。

所有新写入的计划终态必须同时具有 `Outcome`、`OutcomeReason` 和 `Usage.FinishedAt`。重试或重新进入非终态时清除过期结果，保留历史事件。因全计划拒绝而被取消的其他节点，不能把已经明确的 `OutcomeBlocked` 反向覆盖为 failed。

## 3. 第一批：修复等待态处理与失败传播

### 3.1 统一结果分流

在 `runReadyNodes` 和手动 `ExecuteTask` 之间复用结果状态迁移逻辑，避免两个入口继续各自维护一份“非 completed 就失败”的判断。建议新增 `applyTaskResult`，内部将等待与失败拆开；具体命名可随实现调整。

处理顺序：

1. 校验执行围栏：`ExecutionID`、`LeaseOwner`、`ExecutionEpoch` 及现有 revision 约束。陈旧结果不能写入等待、问题、产物或后继状态。结构化问题目前也需要纳入相同围栏。
2. 保留执行异常的优先级：已取消、已超时或执行器明确报错的结果，不能仅因附带 Question / RuntimeAction 就被改成正常等待。
3. 处理有效的结构化问题或待审批动作，进入专用等待迁移。
4. completed 走现有产物校验和完成迁移。
5. blocked、failed、canceled 分别进入对应终结处理；真正失败才进行恢复分类和重试判断。
6. 未识别的结果状态按明确的协议错误处理，记录原因，不能默认为等待或成功。

等待迁移必须满足：

- 节点状态是 `awaiting_approval` 或 `awaiting_input`，有对应 pending 记录；裸等待状态缺少记录时返回协议错误。
- `Task.Error` 为空，`FinishedAt=nil`，不生成虚假的 Failure、失败重试或 replan proposal。
- 保存已发生的 usage 和恢复检查点，设置恢复状态 `Waiting=true、PendingRetry=false`，释放当前执行租约；批准后的恢复继续使用现有审批与工具检查点，避免重复执行已完成的副作用，也不因恢复被误扣一次失败重试。
- 不调用 `blockDependents`，不发布 task_failed，不把后继改为 blocked。
- 等待不扣“失败重试额度”。`TaskAttempts` 仍记录真实启动的执行次数，不能把它与失败重试次数混为一谈。
- 保留已有审批等待预算、结构化问题超时及唤醒机制，不通过延长总预算掩盖状态错误。

在等待与终结分流完成后移除恒 true 的 policy 函数。不要将它简单改为 false，也不要把 failed 一律加入 `allDependenciesCompleted`。

### 3.2 按真实原因阻断硬后继

| 根因 | 后继原因码 | 说明 |
|---|---|---|
| 上游不可重试的 failed | upstream_failed | 真实执行失败 |
| 上游 blocked | upstream_blocked | 缺少仓库、数据或其他可补条件 |
| 上游 canceled | upstream_canceled | 取消导致本轮无法继续 |
| 上游 awaiting_* | 不传播阻断 | 后继保留 pending |

第一批继续执行严格硬依赖。失败不会波及独立分支；传播范围只包括真实硬控制依赖和必需数据依赖。传播后继与判断 ready 必须使用一致的依赖定义，不能一处只读 `Dependencies`、另一处只读 `Edges` 而产生相反结果。

节点终结及其硬后继的阻断必须在同一个 `UpdatePlan` 内提交，使用只修改传入图的内部辅助函数，避免嵌套调用 store。这样不会暴露“上游已结束、后继尚未分类”的中间状态。已完成、正在运行或已明确等待的节点不得被普通阻断遍历覆盖；若出现按硬依赖不应运行的节点，记录一致性错误并按既有取消机制处理。

派发时也不能只相信旧 ready 快照：获取执行租约的状态提交必须重新确认节点仍可执行、硬依赖仍满足且审批有效，避免节点刚被阻断后又被旧派发请求启动。手动执行入口复用同样的合法性判断。

## 4. 第一批：修复终态竞态及空 Outcome

### 4.1 已复现的时序

```text
主循环读到：A=blocked，B=pending，无运行任务
工作线程提交：B=blocked，此时全图已经终结
主循环仍按旧快照进入 noRunning 分支
UpdatePlan 只复查 noRunning / 是否能提升 ready，没有复查 allTerminal
结果：Status=failed，绕过正常汇总，Outcome 为空
```

这说明即使 policy 为 true，也不能保证获得正确的 Outcome。该问题属于业务时序竞态，不能只依赖 `go test -race` 发现。

### 4.2 在当前状态上统一裁决

将正常终结与死锁判断收敛到同一个内部状态裁决入口，例如 `reconcilePlanState`。外层快照仅用于决定何时尝试裁决，真正提交时必须在 `UpdatePlan` 取得的当前图上重新判断：

1. 若已有合法的显式取消、拒绝或超时终结，保留其停止原因和 Outcome，不重新解释其取消后继。
2. 若存在 running、待重试、暂停或有效 awaiting，按当前状态继续等待，不写死锁终态。
3. 对可晋升的节点执行 ready 提升；若已有 ready 或本轮出现新工作，返回执行循环。全局并发槽暂时不足属于等待资源，不是依赖死锁。
4. 若所有节点已经终结，进入统一汇总；不能因为存在 failed / blocked 而跳过 finalizer。
5. 只有既非终态、也无可运行任务、无合法等待时，才认定依赖无法满足。持久化未满足的节点、依赖 ID、缺失产物及机器可读原因，再写 `failed/failed`。

终态汇总和 Outcome 写入必须属于同一次状态提交。finalizer 应保持本地、确定、无网络请求；若未来必须在锁外计算，需要增加快照版本校验，提交前验证状态未变，不能直接回写旧评估。

将最终结果先计算成一个评估值，再一次性提交 `Status / Outcome / OutcomeReason / FinishedAt / Meta`。未注入 finalizer 时使用调度器的严格汇总规则，仍可生成 completed / blocked / failed；finalizer 实际报错时才写 failed 并记录错误。

扩大 finalizer 调用范围时必须同步修改其默认判断：第一批仍将全部任务视为必需，真实失败判 failed、只有条件阻断判 blocked，并保留显式停止原因。不能只移除调用门槛而让当前 finalizer 将“全图 blocked、无 failed 节点”的计划误评为 completed；第二批再增加可选任务验收。

状态提交成功后再发布终态事件、执行清理、释放预算、记录记忆及通知。沿用现有终态标记防止同一轮循环重复执行；存储失败不发布“已完成”。事件追加与状态更新目前不是单一事务，因此不宣称跨进程严格 exactly-once；刷新应始终可以从节点和计划快照恢复结果。

### 4.3 预算与终结的先后

attempt 上限用于阻止继续启动任务，不应用于覆盖一个已经自然结束的结果。裁决时先识别已发生的显式停止，再处理已经全图终结的结果，最后判断尚未结束的计划是否预算耗尽。

- 最后一项可选任务恰好用完次数且失败，全图已经结束：允许按验收结果汇总为 partial。
- 仍有应该执行的任务，但没有剩余 attempts：维持 `CancelPlan("run budget exceeded")`。
- 执行时长或其他预算在运行中已经触发取消：维持 canceled，不在事后改成 partial。

这是对终结优先级的修复，不是调整预算默认值；需要同步修改此前依赖“终态混合结果一律预算取消”的断言。

## 5. 第一批：持久化阻断原因与恢复规则

建议为 `TaskNode` 增加可选的结构化 `block_info`，至少包含以下信息：

| 字段 | 含义 |
|---|---|
| code / message | upstream_failed、upstream_blocked、upstream_canceled 等原因码和可读说明 |
| causes[] | 全部有效阻断来源，不能只保留遍历遇到的第一个上游 |
| causes[].root_task_id / via_task_id | 根因任务与直接传播到当前节点的前驱 |
| causes[].revision / execution_epoch | 根因所属版本和执行轮次，避免旧失败污染重跑 |
| causes[].status | 真实根因状态 |
| occurred_at | 本轮阻断发生时间 |

同时写入简短的 `Task.Error`，例如“因任务 A 缺少数据集而阻断”。详细诊断保存在根因节点和 MissingRequirements 中，不复制成多份长错误。

节点的 block_info 与状态同一次提交，随后事件从同一份原因对象构造。保留现有 `task_blocked`、`upstream_task_id` 字段供旧客户端使用，`reason` 改为真实原因；新客户端优先读取结构化信息。

恢复时重新计算当前有效的阻断来源：

- 某个根因已重试，只移除属于该根因旧轮次的阻断；仍有其他有效阻断时节点继续 blocked。
- 所有终结阻断已消失，节点回到 pending，再按依赖和产物条件决定是否 ready。
- 审批或回答不会直接把无关 blocked 节点全部重置，也不绕过权限、revision、合同或租约校验。
- 清除过期 block_info、Error、FinishedAt，以及上一轮的计划 Outcome；保持恢复预算约束。

[runtime_actions.go](../research-assistant/backend/internal/api/runtime_actions.go)、[plan_questions.go](../research-assistant/backend/internal/api/plan_questions.go) 与 scheduler 的 retry / reset 应共享规则。对于升级前因等待误传播而 blocked 的后继，只有在事件或根因记录能证明来源、且当前已不存在有效硬阻断时才能定向恢复；证据不足时提供显式重跑，不自动放行。

前端从快照显示阻断原因，SSE 用于及时刷新。应同步检查 [useResearchAssistantRuntime.ts](../research-assistant/frontend/src/app/hooks/useResearchAssistantRuntime.ts) 的节点日志、状态恢复，以及 [usePlanCompletionNotification.ts](../research-assistant/frontend/src/app/hooks/usePlanCompletionNotification.ts) 的终态通知，避免只按 failed 状态把“缺条件”显示成普通执行失败。

## 6. 第二批：让部分成功成为可验收的结果

### 6.1 先声明核心目标，再允许可选损失

不能按“有 completed 也有 failed”直接计算 partial。计划执行前必须明确哪些工作属于核心、哪些损失可以接受，并把该判定纳入 revision / 审阅，不能在失败后临时把失败节点降为可选。

建议增加以下契约，字段名可调整，语义必须固定：

| 位置 | 拟议字段 | 语义与兼容默认值 |
|---|---|---|
| TaskNode | `optional: bool` | false 表示核心必需工作；旧计划缺省 false |
| TaskNode | `optional_dependencies: []string` | 等这些节点终结，但不要求其成功；不会替代原有硬 Dependencies |
| PlanGraph | `acceptance` | 核心目标、必需产物及确定的验收规则；复用已有 TaskContract、证据图与校验器，禁止以模型一句“基本完成”代替验收 |
| PlanGraph | `outcome_details` | 核心验收依据、可选损失节点、缺失内容与证据模式，供界面和导出使用 |

旧计划全部按必需任务处理；没有可验证 acceptance 时不自动生成 partial。新计划必须至少有一项非空核心目标，防止“全部任务可选”获得空洞成功。

### 6.2 调度依赖与结果重要性分别判定

`optional` 决定节点失败是否破坏计划核心目标；`optional_dependencies` 决定消费者是否必须等待该生产者成功。两个字段不能互相替代。

- 原有 `Dependencies` 继续要求 completed / skipped，必需产物继续严格校验。
- `optional_dependencies` 只在所指节点本轮终结后满足；awaiting_* 不是终态，不能提前越过待审批任务。
- 新的可选关系应在图中使用明确的关系类型，参与环检测、图展示和重跑影响分析，但不参与硬失败连坐。
- 规划和 API 验证必须统一检查两类关系：禁止同一依赖同时为硬和可选、禁止跨两类关系形成环。
- 如果所谓“可选任务”的产物是核心后继的必需输入，计划并不具备容错条件：应把生产者纳入必需任务，或在审阅时显式设计缺失分支。不能仅加 optional 标签放行。
- ready 判断、初始状态计算、ContextBinder、审批后恢复、重试重置及重跑失效范围必须采用同一规则。避免规划器先把仅有可选依赖的节点设为 ready，绕过新的谓词。
- 可选输入缺失要显式传递缺失说明；不得为其补造数值、伪造产物或把失败任务的诊断产物自动注入为可信输入。

一个可验收的例子：

```text
核心实验 A ──硬依赖──> 主结论报告 C
可选指标 B ──可选依赖──> 主结论报告 C

A 完成并通过核心验收；B 失败；C 等 B 终结后继续，明确报告缺少该指标。
最终：A=completed，B=failed，C=completed；Plan.Status=completed，Outcome=partial。
```

如果 B 事实上是证明主结论所必需的指标，就必须保留硬依赖；此例不具备 partial 条件。

### 6.3 finalizer 的判断顺序

第一批解除“仅 completed 才调用 finalizer”的门槛；第二批将 `任一 failed => failed` 替换为基于验收契约的评估。判定顺序固定如下：

1. 保留显式取消、预算停止、审批拒绝和超时的停止语义。
2. 核心任务真实失败，或已无法满足核心硬依赖：failed；核心条件缺失则 blocked。节点本身的 failed 状态保留，不改写为 completed / skipped。
3. 核心任务执行结束但语义证据不足：needs_review。不能用“部分成功”掩盖主结论缺乏证据。
4. 按现有规则识别 mock / smoke；这些模式优先保留自身 Outcome，outcome_details 另列可选损失，不升格为真实复现的 partial。
5. 核心目标和必需产物已通过验收，仅启用的可选任务出现 failed / blocked / canceled：partial。
6. 核心目标与启用的工作均完成且通过验收：completed。预先禁用导致的 deliberate skipped 不单独制造 partial。

`partial` 至少写明：哪些核心目标已通过、验收引用是什么、哪些可选工作缺失、这些缺失限制了哪些结论。界面显示“部分完成”，通知、历史、导出和记忆不能再单凭 `Status=completed` 宣称全部成功。

## 7. 产物策略

第一、二批都保留失败任务的代码、指标和诊断产物，供详情、导出和修复使用。硬依赖不因 `plan.Artifacts` 中有同名键而满足；可选依赖也不自动认证失败产物。

本方案不开放“失败生产者的任意现存产物可被继续消费”。如果后续确需复用，应作为独立增量：要求明确的消费选择、逐产物校验结果、producer、revision / execution 来源、内容完整性及现有证据校验通过，并同时修改依赖判断和 ContextBinder。仅更改 `artifactsSatisfied` 或 policy 不足以实现该能力。

更小的替代办法是把“生成产物”和“验证某个可选指标”拆成独立任务：已验证的生产任务成功，指标验证任务可以作为可选分支失败。这样下游通过现有成功生产者消费产物，无需把 failed 全局视为可用。

## 8. 实施拆分与兼容处理

| 批次 / 提交 | 修改范围 | 交付门槛 |
|---|---|---|
| A1：等待与传播 | scheduler_dispatch、scheduler_state、单节点入口、执行围栏、原因分类 | SCH-01 修复；审批和问题原有正确入口保持正确 |
| A2：终态裁决 | scheduler_execute、scheduler_events、终结 helper、预算先后 | SCH-02 修复；全部新终态有 Outcome，固定时序测试通过 |
| A3：阻断持久化 | models、传播 / reset、runtime_actions、plan_questions、前端快照与通知 | SCH-03 修复；刷新和恢复后原因一致，多根因恢复正确 |
| B1：partial 契约 | models、planner_graph、planner_names_validation、API 验证 / diff / import / export、ContextBinder、前端合同 | 新字段可审阅、可往返，硬 / 可选依赖一致；旧计划默认严格 |
| B2：partial 汇总 | semantic_validation、计划终结、前端结果展示 / 通知、记忆与导出 | SCH-04 修复；核心成功 + 可选失败能真实产出 partial |

先完成第一批，再发布第二批。SCH-05 随 A1 收敛，不另保留没有作用的策略开关。

兼容要求：

- 新字段使用可选 JSON 字段，旧计划不自动变为容错计划。副本、修订、导入、导出及回放均保留新合同。
- 审批决定只改变合法的对应待审批动作；保留 owner、权限、revision、幂等与取消后不得复活的校验。
- 历史 `Outcome=""` 只在能从状态和事件明确重建时补全；无法判定则在读取层显示“历史结果未记录”，不擅自判为 partial，不自动启动旧计划。
- 历史 blocked 无结构化原因时可以从事件回退展示；不得用猜测的根因授权继续执行。
- 第二批发布前，所有读写计划合同的客户端均需能保留 optional / acceptance 信息。降级到忽略这些字段的旧执行器会改变语义，因此回滚时停止派发相关新计划，保留数据和明确的不兼容提示；不能静默按旧规则执行。
- 实施后同步 scheduler / planner README、中英文使用说明及实际验收报告；架构流程实际变化时再按仓库约定同步图源和预览。

## 9. 回归与验收用例

下列用例是待实现的验收要求，不是已经通过的修复测试报告。并发用例使用 channel / barrier 固定交错顺序，不能只靠 sleep 或循环碰概率。

| 用例 | 场景 | 必须断言 |
|---|---|---|
| A-01 | 前置审批 pending | 上游 awaiting_approval；后继 pending；无 upstream_failed |
| A-02 | 执行中请求工具审批 | 与 A-01 相同；无虚假 Failure、FinishedAt 或失败重试 |
| A-03 | 批准 A-02 后恢复 | 原任务和后继可以完成；已完成工具副作用不重复执行 |
| A-04 | 结构化问题待回答 / 回答后恢复 | 等待与恢复均不误连坐；不绕过未满足硬依赖 |
| A-05 | 陈旧执行返回 Question / RuntimeAction | 状态、问题、审批和后继均不被旧结果修改 |
| A-06 | 仓库缺失 / 数据形状不匹配 | 根因 blocked；硬后继 upstream_blocked；计划 failed/blocked |
| A-07 | 真实失败可重试 / 不可重试 | 重试期间不阻断后继；最终失败才传播 upstream_failed；独立分支继续 |
| A-08 | 读取快照后，下游刚好全部 blocked | 重新判定为已终结；Outcome 正确且不为空，不误报死锁 |
| A-09 | failed 上游 + pending 后继，未提前传播 | 裁决补全原因或明确报告依赖失败；无空 Outcome 和悬空终态 |
| A-10 | 存在合法 awaiting / 延迟重试 / 暂停 | 不判为死锁，不提前 finalizer |
| A-11 | 真正无法满足的依赖 / 缺失必需产物 | failed/failed；原因包含节点、依赖或产物键 |
| A-12 | 拒绝、审批超时、问题超时 | 保留各自停止策略；OutcomeBlocked 不被取消后继覆盖 |
| A-13 | 多次唤醒、清理回调和事件重放 | 不重复执行本轮终态副作用；状态和事件 Outcome 一致 |
| A-14 | 快照刷新 / SSE 断线重连 | 阻断原因仍可见，无需依赖内存日志 |
| A-15 | 两个上游同时阻断，只重试其中一个 | 保留另一个有效原因，后继不能提前 ready |
| A-16 | cancel、retry、reset、重新进入等待 | 新终态有 Outcome；新一轮清除旧 Outcome / 原因 / FinishedAt |
| A-17 | 已有 ready，但全局并发槽暂时耗尽 | 等待资源，不写死锁终态 |
| A-18 | 选出 ready 后、获取租约前节点被阻断 | 拒绝旧派发；后继执行器未被调用 |
| B-01 | 核心 A 成功，可选 B 失败，报告 C 可降级 | C 执行；B 保持 failed；计划 completed/partial，列出损失和验收依据 |
| B-02 | 核心任务失败，部分其他节点成功 | failed，不能因成功节点数量大于零而 partial |
| B-03 | 核心证据不足 / 证据图 degraded | needs_review，不能用 partial 掩盖缺证据 |
| B-04 | 旧计划无 optional 字段 | 全部按必需任务处理，维持严格依赖 |
| B-05 | 可选任务被核心必需产物引用 / 混合关系成环 | 规划或 API 校验拒绝矛盾合同，不在执行时死锁 |
| B-06 | 仅可选依赖、awaiting 可选任务、禁用节点 | 初始 ready 不绕过等待；skipped 不伪造必需产物；禁用本身不制造 partial |
| B-07 | 全部可选、无核心目标 / 无验收依据 | 拒绝无效新合同，或判 needs_review；不能空洞 partial |
| B-08 | smoke / mock 且可选任务损失 | 保留 smoke / mock，详情列损失，不升级为真实复现 |
| B-09 | 失败任务留下同名产物 | 硬后继仍不能运行，可选消费者不把诊断产物当可信证据 |
| B-10 | 最后一次 attempt 自然结束 / 尚有剩余工作 | 前者正常汇总，后者预算取消；运行中已触发取消不得反改 partial |
| B-11 | revision / 导入导出 / clone / 重跑 | optional、验收合同、损失详情和来源不丢失，不读取旧轮产物 |
| B-12 | partial / blocked / needs_review 的 UI 和通知 | 与真实 Outcome 一致，刷新和历史恢复后不显示为全部成功 |

建议新增正式测试文件：`scheduler/result_transition_test.go`、`scheduler/terminal_reconcile_test.go`、`scheduler/blocked_reason_test.go`、`scheduler/partial_outcome_test.go`；补充现有 `planner/plan_finalizer_test.go`、API 审批 / 问题 / revision 测试与前端状态恢复用例。

实施阶段在 `research-assistant/backend` 运行：

```powershell
go test ./internal/scheduler ./internal/planner ./internal/api
go test -race -count=1 ./internal/scheduler ./internal/planner ./internal/api
```

`-race` 需具备可用的 Go race / C 工具链；即便未报告数据竞争，也必须通过固定交错的业务时序用例。先完成对应批次的定向测试，再运行上述包级回归。

涉及前端时，在 `research-assistant/frontend` 运行：

```powershell
npm test
npm run build
```

还需用真实后端验证一条“请求工具审批 → 批准 → 下游继续”的链路，以及一条“核心任务成功、可选指标失败 → partial”的链路；保存最终快照和事件断言，不以截图代替后端结果检查。

## 10. 完成标准

- [x] 第一批用例全部通过：等待不误连坐，终态裁决无竞态空洞，阻断原因可持久化和恢复。
- [x] 第二批用例全部通过：partial 有执行前合同、有核心验收依据、有明确损失，不篡改失败节点和科学证据。
- [x] 计划、节点、事件、界面、通知、导出和记忆使用一致的结果语义。
- [x] 兼容旧计划，审批与执行围栏不退化，预算取消不被泛化成 blocked。
- [x] 提交正式回归测试及验收报告，将文档状态从“待实施”更新为有证据支持的实际状态。

两轮独立复核指出的问题均已修复：第一轮（`reports/scheduler-independent-review-2026-11-11/review.md`）9 类功能遗漏与 1 项字段细节，回归见 `internal/scheduler/terminal_rules_test.go`；第二轮（`reports/scheduler-independent-review-2026-11-12/review.md`）6 项相邻路径遗漏（并发租约、pending 问题、同 revision 重试认证、claims 阈值可信度、API 惰性超时收尾、required 任务被跳过），回归见 `internal/scheduler/dispatch_boundary_test.go` 与 `dispatch_idempotence_test.go`。两个复核脚本可原地复现：17 项全部通过。

证据位置：`research-assistant/backend/internal/scheduler/{result_transition,terminal_reconcile,blocked_reason,partial_outcome,tool_approval_wait,terminal_wait}_test.go`、`internal/planner/plan_acceptance_test.go`、`internal/api/{plan_acceptance,plan_blocked_recovery}_test.go`、`frontend/test/components/{planOutcome,GraphPanelOutcome}.test.tsx`、在线验收脚本 `research-assistant/test/scheduler-acceptance.mjs` 与 `reports/scheduler-acceptance-2026-11-11.json`。