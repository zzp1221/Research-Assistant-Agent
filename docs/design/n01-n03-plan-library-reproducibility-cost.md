# N-01 / N-02 / N-03：计划库、可复现导出与成本预算控制台

- **状态**：设计稿（待评审）
- **日期**：2026-10-28
- **范围**：N-01（计划库 / 研究工作区）、N-02（可复现性导出包）、N-03（成本与预算控制台）
- **关联**：`docs/improvement-2026-10-26/03-new-feature-proposals.md`、`research-assistant/backend/internal/api/plan_runtime.go`、`plan_export.go`、`plan_control.go`、`models/runtime.go`、`models/reproduction.go`、`usage/usage.go`、`frontend/src/app/hooks/useResearchAssistantChatFlow.ts`
- **一句话结论**：三项都不是“从零开始”。计划列表/摘要、md/json/zip 导出、逐节点 usage 已经存在；真正缺的是**计划库产品层、可验证的复现包、可执行的预算闸门**。

---

## 1. 摘要

当前代码已经具备三块底座：

1. **N-01 底座**：`GET /api/plans` 返回 owner 隔离的计划摘要，支持 `limit`、`cursor`、`session_id`；前端会把服务端计划合并进会话列表，并支持打开远端计划。
2. **N-02 底座**：`GET /api/plans/:id/export?format=md/json/zip` 已能导出 README、PlanGraph、事件和 artifact blob；`EnvironmentManifest` 已记录镜像、pip freeze、数据/模型 hash、运行命令等。
3. **N-03 底座**：`TaskUsage/RunUsage` 已记录 LLM calls、tokens、USD、sandbox seconds；计划摘要已带 `usage`；模型 profile 已有输入/输出单价字段。

但这三块都停在“有数据/有接口”，还没有成为用户可用的产品：

- N-01 没有真正的“计划库”页面：计划被塞进会话下拉框，只有前 20 条，不能搜索/过滤/建项目/归档/重命名。
- N-02 是“可携带快照”，不是“可重建复现包”：没有 checksum 清单、依赖锁文件、Dockerfile/run.sh、复现校验。
- N-03 是“事后记账”，不是“事前预算”：`RunBudget` 只有 attempts/duration，没有 token/USD 上限，也没有成本控制台。

---

## 2. 现状盘点

### 2.1 N-01 计划库

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| 服务端列表 | `GET /api/plans`，owner 过滤，`limit` ≤100，offset 型 `cursor`，可选 `session_id` | `plan_runtime.go:460-517` | 无 status/intent_type/时间/关键词过滤；无项目；offset 分页在数据变化时会漂移 |
| 计划摘要 | `planSummary` 含标题、状态、outcome、节点数、时间、paused、run_trigger、usage | `plan_runtime.go:416-458` | 前端 `PlanSummary` 类型未声明 usage/paused；列表无成本/耗时/错误摘要 |
| 前端入口 | 只把远端计划合并进“历史会话”，标题前缀 `[计划]`；点击后创建本地会话并拉 PlanGraph | `useResearchAssistantChatFlow.ts:259-344、412-424` | 没有独立计划库页；没有卡片、筛选、搜索、分页、批量操作 |
| 项目分组 | 无 | 无 `Project` 模型 | 不能把论文、数据集、计划、报告归到同一项目 |
| 重命名/归档 | 无 | 只有 `DELETE /api/plans/:id` | 删除不可逆；不能归档、复制、分享只读链接 |
| 性能 | `store.ListPlans()` 全量加载后在内存过滤/排序 | `plan_runtime.go:475-495` | 1,000+ 计划时首屏依赖全量加载；无索引/游标稳定性 |
| 跨设备 | 服务端 identity + 计划列表可恢复 | `getMe`、`listPlans` | 仅前 20 条；刷新后远端计划被包装成临时本地会话，缺少“计划”这一等公民 |

