# FIX-REPORT-ROUND5：Agent 链路优化 + 意图复杂度路由

- 日期：2026-10-27
- 依据：
  - <code>docs/design/2026-10-27-agent-chain-optimization.md</code>
  - <code>docs/design/intent-complexity-routing.md</code>
- 规格/计划/任务：根目录 <code>spec.md</code> / <code>plan.md</code> / <code>tasks.md</code>
- 测试环境：容器内（<code>golang:1.26.1-bookworm</code>、<code>node:22-bookworm-slim</code>、运行中的 compose 栈）

## 1. 交付摘要

| 编号 | 交付 | 关键落点 |
|---|---|---|
| A | General 计划形状确定性化 | <code>internal/planner/general_plan.go</code>、<code>planner.go buildGeneralNodesV2</code>、<code>GraphMeta.GeneralPlanShape</code> |
| B | 单一 prompt 组装 | <code>internal/prompts/task_prompt.go</code>、<code>internal/prompts/inputs.go</code>、<code>scheduler/executor.go</code>、四个 agent |
| C | Repair 多轮会话 | <code>internal/agent/conversation/session.go</code> + Planner/PaperDebug/Benchmark/AutoResearch/Coder |
| D | Token/Context 预算 | <code>internal/prompts/budget.go</code>、modelprofile <code>context_window_tokens</code>、各重路径 token 上限、<code>prompt_budget</code> 报告 |
| L0-L4 | 复杂度路由 | <code>internal/planner/route.go</code>、Python intent schema/rubric、<code>BuildPlanWithRoute</code>、模板变体 registry、PlanMeta、metrics、golden set |

## 2. 优化点 A：General 计划形状

- 新增 <code>generalPlanShape</code>：direct/research/paper/literature；<code>classifyGeneralPlanShape</code> 规则判定，不使用 LLM。
- <code>buildGeneralNodesV2</code>：direct=1 个 <code>general_synthesis</code>；research=2 个（<code>general_research → general_synthesis</code>）；paper/literature 保持原形状。
- <code>GENERAL_PLAN_FAST_PATH</code>（默认 true）；false 时走 <code>buildGeneralNodesLegacy</code>（旧 3 节点）。
- <code>GraphMeta.GeneralPlanShape</code> + <code>plan_general_shape_total</code> / <code>plan_llm_nodes_total</code> 指标。
- 关键词表移除了 rag / query rewrite 主题词，避免“解释一下 RAG 是什么”被误判为 research。
- 测试：<code>internal/planner/general_plan_test.go</code> 覆盖 7 组输入；旧 3 节点断言改为 legacy 开关测试。

## 3. 优化点 B：Prompt 单一组装

- <code>prompts.BuildTaskPrompt</code> 固定 section 顺序：plan_context(1) → task_instruction(2) → task_inputs(3) → volatile(5)。
- <code>PlanContextForTask</code> 会跳过与 task description 重复的 User intent、与 task_inputs 重复的 paper_metadata / uploaded_files，保证“同一内容只出现一次”。
- <code>executor.buildTaskDescription</code> V2 只返回 node-local description；<code>sharedContext[plan_graph]</code> 注入 plan 对象；不再追加 Global user intent 与 500 字 artifact 摘要。
- Data/Librarian/Answer/Chat/Coder（含主 Eino 路径）统一使用 <code>buildAgentPromptText</code>；Librarian retrieval 作为 volatile。
- <code>PROMPT_CONTEXT_V2=false</code> 回退旧拼接（旧 byte 限制保留为 legacy）。
- 测试：<code>internal/prompts/budget_test.go</code> 断言 User intent / artifact 各出现 1 次、paper metadata 不重复。

## 4. 优化点 C：Repair 多轮会话

- 新增 <code>internal/agent/conversation.Session</code>：system + first user 永久保留；<code>Generate</code> 自动追加 assistant；<code>Trim</code> 只裁剪中间轮次；<code>PrefixHash</code>/<code>Snapshot</code> 记录 prompt hash。
- 接入：
  - Planner：<code>BuildNodesWithSession</code> + <code>RepairNodesWithSession</code>，repair 不再重建首轮 prompt；
  - PaperDebug：首轮带 source context，后续只追加 Run failed 反馈；
  - BenchmarkAdapter：preflight repair 复用同一 session；
  - AutoResearch：trial proposal 复用 session，追加 compact ledger/feedback；
  - Coder self-correction：首轮包含上一轮完整代码。
- <code>Task.Metadata[repair_session]</code> 记录 session_id/turns/prompt_hashes。
- 测试：<code>conversation/session_test.go</code> 断言 assistant 保留、prefix 变化、裁剪保留 system+first user。

## 5. 优化点 D：Token/Context 预算

