# FIX-REPORT-ROUND9：N-05 计划审阅与人在回路（Plan Review & HITL）

- 日期：2026-10-29（Asia/Shanghai）
- 依据：`docs/design/n05-plan-review-hitl.md`
- 基线：`master @ fb8938c` + 前八轮工作区成果
- 测试环境：Docker Compose（backend/frontend/intent/sandbox/optimizer）；Go 测试在 `golang:1.26.1-bookworm`，前端在 `node:22-bookworm-slim`

## 1. 结果摘要

| 编号 | 交付 | 验证 |
|---|---|---|
| A1 | PlanRevision 不可变快照（独立 `internal/revision` Memory/File Store，JSON 深拷贝 + sha256） | Go 单测（追加/latest/并发/冲突/持久化）+ E2E `plan-revisions.mjs` |
| A2 | `base_revision` 乐观并发（409 + current_revision） | Go 单测 + E2E |
| A3 | 运行中保护（in_progress 拒绝编辑/执行新 revision；取消后可执行） | Go 单测 + E2E |
| A4 | Review Gate（模式/显式/成本阈值/高风险/缺需求/L4 → awaiting_review；批准/拒绝状态机） | Go 单测（4 个触发分支）+ E2E `review-hitl.mjs` |
| A5 | Revision diff（节点/字段/边/预算）+ 历史 revision 另存 draft | Go 单测 + E2E |
| A6 | 结构化提问（HTTP 工具端点 + `ask_user_question` 任务类型 + 超时默认/阻塞 + 审计） | Go 单测（创建/回答/超时/恢复）+ Scheduler 单测 + E2E |
| A7 | 跨计划审批收件箱（聚合 runtime actions + questions，keyset cursor，批量低风险 ≤50，高风险逐条） | Go 单测 + E2E |
| A8 | 权限（非 owner 403；收件箱按 owner 隔离） | Go 单测 + E2E |
| A9 | 审计事件（plan_revision_created/executed、plan_reviewed、question_requested/answered/expired、approval_bulk_decided） | E2E 审计查询通过 |

## 2. 与设计文档的差异（以实际为准）

| 设计描述 | 实际实现 |
|---|---|
| `PlanRevision.Graph *PlanGraph // 深拷贝快照`，存储可放文件/DB | 独立 `internal/revision` Memory/File Store，JSON 往返深拷贝 + sha256 hash；不写入 PlanGraph 数组（避免与执行期高频 UpdatePlan 冲突和快照膨胀） |
| 文档 API 只列 `revisions/:revision/approve` | 同时提供 `reject`（5.4 审阅页含拒绝操作），否则拒绝无法落库 |
| `ask_user_question` 为 Agent 工具 schema | 当前仓库没有通用 LLM tool-calling 循环（Eino 链只有 Prompt→Model→Extract）；实现为 task registry 的 `ask_user_question` 任务类型 + HTTP 工具端点 + `TaskExecutionResult.Question`，行为等价（暂停/选项/自由文本/超时/恢复） |
| `PlanGraph.CurrentRevision` 指向最新 approved/executing | CurrentRevision 只在 execute 时更新（指向正在执行的 revision）；"最新草稿"由 revision store 的 latest 表示，避免草案冒充"正在执行" |
| 任务/事件/artifact 都带 revision | 已加字段；历史数据为 0，不回填 |
| 审批收件箱含批量低风险 | risk != high 可批量；high 逐条；批量上限 50；批量与单条共用同一状态机 |
| planner 置信度低触发 review | 以 `MissingRequirements` 非空或复杂度 L4 作为代理，不引入新置信度模型 |
| `GET /api/approvals?kind=approval/question` | kind 支持 `approval|question|all`（默认 all），status 默认 pending |

## 3. 关键实现

### 3.1 数据模型
- `models/revision.go`：`PlanRevision`、`ReviewState`、`PlanQuestion`、`QuestionOption`、revision/review/question 状态常量、`RevisionGraphHash`。
- `PlanGraph` 增加 `CurrentRevision/Review/Questions`；`TaskNode.Revision/Enabled`；`PlanEvent.Revision`；`Artifact.Revision`。
- `models/task.go` 增加 `StatusAwaitingReview`、`StatusAwaitingInput`。