### 2.2 N-02 可复现导出

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| 导出接口 | `GET /api/plans/:id/export?format=md/json/zip` | `plan_export.go:64-89` | 无导出 schema 版本；无大小上限/流式；无导出审计 |
| Markdown | 含 intent、状态、usage、missing requirements、EnvironmentManifest、节点表、代码/结果、artifacts、事件统计 | `plan_export.go:99-198` | 结果截断 8k；没有“如何重跑”的命令；没有 checksum |
| JSON | 完整 PlanGraph 缩进 JSON | `plan_export.go:72-79` | 可能含大 base64/日志；无字段白名单/脱敏 |
| ZIP | README.md、plan.json、events.jsonl、artifact blobs | `plan_export.go:207-265` | 没有 `manifest.json`/`checksums.sha256`；没有 requirements lock/Dockerfile/run.sh；artifact 无映射清单 |
| 环境描述 | `EnvironmentManifest` 有 image/digest、pip freeze、system packages、dataset/model hashes、run command、network/hard limits | `models/reproduction.go:130-155` | 只是“校验结果”，不等于可执行 recipe；不保证每个任务都采集到 |
| 复现校验 | 无 | 无 replay/verify 端点 | 导出的包无法证明“在另一台机器能重建并重跑” |
| 安全 | owner 鉴权；artifact 只读 blob | `authorizedPlan` | 没有 secrets 扫描/脱敏；没有 zip slip 防护（当前只写固定/清洗后的名字） |

### 2.3 N-03 成本与预算

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| 逐节点 usage | `TaskUsage` 记录 prompt/completion tokens、calls、USD、estimated、sandbox/duration、models | `models/runtime.go:32-46` | 无 GPU 分钟、下载字节、存储成本 |
| 计划汇总 | `RunUsage` 在任务完成时累加 | `scheduler.go:1178-1211` | 只有总量；无按模型/节点的时间序列 API |
| 成本计算 | `EstimateCostUSD` 用 `LLM_PRICE_PROMPT_PER_1K_USD` / `LLM_PRICE_COMPLETION_PER_1K_USD` | `usage/usage.go:192-202` | **model profile 的单价字段未被 usage 使用**；多模型计划成本可能错 |
| 预算执行 | `RunBudget` 只有 `MaxTaskAttempts`、`MaxDurationSec`；scheduler/approve 检查 | `models/runtime.go:26-30`、`scheduler.go:1256-1258、1802-1808` | 没有 token/USD/sandbox/download 上限；LLM 可无限花钱 |
| 预算 UI | 无 | 前端只在设置页编辑模型单价 | 计划详情不显示成本；无预算控制台、预警、趋势 |
| 模型路由 | 运行期 profile + routing 已有 | `modelresolver`、`model_profiles` | 路由按 main/vision/fallback，不按价格/预算/任务类型做成本优化 |
| 计划摘要 | 后端 summary 已带 usage | `plan_runtime.go:433` | 前端 `PlanSummary` 未声明/未展示 usage |

---

## 3. 问题在哪

### 3.1 N-01：计划不是“会话的附属物”，但现在被当成会话

1. **发现成本高**：用户只能打开会话下拉框，看到最多 20 条 `[计划]`；无法按状态/类型/时间/关键词找“上周失败的论文复现”。
2. **没有项目语义**：同一课题的论文、数据集、计划、报告无法归组；用户只能靠标题记忆。
3. **操作缺失**：不能重命名、归档、复制、分享；只能删除，且删除不可逆。
4. **性能与一致性**：列表全量加载后过滤；offset 游标在新增计划时会重复/跳过；前端只取 20 条，没有“加载更多”。
5. **信息不足**：卡片没有 outcome、耗时、成本、失败节点、最后错误；用户无法快速判断“哪次值得打开”。
6. **跨设备只是“能恢复”**：远端计划被包装成临时本地会话，刷新后可能重复出现；计划本身没有独立入口。

### 3.2 N-02：是“导出”，不是“复现”

1. **缺可验证清单**：没有 `manifest.json`/`checksums.sha256`，无法判断包是否完整、是否被改动。
2. **缺可执行 recipe**：`EnvironmentManifest` 是 JSON，没有生成的 `requirements.lock`、`system-packages.txt`、`Dockerfile`、`run.sh`；合作者仍需自己猜怎么装、怎么跑。
3. **缺数据/模型内容**：只有 hash/ref，没有打包数据/模型；如果原环境不可用，复现包不完整。
4. **缺 replay 校验**：没有“在干净容器里按 README 跑一遍并对比指标”的流程；“可复现”只是声明。
5. **结果被截断**：节点结果超过 8k 被截断；关键日志/错误可能丢失。
6. **大包风险**：ZIP 在内存中构建；artifact 可能包含大图/二进制；plan.json 可能含 base64；无大小上限/流式。
7. **脱敏风险**：导出整份 PlanGraph/事件，没有 secrets 扫描；RuntimePolicy/Inputs/日志可能包含内部 URL、token、用户数据。

