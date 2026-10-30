# 规格：N-05 计划审阅与人在回路（Plan Review & HITL）（2026-10-29 第九轮）

> 依据：`docs/design/n05-plan-review-hitl.md`（设计稿，2026-10-28）
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处在本文件第 9 节记录，以实际为准。
> 约束：一切与功能相关的测试只在容器内执行（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）。
> 基线：`master @ fb8938c` + 前八轮未提交成果（N-01/N-02/N-03、N-04/N-07、复杂度路由等），本轮不回归、不重复。
> 状态：已实现并通过容器内验证（2026-10-29）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND9.md`。

## 1. 目标

把"能停不能改"的 HITL 升级为"可审阅、可修正、可提问、可批量处理"的闭环。设计文档第 6 节 9 条验收标准是本轮硬性完成条件：

| 编号 | 目标 | 验收信号 |
|---|---|---|
| A1 | PlanRevision 不可变快照 | 改一个节点参数 → 生成 revision 2，revision 1 内容不变；执行 revision 2 后事件/artifact 带 `revision=2` |
| A2 | 乐观并发 | 两个客户端同时基于 revision 2 保存，第二个返回 409 + 当前 revision；不丢更新 |
| A3 | 运行中保护 | `in_progress` 时保存/执行新 revision 返回 409；取消后可执行 |
| A4 | Review Gate | 设置成本阈值/高风险后计划为 `awaiting_review`，不自动执行；批准后执行；拒绝后不执行 |
| A5 | Revision Diff | revision 1→2 显示字段级、边、预算变化；历史 revision 可另存为 draft |
| A6 | 结构化提问 | Agent 提问 → 前端显示选项/自由文本；回答后任务恢复；超时按默认/阻塞；审计记录 |
| A7 | Approval Inbox | 两个计划各有一个 pending action/question，收件箱一次看到；批量批准低风险；高风险不可批量 |
| A8 | 权限 | 非 owner 不能读/改/批；分享只读不能编辑 |
| A9 | 审计 | `plan_revision_created`、`plan_reviewed`、`question_answered`、`approval_bulk_decided` 事件可查 |

## 2. 现状复核（以实际代码为准，2026-10-29）

| 设计文档描述 | 实际（本轮复核） |
|---|---|
| 暂停/恢复有；`PausePlan`/`ResumePlan` 不打断运行中任务 | 属实：`api/plan_control.go:63-89`、`scheduler.go:504-568` |
| 从节点重跑有；直接改当前 plan 无 revision | 属实：`plan_control.go:91-122`、`scheduler.ResetFromNode` |
| 任务 retry/reassign/execute 有 | 属实：`plan_runtime.go:756-909` |
| 计划级审批有；仅 `awaiting_approval` 可批；二进制 | 属实：`plan_runtime.go:720-754`、`models/runtime.go:20-27` |
| 运行时审批有；只有 approve/reject；只按 plan 查看 | 属实：`models/reproduction.go:40-82`、`api/runtime_actions.go` |
| `ApprovalWaitSeconds` 等待不计入执行超时 | 属实：`models/runtime.go:141-147`、`scheduler.go:1862-1885` |
| PlanGraph 无 Revision/CurrentRevision | 属实：`models/graph.go:89-132`（本轮新增前） |
| 计划编辑 API 没有；只有 create/delete/control | 属实：`plan_runtime.go:192-227`（本轮新增前） |
| ask_user_question 全仓库无 | 属实：grep 0（本轮新增前） |
| 审批收件箱无；只有 `/plans/:id/runtime-actions` | 属实：`api/runtime_actions.go:24-30` |
| 计划 diff 无 | 属实 |
| 前端只有运行控制/审批卡 | 属实：`useGraphExecutionViewModel.ts`、`GraphPanel.tsx` |

**结论**：设计文档对现状判断准确。本轮按 Phase 1→4 全部落地；文档未写明的实现细节以第 9 节为准。

补充实际约束（文档未提）：
- 当前没有通用 LLM tool-calling 循环；Eino 链只有 Prompt→Model→Extract。因此 `ask_user_question` 以"任务类型 + 结果携带 Question"实现（见 3.5），并同时提供 HTTP 工具端点，避免伪造不存在的工具调用框架。
- `store.PlanStore` 是 Memory/File 单一接口；revision 需要单独追加型存储（`internal/revision`），不复用可变 plan 对象。
- 计划列表/库已有 owner 隔离与 audit helper（`recordAuditEvent`），收件箱复用。

## 3. 范围

### 3.1 数据模型

新增 `models/revision.go`：

```go
type PlanRevision struct {
    ID             string
    PlanID         string
    Revision       int
    ParentRevision int
    Graph          *PlanGraph
    Status         string // draft | approved | executing | completed | superseded
    CreatedBy      string
    CreatedAt      time.Time
    Reason         string
    DiffSummary    []string
    Hash           string
    ApprovedBy     string
    ApprovedAt     *time.Time
}