### 3.2 Revision 存储与 API
- `internal/revision`：`Store` 接口 + MemoryStore + FileStore（原子 JSON、互斥锁、失败回滚）、`Create` 内部分配 revision、`ConflictError` 携带当前 revision。
- `api/plan_revisions.go`：list/get/create/validate/approve/reject/execute/diff；惰性补建 revision 1；历史 revision 另存为 draft 通过 create 端点完成。
- `api/plan_diff.go`：节点新增/删除/字段级修改、边新增/删除、预算字段变化。
- `api/plan_validation.go`：DAG 无环/无悬挂边、task registry、依赖产物、预算上限、结构化修复建议。

### 3.3 Review Gate
- `api/review_gate.go`：`PLAN_REVIEW_MODE=always`、`require_review`、`REVIEW_COST_THRESHOLD_USD`、高风险任务、缺需求、L4 复杂度；`EstimatedUSD` 只用于阈值/UI，不写入 Usage。
- awaiting_review 时 `POST /execute` 与 revision execute 均返回 409 `review_pending`；批准/拒绝写 `Review` 与 revision 状态 + 事件/审计。

### 3.4 结构化提问
- `api/plan_questions.go`：创建/列表/回答；default_option 必须匹配选项；回答写回任务 `Inputs.question_answer`，任务恢复 ready/pending。
- `scheduler/questions.go`：结果携带 Question 时暂停任务/计划；`expireQuestions` 默认自动回答（auto_answered）或阻塞并记录 `question_timeout`；API 读取时惰性扫描保证无调度协程也生效。
- `scheduler/executor.go`：`ask_user_question` 节点读取答案或返回 Question。

### 3.5 审批收件箱
- `api/approvals.go`：跨 plan 聚合 runtime actions + questions、owner 隔离、keyset cursor、kind/status/plan_id 过滤；批量只允许 low/medium risk、≤50，未知/已处理 id 400/404/409，不产生部分写入。

### 3.6 前端
- `features/review/PlanReviewPanel.tsx`：revision 列表、节点 inspector（名称/描述/执行者/超时/重试/启用/依赖/输入 JSON）、预算、校验结果、diff 并排、保存草稿、批准并运行、拒绝。
- `features/review/ApprovalInboxPanel.tsx`：跨计划列表、type 过滤、批量勾选（高风险禁用）、问题回答、打开计划。
- `GraphPanel`：待回答问题卡（单选/多选/自由文本）、待审阅"去审阅"按钮、新状态文案。
- 顶栏新增"审阅""收件箱"（含待处理角标）。

## 4. 测试证据（容器内）

### 4.1 后端
命令：`cd research-assistant && ./scripts/container-go.sh "go build ./... && go vet ./... && go test ./... -count=1 -timeout 1200s"`
结果：全部通过（28 个有测试的包全绿，另 11 个无测试文件；含 `internal/revision` 新增包、`internal/api`、`internal/scheduler`）。

### 4.2 前端
`node:22-bookworm-slim` 内 `npm test && npm run build && npm run lint`：29 个测试文件 / 126 个测试通过；build 通过；lint 0 error。

### 4.3 E2E（Compose 栈）
- `plan-revisions.mjs`：ALL PASSED（18 项，含 revision 绑定事件断言）。
- `review-hitl.mjs`：ALL PASSED（21 项）。
- 回归：`http-smoke`、`plan-library`、`repro-bundle`、`cost-budget`、`budget-recovery`、`routing-and-prompts` 全部通过。

### 4.4 真实浏览器（Chrome）手工验收
- 顶栏新增"审阅""收件箱"入口可见。
- 打开"计划审阅"：revision 历史（r1）、节点列表、节点 inspector（名称/描述/执行者/超时/重试/启用/依赖/输入 JSON）、预算字段、校验、保存草稿、拒绝、批准并运行均渲染正常。
- 编辑节点名称 → 校验通过 → 保存草稿 → revision 列表出现 r2（"edited in review panel"），最新 revision 为 r2，工作台执行图同步显示新名称。
- "与 r1 比较"显示 DIFF R1 → R2 表格（node_modified / name / 之前 / 之后）。
- 点击"批准并运行"后计划状态切换为待运行，节点全部重置为"待运行"。
- 打开"审批收件箱"：空态文案、批量操作提示（低风险批量/高风险逐条）正常。