### 3.3 N-03：是“账单”，不是“预算”

1. **没有硬闸门**：`RunBudget` 只有 attempts/duration；一个 LLM 循环可以在预算外持续烧钱。
2. **成本不准**：usage 用全局 env 单价，model profile 的 `prompt_price_per_1k`/`completion_price_per_1k` 没有进入计费；多模型计划成本会偏。
3. **没有事前估算/预留**：只在任务完成后累加；不能在派发前判断“这一步会不会超预算”。
4. **没有分层预算**：没有 per-user、per-day/month、per-project 预算；没有软/硬阈值。
5. **没有产品界面**：计划详情不显示成本；没有按节点/模型/时间的成本表；没有预警/趋势。
6. **没有预算感知路由**：已有模型路由，但不会因为预算选择更便宜的模型或降级。

---

## 4. 为什么这么做

### 4.1 N-01：把 Plan 变成一等研究对象

- 研究会持续数天/数周，用户需要“我跑过什么、哪次最好、为什么失败”，而不是聊天记录。
- 计划与会话不是一回事：一个会话可以有多个计划，一个项目可以跨多个会话；用 session 代替 plan 会限制检索和复用。
- 服务端索引 + 稳定游标 + 项目/归档，是“研究平台”而不是“聊天工具”的分界线。

### 4.2 N-02：可复现性必须可验证

- 科研合作的信任来自“别人能重建并得到相同结论”，不是“我导出了一个 zip”。
- 复现包必须自描述：环境、依赖、命令、数据/模型 hash、随机种子、预期输出。
- 必须可校验：每个文件有 SHA-256；包有 manifest；重跑有 verify；否则无法判断“失败是环境差异还是代码差异”。
- 必须安全：导出是数据外流边界，默认脱敏、白名单、大小上限。

### 4.3 N-03：预算要在花钱前生效

- 事后账单只能解释超支，不能阻止超支；研究 Agent 的循环/重试会放大成本。
- 预算需要“估算 → 预留 → 结算 → 释放”的账本，而不是一个最终数字。
- 用户需要看见“哪一步最贵、为什么贵、换模型能省多少”，才能做取舍。
- 预算与模型路由结合，才能实现“便宜模型规划、强模型执行”的闭环。

### 4.4 共同原则

- **服务端权威**：计划库、预算账本、导出清单都以服务端为准。
- **不可变快照**：计划/导出/预算周期都要有版本，不能边跑边改。
- **显式降级**：预算超限、导出缺件、复现失败都要给结构化原因和修复建议。
- **可审计**：谁导出、谁改预算、谁归档都要有 audit event。

---

## 5. 怎么做

### 5.1 N-01 计划库

#### 5.1.1 数据模型

```go
type Project struct {
    ID          string
    OwnerID     string
    Name        string
    Description string
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Archived    bool
}

type PlanIndexEntry struct {
    PlanID       string
    OwnerID      string
    SessionID    string
    ProjectID    string
    Title        string
    IntentType   string
    Status       string
    Outcome      string
    TotalNodes   int
    FailedNodes  int
    CostUSD      float64
    DurationSec  float64
    CreatedAt    time.Time
    UpdatedAt    time.Time
    Archived     bool
}
```

- 索引可用 SQLite/bbolt，也可先做内存索引 + 定期重建；不要每次列表都全量扫 `plans.json`。
- 迁移：启动时从现有 PlanGraph 回填 `PlanIndexEntry`；缺 project 的归入 `Inbox`。

#### 5.1.2 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/projects` | 项目列表 |
| POST | `/api/projects` | 创建项目 |
| PATCH | `/api/projects/:id` | 重命名/描述/归档 |
| DELETE | `/api/projects/:id` | 删除空项目 |
| GET | `/api/plans?q=&status=&intent_type=&project_id=&archived=&sort=&cursor=&limit=` | 搜索/过滤/稳定游标 |
| PATCH | `/api/plans/:id` | 重命名、归档、移动到项目 |
| POST | `/api/plans/:id/duplicate` | 复制为新计划（可选清空执行状态） |
| POST | `/api/plans/:id/share` | 创建只读分享 token（可选） |
| GET | `/api/plans/:id/export` | 已有；分享链接只读 |

