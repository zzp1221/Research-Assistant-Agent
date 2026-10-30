# 实施计划：Agent 链路优化 + 意图复杂度路由（2026-10-27 第五轮）

- 基线：`master @ 0f605c2` + 前四轮未提交成果（已通过容器验证）
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 compose 栈）
- 完成定义：`spec.md` 第 7 节；每阶段结束跑容器内构建/单测，最后统一 E2E。
- 状态：全部阶段完成（2026-10-27）；容器证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND5.md`。

## 阶段 P0 — 基线与规格
1. 复核两份设计文档与实际代码差异，写 `spec.md` / `plan.md` / `tasks.md`。
2. 容器内基线：`go build ./...`、`go vet ./...`、`go test ./...`（记录已知失败，避免把基线问题算作本轮回归）。
3. 归档第四轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND4-*.md`。

## 阶段 P1 — Prompt 单一组装 + Token 预算（B + D）
1. `internal/prompts/budget.go`：PromptBudget/TokenCounter/EstimatedCounter/Section/PromptReport/BuildSections/TruncateRunes/TruncateByTokens。
2. `internal/prompts/task_prompt.go`：PlanContext/TaskInstruction/TaskInputs/BuildTaskPrompt。
3. `internal/prompts/inputs.go`：FormatTaskInputsForPrompt（预算化、key 排序、图片省略）。
4. `scheduler/executor.go`：buildTaskDescription 只留 node-local；sharedContext 注入 plan_context；`PROMPT_CONTEXT_V2` 开关。
5. `agent/{data,librarian,answer,coder,chat}.go` 接入 BuildTaskPrompt；删除重复拼接。
6. `agent/{autoresearch,paper_debug_harness,claim_evidence,benchmark_adapter,visual_verifier}.go`：rune-safe/token 预算；`Task.Metadata["prompt_budget"]`。
7. `modelprofile` + API + 前端设置：ContextWindowTokens。
8. `models/runtime.go` + `metrics.go`：预算字段与指标。
9. 单测：prompts 包（去重/顺序/预算/rune-safe/中文/emoji）、agent 组装、modelprofile。

## 阶段 P2 — General 计划形状（A）
1. `planner/general_plan.go`：classifyGeneralPlanShape/generalNeedsResearch/documentBindingsIfPresent。
2. `planner/planner.go`：buildGeneralNodesV2 按 shape 分支；GENERAL_PLAN_FAST_PATH；GraphMeta.GeneralPlanShape。
3. 指标：plan_general_shape_total / plan_llm_nodes_total。
4. 更新 `planner_test.go` 旧 3 节点断言；新增 6 组 shape 测试（你好/解释/总结/调研/paper/literature）。

## 阶段 P3 — 复杂度路由与模板变体（设计文档 2）
1. Python intent：IntentResponse 字段 + rubric + 解析 + 单测（python 侧）。
2. Go Classification/IntentContext 字段；resolver 合并与枚举校验。
3. `planner/route.go`：ComplexityRouter 规则评分 + RouteDecision + variant 映射 + confirmer hook。
4. `planner/plan_builder.go`：TemplateKey registry + 4 个 intent 的 simple/standard/full 变体。
5. `planner/planner.go`：BuildPlanWithRoute；L0-L4 路由；PlanMeta 写入；fallback。
6. `planner/agent_planner.go`：BuildNodesWithSkeleton/mergeAgentPlanWithSkeleton/节点上限配置。
7. `api/plan_runtime.go`：附件后 Decide；调用 BuildPlanWithRoute。
8. `models/graph.go` GraphMeta 扩展；`metrics.go` 路由指标；前端 contracts + 计划详情展示。
9. `backend/evals/route_eval_dataset.json` + eval 测试。

## 阶段 P4 — Repair 会话（C）
1. `internal/agent/conversation/session.go` + 单测（prefix hash、裁剪保留 system+first user、Generate 追加 assistant）。
2. planner agent repair 接入 session。
3. PaperDebug 2 轮 repair 接入。
4. BenchmarkAdapter plan/generation/preflight 接入。
5. AutoResearch candidate 循环接入。
6. Coder self-correction 接入。
7. `Task.Metadata["repair_session"]` 记录。

## 阶段 P5 — 容器回归、E2E、清理
1. 容器内全量后端：go build/vet/test ./...；关键包 -race。
2. 容器内前端：npm test/build/lint。
3. 重建 backend/intent-recognition 镜像，compose 起栈，跑新增 E2E（routing/repair/prompt-budget）与既有 http-smoke。
4. 全量回归既有 E2E（plan-run、model-profiles、verification、literature、document-intelligence）。
5. 清理临时容器/镜像/卷/脚本/产物；删除死代码。
6. 更新 README/OpenAPI/FIX-REPORT-ROUND5/spec/plan/tasks。

## 风险与对策
- 路由改动影响既有计划形状：所有既有测试先跑基线；变体只在明确 complexity 时启用，默认 standard 保持旧形状。
- BuildPlan 签名：保留旧签名 + BuildPlanWithRoute，避免 18 处调用点全改。
- Prompt 组装改变模型行为：PROMPT_CONTEXT_V2 开关 + 单测断言结构，E2E 抽检输出。
- Repair 会话上下文增长：与 PromptBudget 共用，system+first user 永不裁剪。
- tokenizer 偏差：EstimatedCounter 保守 + Safety 2000 + 10-15% 余量。
- 前端改动风险：只加可选字段与只读 badge，不做布局重构。
- 容器资源：测试用 --rm，结束后清理 dangling 镜像/卷。
