# planner

`planner` 把用户意图转换为可执行的 `PlanGraph`。它是模型输出与确定性调度之间的边界：模型可以提出计划，但只有通过结构校验的 DAG 才会进入 Scheduler。

## 相关架构图

- [运行流程图](../../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../../docs/diagrams/runtime-flow.svg)）
- [系统架构图](../../../../docs/diagrams/architecture.drawio)（[SVG 预览](../../../../docs/diagrams/architecture.svg)）

## 主要文件

| 文件 | 职责 |
|---|---|
| `planner.go` | 主规划器、任务模板、意图到 DAG 的映射 |
| `agent_planner.go` | 基于 Agent 的规划入口与模型交互 |
| `planner_test.go` | 计划结构、依赖与校验测试 |

## 规划流程

1. 接收 `IntentContext`、附件和用户约束。
2. 根据意图选择任务模板（论文复现、代码执行、Benchmark、AutoResearch、通用问答等）。
3. 生成节点、依赖边、Agent 分配和输入输出契约。
4. 校验 DAG：无环、依赖存在、必填输入完整、节点类型合法。
5. 返回 `models.PlanGraph`，由 API 写入 Plan Store。

## 设计约束

- 规划器不执行任务，也不直接访问 Docker。
- 所有节点必须有明确的 `assigned_to` 和任务类型。
- 涉及论文复现时，计划必须包含仓库准备、运行时准备、实验执行和结果对比节点。
- 涉及 AutoResearch 时，计划必须包含契约冻结、候选搜索、Holdout 验证和报告节点。
- 模型输出不可直接信任；解析失败时回退到确定性模板或返回明确错误。

## 测试

```bash
go test ./internal/planner/...
```

测试覆盖 DAG 结构、依赖关系、意图映射和错误输入。
