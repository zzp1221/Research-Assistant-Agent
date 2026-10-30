# 任务清单：Agent 链路优化 + 意图复杂度路由（2026-10-27 第五轮）

## P0 基线与规格
- [x] 复核设计文档与实际代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 go build/vet/test 记录（基线全绿）
- [x] 归档第四轮 spec/plan/tasks

## P1 Prompt 单一组装 + Token 预算（B+D）
- [x] prompts/budget.go（PromptBudget/TokenCounter/Section/BuildSections/rune-safe）
- [x] prompts/task_prompt.go（PlanContext/TaskInstruction/TaskInputs/BuildTaskPrompt）
- [x] prompts/inputs.go（预算化 inputs 格式化）
- [x] executor.buildTaskDescription 去重 + plan_graph 注入 + PROMPT_CONTEXT_V2 开关
- [x] data/librarian/answer/coder/chat 接入统一组装
- [x] autoresearch/paper_debug/claim/benchmark/visual_verifier token 预算
- [x] modelprofile ContextWindowTokens 贯通（API/UI/存储/PlanSnapshot）
- [x] TaskUsage 预算字段 + prompt metrics + prompt_budget 报告落节点 metadata
- [x] 单测（去重/顺序/预算/rune-safe/中文/emoji）

## P2 General 计划形状（A）
- [x] planner/general_plan.go（classifyGeneralPlanShape/generalNeedsResearch/documentBindings）
- [x] buildGeneralNodesV2 按 shape 分支 + GENERAL_PLAN_FAST_PATH
- [x] GraphMeta.GeneralPlanShape + plan_general_shape_total / plan_llm_nodes_total
- [x] planner 测试更新与新增（7 组 shape）

## P3 复杂度路由与模板变体
- [x] Python IntentResponse + rubric + 解析（缺失走规则、未知降 L2）
- [x] Go Classification/IntentContext 字段
- [x] resolver 合并/枚举校验（LLM 不得降级）
- [x] planner/route.go（规则评分/RouteDecision/variant/confirmer hook）
- [x] plan_builder TemplateKey registry + 4 intent 变体
- [x] BuildPlanWithRoute + L0-L4 路由 + GraphMeta 路由字段
- [x] agent_planner skeleton/merge/节点上限
- [x] api/plan_runtime Decide 接线（附件/researchContext 之后）
- [x] metrics + 前端计划详情展示
- [x] route_eval_dataset.json（37 条）+ eval 测试（准确率 100%、under-routing 0）

## P4 Repair 会话（C）
- [x] conversation/session.go + 单测（assistant 保留/prefix hash/裁剪）
- [x] planner repair 接入（build+repair 同一 session）
- [x] PaperDebug 接入（首轮 source，后续只追加失败反馈）
- [x] BenchmarkAdapter 接入
- [x] AutoResearch 接入
- [x] Coder self-correction 接入（带上一轮代码）
- [x] repair_session metadata

## P5 回归、E2E、清理
- [x] 容器内后端全量 go build/vet/test（全绿）+ 关键包 -race（全绿）
- [x] 容器内前端 npm test/build/lint（112 tests，build 通过，lint 0 error）
- [x] 重建 backend/frontend 镜像 + compose 起栈
- [x] 新增 E2E routing-and-prompts（全部通过）
- [x] 既有 E2E 回归（http-smoke 17 项全通过；routing-and-prompts 全通过）
- [x] 清理临时产物/镜像/卷/死代码（删除 debug 测试、dsh-fe-node-modules/dsh-npm-cache 卷、dangling images、builder cache 1.9GB）
- [x] 更新 FIX-REPORT-ROUND5
