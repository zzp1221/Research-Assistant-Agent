# 规格：N-01 计划库 / N-02 可复现导出 / N-03 成本与预算控制台（2026-10-28 第八轮）

> 依据：`docs/design/n01-n03-plan-library-reproducibility-cost.md`（设计稿，2026-10-28）
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处在第 9 节记录，以实际为准。
> 约束：一切与功能相关的测试只在容器内执行（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）。
> 前几轮成果（Top-10、R-01~R-17、运行期模型、N-04/N-07、复杂度路由、Prompt/Repair 优化）为基线，本轮不回归、不重复。
> 状态：已实现并通过容器内验证（2026-10-28）。证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND8.md`。

## 1. 目标

把三项"有底座但未产品化"的能力补齐为可用产品：

| 编号 | 目标 | 验收信号 |
|---|---|---|
| N-01 | 计划库：服务端索引 + 项目分组 + 搜索/过滤/稳定游标 + 重命名/归档/复制 + 独立计划库页面 | 1,000 计划首屏 <1s；翻页无重复/遗漏；过滤正确；归档/恢复/重命名/移动/复制生效；运行中删除 409；跨浏览器仍可见 |
| N-02 | 可复现导出包 v2：manifest + checksums + 环境 recipe + 脱敏 + 大小上限 + 静态校验 | zip 含 manifest.json/checksums.sha256/environment/{requirements.lock,system-packages.txt,Dockerfile,run.sh}；sha256sum -c 全通过；缺 digest/artifact 显示 partial；包内无密钥；大 artifact 进 omitted；重复导出确定性一致 |
| N-03 | 成本与预算控制台：token/USD 预算闸门 + 预留/结算 + profile 计价 + 成本 API/UI | MaxCostUSD=0.01 超预算在派发前暂停并返回 budget_exceeded；计划详情按节点/模型显示 tokens/USD 且总和与 RunUsage 一致；profile 单价生效；80% 预警事件；100% 按 soft/hard 策略；成本页显示今日/本月、剩余、Top 计划/模型 |

## 2. 现状复核（以实际代码为准，2026-10-28）

| 设计文档描述 | 实际（本轮复核） |
|---|---|
| `GET /api/plans` owner 过滤、limit≤100、offset cursor、session_id | 属实：`plan_runtime.go:460-517`；limit 默认 20，offset 型 cursor，全量 `store.ListPlans()` 后内存过滤/排序 |
| `planSummary` 含 title/status/outcome/nodes/时间/paused/run_trigger/usage | 属实：`plan_runtime.go:416-458` |
| 前端只把远端计划合并进会话列表，前缀 `[计划]` | 属实：`useResearchAssistantChatFlow.ts:259-344、412-424`；无独立计划库页 |
| 无 Project 模型、无归档/重命名/复制/分享 | 属实：`models` 无 Project；`plan_runtime.go` 只有 create/list/get/delete |
| 导出 md/json/zip 存在；zip 含 README/plan.json/events.jsonl/artifact blobs | 属实：`plan_export.go:64-286`；无 manifest/checksums/recipe/脱敏/大小上限；zip 全内存 `zipBuffer` |
| `EnvironmentManifest` 有 image/digest/pip freeze/system packages/hashes/run command | 属实：`models/reproduction.go:130-155` |
| `TaskUsage/RunUsage` 记录 LLM calls/tokens/USD/sandbox seconds | 属实：`models/runtime.go:32-111` |
| `EstimateCostUSD` 只用全局 env 单价 | 属实：`usage/usage.go:192-203`；`RecordLLM` 调用它 |
| model profile 有 `PromptPricePer1K/CompletionPricePer1K` 但 usage 未使用 | 属实：`modelprofile/profile.go:66-67`；`usage.RecordLLM` 不接收 profile 价格 |
| `RunBudget` 只有 `MaxTaskAttempts/MaxDurationSec` | 属实：`models/runtime.go:26-30`；scheduler 在 `scheduler.go:1256、1802-1808` 检查 |
| 无 token/USD 预算、无预算台账、无成本 API/UI | 属实：全仓无 budget ledger、`/api/usage/*`、`/api/budgets` |
| `planSummary.Usage` 已带 usage，前端 PlanSummary 未声明 | 属实：`researchAssistantApi.ts:89-102` 缺 usage/paused |

**结论**：设计文档对现状的判断准确，无需纠正。本轮按文档 Phase 0→3 的优先级落地，但以第 6 节验收标准为硬性完成条件。

## 3. 范围

### 3.1 N-01 计划库

#### 3.1.1 数据模型（新增/变更）

- `models.Project`：ID、OwnerID、Name、Description、CreatedAt、UpdatedAt、Archived。
- `models.PlanGraph` 新增字段（全部 omitempty，兼容旧 JSON）：
  - `Title string`：用户重命名后的标题；为空时沿用 UserIntent 截断。
  - `ProjectID string`：所属项目；空表示 Inbox。
  - `Archived bool`、`ArchivedAt *time.Time`。
- `models.PlanIndexEntry`（API 内部摘要投影，不落盘）：PlanID/OwnerID/SessionID/ProjectID/Title/IntentType/Status/Outcome/TotalNodes/CompletedNodes/FailedNodes/CostUSD/DurationSec/CreatedAt/UpdatedAt/Archived。

#### 3.1.2 存储

- `store.PlanStore` 增加轻量摘要接口（不深拷贝节点）：
  ```go
  type PlanSummarySource interface {
      ListPlanSummaries() ([]PlanSummaryRecord, error)
  }
  ```
  Memory/File store 均实现；API 优先使用它，回退到 `ListPlans()`。
- 新增 `internal/project` 包：`Store` 接口 + Memory/File 实现；文件默认 `<PLAN_STORE_PATH>.projects.json`（可由 `PROJECT_STORE_PATH` 覆盖），支持 owner 隔离。
- 迁移：旧计划无 ProjectID 自动归入 Inbox（不强制建项目记录）；启动时无需阻塞回填。

#### 3.1.3 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/projects` | 当前用户项目列表（含 archived 过滤） |
| POST | `/api/projects` | 创建项目 {name, description} |
| PATCH | `/api/projects/:id` | 重命名/描述/归档 |
| DELETE | `/api/projects/:id` | 仅空项目可删；否则 409 |
| GET | `/api/plans?q=&status=&intent_type=&project_id=&archived=&sort=&cursor=&limit=&session_id=` | 搜索/过滤/keyset 游标 |
| PATCH | `/api/plans/:id` | {title?, project_id?, archived?} 重命名/移动/归档 |
| POST | `/api/plans/:id/duplicate` | 复制为新计划（默认清空执行状态与事件） |
| POST | `/api/plans/:id/share` | 创建只读分享 token（Phase 3，可选） |
| GET | `/api/plans/:id/export` | 已有；分享 token 只读 |

- 游标：`(updated_at, id)` keyset，base64url 编码；返回 `next_cursor`；筛选变化重置。
- `archived`：`false`（默认，不含归档）/ `true`（仅归档）/ `all`。
- `sort`：`updated_desc`（默认）/ `created_desc` / `cost_desc`。
- 所有写操作 owner 校验 + audit event（`plan_renamed`、`plan_archived`、`plan_moved`、`plan_duplicated`、`project_created` 等）。
- 删除：运行中（in_progress/awaiting_approval）返回 409；成功后清理 sandbox、保留 audit event。

#### 3.1.4 前端 PlanLibrary

- 入口：顶栏"计划库"按钮，打开全屏计划库面板。
- 布局：左侧项目 rail（全部/Inbox/项目/已归档）+ 搜索/状态/类型筛选 + 计划卡片网格 + "加载更多"。
- 卡片：标题、状态/outcome、完成节点数、耗时、成本、更新时间、失败节点数、run_trigger。
- 操作：打开（载入工作台）、重命名、归档/恢复、移动项目、复制、导出 zip、删除（二次确认）。
- 远端计划直接打开，不创建临时本地会话；本地只记录最近打开的 plan id。
- 兼容：`PlanSummary` 类型补 `paused/usage/failed_nodes/project_id/archived/title`。

### 3.2 N-02 可复现导出包 v2

#### 3.2.1 包结构（zip）

```text
plan-<id>/
  README.md
  manifest.json
  checksums.sha256
  plan.json
  events.jsonl
  environment/
    requirements.lock
    system-packages.txt
    Dockerfile
    run.sh
  artifacts/
    <artifact-key>.<ext>
  provenance/
    model-snapshot.json
    runtime-policy.json
    dataset-hashes.json
    model-hashes.json
```

#### 3.2.2 manifest 与校验

- `schema_version: "repro.bundle/v1"`。
- 字段：plan_id、revision（plan.UpdatedAt UTC RFC3339）、created_at（= plan.UpdatedAt，保证重复导出确定性）、generator、files[{path,size,sha256,kind}]、environment{image,image_digest}、run_command、expected、omitted[]、missing[]、reproducibility（complete/partial）。
- `checksums.sha256`：`<sha256>  <path>` 每行一个，可用 `sha256sum -c` 校验（manifest.json 自身不列入，避免自引用）。
- 禁止绝对路径与 `..`；artifact 文件名清洗。
- 只导出白名单类型；超过单文件/总大小上限的 artifact 记入 manifest.omitted 并给出原因。

#### 3.2.3 环境 recipe

- `requirements.lock`：由 `EnvironmentManifest.PipFreeze` 生成 `name==version`（无版本则 `name`）。
- `system-packages.txt`：每行一个。
- `Dockerfile`：`FROM <image>@<digest>`（无 digest 时 `FROM <image>` 且 manifest partial + missing 记录）；`RUN apt-get install ...`（若有）；`COPY requirements.lock /tmp/` + `RUN pip install -r /tmp/requirements.lock`（若有）；`WORKDIR`；`CMD ["bash","/workspace/environment/run.sh"]`。
- `run.sh`：`#!/usr/bin/env bash` + `set -euo pipefail`；导出 seed/env；cd 到 working dir；执行 RunCommand（shell 转义）；无 RunCommand 时输出说明并 exit 2 且 manifest partial。
- `provenance/*`：原样落 ModelSnapshot/RuntimePolicy/DatasetHashes/ModelHashes。

#### 3.2.4 脱敏与安全

- 递归扫描 plan.json/events.jsonl/README.md 的 map key：`authorization`、`api_key`、`apikey`、`token`、`password`、`secret`、`access_key`、`private_key`（大小写不敏感）→ `[REDACTED]`。
- 文本模式：`Bearer\s+[A-Za-z0-9._-]+`、`sk-[A-Za-z0-9]{8,}`、`ghp_[A-Za-z0-9]{20,}`、`AKIA[0-9A-Z]{16}` → 脱敏。
- 脱敏发生在序列化前；导出后不出现原始密钥。
- 大小上限：`EXPORT_MAX_FILE_MB`（默认 16）、`EXPORT_MAX_TOTAL_MB`（默认 128），zip 流式写 `http.ResponseWriter`。
- 失败返回 `export_failed` + 缺件列表；不生成半包。

#### 3.2.5 静态复现校验

- `POST /api/plans/:id/reproduce/verify`：服务端按同一规则重建 bundle，逐文件比对 manifest 中 sha256 与必需文件清单；返回 `{status: passed|partial|failed, checks: [...], missing: [...], reproducibility}`。
- 明确：只做静态校验 + recipe 完整性，不宣称在干净容器重跑；文档/README 中写明边界。

#### 3.2.6 导出审计

- `plan_exported` 事件：actor、format、bytes、bundle_sha256（manifest 哈希）、omitted_count。

### 3.3 N-03 成本与预算控制台

#### 3.3.1 数据模型

```go
type RunBudget struct {
    MaxTaskAttempts   int
    MaxDurationSec    int
    MaxTokens         int     // prompt+completion
    MaxCostUSD        float64
    MaxSandboxSeconds float64
    MaxDownloadBytes  int64
    Policy            string  // soft | hard (默认 soft)
}
```

- `TaskUsage` 新增 `CostSource string`（profile/env/estimated/unknown）。
- `RunUsage` 新增 `CostSource string`、`BudgetStatus string`（ok/warning/exceeded）、`BudgetWarnings []string`。
- 新增 `internal/budget` 包：
  - `Entry`：OwnerID、PlanID、TaskID、Model、Prompt、Completion、CostUSD、Estimated、CostSource、At。
  - `UserBudget`：OwnerID、Period（day/month）、LimitUSD、Policy、UpdatedAt。
  - `Store`：内存 + JSON 文件持久化（`BUDGET_STORE_PATH`，默认 `<PLAN_STORE_PATH>.budget.json`）。
  - `Manager`：`Reserve(owner, plan, task, estimateUSD, estimateTokens)`、`Settle(reservation, actual)`、`ReleasePlan(planID)`、`Summary(owner, period)`、`Timeseries(owner, from, to, group)`、`Alerts(owner)`。
  - 预算语义：无配置=不限制；soft=达到 100% 暂停并等待人工提高上限；hard=取消计划；80% 发 warning（每计划一次）。

#### 3.3.2 预算执行（调度器）

- 派发任务前（`markTaskStarted` 之前）估算：`estimate = max(历史单任务均值, BUDGET_DEFAULT_TASK_ESTIMATE_USD)`；检查：
  - 计划级：`Usage.LLMCostUSD + 已预留 + estimate > MaxCostUSD` → `budget_exceeded`；
  - token：`Usage.LLMPromptTokens+CompletionTokens + estimatedTokens > MaxTokens` → `budget_exceeded`；
  - sandbox：`Usage.SandboxSeconds + 已预留 > MaxSandboxSeconds` → `budget_exceeded`。
- 任务完成：实际 usage 结算，释放预留；`RunUsage.AddTaskUsage` 增加 `CostSource` 合并（profile > env > estimated > unknown 的"最弱"来源）。
- 80%：写 `budget_warning` 事件 + metric；100%：soft 设 `Paused=true` + `plan_paused` 事件（reason=budget_exceeded），hard `CancelPlan`。
- 计划终态：释放该计划全部预留。

#### 3.3.3 profile 计价

- `llmclient` 新增 `WithModelPrice(ctx, promptPer1K, completionPer1K)`；`RecordUsage` 读取并传给 `Recorder.RecordLLMWithPrice`。
- `usage.Recorder` 新增 `RecordLLMWithPrice(model, prompt, completion, estimated, promptPrice, completionPrice)`；价格均为 0 时回退 env；仍为 0 → `CostSource=unknown`、`Estimated=true`。
- `agent.dynamicChatModel` 在 `Generate/Stream` 前注入 `WithModelPrice(resolved.Profile.PromptPricePer1K, ...)`；`usageChatModel` 通过 `ResolvedModelPrice(ctx)` 同步注入，保证外层记录也使用 profile 价格。
- `TaskUsage.CostSource`/RunUsage 汇总；计划摘要与 `GET /api/plans/:id/usage` 暴露 `cost_source`。

#### 3.3.4 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/usage/summary?period=day|month` | 当前用户总花费/预算/剩余/Top 计划/Top 模型 |
| GET | `/api/plans/:id/usage` | 计划/节点/模型明细（节点 usage + models 聚合） |
| GET | `/api/usage/timeseries?from=&to=&group=day|model|plan` | 趋势 |
| GET/PUT | `/api/budgets` | 用户预算（period/limit/policy） |
| GET | `/api/budgets/alerts` | 预警历史 |

#### 3.3.5 前端成本控制台

- 顶栏"成本"按钮打开成本页：今日/本月花费、预算进度条、剩余、Top 计划/模型、按日趋势（纯 CSS/SVG 柱状图）、预算设置表单（日/月、限额、soft/hard）。
- 计划详情：masthead 显示总成本；ExecutionSidebar 节点详情显示该节点 tokens/USD/耗时/模型；计划详情使用 `GET /api/plans/:id/usage` 汇总。
- 计划创建/审批：若配置预算，显示"预计成本/预算余量"（可放在成本页/计划卡片）。

#### 3.3.6 预算感知路由（Phase 3，最小实现）

- `RunBudget.Policy` 与 `IntentContext.Constraints["cost_policy"]` 支持 `quality_first|balanced|budget_first`；
- `budget_first` 时复杂度路由优先降级模板变体（L3→L2、full→standard），记录 `route_reason=cost_policy`；不改变既有默认行为。

### 3.4 共同原则

- 服务端权威、不可变快照、显式降级、可审计。
- 所有新增字段 omitempty，旧计划可反序列化。
- 新增 API 必须有 owner 隔离、错误码、审计事件。

## 4. 关键设计决策

1. **索引 vs 全量扫描**：不引入 SQLite/bbolt 依赖；在 store 层新增轻量摘要投影，避免每次深拷贝节点；1,000 计划规模下内存排序 <10ms。若未来 >10k 再换存储。
2. **项目存储独立**：项目是轻量元数据，独立 JSON 文件比塞进 plans.json 更安全；删除项目只允许空项目，避免悬挂引用。
3. **导出确定性**：manifest 的 created_at 使用 plan.UpdatedAt，不写导出时刻；导出时刻只进 audit event。这样同计划同版本重复导出字节级一致（artifact blob 未变化时）。
4. **脱敏在序列化前**：对 map 与文本双重扫描；宁可多脱敏，不可漏密钥。
5. **预算双轨**：计划级硬闸门用 RunBudget（不依赖用户台账）；用户日/月预算用 budget.Manager（预留/结算）。两者都实现，互不替代。
6. **soft/hard**：默认 soft（暂停，可恢复）；hard 仅显式配置时取消。避免误杀。
7. **计价来源优先级**：profile > env > estimated/unknown；无价格时 UI 明示"估算/未配置"，不伪造 0 成本。
8. **前端视图**：不引入 router 依赖；沿用现有全屏面板模式（ModelSettingsPanel/HelpDrawer），新增 PlanLibraryPanel/CostConsolePanel，避免破坏工作台布局。

## 5. 契约（新增/变更）

- `GET /api/plans`：新增 q/status/intent_type/project_id/archived/sort；`next_cursor` 改为不透明 keyset；响应保留 `plans/total/next_cursor`。
- `planSummary`：新增 `paused/usage/project_id/archived/title`。
- `POST /api/plan` 响应 `plan_graph.budget` 增加 max_tokens/max_cost_usd/max_sandbox_seconds/max_download_bytes/policy；`usage` 增加 cost_source/budget_status。
- `GET /api/plans/:id/export`：zip 结构升级；md/json 增加 schema 版本与"如何复现"。
- `POST /api/plans/:id/reproduce/verify`：静态校验。
- `GET /api/usage/summary|timeseries`、`GET /api/plans/:id/usage`、`GET/PUT /api/budgets`、`GET /api/budgets/alerts`。
- `/api/metrics`：新增 `budget_exceeded_total{policy}`、`budget_warning_total`、`plan_export_total{format}`、`export_omitted_bytes`。

## 6. 测试策略（全部容器内）

1. 后端：`scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."`；新增包 `-race`。
2. 前端：`node:22-bookworm-slim` 容器内 `npm ci && npm test && npm run build && npm run lint`。
3. 单测重点：
   - store/project：owner 隔离、文件持久化、空项目删除；
   - api/plan_runtime：1,000 计划列表性能与游标稳定性、过滤组合、归档默认隐藏、重命名/移动/复制、运行中删除 409；
   - api/plan_export：manifest/checksums 校验、recipe 内容、脱敏、omitted、partial、确定性、verify 端点；
   - usage：profile 计价、CostSource 合并、无价格回退；
   - budget：预留/结算/释放、soft/hard、80% warning、并发安全；
   - scheduler：MaxCostUSD=0.01 派发前暂停、token/sandbox 闸门、预算事件。
4. E2E（compose 栈）：新增 `test/e2e/plan-library.mjs`、`test/e2e/repro-bundle.mjs`、`test/e2e/cost-budget.mjs`；既有 `http-smoke.mjs`、`plan-run.mjs` 等回归。
5. 回归：现有后端/前端全部测试；不得新增失败。
6. 清理：临时容器/镜像/卷/脚本/产物；不删除用户数据卷。

## 7. 完成条件

- 第 6 节全部测试通过（容器内证据，保存到 `docs/improvement-2026-10-26/FIX-REPORT-ROUND8.md`）；
- 设计文档第 6 节 N-01/N-02/N-03 验收标准逐条可复现；
- 无新增回归（现有后端/前端测试全绿）；
- 无密钥/隐私泄漏；
- 清理本轮产生的临时容器/镜像/卷/文件/死代码。

## 8. 非目标

- 不做完整协作/权限系统（N-12）、不做跨 provider 自动采购；
- 不承诺所有历史计划 100% 复现（缺数据/模型/镜像明确 partial）；
- 不做完整 replay（在干净容器重跑对比指标）——本轮只做静态校验 + recipe 完整性；完整 replay 留待后续；
- 不做实时多用户协同编辑；
- 不重写现有计划图渲染/执行逻辑。

## 9. 与设计文档的差异（以实际为准）

| 文档描述 | 实际/本轮决策 |
|---|---|
| 文档建议 SQLite/bbolt 做索引 | 实际计划全量在内存（Memory/File store），无需新增依赖；采用轻量摘要投影 + keyset，1,000 规模满足 <1s，避免引入运维复杂度 |
| 文档说 POST /api/plans/:id/share（可选） | 本轮若时间允许实现只读 token；否则明确列为 Phase 3 未完成项，不影响第 6 节验收 |
| 文档说完整 replay 校验 | 本轮只做静态校验（checksums + recipe + manifest 一致性），不启动 Docker 构建；README/verify 响应明确标注 static_only |
| 文档说 GPU 分钟/下载字节成本 | 本轮记录 SandboxSeconds/DownloadBytes 预算字段，但不计算 GPU/存储美元（无单价来源）；保持"可扩展字段" |
| 文档说预算感知路由完整 | 本轮只实现 cost_policy 对模板变体的最小降级；不做自动 provider 比价 |
