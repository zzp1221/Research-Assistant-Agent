# N-05 计划审阅与人在回路（Plan Review & HITL）

- **状态**：设计稿（待评审）
- **日期**：2026-10-28
- **范围**：计划 revision、执行前审阅、节点/参数编辑、结构化提问、审批收件箱、计划 diff
- **关联**：`docs/improvement-2026-10-26/03-new-feature-proposals.md`、`research-assistant/backend/internal/api/plan_runtime.go`、`plan_control.go`、`runtime_actions.go`、`models/graph.go`、`models/runtime.go`、`models/reproduction.go`、`research-assistant/frontend/src/app/viewModels/useGraphExecutionViewModel.ts`
- **一句话结论**：暂停/恢复/从节点重跑、运行时审批、计划级审批已经存在；N-05 真正缺的是**可编辑的 revision、执行前审阅、结构化提问、跨计划审批收件箱和 diff**。

---

## 1. 摘要

当前系统已经能“控制”计划，但还不能“审阅和修正”计划：

- 已有 `POST /api/plans/:id/pause|resume`、`POST /api/plans/:id/tasks/:taskId/run|retry|reassign|execute`、`POST /api/plans/:id/approve`、`GET/POST /api/plans/:id/runtime-actions`。
- 已有审批等待时间 `ApprovalWaitSeconds`、审批超时、终态保护、SSE 事件和审计。
- 但 `PlanGraph` 没有 revision 字段；没有“编辑计划/保存草稿/对比 revision”的 API；没有 `ask_user_question` 工具；没有跨计划审批收件箱。

所以 N-05 的改进版不是再做一个“暂停按钮”，而是补：

1. **PlanRevision**：计划不可变快照，编辑产生新 revision，执行绑定 revision；
2. **Review Gate**：昂贵/高风险计划在执行前进入审阅，不再直接跑；
3. **结构化提问**：Agent 真正需要用户决策时暂停并提问，而不是只发 approve/reject；
4. **Approval Inbox**：跨计划查看/处理审批与提问；
5. **Diff**：revision 之间对比节点、参数、依赖、预算。

---

## 2. 现状盘点

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| 暂停/恢复 | 有；`PausePlan`/`ResumePlan` 不打断运行中任务 | `api/plan_control.go:63-89` | 暂停后只能继续，不能改参数再继续 |
| 从节点重跑 | 有；`ResetFromNode` + `launchPlan` | `api/plan_control.go:91-122` | 直接改当前 plan，不产生 revision；无法对比/回滚 |
| 任务重试/改派/单节点执行 | 有 `retry`/`reassign`/`execute` | `api/plan_runtime.go:761-…、routes` | 只改任务状态/执行者，不能改输入/依赖 |
| 计划级审批 | 有；`ApprovalState`，仅 `awaiting_approval` 可批 | `api/plan_runtime.go:725-759`、`models/runtime.go:17-24` | 二进制批准；不能在批准前编辑；不是通用审阅 gate |
| 运行时审批 | 有；image pull/deps/dataset/model/GPU/network 等 `RuntimeActionKind` | `models/reproduction.go:40-82`、`api/runtime_actions.go` | 只有 approve/reject；没有“选项/自由文本/默认值”；只按 plan 查看 |
| 审批等待预算 | 有；`RunUsage.ApprovalWaitSeconds`，等待时间不计入执行超时 | `models/runtime.go:80-98`、`scheduler` | 没有收件箱/提醒/批量处理 |
| Plan revision | **没有**：`PlanGraph` 无 `Revision`/`CurrentRevision` | `models/graph.go:89-125` | 编辑会覆盖当前 plan；无历史/回滚/对比 |
| 计划编辑 API | **没有**：只有 create/delete/control | `api/plan_runtime.go:133-155` | 不能改参数、增删节点、改依赖、跳过节点 |
| ask_user_question | **没有**：全仓库无该工具/模型 | grep 0 | Agent 无法结构化提问；只能靠 runtime action 或直接失败 |
| 审批收件箱 | **没有**：只有 `/plans/:id/runtime-actions` | `api/runtime_actions.go:24-30` | 无法跨计划看 pending；无批量；无统一审计 |
| 计划 diff | **没有** | 无 diff API/组件 | 用户无法知道改了什么、为什么改 |
| 前端审阅 | 只有运行控制/审批卡片 | `useGraphExecutionViewModel.ts:47-74`、`GraphPanel` | 没有计划编辑器、revision 选择、diff、收件箱 |