### 4.4 环境限制（重要）
- 容器内 LLM Provider 返回 403："分组「高速满血v4.1f-1017 #1038」的计费倍率已由 0.05 上调为 0.07，请求已暂停（未扣费）"。
- 因此依赖真实 LLM 的端到端执行（`plan-run.mjs --execute` 的节点执行）在容器内被上游拒绝；这是账户/计费状态，不是本轮代码缺陷。
- 所有不依赖 LLM 的路径（计划创建、revision、审阅门、提问、收件箱、导出、预算、审计）均已在容器内验证通过；需要 LLM 的单元/集成测试使用 stub runner 覆盖。
## 5. 清理

- 临时容器/镜像：本轮构建仅使用 `--rm` 测试容器；最终 compose 镜像为本轮重建的 backend/frontend，无 dangling 镜像残留。验证 UI 时临时拉取的 `mcr.microsoft.com/playwright:v1.61.1-jammy`（3.24 GB，未使用）已删除。
- 临时文件：已删除 `/tmp` 下本轮产生的 JSON/脚本；仓库内无临时报告目录。
- 未删除任何用户数据卷。

## 6. 结论

- N-05 九条验收标准 A1–A9 全部在容器内通过（Go 单测 + 前端组件测试 + Compose E2E + 真实浏览器手工验收）。
- 无新增回归：既有后端 39 个包全绿；前端 29 个测试文件 / 126 个测试全绿；既有 E2E（http-smoke、plan-library、repro-bundle、cost-budget、budget-recovery、routing-and-prompts）全通过。
- 唯一未覆盖的是依赖真实 LLM 的任务执行，因为容器内 LLM provider 因计费倍率调整返回 403；已在此报告和 `docs/verification.md` 中明确记录。

## 7. LLM 恢复后的复测（2026-10-29 17:26 起）

计费倍率确认由用户完成后，重新执行依赖真实 LLM 的测试：

| 测试 | 命令/方式 | 结果 |
|---|---|---|
| 真实 LLM 计划执行（7 节点代码执行 + 绘图 + 视觉校验） | `plan-run.mjs --execute --approve-all` | **completed / outcome=completed**；LLM calls=3、tokens 1902/1330；沙箱 205.48s；生成 `2970×1768` PNG 并通过 visual 校验（置信度 0.95） |
| N-05 revision 专项 | `plan-revisions.mjs` | **ALL PASSED** |
| N-05 审阅/HITL 专项 | `review-hitl.mjs` | **ALL PASSED** |
| 既有回归 | http-smoke / plan-library / repro-bundle / cost-budget / budget-recovery / routing-and-prompts | 全部通过 |
| 文档智能（e2e overlay：PDF_OCR_MAX_PAGES=20） | `document-intelligence.mjs` | all checks passed |
| 模型 profile（容器网络 + ADMIN_TOKEN + MOCK_VISION_URL） | `model-profiles.mjs` | all checks passed |
| 视觉校验（mock-vision，A–D 场景） | `verification.mjs` | all scenarios passed |
| 沙箱网络 | `sandbox-network.mjs` | all checks passed |
| Round7 真实场景（code / plot / paper / literature） | `round7-real-scenarios.mjs` | all real-scenario checks passed（code 3/3、plot 7/7 生成 2970×1769 PNG、paper 4/4 分析 24021 字符并命中 Transformer/BLEU/页码、文献 5/5 相关） |

**结论**：LLM 计费暂停已解除，此前唯一未覆盖的真实 LLM 执行链路现已完整验证通过；N-05 九条验收标准与既有功能均无回归。

### 复跑注意（E2E 环境差异）
- `document-intelligence` 需要 `test/docker-compose.e2e.yml` overlay（`PDF_OCR_MAX_PAGES=20`、`DOCUMENT_CACHE_DISABLED=true`），默认栈为 5 页预算，断言 20 页 OCR 会失败。
- `model-profiles` / `verification` 需要 `MOCK_VISION_URL=http://mock-vision:9099` 且从 compose 网络内运行，并传入 `ADMIN_TOKEN`。
- `sandbox-network` 需要 `SANDBOX_URL=http://docker-sandbox:8082` + `SANDBOX_API_TOKEN` 且从 compose 网络内运行。
- `round7-real-scenarios` 需要挂载 `/reports` 与 `/papers` 目录。
