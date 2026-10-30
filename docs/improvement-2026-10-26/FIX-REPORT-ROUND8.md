# FIX-REPORT-ROUND8：N-01 计划库 / N-02 可复现导出 / N-03 成本与预算控制台

- 日期：2026-10-28（Asia/Shanghai）
- 依据：`docs/design/n01-n03-plan-library-reproducibility-cost.md`
- 基线：`master @ fb8938c` + 前几轮工作区成果
- 测试环境：Docker Compose（backend/frontend/intent/sandbox/optimizer）；Go 测试在 `golang:1.26.1-bookworm`，前端在 `node:22-bookworm-slim`

## 1. 结果摘要

| 编号 | 交付 | 验证 |
|---|---|---|
| N-01 | 计划库后端（项目、搜索、过滤、keyset 分页、重命名/移动/归档/复制、运行中删除 409）+ 前端独立计划库页面 | Go 单测（含 1,000 计划 <1s、翻页无重复/遗漏、过滤/归档/重命名/复制）；E2E `plan-library.mjs` 全通过 |
| N-02 | 可复现导出包 v2（manifest/checksums/environment recipe/provenance/artifact 映射/脱敏/大小上限/omitted/partial）+ 静态 verify + 审计/指标 | Go 单测（结构、sha256、recipe、脱敏、omitted、partial、确定性、verify）；E2E `repro-bundle.mjs` 全通过（`sha256sum -c` 全通过） |
| N-03 | 成本与预算控制台（RunBudget 扩展、profile 计价、预算台账/预留/结算、80% 预警、soft/hard 闸门、usage/budget API、前端成本页、节点成本） | Go 单测（usage 计价、budget、scheduler 闸门）；E2E `cost-budget.mjs` / `budget-recovery.mjs` 全通过 |

## 2. 与设计文档的差异（以实际为准）

| 设计描述 | 实际实现 |
|---|---|
| 建议 SQLite/bbolt 建计划索引 | 现有 Memory/File PlanStore 已全量持有计划；新增 `PlanSummaryRecord` 轻量投影（不深拷贝节点）+ keyset 游标，1,000 计划列表满足 <1s，避免新增存储依赖 |
| `POST /api/plans/:id/share`（可选） | 本轮未实现只读分享 token；N-12 协作/分享为独立非目标。计划库已具备 owner 隔离与导出，分享留待后续 |
| 完整 replay（干净容器重跑并对比指标） | 本轮实现静态校验（必需文件、manifest 大小/哈希、checksums 自洽），响应明确 `static_only=true`；未宣称已完成动态 replay |
| GPU 分钟/下载字节/存储成本 | 记录 `MaxDownloadBytes`/沙箱秒数字段；未计算 GPU/存储美元（缺少单价来源），保持扩展字段 |
| 预算感知路由完整闭环 | 实现 `cost_policy=budget_first` 对模板变体的最小降级（L3/L4→L2、full→standard），记录 `cost_policy_budget_first`；不做跨 provider 自动比价 |
| `manifest.json` 自身不列入 checksums | 实现将 manifest.json 列入 `checksums.sha256`（manifest 本身不列 checksums，无自引用），因此 `sha256sum -c` 能同时校验 manifest 与全部数据文件 |

## 3. N-01 实现

- 模型：`models.Project`；`PlanGraph` 新增 `Title/ProjectID/Archived/ArchivedAt`（omitempty，旧 JSON 兼容）。
- 存储：`store.PlanSummaryRecord/PlanSummarySource`，Memory/File 均实现 `ListPlanSummaries`（不深拷贝节点）；`internal/project` Memory/File Store（owner 隔离、持久化、重名/空名校验）。
- API：
  - `GET/POST /api/projects`、`PATCH/DELETE /api/projects/:id`（非空项目 409）。
  - `GET /api/plans`：`q/status/intent_type/project_id/archived/sort/cursor/limit/session_id`；游标为 `(updated_at,id)` keyset（数字 cursor 按 legacy offset 兼容）。
  - `PATCH /api/plans/:id`：title/project_id/archived，支持 budget 更新（软暂停恢复）；`POST /api/plans/:id/duplicate` 清空执行状态/artifacts/usage。
  - 写操作 owner 校验 + audit event；运行中/待审批计划删除 409。
- 前端：`PlanLibraryPanel`（项目 rail、搜索/状态/意图/排序/归档过滤、卡片、加载更多、重命名/移动/归档/复制/导出/删除），顶栏“计划库”入口；远端计划使用稳定 `plan:<id>` 会话视图，不重复创建本地副本。

## 4. N-02 实现