**结论**：当前 HITL 是“运行控制 + 危险操作审批”，不是“计划审阅”。这正好解释了用户为什么仍要为改一个参数重新生成整个计划。

---

## 3. 问题在哪

### 3.1 “能停不能改”：修正成本太高

- 用户发现“数据集路径错了/超参不对/依赖漏了”，只能：
  - 取消当前计划，重新写 prompt 生成新计划；或
  - 让 Coding Agent 在运行中改代码。
- 前者丢失已有 artifacts/上下文，后者不可审计、不可复现。
- 根因：没有“计划草稿 + revision”这一层；PlanGraph 是可变对象，编辑没有安全边界。

### 3.2 没有执行前审阅：昂贵计划直接开跑

- 计划创建后可以立即 execute；只有高风险计划有 `ApprovalState`。
- 用户无法在“花 30 分钟/几美元”之前检查：节点是否合理、依赖是否正确、预算是否足够、数据/模型是否选对。
- 根因：没有 review gate/`awaiting_review` 状态；审批只覆盖“高风险”，不覆盖“高成本/高影响”。

### 3.3 运行时审批语义太窄：approve/reject 不是“提问”

- `RuntimeActionRequest` 是为“有副作用的操作”设计的（拉镜像、装依赖、下载数据、联网、GPU）。
- 但研究中有大量“选择题”：用哪个数据集？是否保留失败 trial？是否继续扩大搜索？这些不是批准/拒绝，而是需要选项/自由文本。
- 根因：没有 `PlanQuestion`/`ask_user_question`；Agent 只能猜或失败。

### 3.4 没有 revision/diff：无法回答“为什么这次结果不同”

- `runFromNode` 直接 reset 当前 plan 的任务；`retry/reassign` 直接改任务；没有 revision 记录。
- 两次运行之间节点/参数/预算变化不可见；用户无法对比、无法回滚、无法审计“谁改了什么”。
- 根因：PlanGraph 没有版本；事件只记录动作，不记录计划快照。

### 3.5 审批分散：容易漏、无法批量

- pending runtime action 只能在对应 plan 的页面看；切换计划就看不到。
- 没有“所有待处理审批/提问”的聚合视图；没有批量批准低风险操作；没有提醒。
- 根因：没有 owner 级 approval inbox/query。

### 3.6 编辑与执行并发：没有一致性边界

- 当前 `runFromNode` 会拒绝 `StatusInProgress`，但其他编辑（若新增）没有统一锁。
- 如果允许“边跑边改”，scheduler 可能读到半更新图；任务/lease/epoch 只解决执行并发，不解决 plan 版本并发。
- 根因：没有 `revision_id` 绑定执行、没有 optimistic concurrency（`base_revision`）。

---

## 4. 为什么这么做

### 4.1 研究计划是“假设”，不是一次性脚本

- 研究过程天然迭代：换数据集、改超参、跳过失败分支、扩大搜索。
- 如果每次修改都要重新生成计划，用户会失去上下文/artifacts，也会重复花费。
- 正确模型是“计划草稿 + 不可变 revision”：编辑是便宜的，执行是昂贵的。

### 4.2 不可变 revision 是可复现/可审计的前提

- 执行必须绑定一个确定的图快照；否则同一 plan id 的两次运行不可比。
- revision 保留父版本、作者、原因、diff；可以回滚、对比、追责。
- 编辑运行中的 revision 必须被拒绝；要改就创建新 revision 并重新执行。

### 4.3 Review Gate 要按“成本/风险”触发，而不是所有计划都拦

- 所有计划都强制审阅会拖慢简单任务；只对高成本/高风险/用户显式要求审阅的计划进入 `awaiting_review`。
- 触发条件：预计成本超阈值、包含 GPU/下载/联网/长任务、用户开启“执行前审阅”、或 plan 生成置信度低。
- 审阅页必须能“直接跑”或“改完再跑”，不能只给一个 approve 按钮。

### 4.4 提问与审批分开

- 审批：有副作用、需要 allow/deny，默认拒绝更安全。
- 提问：无副作用、需要信息，选项/自由文本/默认值；超时可以走默认或阻塞。
- 两者共用“暂停-恢复”机制，但数据模型/UI/审计语义不同，不能混成一个 `RuntimeAction`。

### 4.5 先做“约束编辑”，不做完整 DAG 画布