游标：`(updated_at, id)` 的 keyset pagination，避免 offset 漂移；`q` 对 title + user_intent 做前缀/全文；状态/类型/项目走索引。

#### 5.1.3 前端 PlanLibrary

- 入口：左侧栏“计划库”或工作台顶部“计划”。
- 布局：项目 rail + 搜索/筛选栏 + 计划卡片列表 + 批量操作。
- 卡片：标题、状态/outcome、节点完成数、耗时、成本、更新时间、失败节点摘要、缩略图（若有）。
- 操作：打开、重命名、归档、移动项目、复制、导出、分享；删除仅二次确认。
- 分页：滚动加载/“加载更多”，使用 `next_cursor`；筛选变化重置游标。
- 远端计划直接打开，不再创建临时本地会话；localStorage 只存最近/收藏的 plan id。

#### 5.1.4 归档/删除语义

- 归档：默认列表隐藏，可筛选“已归档”；仍可导出/分享/恢复。
- 删除：仅 owner；运行中拒绝；删除后清理 sandbox，保留 audit event 但不可恢复。
- 分享：只读 token + 过期；不暴露 owner/session/密钥；只能看摘要/报告/白名单 artifact。

### 5.2 N-02 可复现导出包 v2

#### 5.2.1 包结构

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

#### 5.2.2 manifest 与校验

```json
{
  "schema_version": "repro.bundle/v1",
  "plan_id": "…",
  "revision": 3,
  "created_at": "…",
  "generator": "research-assistant/…",
  "files": [
    {"path": "plan.json", "size": 12345, "sha256": "…"},
    {"path": "artifacts/plot.png", "size": 6789, "sha256": "…"}
  ],
  "environment": {"image": "…", "image_digest": "…"},
  "run_command": ["python", "…"],
  "expected": {"metrics": "metrics.json", "report": "README.md"}
}
```

- `checksums.sha256` 用 `sha256sum -c` 可验。
- 导出时对每个文件计算 hash；禁止绝对路径/`..`；文件名清洗。
- 只包含白名单 artifact 类型；大文件超限给 warning 或不打包，写进 manifest 的 `omitted`。

#### 5.2.3 环境 recipe

- 从 `EnvironmentManifest.PipFreeze` 生成 `requirements.lock`（保留版本）。
- 生成 `Dockerfile`：`FROM <image>@<digest>` + apt/pip + `WORKDIR` + `CMD`。
- 生成 `run.sh`：设置 env、seed、数据/模型挂载路径、执行 `RunCommand`。
- 若 `ImageDigest` 为空，manifest 标记 `reproducibility: partial` 并给 remediation。

#### 5.2.4 复现校验

- 新增 `POST /api/plans/:id/reproduce/verify`（或 CLI `ra reproduce`）：
  1. 解包到临时目录；
  2. 校验 checksums；
  3. 用 Dockerfile 构建/启动；
  4. 按 run.sh 执行；
  5. 对比 `expected` 指标/输出 hash；
  6. 返回 `passed/failed/partial` + 差异。
- 第一版可只做“静态校验 + 环境构建”，重跑放第二阶段；不要宣称未验证的“可复现”。

#### 5.2.5 安全与性能

- 导出前扫描 `Authorization`、`api_key`、`token`、`password` 等字段；默认脱敏。
- ZIP 流式写 `http.ResponseWriter`，不全部驻留内存；单文件/总大小上限。
- 失败时返回 `export_failed` + 缺件列表，不生成半包。
- 导出事件 `plan_exported`（actor、format、bytes、hash）。

### 5.3 N-03 成本与预算控制台

#### 5.3.1 数据模型