- <code>PromptBudget</code>：默认 128000 - 8192 - 2000 = 117808 prompt tokens；支持 <code>PROMPT_CONTEXT_WINDOW</code> / <code>PROMPT_MAX_OUTPUT_TOKENS</code> / <code>PROMPT_SAFETY_TOKENS</code>。
- <code>BuildSections</code>：优先级分配、mandatory（system/plan/task）超预算 fail-closed、section marker/truncation marker 计入预算、rune-safe 截断。
- <code>TruncateRunes/TruncateUTF8Bytes/TruncateByTokens</code>；旧 <code>input_summary.go</code>、PaperDebug/Benchmark/Visual 截断全部改为 UTF-8 安全。
- 重路径 token 上限：AutoResearch source 50k / ledger 4k、PaperDebug 48k / diagnostic 8k、Claim paper 24k / evidence 32k、Benchmark repo 8k、Planner intent/nodes 24k、Coder repair code 24k / error 8k、Dependency error 8k、Verification result 8k。
- <code>modelprofile.Profile.ContextWindowTokens</code>（默认 128000）贯通 Profile/Input/API/前端设置/PlanSnapshot；executor 用它覆盖 prompt budget。
- <code>TaskUsage.prompt_budget_limit</code> / <code>prompt_truncated_sections</code>；<code>Task.Metadata[prompt_budget]</code>；指标 <code>llm_prompt_truncated_sections_total</code> / <code>llm_prompt_over_budget_total</code>。
- 测试：<code>budget_test.go</code> 覆盖优先级、超预算、中文/emoji rune-safe。

## 6. L0-L4 复杂度路由

- Python intent：<code>IntentResponse</code> 增加 complexity/capabilities/route_hint/complexity_confidence/complexity_reason；分类 prompt 增加 rubric 与“不可信数据”声明；缺失 complexity 返回 None（走 Go 规则），未知枚举降 L2。
- Go：<code>Classification</code>/<code>IntentContext</code> 新字段（omitempty）；<code>resolver.mergeAndValidate</code> 取 max(rule, LLM)，LLM 不得降级；<code>postValidate</code> 校验枚举。
- <code>ComplexityRouter.Decide(intent, attachments, researchContext)</code>：规则评分（附件/repo/dataset、多框架、多步骤、高级 artifact、多指标、自定义流程、长文本、简单信号），映射 L0-L4 与 variant；可选 <code>ComplexityConfirmer</code> hook 默认关闭。
- <code>plan_builder.go</code> 改为 <code>TemplateKey{IntentType,Variant}</code> registry：General/Code_Execution/Framework_Evaluation 的 simple/standard/full，Paper_Reproduction 的 light/standard/full；其他 intent 默认 standard。
- <code>BuildPlanWithRoute</code>：L0-L2 确定性模板（不调用 planner LLM），L3/L4 skeleton + agent 增强（<code>BuildNodesWithSkeleton</code> + <code>mergeAgentPlanWithSkeleton</code>），失败回退 standard；<code>BuildPlan</code> 保留为兼容入口。
- <code>GraphMeta</code> 增加 PlanningRoute/Complexity/ComplexitySource/TemplateVariant；SSE plan_created 与 <code>/api/plans/:id</code> 可见；前端计划详情展示复杂度/规划路径/模板变体。
- 指标：<code>plan_route_total</code>、<code>planner_fallback_total</code>、<code>plan_create_duration_seconds</code>、<code>plan_general_shape_total</code>、<code>plan_llm_nodes_total</code>。
- Golden set：<code>backend/evals/route_eval_dataset.json</code> 37 条；<code>route_eval_test.go</code> 断言 accuracy ≥90%、under-routing ≤1、route_hint ≥95%。

## 7. 与设计文档的差异（以实际为准）

| 设计描述 | 实际决策 |
|---|---|
| 模糊区间调用一次轻量 LLM 确认 | 仓库无独立轻量路由端点；实现 ComplexityConfirmer hook（默认关闭），规则优先，文档化偏差 |
| Code_Execution 单图绘制 = L1 simple | 仓库 plot 契约要求 render_plot + runtime 准备链；plot 代码请求强制 L2 standard，避免破坏执行 |
| Paper_Reproduction 轻量 = light | 默认最低 L2 standard；light 仅保留为显式变体，避免丢失 claim/evidence |
| General L3 走 skeleton + agent | General 保持确定性 shape；仅 L4 增加 verify 节点，避免把 General 交给 agent planner |
| PlanMeta 新类型 | 实际使用既有 models.GraphMeta，新增字段保持向后兼容 |
| 文档称 Prompt token 下降 10-30% | 以单测“内容只出现一次”为准；未做真实 provider token 对比（无稳定外部模型配额） |

## 8. 容器内验证证据