- 完整拖拽 DAG 编辑器成本高、容易生成非法图。
- 第一阶段用“节点 inspector + 表单 + 依赖选择 + 校验”，支持改参数/增删节点/改依赖/跳过；画布拖拽后置。
- 这样能最快解决“改一个参数要重生成”的痛点，又不引入大量图编辑 bug。

---

## 5. 怎么做

### 5.1 数据模型

```go
type PlanRevision struct {
    ID             string
    PlanID         string
    Revision       int
    ParentRevision int
    Graph          *PlanGraph // 深拷贝快照
    Status         string     // draft | approved | executing | completed | superseded
    CreatedBy      string
    CreatedAt      time.Time
    Reason         string
    DiffSummary    []string
    ApprovedBy     string
    ApprovedAt     *time.Time
}

type PlanGraph struct {
    // 现有字段...
    CurrentRevision int    `json:"current_revision"`
    Review          ReviewState `json:"review,omitempty"`
}

type ReviewState struct {
    Required     bool
    Status       string // not_required | pending | approved | rejected
    Reason       string
    EstimatedUSD float64
    Risk         string
}

type PlanQuestion struct {
    ID             string
    PlanID         string
    Revision       int
    TaskID         string
    Header         string
    Prompt         string
    Options        []QuestionOption
    MultiSelect    bool
    AllowFreeText  bool
    DefaultOption  string
    Status         string // pending | answered | expired | canceled
    Answer         string
    AnswerOption   string
    AnsweredBy     string
    AnsweredAt     *time.Time
    ExpiresAt      *time.Time
}

type QuestionOption struct {
    ID          string
    Label       string
    Description string
}
```

约束：

- `PlanRevision` 只追加，不覆盖；`PlanGraph.CurrentRevision` 指向最新 approved/executing。
- `Graph` 必须深拷贝，禁止指针共享；revision 存储可放文件/DB，带 hash。
- `PlanQuestion` 与 `RuntimeActionRequest` 分开；审批有副作用，提问没有。
- 任务/事件/artifact 都带 `revision`，便于对比。

### 5.2 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/plans/:id/revisions` | revision 列表（不含大图） |
| GET | `/api/plans/:id/revisions/:revision` | revision 详情 |
| POST | `/api/plans/:id/revisions` | 以 `base_revision` + 新图创建 draft；乐观并发 |
| POST | `/api/plans/:id/revisions/:revision/validate` | 校验 DAG/任务类型/依赖/预算 |
| POST | `/api/plans/:id/revisions/:revision/approve` | 审阅通过（review gate） |
| POST | `/api/plans/:id/revisions/:revision/execute` | 执行指定 revision |
| GET | `/api/plans/:id/diff?from=1&to=2` | 节点/参数/依赖/预算 diff |
| GET | `/api/plans/:id/questions` | 当前计划提问 |
| POST | `/api/plans/:id/questions/:questionId/answer` | 回答并恢复 |
| GET | `/api/approvals?status=pending&kind=approval/question` | 跨计划收件箱 |
| POST | `/api/approvals/bulk` | 批量处理低风险审批（可选） |

编辑请求示例：

```json
{
  "base_revision": 2,
  "reason": "把数据集换成用户上传的 v2",
  "graph": { "...": "完整 PlanGraph" }
}
```

- 服务端校验 `base_revision` 是否仍为当前最新；冲突返回 409 + 当前 revision。
- 校验失败返回结构化 `validation_errors[]`（节点/字段/原因/修复建议）。
- 执行时 scheduler 固定 `revision`；运行中拒绝新 revision 执行，直到当前完成/取消。

### 5.3 编辑能力与校验

第一阶段支持：

- 改节点字段：`Name`、`Description`、`Inputs`、`TimeoutSeconds`、`RetryLimit`、`AssignedTo`；
- 增/删节点；
- 增/删依赖边；
- 跳过节点（`Enabled=false` 或 `Status=skipped`）；
- 改计划预算（`MaxCostUSD`/`MaxDurationSec` 等）；
- 改运行策略（`RuntimePolicy` 的有限字段，不能提权）。

校验：

1. DAG 无环、无悬挂边；
2. `Type`/`AssignedTo` 在 task registry 中存在；
3. 依赖产物满足 `RequiredArtifacts`/`InputBindings`；
4. 预算/超时/重试为正数且不超过系统上限；
5. 运行中 revision 不可编辑；
6. 权限：用户只能编辑自己的 plan；共享只读不能编辑。

### 5.4 Review Gate