```go
type RunBudget struct {
    MaxTaskAttempts   int
    MaxDurationSec    int
    MaxTokens         int
    MaxCostUSD        float64
    MaxSandboxSeconds float64
    MaxDownloadBytes  int64
    Policy            string // soft | hard
}

type BudgetLedger struct {
    OwnerID       string
    Period        string // day/month
    LimitUSD      float64
    SpentUSD      float64
    ReservedUSD   float64
    UpdatedAt     time.Time
}

type CostEntry struct {
    PlanID     string
    TaskID     string
    Model      string
    Prompt     int
    Completion int
    CostUSD    float64
    Estimated  bool
    At         time.Time
}
```

#### 5.3.2 预留/结算

- 派发任务前：
  - 用 prompt budget/历史均价估算 `reserved = prompt_tokens * price + max_output * price`；
  - 检查 `spent + reserved + estimate <= limit`；超限则暂停/拒绝，返回 `budget_exceeded` + 当前/上限/建议。
- 任务完成：
  - 用实际 usage 结算，`reserved` 释放，`spent` 增加；
  - 估算与实际差异写入 `estimated`/`cost_source`。
- 80% 发 `budget_warning`；100% 按 `Policy` 暂停或拒绝；事件/指标/审计。

#### 5.3.3 成本计算修正

- `usage.RecordLLM` 接收 model profile 的单价（或 resolver 注入的 price），不再只用全局 env。
- 没有 profile 价格时回退 env；仍无价格则 `estimated=true` 且 UI 明示“未配置单价，成本为估算/0”。
- 计划摘要返回 `usage.cost_source`（profile/env/estimated/unknown）。