type ReviewState struct {
    Required     bool
    Status       string // not_required | pending | approved | rejected
    Reason       string
    Risk         string // low | medium | high
    EstimatedUSD float64
    ReviewedBy   string
    ReviewedAt   *time.Time
}

type PlanQuestion struct {
    ID            string
    PlanID        string
    Revision      int
    TaskID        string
    NodeType      string
    Header        string
    Prompt        string
    Options       []QuestionOption
    MultiSelect   bool
    AllowFreeText bool
    DefaultOption string
    Status        string // pending | answered | expired | canceled
    Answer        string
    AnswerOption  string
    AnsweredBy    string
    AnsweredAt    *time.Time
    ExpiresAt     *time.Time
    AutoAnswered  bool
}
```

`PlanGraph` 新增（全部 omitempty / 零值兼容旧 JSON）：
- `CurrentRevision int`
- `Review ReviewState`
- `Questions []PlanQuestion`

`TaskNode` 新增 `Revision int`；`PlanEvent` 新增 `Revision int`；`Artifact` 新增 `Revision int`。
新增状态：`StatusAwaitingReview = "awaiting_review"`、`StatusAwaitingInput = "awaiting_input"`。

约束：
- revision 只追加不覆盖；`Graph` 必须深拷贝（JSON 往返），带 sha256 hash。
- 编辑运行中的 plan 被拒绝；要改就新建 revision。
- 旧计划无 revision：首次访问 revisions 时惰性补建 revision 1（内容=当前图），不阻塞启动。

### 3.2 Revision 存储

新增 `internal/revision`：
- `Store` 接口：`Create(rev)`、`List(planID)`（按 revision 升序、不含大图可选）、`Get(planID, revision)`、`Latest(planID)`、`UpdateStatus(planID, revision, status, approvedBy, at)`、`DeletePlan(planID)`。
- MemoryStore + FileStore（`REVISION_STORE_PATH`，默认 `<PLAN_STORE_PATH>.revisions.json`，JSON 文件原子替换 + 互斥锁）。
- `NextRevision(planID)` 由 store 内部分配，保证并发下不重复；Create 校验 `ParentRevision` 与当前最新一致，否则返回 `ErrRevisionConflict`（带 CurrentRevision）。

### 3.3 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/plans/:id/revisions` | revision 列表（默认不含 graph；`include_graph=true` 时含） |
| GET | `/api/plans/:id/revisions/:revision` | revision 详情（含 graph） |
| POST | `/api/plans/:id/revisions` | `{base_revision, reason, graph}` 创建 draft；乐观并发 |
| POST | `/api/plans/:id/revisions/:revision/validate` | 校验 DAG/任务类型/依赖/预算 |
| POST | `/api/plans/:id/revisions/:revision/approve` | 审阅通过（review gate） |
| POST | `/api/plans/:id/revisions/:revision/reject` | 审阅拒绝 |
| POST | `/api/plans/:id/revisions/:revision/execute` | 执行指定 revision |
| GET | `/api/plans/:id/diff?from=&to=` | 节点/字段/边/预算 diff |
| GET | `/api/plans/:id/questions` | 当前计划提问 |
| POST | `/api/plans/:id/questions` | 创建提问（Agent 工具端点） |
| POST | `/api/plans/:id/questions/:questionId/answer` | 回答并恢复 |
| GET | `/api/approvals?status=pending&kind=approval\|question\|all&plan_id=&cursor=&limit=` | 跨计划收件箱 |
| POST | `/api/approvals/bulk` | 批量处理低风险审批 |

