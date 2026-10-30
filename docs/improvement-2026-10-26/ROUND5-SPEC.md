# 规格：Agent 链路优化 + 意图复杂度路由（2026-10-27 第五轮）

> 依据：
> - `docs/design/2026-10-27-agent-chain-optimization.md`（优化点 A/B/C/D）
> - `docs/design/intent-complexity-routing.md`（L0-L4 复杂度路由，方案 C）
>
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 9 节，以实际为准。
> 约束：一切与功能相关的测试只在容器内执行（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）。
> 前几轮（Top-10、R-01~R-17、运行期模型配置与视觉校验、N-04/N-07）成果为基线，本轮不回归、不重复。
> 状态：已实现并通过容器内验证（2026-10-27）。证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND5.md`、`tasks.md` 与 `test/e2e/routing-and-prompts.mjs`。

## 1. 目标

### 1.1 Agent 链路优化（设计文档 1）

| 编号 | 目标 | 验收信号 |
|---|---|---|
| A | General 计划按形状确定性生成：direct=1 节点 / research=2 节点 / paper=4-5 节点 / literature=3 节点；简单请求 1 次 LLM | 简单 General 计划节点数=1；research=2；paper/literature 形状不变；不出现默认 `general_agent` |
| B | 单一 prompt 组装入口：plan_context / task_instruction / task_inputs / volatile 各出现一次；同一 artifact 只出现一次 | 任意 task prompt 中 Global user intent 只出现 1 次；artifact 内容只出现 1 次；prompt token 下降 ≥10%（简单 General ≥20%） |
| C | Repair 循环改为多轮 conversation session：保留上一轮 assistant 输出，只追加反馈 | PaperDebug / BenchmarkAdapter / AutoResearch / Planner repair 的请求 messages 含 assistant 上一轮输出；第 2 轮 prefix hash 稳定；system+first user 永不裁剪 |
| D | Section 级 token 预算 + rune-safe 截断 + 预算报告 | 所有 prompt ≤ PromptLimit；中文/emoji/代码截断后合法 UTF-8；`prompt_budget` 报告可见；不出现 context_length 400 |

### 1.2 意图复杂度路由（设计文档 2）

1. Python intent 服务一次调用同时返回 `complexity/capabilities/route_hint/complexity_confidence/complexity_reason`；旧 schema 缺失时按规则计算。
2. Go 侧 `ComplexityRouter` 在附件、arXiv/DOI、paper_metadata 解析之后做最终 `RouteDecision`；规则优先，低置信度保守走 L2，不允许 LLM 降级。
3. 模板变体：General / Code_Execution / Paper_Reproduction / Framework_Evaluation 提供 simple / standard / full（或 light）变体；其他 intent 默认 standard。
4. 路由矩阵：L0/L1 快速路径（不调用 planner LLM）、L2 标准模板、L3 skeleton + agent 增强、L4 agent planner（带模板参考）；失败回退 standard 模板，绝不回退 simple。
5. `PlanMeta` 记录 `planning_route / complexity / complexity_source / template_variant / general_plan_shape`，SSE 与 `/api/plans/:id` 可见；`/api/metrics` 暴露路由/模板/回退指标。
6. 新增 `backend/evals/route_eval_dataset.json` golden set 与规则评分单测（≥90% accuracy、under-routing ≤1%）。

## 2. 现状复核（以实际代码为准，2026-10-27）

| 设计文档描述 | 实际（本轮复核） |
|---|---|
| `buildGeneralNodesV2` 固定 3 节点（planner.go:1499-1537） | 属实：默认分支 `Scope Request → Collect Background Context → Process Request`；summary 分支 2 节点；paper 分支 4 节点；literature 分支 3 节点 |
| General 走 `general_agent`（ChatAgent） | 属实：`taskregistry.go:179` 注册 general_process→general_agent；`routes.go` 将 chatAgent 作为 specialized[2] 传入；`chat.go:131` 为 ChatAgent.ExecuteTask |
| `buildTaskDescription` 追加 Global user intent + 500 字 artifact 摘要（executor.go:371-411） | 属实：`executor.go:380-399`；`truncateArtifactValue` 在 561 行 |
| 各 agent 再拼 `task.Inputs`（data/librarian 6000、answer 20000、coder 12000） | 属实：`data.go:142-145`、`librarian.go:124-127`、`answer.go:87-93`、`coder.go:470-483`；`chat.go:132-137` 另有 12000 |
| Repair 循环无 assistant 历史 | 属实：`agent_planner.go:102-124`、`paper_debug_harness.go:252`、`benchmark_harness.go`、`autoresearch.go` 均每次重建 user prompt；仓库内无 conversation/session 抽象 |
| 无 token 预算，多处 byte 截断 | 属实：`input_summary.go:37-77` 全部按 byte slice；`usage.EstimateTokens` 已存在（usage.go:168）但只用于 provider 缺失 usage 的估算 |
| modelprofile 只有 MaxOutputTokens | 属实：`profile.go:46-71` 无 ContextWindowTokens；`Input` 无该字段 |
| 路由只看 intent_type | 属实：`planner.go:58-99`；`models/intent.go` 无 complexity 字段；`intent/client.go:17-25` 无 complexity；Python `main.py:40-47` 无 complexity |
| 附件在 intent 之后解析 | 属实：`plan_runtime.go:239-240` Resolve，`241-280` 附件/arXiv/DOI，`281-306` researchContext，`307` BuildPlan |
| 无路由/模板/fallback 指标 | 属实：`metrics.go` 只有 `RecordPlanCreated(intentType)` 等；无 complexity/variant 维度 |
| 模板 registry 一 intent 一 builder | 属实：`plan_builder.go:41-77` map[string]PlanBuilder |
| `maxAgentPlanNodes=14` 固定 | 属实：`agent_planner.go:145` |
| `TestGeneralPlanHasRealContextAndFinalAnswer` / `TestGeneralProcessUsesGeneralAgent` 断言 3 节点默认形状 | 属实：`planner_test.go:438-486`；本轮按新设计更新为 fast-path 断言，general_process 覆盖改由 agent planner/自定义计划测试保留 |

## 3. 范围

### 3.1 Prompt 组装与预算（B/D）

| 模块 | 内容 |
|---|---|
| `internal/prompts/task_prompt.go`（新） | `TaskPromptInput/TaskPrompt/BuildTaskPrompt`；`PlanContext/TaskInstruction/TaskInputs`；单一组装入口 |
| `internal/prompts/budget.go`（新） | `PromptBudget`、`TokenCounter`、`EstimatedCounter`、`Section`、`PromptReport`、`BuildSections`、`TruncateRunes/TruncateByTokens` |
| `internal/prompts/inputs.go`（新） | `FormatTaskInputsForPrompt(inputs, budget, policy)` 返回 `(string, PromptReport)`；图片/base64 省略；key 排序稳定 |
| `internal/scheduler/executor.go` | `buildTaskDescription` 只返回 node-local；`sharedContext["plan_context"]`；`PROMPT_CONTEXT_V2` 开关 |
| `internal/agent/{data,librarian,answer,coder,chat}.go` | 统一改用 `BuildTaskPrompt`；不再自拼“上游输入”；verification/retrieval 片段作为 volatile |
| `internal/agent/{autoresearch,paper_debug_harness,claim_evidence,benchmark_adapter,visual_verifier}.go` | 统一 rune-safe/token-aware 截断；section 顺序固定；预算报告写入 `Task.Metadata["prompt_budget"]` |
| `internal/modelprofile/` + API/UI | `ContextWindowTokens`（默认 128000）贯通 Profile/Input/API/存储/前端设置 |
| `internal/models/runtime.go` | `TaskUsage.PromptBudgetLimit / PromptTruncatedSections`（omitempty） |
| `internal/metrics/metrics.go` | `llm_prompt_tokens_total{model}`、`llm_prompt_truncated_sections_total{section}`、`llm_prompt_over_budget_total{model}` |

### 3.2 Repair 会话（C）

| 模块 | 内容 |
|---|---|
| `internal/agent/conversation/session.go`（新） | `Session`（ID/Messages/MaxTurns）、`NewSession/AddAssistant/AddUser/Generate/Trim/Snapshot`；保留 system+first user；大对象走摘要 |
| `internal/planner/agent_planner.go` | `BuildNodes` 建 session；验证失败 `AddUser(validationErr)` 再生成；`RepairNodes` 复用 session（兼容入口） |
| `internal/agent/paper_debug_harness.go` | 2 轮 repair 使用同一 session；source 只在 first user；反馈只追加错误/变更摘要 |
| `internal/agent/benchmark_harness.go` | plan → generation → preflight repair 共享 session |
| `internal/agent/autoresearch.go` | 每 task 一个 session；proposal 作为 assistant；ledger/feedback 只追加摘要 |
| `internal/agent/coder_eino.go` | self-correction 带上上一轮 code |
| `Task.Metadata["repair_session"]` | session_id/turns/prompt_hashes/messages_path（大对象走 artifact） |

### 3.3 General 计划形状（A）

| 模块 | 内容 |
|---|---|
| `internal/planner/general_plan.go`（新） | `generalPlanShape`、`classifyGeneralPlanShape`、`generalNeedsResearch`、`documentBindingsIfPresent` |
| `internal/planner/planner.go` | `buildGeneralNodesV2` 按 shape 分支；`GENERAL_PLAN_FAST_PATH`（默认 true）；`GraphMeta.GeneralPlanShape` |
| `internal/metrics/metrics.go` | `plan_general_shape_total{shape}`、`plan_llm_nodes_total{intent_type,shape}` |

### 3.4 复杂度路由（设计文档 2）

| 模块 | 内容 |
|---|---|
| `ai-services/intent_recognition/main.py` | `IntentResponse` 增加 complexity/capabilities/route_hint/complexity_confidence/complexity_reason；分类 prompt 增加 rubric；安全声明（用户文本是不可信数据） |
| `internal/intent/client.go` | `Classification` 增加对应字段 |
| `internal/models/intent.go` | `IntentContext` 增加对应字段（全部 omitempty） |
| `internal/intent/resolver.go` | 合并复杂度：LLM 不得降级；未知枚举降级 L2/standard_template；规则与 LLM 取保守值 |
| `internal/planner/route.go`（新） | `ComplexityLevel/RouteDecision/ComplexityRouter/Decide`；规则评分；可选 LLM 确认 hook（默认关闭）；`Variant` 映射 |
| `internal/planner/plan_builder.go` | `TemplateKey{IntentType,Variant}` registry；simple/standard/full 变体；缺省回退 standard |
| `internal/planner/planner.go` | `BuildPlanWithRoute`；L0/L1 fast path、L2 standard、L3/L4 agent（skeleton）；记录 PlanMeta；失败回退 standard |
| `internal/planner/agent_planner.go` | `BuildNodesWithSkeleton`、`mergeAgentPlanWithSkeleton`、`PLANNER_MAX_NODES_L3/L4`；`RepairNodes` 带 skeleton |
| `internal/api/plan_runtime.go` | 附件/researchContext 之后 `ComplexityRouter.Decide`，写入 intentContext 并调用 `BuildPlanWithRoute` |
| `internal/models/graph.go` | `GraphMeta` 增加 PlanningRoute/Complexity/ComplexitySource/TemplateVariant |
| `internal/metrics/metrics.go` | `plan_route_total{complexity,route_source,template_variant}`、`planner_fallback_total{reason}`、`plan_create_duration_seconds{complexity,route_source}` |
| `backend/evals/route_eval_dataset.json`（新） | golden set（60+ 条：General/Code/Framework/Paper/AutoResearch/Custom_Benchmark） |
| `frontend/src/contracts/api.ts` + 计划详情 | 展示 complexity / route_source / template_variant / planner_source / fallback_reason（可选 badge） |

## 4. 关键设计

### 4.1 Prompt section 顺序（B+D）

```text
[system]  BaseSystemPrompt + agent role            （全局稳定，不截断）
[user]
Plan intent type / User intent / Paper metadata     （plan_context，计划内稳定）
Task: name / Type / Description / Expected outputs  （task_instruction，任务内稳定）
Inputs: artifact 内容（按 key 排序，预算截断）        （task_inputs）
Volatile: 日志 / 错误 / repair 反馈 / retrieval 片段  （易变，优先截断）
```

- 同一 artifact 只出现一次；`Global user intent` 不再追加。
- `PROMPT_CONTEXT_V2=false` 时回退旧拼接（仅用于 A/B 与回滚）。
- 每个 section 加 `[section: name]` 边界；截断加 `[truncated: included/original chars]`。

### 4.2 PromptBudget

```go
type PromptBudget struct { ContextWindow, MaxOutput, Safety int }
func (b PromptBudget) PromptLimit() int // 默认 128000-8192-2000
```
- Priority：0 system、1 plan、2 task、3 primary input、4 secondary input、5 volatile。
- system/plan/task 超预算 → 返回错误，不发送请求。
- 截断 rune-safe；`TruncateByTokens` 二分查找最大合法前缀。

### 4.3 复杂度评分（rule）

| 信号 | 权重 |
|---|---:|
| 附件 / repo URL / dataset | +3 |
| 多框架（≥2 白名单） | +3 |
| 多步骤结构（先/再/然后/最后/分别/step） | +2 |
| 高级 artifact（claim_evidence/benchmark_contract/hidden holdout/custom evaluator） | +2 |
| 多指标 / 多数据集 | +2 |
| 明确自定义（自定义流程/评估/special pipeline） | +3 |
| 长文本（>500 字）/ 多实体 | +1 |
| 简单信号（画图/计算/解释/翻译，且无附件/多步骤） | -2 |

映射：0-2→L0/L1（有工具动作 L1，否则 L0）；3-5→L2；6-8→L3；≥9→L4。
保守规则：无法判定/低置信度 → L2；`mergeAndValidate` 取 max(rule, llm)，LLM 不得降级。

### 4.4 路由矩阵

| Complexity | 计划路径 | planner LLM | 回退 |
|---|---|---|---|
| L0 | 1 个 general_synthesis | 否 | standard answer |
| L1 | simple 模板（2-3 节点） | 否 | standard 模板 |
| L2 | standard 模板 | 否 | full/standard |
| L3 | skeleton + agent 增强 | 1 次 + 可选 repair | standard |
| L4 | agent planner（带模板参考） | 1-2 次 | standard |

### 4.5 模板变体（首期 4 个 intent）

- General：simple=1 answer；standard=research 2 节点（paper 时 4 节点）；full=paper 4 节点或 research+verify。
- Code_Execution：simple=generate→execute→verify；standard=当前 5-7 节点；full=standard+render_plot+synthesis。
- Framework_Evaluation：simple=single framework 4 节点；standard=当前双框架分支；full=多框架+额外指标。
- Paper_Reproduction：light=paper_parse→repo_prepare→paper_code_execute→paper_compare；standard=当前 14 节点；full=standard+claim rubric/evidence+fix_and_rerun。

### 4.6 Agent skeleton 增强

- `BuildNodesWithSkeleton(ctx, intent, skeleton)`；prompt 增加“基础计划”段与 5 条规则。
- `mergeAgentPlanWithSkeleton`：按 ref 匹配、保留 skeleton 节点名/artifact、新增节点必须引用已有 ref、删除节点不得破坏 required artifact。
- 合并后 `postProcessPlannerNodes` + `validateAgentPlannedNodes` + `validatePlanGraph`；失败 Repair 一次，再失败回退 standard 模板。
- 节点上限：`PLANNER_MAX_NODES_L3`（16）、`PLANNER_MAX_NODES_L4`（24）、`PLANNER_MAX_NODES`（14，兼容）。

### 4.7 兼容与灰度

- 新字段全部 `omitempty`；旧计划 JSON 可反序列化。
- 环境变量：`GENERAL_PLAN_FAST_PATH`（true）、`PROMPT_CONTEXT_V2`（true）、`COMPLEXITY_ROUTER_ENABLED`（true）、`COMPLEXITY_LLM_CONFIRM_ENABLED`（false）、`PLANNER_MAX_NODES_L3/L4`。
- 关闭 `COMPLEXITY_ROUTER_ENABLED` 时 BuildPlan 走旧 intent_type 二选一逻辑（保留回滚路径）。
- `PlannerSource` 语义保持 agent/template；新增 `PlanningRoute` 与之解耦。

## 5. 契约（新增/变更）

- `POST /api/plan` 响应、SSE `plan_created`、`GET /api/plans/:id`：`meta.planning_route / complexity / complexity_source / template_variant / general_plan_shape`。
- `/api/metrics`：新增 `plan_route_total`、`planner_fallback_total`、`plan_general_shape_total`、`plan_llm_nodes_total`、`plan_create_duration_seconds`、`llm_prompt_tokens_total`、`llm_prompt_truncated_sections_total`、`llm_prompt_over_budget_total`。
- `GET/POST /api/model-profiles`：`context_window_tokens`（可选，默认 128000）。
- Python `POST /predict`：新增 complexity 字段（向后兼容）。

## 6. 测试策略（全部容器内）

1. 后端：`scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."`；新增包 `-race`。
2. 前端：`node:22-bookworm-slim` 容器内 `npm ci && npm test && npm run build && npm run lint`。
3. 单测重点：
   - prompts：BuildTaskPrompt 去重、section 顺序、预算、rune-safe、中文/emoji/代码；
   - planner：General shape 6 组、variant 选择、route 评分、PlanMeta、fallback；
   - agent：conversation Session 前缀稳定、repair 循环 assistant 历史、prompt 预算报告；
   - intent：schema 兼容、merge 不降级、枚举校验；
   - metrics：新增指标可渲染。