### 8.1 后端
- 基线（改动前）：<code>go build ./... && go vet ./... && go test ./...</code> 全绿。
- 改动后：<code>scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."</code> 全绿（含新增 conversation/prompts/planner/evals 测试）。
- 关键包 <code>-race</code>：<code>go test -race ./internal/agent/conversation/ ./internal/prompts/ ./internal/planner/</code> 全绿。
- 关键新增测试：
  - <code>TestGeneralDirectPlanHasSingleAnswerNode</code>、<code>TestClassifyGeneralPlanShape</code>（7 组）、<code>TestGeneralLegacyPlanUsesGeneralAgent</code>
  - <code>TestBuildTaskPromptDeduplicatesIntentAndArtifact</code>、<code>TestBuildTaskPromptOmitsDuplicatePaperMetadata</code>、<code>TestBuildSectionsPriorityAndBudget</code>
  - <code>TestSessionGenerateAppendsAssistant</code>、<code>TestSessionTrimKeepsSystemAndFirstUser</code>
  - <code>TestRouteEvalDataset</code>（37 条，accuracy 100%、under-routing 0、route_hint 100%）
  - <code>TestComplexityRouterRuleLevels</code>、<code>TestComplexityRouterNeverDowngradesLLMLevel</code>、<code>TestMergeAgentPlanWithSkeletonKeepsSkeletonIdentity</code>
  - <code>TestTemplateVariantsBuildValidPlans</code>（12 个 intent×variant 组合全部通过 validatePlanGraph）
  - <code>TestComplexityRouterDisabledRestoresLegacyGeneral</code>（关闭路由恢复旧 3 节点）

### 8.2 Python intent
- <code>python -m unittest test_service</code>（intent-recognition 镜像内、挂载源码）：8 tests OK。
- 新增：complexity 字段解析、缺失返回 None、未知枚举降 L2。

### 8.3 前端
- <code>node:22-bookworm-slim</code> 容器内 <code>npm ci && npm run test && npm run build && npm run lint</code>：
  - 25 个测试文件 / 112 个测试通过；
  - <code>tsc -b && vite build</code> 通过；
  - ESLint 0 error（修复了 1 个 hooks deps warning）。

### 8.4 运行栈 E2E
- 重建 backend/frontend 镜像并重启 compose。
- <code>test/e2e/routing-and-prompts.mjs</code>：全部通过（direct 1 节点/L0/fast_path、research 2 节点/L2/standard、simple code 3 节点/L1、paper 4 节点/L2、计划详情 meta、metrics 指标）；E2E 在 node:22-bookworm-slim 容器内执行（RA_API_URL=http://host.docker.internal:18080）。
- <code>test/e2e/http-smoke.mjs</code>：17 项全部通过（plan/幂等/暂停/恢复/导出/上传/限流）。
- 最终重建 backend 镜像后再次执行上述两个 E2E：均通过（routing-and-prompts passed / all smoke checks passed）。
- 既有前四轮 E2E 脚本保留；本轮未改动其路径。

## 9. 回滚开关

| 开关 | 默认 | 作用 |
|---|---|---|
| GENERAL_PLAN_FAST_PATH | true | false → General 旧 3 节点 |
| PROMPT_CONTEXT_V2 | true | false → 旧 prompt 拼接（含 Global user intent / 500 字 artifact 摘要） |
| COMPLEXITY_ROUTER_ENABLED | true | false → 按 intent_type 二选一旧路由；General 恢复旧 3 节点 |
| COMPLEXITY_LLM_CONFIRM_ENABLED | false | 预留轻量 LLM 确认 hook（当前未接线） |
| PLANNER_MAX_NODES_L3 / L4 | 16 / 24 | agent planner 节点上限 |
| PROMPT_CONTEXT_WINDOW / PROMPT_MAX_OUTPUT_TOKENS / PROMPT_SAFETY_TOKENS | 128000/8192/2000 | prompt 预算默认值 |

## 10. 已知限制与后续

1. intent-recognition 镜像重建：本轮构建时 Docker Hub python:3.12-slim 元数据拉取超时，运行中的 intent 容器仍为旧镜像；新 Python schema 已通过“旧镜像 + 挂载源码”的容器内单测验证，旧镜像对 Go 侧向后兼容（缺失字段走规则）。网络恢复后执行 <code>docker compose build intent-recognition && docker compose up -d intent-recognition</code> 即可。
2. LLM 路由确认 hook 默认关闭：仓库没有独立轻量路由模型端点，规则评分 + intent LLM 已覆盖；后续可接入小模型。
3. vision token 仍未计入 TaskUsage（前轮既有缺口，非本轮引入）。
4. 前端展示：计划详情展示复杂度/规划路径/模板变体；未做复杂可视化。
5. 模板变体：仅 4 个 intent 首期变体，其他 intent 保持 standard。

## 11. 清理

- 删除临时调试测试文件 debug_route_test.go；
- 删除本轮前端测试卷 dsh-fe-node-modules / dsh-npm-cache；
- docker image prune（无 dangling）、docker builder prune（回收 1.9GB 构建缓存）；
- 前端测试在容器内临时目录完成，不落盘到仓库；
- 保留 compose 运行所需镜像与数据卷；
- 未删除前四轮已有产物（属于用户既有工作区）。