语义：
- `POST revisions`：`base_revision` 必须等于当前最新 revision；不等返回 409 `revision_conflict` + `current_revision`。plan 状态 `in_progress`/`awaiting_approval`/`awaiting_review` 时返回 409 `plan_running`（awaiting_review 允许编辑？设计 5.3 第 5 条只说"运行中不可编辑"，所以 awaiting_review 允许保存 draft）。创建成功后把新图应用为 plan 当前图（保留 plan 状态/usage/artifacts），PlanGraph.CurrentRevision 不变（CurrentRevision 指向最后一次执行绑定的 revision；draft 另记 latest revision）。返回 `{revision, plan}`。
- `POST revisions/:revision/execute`：plan 必须非 in_progress；revision 必须存在；若 `Review.Required && Review.Status != approved` 返回 409 `review_pending`；把 revision.Graph 深拷贝回 plan（节点状态重置为 pending、清空 lease/execution/error/result，保留 artifacts/预算），设置 `CurrentRevision=revision`、`Review` 为已批准、状态 pending，然后 launch。事件 `plan_revision_executed`。
- `approve`：仅当 plan.Review.Status == pending 且 revision == latest 时通过；写 `Review.Status=approved`、ReviewedBy/At，plan.Status 从 awaiting_review → pending；事件 `plan_reviewed` + audit `plan_reviewed`。reject → `Review.Status=rejected`，plan.Status 保持 awaiting_review 但标记 rejected（执行会被拒绝）；事件 `plan_reviewed`。
- `validate` 返回 `{valid, errors:[{node_id,field,message,remediation}]}`。
- 运行中保护：`POST revisions` 与 `execute` 在 `in_progress` 时 409；取消后可执行（状态 canceled 时 execute 允许，重置为 pending 再跑）。

### 3.4 Review Gate

触发（任一，按顺序计算）：
1. `PLAN_REVIEW_MODE=always`（部署级强制）；
2. payload `require_review=true`；
3. `REVIEW_COST_THRESHOLD_USD > 0` 且预计成本 > 阈值；
4. 风险节点：含 GPU/大下载/联网/长任务（节点类型 `autoresearch_run`/`paper_code_execute`/`benchmark_execute`/`experiment_run`/`fix_and_rerun`，或 reproduction spec GPURequired，或 `install_dependencies`/`image_prepare`/`runtime_probe`）；
5. `len(plan.MissingRequirements) > 0` 或复杂度 L4（planner 低置信度代理指标）。

预计成本：`节点数 × BUDGET_DEFAULT_TASK_ESTIMATE_USD(默认0.02)` + 已配置预算的一半（若有），只用于阈值比较和 UI 展示，不写入 Usage。

行为：
- plan.Status = `awaiting_review`；`Review={Required:true,Status:pending,Reason,Risk,EstimatedUSD}`。
- `POST /plans/:id/execute` 对 awaiting_review 返回 409 `review_pending`（必须走 revision approve/execute）。
- 批准后 Status=pending；拒绝后保持 awaiting_review 且 Review.Status=rejected。
- 前端在计划详情显示"待审阅"badge 与入口。

### 3.5 结构化提问

数据流（贴合现有架构，不伪造 LLM tool-calling）：
1. **工具端点**：`POST /api/plans/:id/questions` 接收 `{task_id, header, prompt, options[], multi_select, allow_free_text, default_option, timeout_seconds}`；创建 `PlanQuestion`（status=pending，ExpiresAt=now+timeout 或默认 900s），任务置 `awaiting_input`，计划置 `awaiting_input`，事件 `question_requested`；返回 question。
2. **任务类型**：task registry 新增 `ask_user_question`（allowed `general_agent`/`answer_agent`）。`RoutedTaskExecutor` 对 `ask_user_question` 节点：若该节点输入里已有 `question_answer`（回答后由 answer 端点写回）→ 完成任务，结果=答案；否则返回 `TaskExecutionResult{Status: awaiting_input, Question: draft}`。scheduler 收到带 Question 的结果时创建 question、置任务/计划 awaiting_input，不标记失败、不重试。
3. **回答**：`POST /questions/:id/answer` 写 Answer/AnswerOption/AnsweredBy/AnsweredAt；把任务从 awaiting_input 重置为 pending（运行中计划置 in_progress），清空 lease/epoch；写回任务 Inputs[`question_answer`]；事件 `question_answered` + audit。
4. **超时**：scheduler 循环 `expireQuestions`：pending 且过期 → 有 default_option 则自动回答（AutoAnswered=true，AnsweredBy=`system:timeout`）并恢复任务；否则任务 blocked + MissingRequirement(kind=question_timeout)，计划 failed/blocked。事件 `question_answered`（auto=true）或 `question_expired`。
5. **审计**：谁回答、何时、选了什么、是否超时，均写事件与 audit（`question_answered`）。