- 触发条件（任一）：
  - 预计成本 > `REVIEW_COST_THRESHOLD_USD`；
  - 含 GPU/大下载/联网/长任务；
  - 用户开启“执行前审阅”；
  - planner 置信度低/有 missing requirements。
- 状态：`awaiting_review`；前端显示“待审阅”，不自动跑。
- 审阅页：
  - 左：DAG 概览 + 节点列表；
  - 右：节点 inspector（参数/输入/依赖/预算）；
  - 顶：预计成本/耗时/风险/校验结果；
  - 操作：`保存草稿`、`校验`、`批准并运行`、`拒绝`。
- 批准后：`Review.Status=approved`，记录 actor/time；执行 `revision`；事件 `plan_reviewed`。

### 5.5 结构化提问 ask_user_question

Agent 工具 schema：

```json
{
  "header": "选择数据集",
  "question": "使用哪个版本的训练集？",
  "options": [
    {"id": "v1", "label": "v1（原始）", "description": "复现论文原设置"},
    {"id": "v2", "label": "v2（清洗后）", "description": "指标更高但不可直接对比"}
  ],
  "multi_select": false,
  "allow_free_text": true,
  "default_option": "v1",
  "timeout_seconds": 900
}
```

流程：

1. Agent 调工具 → 后端创建 `PlanQuestion`，任务状态 `awaiting_input`，计划 `awaiting_input`；
2. SSE 推送 `question_requested`；前端显示选项/自由文本/倒计时；
3. 用户回答 → `POST /questions/:id/answer` 写入答案，任务恢复；
4. 超时：若 `default_option` 存在则自动回答并记录 `auto_answered`；否则任务 `blocked` + missing requirement；
5. 审计：谁回答、何时、选了什么、是否超时。

与审批的区别：

| 维度 | RuntimeAction | PlanQuestion |
|---|---|---|
| 目的 | 是否允许有副作用操作 | 获取决策信息 |
| 选项 | approve/reject | 多选/单选/自由文本 |
| 默认 | 拒绝更安全 | 可配置默认/阻塞 |
| UI | 审批卡 | 问答卡 |
| 审计 | 允许/拒绝 | 答案/来源/超时 |

### 5.6 审批收件箱

- 后端：`GET /api/approvals?status=pending&kind=approval/question&plan_id=&cursor=`。
- 返回：plan 标题、节点、类型、风险、预计成本、等待时长、过期时间。
- 前端：顶栏铃铛 + 侧栏；按计划/类型/风险过滤；“批准/拒绝/回答/打开计划”。
- 批量：只允许低风险、同一类型的 approve/reject；高风险逐条确认；批量操作写 audit。
- 提醒：站内 toast；可选邮件/Webhook（后续）。

### 5.7 Diff

- 比较 revision from/to：
  - 节点新增/删除/修改（字段级）；
  - 边新增/删除；
  - 预算/超时/重试变化；
  - 输入/依赖变化。
- 输出 `diff[]`：`{kind, node_id, field, before, after}`；前端并排/行内高亮。
- 执行页显示“当前运行 revision 3；最新 draft revision 4”，避免误以为在跑最新图。

### 5.8 前端结构

```text
计划详情
  ├─ 顶部：状态 / revision 选择 / 审阅 badge / 运行按钮
  ├─ 左：DAG 概览（只读高亮）
  ├─ 右：节点 inspector（编辑表单）
  ├─ 底部：校验结果 / diff / 预算
  └─ 审批收件箱（全局侧栏）
        ├─ 运行时审批
        └─ 计划提问
```

- 编辑态与运行态分离：运行中只读；`编辑` 创建 draft。
- `保存` 只保存 draft，不自动执行；`批准并运行` 才执行。
- 失败回滚：选择历史 revision → `另存为 draft` → 修改，不直接改历史。

### 5.9 实施阶段

| 阶段 | 周期 | 内容 | 交付 |
|---|---|---|---|
| Phase 0（已有） | — | pause/resume、run-from-node、retry/reassign、runtime actions、plan approval | 当前能力 |
| Phase 1 | 1–2 周 | PlanRevision 存储/API、read-only diff、节点参数编辑、校验 | 改参数不再重生成；可对比 revision |
| Phase 2 | 2 周 | Review Gate、审阅页、`awaiting_review`、执行绑定 revision | 昂贵计划先审后跑 |
| Phase 3 | 2 周 | 增删节点/边、跳过节点、完整 diff、回滚 | 约束式 DAG 编辑 |
| Phase 4 | 2 周 | ask_user_question、问答卡、审批收件箱、批量低风险审批 | 真正 HITL 闭环 |

