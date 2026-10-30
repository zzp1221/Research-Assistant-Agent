# 实施计划：N-01 计划库 / N-02 可复现导出 / N-03 成本与预算控制台（2026-10-28 第八轮）

- 基线：`master @ fb8938c` + 前几轮未提交成果（后端 build/vet 基线已通过）
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 compose 栈）
- 完成定义：`spec.md` 第 7 节；每阶段结束跑容器内构建/单测，最后统一 E2E + 回归。
- 状态：已完成（2026-10-28）；容器证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND8.md`。

## 阶段 P0 — 基线与规格
1. 复核设计文档与实际代码差异，写 `spec.md` / `plan.md` / `tasks.md`（已完成，差异见 spec 第 9 节）。
2. 容器内基线：`go build ./...`、`go vet ./...`（已通过）。
3. 归档第五轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND5-*.md`（已完成）。

## 阶段 P1 — N-01 计划库后端
1. `models/project.go`：Project 结构 + 校验常量。
2. `models/graph.go`：PlanGraph 增加 Title/ProjectID/Archived/ArchivedAt（omitempty）。
3. `store`：新增 `PlanSummaryRecord/PlanSummarySource`；Memory/File 实现 `ListPlanSummaries`；轻量投影（不深拷贝）。
4. `internal/project/store.go`：Store 接口 + MemoryStore + FileStore（owner 隔离、持久化、空项目删除）。
5. `api/projects.go`：GET/POST/PATCH/DELETE /api/projects。
6. `api/plan_runtime.go`：listPlans 重写（q/status/intent_type/project_id/archived/sort/keyset）；PATCH /api/plans/:id；POST /api/plans/:id/duplicate。
7. `api/plan_runtime.go`：summary 增加 title/project_id/archived/paused/usage/failed_nodes。
8. 单测：store/project + plan_runtime（1000 计划、过滤、游标、归档、重命名、复制、运行中删除 409）。

## 阶段 P2 — N-01 前端计划库
1. `contracts/api.ts`：PlanSummary 补字段；Project 类型；PlanLibrary 查询类型。
2. `services/api/researchAssistantApi.ts`：listProjects/createProject/updateProject/deleteProject/listPlans 扩展/patchPlan/duplicatePlan。
3. `features/plan-library/PlanLibraryPanel.tsx`：项目 rail、搜索/筛选、卡片网格、加载更多、操作菜单、删除确认。
4. `ResearchAssistantApp.tsx`：顶栏"计划库"入口 + 打开计划回调（载入工作台，不建临时会话）。
5. 组件测试：PlanLibraryPanel 渲染/筛选/操作（vitest + testing-library）。

## 阶段 P3 — N-02 可复现导出包 v2
1. `api/plan_export.go` 重构：bundle builder（内存 entry + sha256）、manifest、checksums、environment recipe、provenance、omitted/missing、reproducibility。
2. 脱敏：`api/redact.go`（map key + 文本模式 + 深度限制）；plan.json/events/README 均脱敏。
3. 大小上限 + 流式 zip：`EXPORT_MAX_FILE_MB/EXPORT_MAX_TOTAL_MB`；错误码 `export_failed`。
4. `POST /api/plans/:id/reproduce/verify`：静态校验（必需文件、checksum、manifest 自洽）。
5. Markdown/JSON 导出升级：schema 版本、如何复现章节、脱敏。
6. 审计事件 `plan_exported` + 指标。
7. 单测：bundle 结构、sha256sum 校验、recipe、脱敏、omitted、partial、确定性、verify。

## 阶段 P4 — N-03 成本与预算后端
1. `models/runtime.go`：RunBudget 扩展（MaxTokens/MaxCostUSD/MaxSandboxSeconds/MaxDownloadBytes/Policy）；TaskUsage.CostSource；RunUsage.CostSource/BudgetStatus/BudgetWarnings。
2. `usage/usage.go`：`RecordLLMWithPrice`、CostSource、Snapshot.ByModel.CostSource；`EstimateCostWithPrice`。
3. `llmclient/generate.go`：`WithModelPrice/ModelPriceFromContext`；`RecordUsage` 使用价格。
4. `agent/dynamic_model.go` + `usage_model.go`：profile 单价注入（Generate/Stream/Stream 包装）。
5. `internal/budget`：Entry/UserBudget/Reservation/Store（Memory+File）/Manager（Reserve/Settle/Release/Summary/Timeseries/Alerts）。
6. `scheduler`：预算检查（cost/token/sandbox）、80% warning、soft pause/hard cancel、预留/结算/释放、事件与指标。
7. `api/usage.go`：`/api/usage/summary`、`/api/usage/timeseries`、`/api/plans/:id/usage`、`/api/budgets`、`/api/budgets/alerts`。
8. `api/plan_runtime.go`：创建计划时从 env/payload 读取预算上限（PLAN_MAX_COST_USD 等）；summary/usage 暴露 cost_source。
9. 单测：usage 计价、budget manager、scheduler 预算闸门、API handler。

## 阶段 P5 — N-03 前端成本控制台
1. `contracts/api.ts`：UsageSummary/PlanUsage/Timeseries/Budget 类型；GraphTask.usage。
2. `services/api/researchAssistantApi.ts`：getUsageSummary/getPlanUsage/getUsageTimeseries/getBudget/saveBudget。
3. `features/cost/CostConsolePanel.tsx`：今日/本月、预算进度、Top 计划/模型、趋势柱状图、预算表单。
4. `ResearchAssistantApp.tsx`：顶栏"成本"入口 + masthead 总成本。
5. `ExecutionSidebar.tsx`：节点 usage（tokens/USD/耗时/模型/来源）展示。
6. 组件测试。

## 阶段 P6 — 容器回归、E2E、清理
1. 后端全量：`go build ./... && go vet ./... && go test ./...`；新增包 `-race`。
2. 前端容器：`npm ci && npm test && npm run build && npm run lint`。
3. 重建 backend/frontend 镜像 + compose 起栈（含 mock-vision profile 按需）。
4. 新增 E2E：`plan-library.mjs`、`repro-bundle.mjs`、`cost-budget.mjs`。
5. 回归既有 E2E：http-smoke、plan-run、model-profiles、verification、literature-plan、document-intelligence 等。
6. 清理临时容器/镜像/卷/脚本/产物；删除死代码。
7. 更新 README/OpenAPI/FIX-REPORT-ROUND8/spec/plan/tasks。

## 风险与对策
- 计划列表兼容：保留旧查询参数与响应字段；keyset cursor 与旧 offset 数字不兼容时，旧客户端传数字按 offset 解释（向后兼容）。
- 导出包内存：逐文件构建 entry，超过上限立即 omitted；最终 zip 流式写响应，避免双份内存。
- 脱敏误伤：仅对敏感 key/明确密钥模式生效，artifactId/节点名不受影响；单测覆盖。
- 预算误杀：默认 soft 且只在显式配置上限时生效；无配置=不限制；暂停可恢复。
- profile 价格缺失：回退 env；都缺失则 estimated/unknown，不阻断执行。
- 前端新增面板：沿用现有全屏面板模式与设计 token，不引入 router；组件测试覆盖。
- 容器资源：测试用 --rm；结束后清理 dangling 镜像/卷，不删除 compose 数据卷。