### 3.6 审批收件箱

- 后端聚合当前 owner 所有 plan 的 pending runtime actions + pending questions，不新建全局表；按 `(requested_at, id)` 倒序，cursor 为 base64 的 `{t,id}`。
- 返回项：`{id, kind: approval|question, plan_id, plan_title, task_id, node_type, title, prompt, risk, estimated_usd, waiting_seconds, expires_at, action_kind, options, status}`。
- `POST /api/approvals/bulk`：`{kind:"approval", ids:[], decision:"approve"|"reject"}`；仅允许 risk != high（low/medium），单次 ≤50；任一项 high → 400 `high_risk_requires_individual`；任一项非 pending/非 owner → 409；成功逐项执行与单条决策相同的状态机；事件 `approval_bulk_decided` + audit。
- 前端：顶栏铃铛（pending 数）+ 收件箱侧栏；按计划/类型过滤；批准/拒绝/回答/打开计划；批量只对低风险勾选。

### 3.7 Diff

- `GET /api/plans/:id/diff?from=1&to=2` 返回 `{from,to,diff:[{kind,node_id,field,before,after}]}`。
- kind：`node_added`、`node_removed`、`node_modified`（字段级：name/type/description/assigned_to/timeout_seconds/retry_limit/inputs/dependencies/enabled）、`edge_added`、`edge_removed`、`budget_changed`（max_cost_usd/max_tokens/max_sandbox_seconds/max_duration_seconds/policy）。
- 历史 revision 另存为 draft：`GET revisions/:r` 取 graph → `POST revisions` 以 `base_revision=latest` 提交 → 新 draft。

### 3.8 前端

- 顶栏新增"审阅"徽标/铃铛；新增 `ApprovalInboxPanel`（收件箱）与 `PlanReviewPanel`（审阅抽屉）。
- `PlanReviewPanel`：revision 选择、节点列表、节点 inspector（name/description/inputs/timeout/retry/assigned_to/enabled）、依赖多选、预算字段、校验按钮、diff 视图（并排）、保存草稿、批准并运行、拒绝。
- `GraphPanel`：新增待回答 question 卡片（单选/多选/自由文本 + 提交）；计划状态新增 awaiting_review/awaiting_input 文案。
- `useGraphExecutionViewModel`：等待审阅时禁用"运行"，显示"去审阅"。
- `contracts/api.ts` + `researchAssistantApi.ts` 增加 Revision/Question/Inbox/Diff 类型与调用。

### 3.9 审计与事件

新增事件类型：`plan_revision_created`、`plan_revision_executed`、`plan_reviewed`、`question_requested`、`question_answered`、`question_expired`、`approval_bulk_decided`。
audit 同步记录（复用 `recordAuditEvent`），可在 `/api/audit/export` 查询。

## 4. 关键设计决策

1. **Revision 独立存储**：不把 revision 数组塞进 PlanGraph（会与执行期高频 UpdatePlan 冲突并导致快照膨胀）；独立 JSON 文件 + 内存索引，Create 原子分配 revision。
2. **草稿与执行分离**：POST revisions 只改 plan 的"可编辑视图"，真正执行必须走 `/revisions/:r/execute`，执行前把 revision 图重置为待运行状态，保证同一 revision 可重复执行且不受上次运行状态污染。
3. **乐观并发用 base_revision==latest**：无需额外 locking；创建时在 store 内二次校验，避免 TOCTOU。
4. **Review Gate 保守触发**：默认不拦截简单任务；只有显式阈值/高风险/缺需求才 awaiting_review，避免拖慢普通计划。
5. **Question 与 RuntimeAction 分开**：Question 无副作用、可自由文本/默认值；RuntimeAction 有副作用、approve/reject。两者共用"暂停-恢复"但模型/UI/审计独立。
6. **收件箱是查询而非新表**：跨 plan 聚合，避免双写不一致；cursor 基于时间+id，稳定排序。
7. **ask_user_question 作为任务类型 + HTTP 工具端点**：不引入半成品 tool-calling；任务在缺答案时返回 awaiting_input，回答后重跑并读取 `question_answer`，可测试、可恢复、可超时。
8. **前端不引入 router**：沿用现有全屏面板模式。

## 5. 契约（新增/变更）

