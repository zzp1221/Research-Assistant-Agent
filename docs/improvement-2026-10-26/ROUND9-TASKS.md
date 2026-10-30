# 任务清单：N-05 计划审阅与人在回路（2026-10-29 第九轮）

## P0 基线与规格
- [x] 复核设计文档与实际代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 归档第八轮 spec/plan/tasks（ROUND8-*）
- [x] 容器内基线 go build/vet

## P1 Revision 模型与存储
- [x] models/revision.go（PlanRevision/ReviewState/PlanQuestion/QuestionOption）
- [x] PlanGraph CurrentRevision/Review/Questions；TaskNode.Revision；PlanEvent.Revision；Artifact.Revision
- [x] StatusAwaitingReview / StatusAwaitingInput
- [x] internal/revision Store（Memory/File）
- [x] 单测（追加/latest/并发/冲突/持久化）

## P2 Revision API 与 Diff
- [x] GET /plans/:id/revisions（list/get）
- [x] POST /plans/:id/revisions（base_revision 乐观锁）
- [x] POST /plans/:id/revisions/:r/validate
- [x] POST /plans/:id/revisions/:r/execute
- [x] POST /plans/:id/revisions/:r/approve|reject
- [x] GET /plans/:id/diff
- [x] 惰性补建 revision 1 + 路由注册
- [x] 单测（A1/A2/A3/A5/权限）

## P3 Review Gate
- [x] 触发条件（模式/显式/成本阈值/高风险/缺需求/L4）+ EstimatedUSD
- [x] createPlan → awaiting_review
- [x] execute/revision execute 拦截
- [x] approve/reject 状态机
- [x] 前端文案与禁用运行
- [x] 单测（A4）

## P4 结构化提问
- [x] models.PlanQuestion 完整字段
- [x] GET/POST /plans/:id/questions + answer
- [x] scheduler 处理 awaiting_input + expireQuestions
- [x] taskregistry ask_user_question
- [x] executor ask_user_question 节点
- [x] 前端 QuestionCard
- [x] 单测（A6）

## P5 审批收件箱
- [x] GET /api/approvals（聚合 + cursor + filter）
- [x] POST /api/approvals/bulk（低风险批量）
- [x] 前端 ApprovalInboxPanel + 铃铛
- [x] 单测（A7）

## P6 前端审阅页与 diff
- [x] contracts 类型
- [x] api client 方法
- [x] PlanReviewPanel（inspector/diff/校验/保存/批准/拒绝）
- [x] GraphPanel 状态文案 + QuestionCard 接入
- [x] 组件测试

## P7 回归、E2E、清理
- [x] 后端容器 build/vet/test 全绿
- [x] 前端容器 test/build/lint 全绿
- [x] 重建镜像 + compose 起栈
- [x] 新增 E2E plan-revisions.mjs
- [x] 新增 E2E review-hitl.mjs
- [x] 既有 E2E 回归
- [x] 清理临时产物/镜像
- [x] FIX-REPORT-ROUND9

## 未实现（明确记录）
- [ ] 完整拖拽 DAG 画布（设计明确后置）
- [ ] 邮件/Webhook 提醒（后续）
- [ ] 多人实时协同编辑（N-12）