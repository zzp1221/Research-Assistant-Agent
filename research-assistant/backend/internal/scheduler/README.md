# scheduler

简体中文 | [English](README.en.md)

`scheduler` 负责执行 `PlanGraph`：按依赖调度节点、维护任务租约、处理重试/取消、执行仓库准备、注入记忆，并把 Artifact 路由给下游 Agent。

## 相关架构图

- [运行流程图](../../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../../../docs/diagrams/agent-execution-flow.svg)）

## 主要文件

| 文件 | 职责 |
|---|---|
| `scheduler.go` | 调度器核心、并发槽、停滞检测、结果外置与计划级清理、MemorySink 接口 |
| `scheduler_control.go` | pause / resume / cancel / retry / reassign / 从节点重跑 |
| `scheduler_execute.go` | 计划与单节点执行入口、就绪节点提升、终态记忆写入 |
| `scheduler_dispatch.go` | 就绪节点选择、并发派发、任务用量与日志转发 |
| `scheduler_state.go` | 任务开始/完成状态迁移、Artifact 校验标注 |
| `scheduler_lease_approval.go` | 失败处理、租约过期、审批超时、预算与阻塞 |
| `scheduler_events.go` | 事件发布与终态汇总辅助 |
| `executor.go` | 单节点执行、Agent 调用、Prompt 组装、记忆注入、Artifact 写入 |
| `graph.go` | DAG 入度、就绪队列与依赖管理 |
| `policy.go` | 调度策略与预算判断 |
| `runtime_gate.go` | 运行时审批门禁（镜像/依赖/数据/GPU/网络）与 tool_call 审批门 |
| `repo_discovery.go` | GitHub 仓库发现、元数据与 revision 解析 |
| `repo_prepare.go` | 克隆、工作区准备、依赖分析与运行时准备 |
| `reproduction_mode.go` | 论文复现模式与 smoke 约束 |

## 执行模型

1. 从 Plan Store 读取计划，计算每个节点入度。
2. 入度为 0 的节点进入 ReadyQueue，由 Worker 并发执行。
3. 节点执行前获取租约，防止重复执行；完成后写入 Artifact 并发布事件。
4. 节点失败时按策略重试、标记阻塞或取消后继节点。
5. 计划达到终态后汇总状态和事件，并调用 `MemorySink.RecordPlan` 写入计划级记忆。

## 任务控制与运行时审批

- 计划级：`pause` 停止派发新节点，运行中的节点自然结束；`resume` 继续；`cancel` 向运行节点传播 `context.Cancel`。
- 任务级：`retry` 重置失败/阻塞/取消节点及其阻塞后继；`reassign` 修改 `assigned_to` 并重置任务；`run`（从节点重跑）重置指定节点及下游后重新执行。
- `RuntimeActionRequest`：镜像拉取、依赖安装、数据/模型下载、GPU、网络访问等操作在 `confirm_important` 模式下暂停计划，等待计划所有者 `approve/reject`；`full_access` 在安全边界内自动执行并记录审计。
- 运行时动作有超时窗口（`APPROVAL_TIMEOUT_SECONDS`）；拒绝或超时会终止计划并标记 `outcome=blocked`，不会把等待审批当作终态。
- `SetToolGate` 把 `tool_call` 节点的 `AllowedTools` / `ToolGrant` 判断接入同一审批门：必然被拒绝的调用不会弹审批。

## 结果流转与等待态

- 节点结果只经过一个入口 `applyTaskResult`：等待（审批 / 问题）、完成、`blocked`、`failed`、`canceled`、未知协议错误。自动派发与手动单节点执行共用它，不存在第二份"非 completed 即失败"的判断。
- `awaiting_approval` / `awaiting_input` 必须在同一状态提交内写入对应的 pending 审批或问题记录；裸等待状态返回协议错误，不写状态。等待不写 `Error` / `FinishedAt` / `Failure`，不扣失败重试，恢复检查点置 `Waiting=true、PendingRetry=false`，并释放执行租约。
- 执行异常优先于等待：已取消、已超时或执行器明确报错的结果即使附带 Question / RuntimeAction 也按终结处理，附件只留作诊断。
- `tool_call` 节点遇到"需要审批"时返回 `awaiting_approval` 而不是失败；批准后用同一 call_id / 参数恢复，已完成工具副作用不会重复执行。
- 执行围栏统一校验 `ExecutionID / LeaseOwner / ExecutionEpoch`，问题记录与审批记录同样受约束；陈旧结果不能写等待、问题、审批或后继状态。
- 派发在获取租约时重新确认节点可执行：`markTaskStarted` 的 `dispatchGuard` 检查启用状态、硬依赖、产物、待审批动作，手动入口复用同一 `dispatchLegality`。

## 失败传播与阻断原因