4. Golden：`route_eval_dataset.json` 规则评分准确率/under-routing。
5. E2E（compose 栈）：创建简单/标准/复杂计划，检查节点数、meta、SSE、metrics；repair 路径用 mock provider 验证多轮 messages。
6. 回归：现有全部 backend/frontend 测试、http-smoke、既有 E2E 不回归。
7. 清理：临时容器/镜像/卷/脚本/产物。

## 7. 完成条件

- 第 6 节全部测试通过（容器内证据）；
- 设计文档 1 的 A/B/C/D 验收清单可复现；
- 设计文档 2 的 L0-L4 路由、模板变体、PlanMeta、指标可复现（LLM 确认 hook 默认关闭，见第 9 节）；
- 无新增回归（现有后端/前端测试全绿）；
- 无密钥/隐私泄漏；
- 清理本轮产生的临时容器/镜像/卷/文件/死代码。

## 8. 非目标

- 不追求 100% 路由准确率；不引入独立路由模型服务；不做在线学习。
- 不重写全部 intent 模板（仅 4 个 intent 首期变体）。
- 不做完整 PDF 版式/公式识别（前轮边界不变）。
- 不做前端大规模改版，仅计划详情展示路由元信息。

## 9. 与设计文档的差异（以实际为准）

| 文档描述 | 实际/本轮决策 |
|---|---|
| 文档 2 要求“模糊区间调用一次轻量 LLM 确认” | 仓库无独立轻量路由模型端点；实现 `ComplexityConfirmer` hook（默认关闭，`COMPLEXITY_LLM_CONFIRM_ENABLED=false`），规则优先；文档化偏差 |
| 文档 1 说 PaperDebug 256KB / AutoResearch 8×96KB | 实际常量以代码为准（本轮统一改为 token 预算 + 保留字节硬上限） |
| 文档 1 说 `agent/data.go` 是 General 入口 | 实际 General 由 `chat.go`（ChatAgent）执行；`data.go` 为 DataAgent；两者都接入统一组装 |
| 文档 2 说模板 registry 一 intent 一 builder | 实际 `plan_builder.go` 已是 PlanBuilder 接口；本轮扩展为 TemplateKey registry，旧函数保留为 standard 变体 |
| 文档 2 说 `BuildPlan(ctx,intent,route)` | 为兼容 18 处既有调用，保留 `BuildPlan(ctx,intent)` 并新增 `BuildPlanWithRoute`；`BuildPlan` 内部计算默认 route |
| 文档 1 说 prompt token 下降 10-30% | 以实测为准；单测断言“同一 artifact 出现 1 次”，token 对比作为报告数据 |