#### 5.3.4 API 与 UI

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/usage/summary?period=day/month` | 当前用户总花费/预算/剩余 |
| GET | `/api/plans/:id/usage` | 计划/节点/模型明细 |
| GET | `/api/usage/timeseries?from=&to=&group=day/model/plan` | 趋势 |
| GET/PUT | `/api/budgets` | 用户/项目预算 |
| GET | `/api/budgets/alerts` | 预警历史 |

前端：
- 工作台“成本”页：今日/本月花费、预算进度、Top 计划/模型、趋势图；
- 计划详情：总成本 + 节点表（tokens/USD/耗时）+ 超预算标记；
- 计划创建/审批：显示“预计成本/预算余量”；
- 预警 toast + 审批卡中的成本预估。

#### 5.3.5 预算感知路由

- 复用现有 model routing；新增 `cost_policy`：`quality_first | balanced | budget_first`。
- 低复杂度任务（L0/L1）优先便宜模型；高风险/代码/视觉保底强模型；超预算时降级并记录 `route_reason`。
- 不在本设计里做自动“买更便宜 provider”；只做已有 profile 的选择。

### 5.4 实施阶段

| 阶段 | 周期 | 内容 | 交付 |
|---|---|---|---|
| Phase 0 | 1–2 周 | PlanIndex + 稳定游标；导出 manifest/checksums；计划详情成本只读 | 1,000 计划首屏 <1s；zip 可校验；计划页显示成本 |
| Phase 1 | 2 周 | 项目/搜索/归档/重命名/复制；预算模型 + 预留/结算；成本页 | 计划库可用；超预算暂停 |
| Phase 2 | 2 周 | 导出 recipe（lock/Dockerfile/run.sh）+ 静态复现校验；预算告警/趋势 | 复现包可在干净容器构建；预算预警 |
| Phase 3 | 2 周 | 分享只读链接；完整 replay 对比；预算感知路由 | 分享/复现/成本闭环 |

---

## 6. 验收标准（容器内）

### N-01

1. 造 1,000 个计划，`GET /api/plans?limit=20` 首屏 <1s；翻页无重复/遗漏。
2. 按 `status=failed`、`intent_type=Paper_Reproduction`、`q=attention`、`project_id` 过滤正确。
3. 归档后默认列表不出现，筛选“已归档”可见；恢复后回到列表。
4. 重命名/移动项目/复制后，详情与列表一致；删除运行中计划返回 409。
5. 换浏览器/清 localStorage 后，计划库仍能看到并打开计划。

### N-02

1. 导出 zip 含 `manifest.json`、`checksums.sha256`、`requirements.lock`、`Dockerfile`、`run.sh`；`sha256sum -c` 全通过。
2. 在干净容器按 `run.sh` 能启动；环境/命令/seed 与 manifest 一致。
3. 缺 artifact/缺 digest 时显示 `partial` + 缺件清单，不静默成功。
4. 导出包不含 API key/token/Authorization；大 artifact 超限有 `omitted`。
5. 同一计划重复导出生成相同文件 hash（时间戳字段除外）。

### N-03

1. 设置 `MaxCostUSD=0.01`，运行超预算计划在派发前/80% 时暂停，返回 `budget_exceeded`；不产生超支。
2. 计划详情按节点/模型显示 tokens/USD；总和与 `RunUsage` 一致。
3. 配置 profile 单价后，成本使用 profile 价格；未配置时显示 estimated/unknown。
4. 80% 预警事件/指标出现；100% 按 soft/hard 策略暂停/拒绝。
5. 预算页显示今日/本月花费、剩余、Top 计划/模型。

---

## 7. 与旧提案的差异

| 项 | 旧提案 | 本文改进版 |
|---|---|---|
| N-01 | “服务端 plan/session 列表、项目、归档、分享”整体从零 | 列表/摘要/远端打开已存在；重点补计划库 UI、项目/搜索/归档、索引/游标 |
| N-02 | “一键 zip + 环境重建 + hash”整体从零 | md/json/zip + EnvironmentManifest 已存在；重点补 manifest/checksums/recipe/replay/脱敏 |
| N-03 | “成本控制台 + 预算”整体从零 | usage/成本/模型价格已存在；重点补预算闸门、profile 计价、成本 UI、预算路由 |
| 工作量 | N-01 L、N-02 M、N-03 M | 拆成 4 个阶段，先修底座再产品化；避免重复实现已有接口 |

---

## 8. 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| 计划列表/摘要 | `backend/internal/api/plan_runtime.go:416-517` | summary/list/get/delete |
| 计划控制 | `backend/internal/api/plan_control.go` | pause/resume/run-from-node |
| 导出 | `backend/internal/api/plan_export.go` | md/json/zip |
| 预算/usage 模型 | `backend/internal/models/runtime.go` | RunBudget/RunUsage/TaskUsage |
| 环境清单 | `backend/internal/models/reproduction.go:130-155` | EnvironmentManifest |
| 成本计算 | `backend/internal/usage/usage.go:192-202` | env 单价 |
| 调度计费 | `backend/internal/scheduler/scheduler.go:1178-1211、1256-1258、1802-1808` | usage 累加/预算检查 |
| 前端计划库 | `frontend/src/app/hooks/useResearchAssistantChatFlow.ts:259-344、412-424` | 远端计划合并/打开 |
| 前端 API | `frontend/src/services/api/researchAssistantApi.ts:89-118` | listPlans/deletePlan |
| 设置单价 | `frontend/src/features/settings/ModelSettingsPanel.tsx` | profile 单价 UI |

---

## 9. 非目标与风险

### 非目标

- 不把计划库做成完整协作/权限系统（N-12 再做）；
- 不承诺所有历史计划都能 100% 复现（缺数据/模型/镜像时明确 partial）；
- 不做跨 provider 自动采购/比价；
- 不导出用户无权分发的内容；
- 不做实时多用户协同编辑。

### 风险与缓解

| 风险 | 缓解 |
|---|---|
| 1,000+ 计划索引迁移慢 | 启动后台回填 + 旧路径 fallback；索引可重建 |
| 计划列表泄露 | owner 过滤 + 分享 token 白名单 + audit |
| 导出包过大 | 流式 zip + 大小上限 + `omitted` 清单 |
| 导出泄密 | 字段白名单 + secrets 扫描 + 默认脱敏 |
| 预算误杀 | soft 模式先警告；hard 仅对显式配置；可手动提高上限 |
| 成本估算偏差 | 记录 estimated/cost_source；结算时校正；UI 明示 |
| 复现环境漂移 | image digest + lock + checksum + verify；缺件不冒充成功 |

---

## 10. 结论

- **N-01 先做“可发现”**：计划库 UI + 项目/搜索/归档 + 索引/游标；不要继续把计划塞进会话。
- **N-02 先做“可验证”**：manifest + checksums + recipe + replay；不要只加一个 zip 按钮。
- **N-03 先做“能刹车”**：token/USD 预算 + 预留/结算 + profile 计价；不要只做事后账单。
- 三项共享同一套不可变快照/审计原则，建议按 Phase 0→3 顺序落地。