- 包结构：README.md、manifest.json、checksums.sha256、plan.json、events.jsonl、environment/{requirements.lock,system-packages.txt,Dockerfile,run.sh}、provenance/{model-snapshot,runtime-policy,dataset-hashes,model-hashes}.json、artifacts/*。
- manifest：`repro.bundle/v1`，files[]（path/size/sha256/kind）、artifacts[]、environment、run_command、expected、omitted[]、missing[]、reproducibility=complete|partial；`created_at`=plan.UpdatedAt 保证重复导出确定性。
- 环境 recipe：PipFreeze→requirements.lock；SystemPackages→system-packages.txt；Dockerfile 使用 `image@digest`（缺 digest 时 FROM image + partial）；run.sh 设置 PYTHONHASHSEED=0、按 RunCommand 执行（无命令 exit 2 + partial）。
- 安全：map key 与文本双重脱敏（Authorization/api_key/token/password/secret/私钥/常见 key 模式）；inline base64 图片替换为 artifact 指针；artifact 类型白名单；单文件/总大小上限（默认 16/128MB），超限进 omitted；zip 流式写出。
- 审计/指标：`plan_exported`（format/bytes/bundle_sha256/omitted/reproducibility）、`plan_export_total`、`export_omitted_bytes_total`。
- 校验：`POST /api/plans/:id/reproduce/verify` 返回 passed|partial|failed + checks/missing/omitted + `static_only=true`。

## 5. N-03 实现

- 模型：`RunBudget` 增加 MaxTokens/MaxCostUSD/MaxSandboxSeconds/MaxDownloadBytes/Policy；`TaskUsage.CostSource/ModelBreakdown`；`RunUsage.CostSource/BudgetStatus/BudgetWarnings`。
- 计价：`usage.RecordLLMWithPrice`（profile > env > unknown）；`llmclient.WithModelPrice/ModelPriceFromContext`；`dynamicChatModel/usageChatModel/GenerateWithUsage` 注入 profile 单价。
- 台账：`internal/budget`（Entry/UserBudget/Alert/Reservation + Memory/File Store + Manager Reserve/Settle/Release/Summary/Timeseries/Alerts），默认 `BUDGET_STORE_PATH` 或 `<PLAN_STORE_PATH>.budget.json`。
- 调度：派发前 `budgetGate`（cost/token/sandbox，含下一步估算）→ 80% warning → 100% soft 暂停/hard 取消；任务完成结算预留；计划终态释放预留；事件 `budget_warning/budget_exceeded/plan_paused` + 指标。
- API/UI：`/api/usage/summary`、`/api/usage/timeseries`、`/api/plans/:id/usage`、`GET/PUT /api/budgets`、`/api/budgets/alerts`；前端 `CostConsolePanel`（今日/本月/预算进度/剩余/Top 计划/模型/趋势/预警/预算表单）；计划详情 masthead 总成本 + ExecutionSidebar 节点/模型成本。

## 6. 测试证据（全部容器内）

### 6.1 后端

命令：`cd research-assistant && ./scripts/container-go.sh "go build ./... && go vet ./... && go test ./... -count=1 -timeout 900s"`

结果：通过。包含：
- `internal/api`：计划库 1,000 计划分页/过滤/归档/重命名/复制/409、项目 CRUD、导出 bundle/脱敏/partial/确定性/verify、usage/budget API；
- `internal/budget`：预留/结算/释放/Summary/Timeseries/Alerts（-race 通过）；
- `internal/usage`：profile/env/unknown 计价与 CostSource 合并；
- `internal/scheduler`：MaxCostUSD 派发前暂停、hard 取消、80% warning、用户预算阻断；
- `internal/project`：owner 隔离/校验/文件持久化；
- 其余既有包全绿（agent 67.9s、scheduler 12.8s 等）。

### 6.2 前端

命令：`node:22-bookworm-slim` 内 `npm test && npm run build && npm run lint`

结果：27 个测试文件 / 117 个测试通过；build 通过；lint 0 error（仅 pdfjs 既有 eval 警告与 chunk 体积提示）。

### 6.3 其它 Go 模块

`go test ./docker-core/... ./research-assistant/docker-sandbox/... ./tool/...`：通过。

### 6.4 E2E（Compose 栈）

| 脚本 | 结果 |
|---|---|
| `http-smoke.mjs` | 17/17 通过（既有回归） |
| `plan-run.mjs --execute --approve-all`（7 节点 Code_Execution） | 通过，status=completed |
| `plan-library.mjs` | 通过（项目/重命名/移动/归档/恢复/复制/keyset/导出/运行中删除 409） |
| `repro-bundle.mjs` | 通过（manifest/checksums/recipe/provenance/哈希/sha256sum -c/verify/确定性） |
| `cost-budget.mjs` | 通过（预算配置/计划预算/usage 端点/预算状态/趋势/预警） |
| `budget-recovery.mjs` | 通过（tiny budget 派发前暂停 → 提高预算后恢复） |

## 7. 已知边界与未完成项

1. 分享只读链接（可选 Phase 3）未实现；计划库 owner 隔离、导出与归档已可用。
2. 复现校验为静态校验，不含在干净容器构建/重跑；响应与 README 均明确 `static_only`。
3. GPU 分钟/下载字节/存储成本未折算美元（无单价来源）；RunBudget 已保留 MaxDownloadBytes 字段，SandboxSeconds 已记录。
4. 预算感知路由仅做 `cost_policy=budget_first` 的模板降级，不做跨 provider 自动比价。
5. 用户预算按天/月单周期配置；无项目级预算（设计列为后续）。

## 8. 清理

- 删除临时脚本/空报告目录；不删除 Compose 数据卷（保留既有计划/项目/预算数据）。
- 本轮构建产生的 dangling 镜像/缓存清理见 `docs/verification.md` 的清理记录。
