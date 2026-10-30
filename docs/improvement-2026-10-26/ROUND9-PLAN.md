# 实施计划：N-05 计划审阅与人在回路（Plan Review & HITL）（2026-10-29 第九轮）

- 依据：`docs/design/n05-plan-review-hitl.md` + `spec.md`
- 基线：`master @ fb8938c` + 前八轮未提交成果（容器内 build/vet 基线已通过）
- 测试环境：容器内（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）
- 完成定义：`spec.md` 第 7 节；每阶段结束跑容器内构建/单测，最后统一 E2E + 回归。
- 状态：已完成（2026-10-29）；容器证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND9.md`。

## 阶段 P0 — 基线与规格（本轮）
1. 复核设计文档与实际代码差异，写 `spec.md` / `plan.md` / `tasks.md`（差异见 spec 第 9 节）。
2. 归档第八轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND8-*.md`。
3. 容器内基线：`go build ./...`、`go vet ./...`（确认起点无编译错误）。

## 阶段 P1 — Revision 模型与存储
1. `models/revision.go`：PlanRevision、ReviewState、PlanQuestion、QuestionOption、状态常量、hash 计算。
2. `models/graph.go`：PlanGraph 增加 CurrentRevision/Review/Questions；TaskNode.Revision；PlanEvent.Revision；Artifact.Revision。
3. `models/task.go`：StatusAwaitingReview / StatusAwaitingInput。
4. `internal/revision/store.go`：Store 接口 + MemoryStore + FileStore + NextRevision + 冲突错误。
5. 单测：追加/latest/并发/冲突/持久化/删除。

## 阶段 P2 — Revision API 与 Diff
1. `api/plan_revisions.go`：GET list/get、POST create（base_revision 乐观锁）、POST validate、POST execute、POST approve/reject。
2. `api/plan_diff.go`：节点/字段/边/预算 diff。
3. `api/plan_runtime.go`：注册路由；createPlan 惰性补建 revision 1；execute 绑定 revision；状态守卫。
4. `api/plan_validation.go`（或 planner 暴露）：DAG 环/悬挂边/任务 registry/预算/依赖产物校验。
5. 单测：A1/A2/A3/A5、权限 403、validate 错误结构。

## 阶段 P3 — Review Gate
1. `models/graph.go` ReviewState 落盘；`api/review_gate.go`：触发条件计算（模式/显式/成本阈值/高风险/缺需求/L4）、EstimatedUSD。
2. `createPlan` 调用 gate → awaiting_review；`executePlan` 拦截；revision approve/reject 状态机。
3. 前端 await_review 文案与禁用运行。
4. 单测：A4 三条（阈值触发、批准执行、拒绝不执行）+ 简单计划不触发。

## 阶段 P4 — 结构化提问
1. `models` PlanQuestion 完成；`api/plan_questions.go`：GET list、POST create、POST answer、超时扫描。
2. `scheduler`：`TaskExecutionResult.Question` 字段；`runReadyNodes` 处理 awaiting_input；`expireQuestions` 循环；answer 恢复。
3. `planner/taskregistry`：新增 `ask_user_question` 类型（allowed general_agent/answer_agent）。
4. `scheduler/executor`：ask_user_question 节点读取 `question_answer`；无答案返回 Question。
5. 前端：GraphPanel QuestionCard（选项/多选/自由文本/倒计时）。
6. 单测：A6 创建/回答/超时默认/超时阻塞/审计。

## 阶段 P5 — 审批收件箱
1. `api/approvals.go`：GET 聚合（runtime actions + questions，owner 隔离，cursor，filters）、POST bulk。
2. 事件/审计：approval_bulk_decided、question_answered。
3. 前端：ApprovalInboxPanel + 顶栏铃铛数。
4. 单测：A7 跨计划聚合、批量低风险成功、高风险拒绝、单条路径一致。

## 阶段 P6 — 前端审阅页与 diff
1. `contracts/api.ts`：Revision/ReviewState/PlanQuestion/InboxItem/DiffEntry 类型。
2. `services/api/researchAssistantApi.ts`：listRevisions/getRevision/createRevision/validateRevision/approveRevision/rejectRevision/executeRevision/getPlanDiff/listQuestions/createQuestion/answerQuestion/listApprovals/bulkApprovals。
3. `features/review/PlanReviewPanel.tsx`：revision 选择、节点 inspector 表单、依赖多选、预算、校验、diff 并排、保存草稿、批准并运行、拒绝。
4. `features/review/ApprovalInboxPanel.tsx`：列表、过滤、批量、单条决策/回答、打开计划。
5. `ResearchAssistantApp.tsx`：顶栏入口/铃铛；GraphPanel awaiting_review/awaiting_input 文案。
6. 组件测试。

## 阶段 P7 — 容器回归、E2E、清理
1. 后端全量：`go build ./... && go vet ./... && go test ./...`；新增包 `-race`。
2. 前端容器：`npm ci && npm test && npm run build && npm run lint`。
3. 重建 backend/frontend 镜像 + compose 起栈。
4. 新增 E2E：`plan-revisions.mjs`（A1/A2/A3/A5）、`review-hitl.mjs`（A4/A6/A7/A8/A9）。
5. 回归既有 E2E：http-smoke、plan-run、plan-library、repro-bundle、cost-budget、budget-recovery。
6. 清理临时容器/镜像/卷/脚本/产物；删除死代码。
7. 更新 README/OpenAPI/FIX-REPORT-ROUND9/spec/plan/tasks。

## 风险与对策
- Revision 与 plan 双写不一致：以 revision store 为准，plan 仅保存 current_revision/review；创建成功后统一 UpdatePlan 应用图，失败回滚 revision 状态为 draft。
- 执行中的 revision 保护：execute 前检查 plan.Status；in_progress/awaiting_approval/awaiting_review（未批准）全部 409。
- Question 与 scheduler 循环冲突：awaiting_input 不进入 allTerminal；scheduler 等待 500ms 轮询，answer 后 notifyProgress 唤醒。
- 超时自动回答的幂等：UpdatePlan 内二次检查 status==pending，避免重复回答。
- 收件箱性能：只扫 owner 的 plan 列表（已有 ListPlanSummaries 轻量投影）+ revision store，未知量级 <10k，内存过滤可接受。
- 前端面板复杂：拆成 PlanReviewPanel / ApprovalInboxPanel / QuestionCard 三个组件，component test 覆盖；不引入 router。
- 旧计划兼容：CurrentRevision=0 视为 revision 1，惰性补建；Review 零值=not_required。
- 容器资源：测试用 --rm；结束后清理 dangling 镜像/卷，不删除 compose 数据卷。