- `PlanGraph.current_revision`、`review`、`questions`；`TaskNode.revision`；`PlanEvent.revision`；`Artifact.revision`。
- 状态机新增 `awaiting_review`、`awaiting_input`；`isTerminalPlanStatus` 不含这两个状态。
- `POST /api/plan` 响应：`plan_graph.review`、`plan_graph.current_revision`；payload 支持 `require_review`。
- 上述 10 个 revision/question/inbox 端点。
- `GET /api/plans/:id` 返回 `review/questions/current_revision`。
- 兼容：旧 plan 无 revision 字段 → 0/空；旧查询参数不变。

## 6. 测试策略（全部容器内）

1. 后端：`cd research-assistant && ./scripts/container-go.sh "go build ./... && go vet ./... && go test ./... -count=1 -timeout 1200s"`。
2. 前端：`node:22-bookworm-slim` 内 `npm ci && npm test && npm run build && npm run lint`。
3. 单测重点：
   - `internal/revision`：追加、latest、并发 Create、冲突、文件持久化、owner 隔离。
   - `api`：revision CRUD/409/validate/diff/execute 绑定 revision、review gate 触发与批准/拒绝、question 创建/回答/超时、inbox 聚合/批量/高风险拒绝、权限 403。
   - `scheduler`：awaiting_review 不执行、question 超时默认/阻塞、revision 事件注入。
   - 前端组件：PlanReviewPanel 渲染/保存/校验、ApprovalInboxPanel 批量、QuestionCard 回答。
4. E2E（compose 栈）：新增 `test/e2e/plan-revisions.mjs`、`test/e2e/review-hitl.mjs`；覆盖 A1-A9 全部验收点。
5. 回归：既有后端/前端测试与既有 E2E（http-smoke/plan-run/plan-library/repro-bundle/cost-budget 等）。
6. 清理：临时容器/镜像/卷/脚本/产物；不删除用户数据卷。

## 7. 完成条件

- 第 6 节全部测试通过（容器内证据保存到 `docs/improvement-2026-10-26/FIX-REPORT-ROUND9.md`）；
- 设计文档第 6 节 A1-A9 逐条可复现；
- 无新增回归（现有后端/前端测试全绿）；
- 无密钥/隐私泄漏；
- 清理本轮产生的临时容器/镜像/卷/文件/死代码。

## 8. 非目标

- 不做多人实时协同编辑（N-12）；
- 不做任意脚本/任意 DAG 节点注入；只允许 task registry 内的类型；
- 不绕过审批直接执行高风险操作；
- 不在运行中热改 revision；
- 不做完整拖拽 DAG 画布（第一阶段 inspector + 表单）；
- 不保证所有提问都能自动回答；超时允许阻塞；
- 不做邮件/Webhook 提醒（后续）。

## 9. 与设计文档的差异（以实际为准）

| 文档描述 | 实际/本轮决策 |
|---|---|
| `PlanRevision.Graph *PlanGraph // 深拷贝快照`，存储可放文件/DB | 独立 `internal/revision` Memory/File Store，JSON 往返深拷贝 + sha256；不写入 PlanGraph 数组 |
| 文档 API 只列 `revisions/:revision/approve` | 实际同时提供 `reject`（5.4 审阅页有拒绝操作），否则拒绝无法落库 |
| `ask_user_question` 是 Agent 工具 schema | 当前无通用 tool-calling 循环；实现为 task registry 的 `ask_user_question` 任务类型 + HTTP 工具端点 + `TaskExecutionResult.Question`，行为等价（暂停/选项/自由文本/超时/恢复），并在文档中明确 |
| 文档说 `PlanGraph.CurrentRevision` 指向最新 approved/executing | 实际 CurrentRevision 只在 `execute` 时更新（指向正在执行的 revision）；最新 draft 由 revision store 的 latest 表示，避免"最新草案"冒充"正在执行" |
| 文档说"任务/事件/artifact 都带 revision" | 已加字段；历史 plan 为 0，不回填 |
| 文档说审批收件箱含批量低风险 | 实现 risk != high 可批量；high 逐条；批量上限 50 |
| 文档说 planner 置信度低触发 review | 实际以 `MissingRequirements` 非空或复杂度 L4 作为代理；不引入新的置信度模型 |
| 文档说 `GET /api/approvals?kind=approval/question` | 实际 kind 支持 `approval|question|all`（默认 all），`status` 默认 pending |