---

## 6. 验收标准（容器内）

1. **Revision**：改一个节点参数 → 生成 revision 2，revision 1 不变；执行 revision 2 后事件/artifact 带 `revision=2`。
2. **并发**：两个客户端同时基于 revision 2 保存，第二个返回 409 + 当前 revision；不丢更新。
3. **运行中保护**：`StatusInProgress` 时保存/执行新 revision 返回 409；取消后可执行。
4. **Review Gate**：设置成本阈值，计划创建后为 `awaiting_review`，不自动执行；批准后执行；拒绝后不执行。
5. **Diff**：revision 1→2 显示字段级变化、边变化、预算变化；历史 revision 可另存为 draft。
6. **Question**：Agent 提问 → 前端显示选项/自由文本；回答后任务恢复；超时按默认/阻塞；审计记录。
7. **Inbox**：两个计划各有一个 pending action/question，收件箱一次看到；批量批准低风险；高风险不可批量。
8. **权限**：非 owner 不能读/改/批；分享只读不能编辑。
9. **审计**：`plan_revision_created`、`plan_reviewed`、`question_answered`、`approval_bulk_decided` 事件可查。

---

## 7. 与旧提案的差异

| 项 | 旧提案 | 本文改进版 |
|---|---|---|
| 暂停/恢复 | 列为待做 | **已实现**；不重复 |
| 从节点重跑 | 列为待做 | **已实现**；但没有 revision |
| 运行时审批 | 列为待做 | **已实现**；但只有 approve/reject |
| DAG 编辑器 | 直接做完整编辑器 | 先做约束式 inspector + 校验；完整画布后置 |
| ask_user_question | 假设“后端已有工具” | **实际没有**；本设计从零定义工具/模型/流程 |
| 工作量 | L（3–4 周） | 拆成 Phase 1–4，先 revision/审阅，再提问/收件箱 |

---

## 8. 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| 计划控制 | `backend/internal/api/plan_control.go` | pause/resume/run-from-node |
| 计划审批 | `backend/internal/api/plan_runtime.go:725-759` | plan approval |
| 运行时审批 | `backend/internal/api/runtime_actions.go` | approve/reject/timeout |
| 模型 | `backend/internal/models/graph.go:89-125` | PlanGraph 无 revision |
| 审批模型 | `backend/internal/models/runtime.go:17-30`、`reproduction.go:40-82` | ApprovalState/RuntimeAction |
| 前端控制 | `frontend/src/app/viewModels/useGraphExecutionViewModel.ts:47-74` | pending action/decision |
| 前端 API | `frontend/src/services/api/researchAssistantApi.ts:160-190` | runtime actions |
| 调度 | `backend/internal/scheduler/scheduler.go` | task lease/epoch/status |

---

## 9. 非目标与风险

### 非目标

- 不做多人实时协同编辑（后置到 N-12）；
- 不做任意脚本/任意 DAG 节点注入；只允许 task registry 内的类型；
- 不绕过审批直接执行高风险操作；
- 不在运行中热改 revision；要改就新建 revision；
- 不保证所有提问都能自动回答；超时允许阻塞。

### 风险与缓解

| 风险 | 缓解 |
|---|---|
| 编辑破坏 DAG 语义 | 保存前校验 + 禁止环/悬挂边 + 任务 registry 校验 |
| revision 存储膨胀 | 只存必要快照；大 artifact 用 ref；GC 旧 draft |
| 审阅拖慢简单任务 | 按成本/风险触发；简单计划默认不审 |
| 提问疲劳 | 只在真正歧义时问；提供默认/超时；限制每计划问题数 |
| 批量误批 | 只允许低风险；高风险逐条；审计 |
| 并发覆盖 | base_revision 乐观锁 + 409；运行中禁止编辑 |
| 权限泄露 | owner 校验；分享只读；审计 |

---

## 10. 结论

- **N-05 的第一步不是画布，而是 revision**：没有不可变计划快照，编辑/审阅/diff/回滚都无法成立。
- **第二步是 Review Gate**：让昂贵计划先审后跑，而不是等失败了再取消。
- **第三步才是提问与收件箱**：把“有副作用的审批”和“获取信息的提问”分开。
- 现有 pause/resume/run-from-node/runtime approval 是良好底座，应复用而不是重写。
