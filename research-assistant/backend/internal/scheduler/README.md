# scheduler

`scheduler` 负责执行 `PlanGraph`：按依赖调度节点、维护任务租约、处理重试/取消、执行仓库准备，并把 Artifact 路由给下游 Agent。

## 相关架构图

- [运行流程图](../../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../../../docs/diagrams/agent-execution-flow.svg)）

## 主要文件

| 文件 | 职责 |
|---|---|
| `scheduler.go` | 调度循环、并发控制、节点状态机 |
| `executor.go` | 单节点执行、Agent 调用、Artifact 写入 |
| `graph.go` | DAG 入度、就绪队列与依赖管理 |
| `policy.go` | 调度策略与预算判断 |
| `repo_discovery.go` | GitHub 仓库发现、元数据与 revision 解析 |
| `repo_prepare.go` | 克隆、工作区准备、依赖分析与运行时准备 |
| `reproduction_mode.go` | 论文复现模式与 smoke 约束 |

## 执行模型

1. 从 Plan Store 读取计划，计算每个节点入度。
2. 入度为 0 的节点进入 ReadyQueue，由 Worker 并发执行。
3. 节点执行前获取租约，防止重复执行；完成后写入 Artifact 并发布事件。
4. 节点失败时按策略重试、标记阻塞或取消后继节点。
5. 计划达到终态后汇总状态和事件。

## 租约与恢复

- 任务租约包含执行者标识和过期时间；旧租约的结果会被隔离。
- 文件 Plan Store 使用原子写入，服务重启后可恢复未完成计划。
- 取消会向运行中的节点传播 `context.Cancel`，并停止派发新节点。

## 仓库任务

`repo_discovery.go` 支持从用户输入或论文信息中解析 GitHub 仓库，记录默认分支、commit 和获取方式。`repo_prepare.go` 负责克隆、清理工作区、生成依赖清单，并在受限范围内准备运行时。

## 测试

```bash
go test ./internal/scheduler/...
```

测试覆盖 DAG 调度、租约、重试、仓库发现/准备和复现模式。真实 Git/Docker 用例需要网络和 daemon。