- 硬依赖只在一处定义：`hardDependencyIDs` = 节点 `Dependencies` + 产物生产者（`data` 边）。ready 判断、初始状态、失败传播、恢复共用这一份。
- 上游 `failed` / `blocked` / `canceled` 在同一个 `UpdatePlan` 内按 `upstream_failed` / `upstream_blocked` / `upstream_canceled` 阻断硬后继；`awaiting_*` 不传播，后继保持 pending。独立分支不受影响。
- `TaskNode.block_info` 记录 `code/message/causes[]/occurred_at`；`causes[]` 保留全部根因（`root_task_id / via_task_id / revision / execution_epoch / status`），事件从这里构造，同时保留 `task_blocked / upstream_task_id` 兼容字段。
- 恢复按根因重算：某根因重试后只移除它旧轮次的 cause；仍有其他有效 cause 则继续 blocked；全部消失才回 `pending` 并按依赖重新判定 ready。升级前无结构化原因的 blocked 节点不自动放行，需显式重跑。

## 终态裁决

- `reconcilePlanState` 是唯一终态入口：外层快照只决定何时尝试，所有判断在提交时的当前图上完成，避免"快照 A=blocked、B=pending，实际已全终结"写出空 `Outcome`。
- 顺序：保留显式停止 → 提升可运行节点 → 有 ready 但暂时无法派发（全局并发槽不足）继续等待 → 合法等待 / 延迟重试 / 暂停 → 全终结汇总 → 真正的依赖死锁。
- 终态评估值与 `Status / Outcome / OutcomeReason / OutcomeDetails / FinishedAt` 一次提交；终态事件、清理、预算释放、记忆与通知只在提交成功的迁移上执行一次。
- `PlanFinalizer` 对每个已结束的图调用（不再只在"无 failed"时调用）；顺序为停止语义 → 核心失败 `failed` / 核心阻断 `blocked` → 证据不足 `needs_review` → mock/smoke → 验收合同 → 仅可选损失 `partial` → `completed`。
- 预算只阻止新的启动：全图已终结时按验收结果汇总；仍有应执行任务但没有剩余 attempts 才 `CancelPlan("run budget exceeded")`。

## 部分成功合同

- 新字段（可选，旧计划缺省为严格必需）：`TaskNode.optional`、`TaskNode.optional_dependencies`、`PlanGraph.acceptance`、`PlanGraph.outcome_details`。
- `optional` 决定"失败是否破坏核心目标"；`optional_dependencies` 只决定"是否必须等待生产者成功"。两者不能互换：硬 `dependencies` 仍要求 completed/skipped 与必需产物。
- 图关系新增 `optional` 边；规划与 API 校验都同时检查硬/可选关系：同一依赖不得同时属于两类，不得跨类成环，可选生产者的产物不得成为必需输入。
- `acceptance` 在审阅前冻结：至少一个核心目标，每个目标必须有可验证证据（产物键或 `min_verified_claims`），全部任务可选时拒绝生成合同。
- `outcome_details` 记录已通过的核心目标、可选损失、缺失产物与限制说明；界面、通知、导出和记忆都读同一份结果。

## 记忆注入

`RoutedTaskExecutor` 在调用 Agent 前从 `sharedContext` 读取：

- `core_memory`：稳定前缀，放在 prompt 前部。
- `retrieved_memory`：按任务检索的 episode/procedure/negative。
- `memory_refs`：写入计划与 Artifact 元数据。

任务结束调用 `MemorySink.RecordTask`；计划终态调用 `RecordPlan`。记忆失败只记录日志，不影响任务结果。

## 结果校验

- `PlanFinalizer` 根据节点状态、Artifact、EnvironmentManifest、TrialLedger 与 Claim-Evidence 生成 `PlanOutcome`（completed/partial/smoke/mock/blocked/failed/needs_review）。
- 视觉校验结果写入 `plan.verification`、`node.verification`、`artifact.metadata.verification` 与 `verification_*` 指标；视觉 fail 是 advisory，不伪造 completed。
- Artifact 必须非空、类型合法且 producer 唯一；不满足时任务失败而不是伪造成功。

## 租约与恢复

- 任务租约包含执行者标识和过期时间；旧租约的结果会被隔离。
- 文件 Plan Store 使用原子写入，服务重启后可恢复未完成计划。
- 取消会向运行中的节点传播 `context.Cancel`，并停止派发新节点。

## 仓库任务

`repo_discovery.go` 支持从用户输入或论文信息中解析 GitHub 仓库，记录默认分支、commit 和获取方式。`repo_prepare.go` 负责克隆、清理工作区、生成依赖清单，并在受限范围内准备运行时。

## 测试

```bash
go test ./internal/scheduler/...
go test -race -count=1 ./internal/scheduler/...
```

测试覆盖 DAG 调度、租约、重试/改派/从节点重跑、运行时审批、仓库发现/准备、复现模式和验证结果汇总。真实 Git/Docker 用例需要网络和 daemon。

调度语义的回归集集中在 `result_transition_test.go`、`terminal_reconcile_test.go`、`blocked_reason_test.go`、`partial_outcome_test.go`、`tool_approval_wait_test.go`、`terminal_wait_test.go` 与 `dispatch_and_round_test.go`；它们用 channel/barrier 固定交错顺序，不依赖 sleep 碰概率。Windows 主机上 `repo_prepare_test.go` 的 4 个用例需要 `core.symlinks=true` 与 `core.autocrlf=false`，与调度逻辑无关。