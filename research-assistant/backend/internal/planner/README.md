# planner

`planner` 把用户意图转换为可执行的 `PlanGraph`。它是模型输出与确定性调度之间的边界：模型可以提出计划，但只有通过结构校验的 DAG 才会进入 Scheduler。

## 相关架构图

- [运行流程图](../../../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../../../docs/diagrams/runtime-flow.svg)）
- [系统架构图](../../../../docs/diagrams/architecture.drawio)（[SVG 预览](../../../../docs/diagrams/architecture.svg)）

## 主要文件

| 文件 | 职责 |
|---|---|
| `planner.go` | 主规划器、任务模板、意图到 DAG 的映射；`BuildPlanWithRoute` 入口 |
| `route.go` | `ComplexityRouter`：L0–L4 规则评分、route/variant 决策、confirmer hook |
| `plan_builder.go` | `TemplateKey{IntentType,Variant}` registry 与 simple/standard/full 模板 |
| `plan_variants.go` | General/Code/Framework/Paper 的模板变体实现 |
| `general_plan.go` | General 计划形状：direct/research/paper/literature 确定性分类 |
| `agent_planner.go` | 基于 Agent 的规划入口、skeleton 合并与 repair 会话 |
| `taskregistry.go` | 任务类型到 Agent 的注册表 |
| `planner_test.go` | 计划结构、依赖、路由、变体与校验测试 |

## 规划流程

1. 接收 `IntentContext`、附件和用户约束。
2. 根据意图与复杂度选择任务模板（论文复现、代码执行、Benchmark、AutoResearch、通用问答等）。
3. 生成节点、依赖边、Agent 分配和输入输出契约。
4. 校验 DAG：无环、依赖存在、必填输入完整、节点类型合法。
5. 返回 `models.PlanGraph`，由 API 写入 Plan Store。

### 复杂度路由与模板变体

- `ComplexityRouter.Decide` 在附件/论文上下文解析后做最终路由：规则评分优先，L0/L1 走 fast path（不调用 planner LLM），L2 走标准模板，L3/L4 走 skeleton + agent 增强。
- General 计划按 `general_plan.go` 的规则选择 direct（1 节点）/ research（2 节点）/ paper（4 节点）/ literature（3 节点）；`GENERAL_PLAN_FAST_PATH=false` 可回退旧 3 节点。
- 模板变体：General / Code_Execution / Framework_Evaluation 提供 simple/standard/full，Paper_Reproduction 提供 light/standard/full；其他 intent 默认 standard。
- 路由与变体写入 `GraphMeta`（`planning_route` / `complexity` / `complexity_source` / `template_variant` / `general_plan_shape`），并暴露到 SSE、计划详情与 `/api/metrics`。

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
