# scheduler

`scheduler` 负责执行 `PlanGraph`：按依赖调度节点、维护任务租约、处理重试/取消、执行仓库准备，并把 Artifact 路由给下游 Agent。

## 相关架构图

- [运行流程图](../../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../../../docs/diagrams/agent-execution-flow.svg)）

## 主要文件

| 文件 | 职责 |
|---|---|
| `scheduler.go` | 调度器核心、并发槽、停滞检测、结果外置与计划级清理 |
| `scheduler_control.go` | pause / resume / cancel / retry / reassign / 从节点重跑 |
| `scheduler_execute.go` | 计划与单节点执行入口、就绪节点提升 |
| `scheduler_dispatch.go` | 就绪节点选择、并发派发、任务用量与日志转发 |
| `scheduler_state.go` | 任务开始/完成状态迁移、Artifact 校验标注 |
| `scheduler_lease_approval.go` | 失败处理、租约过期、审批超时、预算与阻塞 |
| `scheduler_events.go` | 事件发布与终态汇总辅助 |
| `executor.go` | 单节点执行、Agent 调用、Prompt 组装、Artifact 写入 |
| `graph.go` | DAG 入度、就绪队列与依赖管理 |
| `policy.go` | 调度策略与预算判断 |
| `runtime_gate.go` | 运行时审批门禁（镜像/依赖/数据/GPU/网络） |
| `repo_discovery.go` | GitHub 仓库发现、元数据与 revision 解析 |
| `repo_prepare.go` | 克隆、工作区准备、依赖分析与运行时准备 |
| `reproduction_mode.go` | 论文复现模式与 smoke 约束 |

## 执行模型

1. 从 Plan Store 读取计划，计算每个节点入度。
2. 入度为 0 的节点进入 ReadyQueue，由 Worker 并发执行。
3. 节点执行前获取租约，防止重复执行；完成后写入 Artifact 并发布事件。
4. 节点失败时按策略重试、标记阻塞或取消后继节点。
5. 计划达到终态后汇总状态和事件。

## 任务控制与运行时审批

- 计划级：`pause` 停止派发新节点，运行中的节点自然结束；`resume` 继续；`cancel` 向运行节点传播 `context.Cancel`。
- 任务级：`retry` 重置失败/阻塞/取消节点及其阻塞后继；`reassign` 修改 `assigned_to` 并重置任务；`run`（从节点重跑）重置指定节点及下游后重新执行。
- `RuntimeActionRequest`：镜像拉取、依赖安装、数据/模型下载、GPU、网络访问等操作在 `confirm_important` 模式下暂停计划，等待计划所有者 `approve/reject`；`full_access` 在安全边界内自动执行并记录审计。
- 运行时动作有超时窗口（`APPROVAL_TIMEOUT_SECONDS`）；拒绝或超时会终止计划并标记 `outcome=blocked`，不会把等待审批当作终态。

## 结果校验

- `PlanFinalizer` 根据节点状态、Artifact、EnvironmentManifest、TrialLedger 与 Claim-Evidence 生成 `PlanOutcome`（completed/partial/smoke/mock/blocked/failed/needs_review）。
- 视觉校验结果写入 `plan.verification`、`node.verification`、`artifact.metadata.verification` 与 `verification_*` 指标；视觉 fail 是 advisory，不伪造 completed。

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
