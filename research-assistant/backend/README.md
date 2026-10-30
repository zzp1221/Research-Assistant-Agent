# backend

Go 后端是 Research Assistant Agent 的业务核心，负责 HTTP API、意图识别、任务规划、DAG 调度、Agent 编排、沙箱调用、上传与文档管理、文献检索以及 SSE 事件推送。

模块路径：`research-assistant-backend`，Go 版本见 [go.mod](go.mod)。

## 架构与流程

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

一次计划执行的生命周期：

```text
HTTP API -> Intent Router -> Planner + DAG Validator -> PlanStore
          -> Scheduler (lease/retry/cancel/budget/approval)
          -> RoutedTaskExecutor -> 专业 Agent -> Sandbox gateway/executor
          -> Artifact 校验 -> Event Bus -> SSE
```

## 入口

| 入口 | 说明 |
|---|---|
| `cmd/api/main.go` | 纯 API 服务，监听 `:8080`，适合与独立沙箱服务配合 |
| `cmd/app/main.go` | 单文件应用，内嵌前端静态资源；运行时必须配置 `SANDBOX_URL`，不会自动拉起本地沙箱 |

两个入口都暴露 `GET /ping` 作为轻量存活检查。

## 目录结构

```text
backend/
├── cmd/
│   ├── api/                 纯 API 入口
│   ├── app/                 单文件应用入口
│   └── mockmcp/             容器测试用 MCP stdio/HTTP mock server
├── internal/
│   ├── agent/               各专业 Agent、repair 会话、AutoResearch/Benchmark/论文复现执行
│   ├── agentctx/            任务上下文传递
│   ├── agentprofile/        AgentProfile/PromptVersion 存储、模板渲染、评测与 profile runner
│   ├── api/                 HTTP 路由、认证、上传、计划运行时、文献/文档/模型 API
│   ├── appconfig/           LLM 配置加载（环境变量 + 可选 config.toml）
│   ├── assist/              PDF 划词翻译/解释（无工具、无代码执行）
│   ├── audit/               审计事件导出
│   ├── blobstore/           Artifact 大对象的内容寻址存储（blob:<sha256>）
│   ├── catalog/             数据驱动的仓库与 smoke recipe 目录
│   ├── documents/           PDF 解析、OCR、版面/表格/图注、quote locate、质量报告
│   ├── events/              事件总线
│   ├── intent/              规则/LLM/hybrid/shadow 意图路由与后校验
│   ├── literature/          多 provider 检索、文献库、BibTeX、引用图
│   ├── llmclient/           OpenAI-compatible 文本客户端（重试/超时）
│   ├── metrics/             Prometheus 指标
│   ├── modelprofile/        per-user/全局模型 profile、加密存储、routing、视觉探测
│   ├── modelresolver/       按 owner/role 解析模型 profile 与 revision 缓存
│   ├── models/              计划、任务、Artifact、Runtime、AutoResearch 契约
│   ├── oaichat/             多模态 OpenAI-compatible 客户端（视觉校验/探测）
│   ├── paperid/             arXiv ID 解析
│   ├── planner/             意图到 DAG 的规划器、复杂度路由、模板注册表
│   ├── prompts/             Prompt 组装与 token 预算
│   ├── sandbox/             沙箱 gateway HTTP 客户端（含 OpenSandbox）
│   ├── scan/                可选 ClamAV 恶意文件扫描
│   ├── scheduler/           DAG 执行、租约、运行时审批、仓库发现与准备
│   ├── store/               计划与事件存储（文件/内存）
│   ├── tools/               工具注册表、JSON Schema 校验、native/HTTP/MCP 传输、ToolInvoker
│   ├── uploads/             上传路径工具
│   ├── uploadstore/         上传配额、去重、生命周期
│   ├── usage/               token/费用/沙箱耗时统计
│   ├── webread/             网页正文抽取与 SSRF/robots 防护
│   └── workspace/           工作区路径
├── evals/                   规划评测数据集与测试
├── tests/                   跨模块集成测试
└── webui/                   前端嵌入入口
```

## 主要包职责

### planner

把用户意图、附件和上下文转换为 `PlanGraph`。规划器负责节点类型、依赖关系、输入输出契约和基础校验，确保 Scheduler 只接收合法 DAG。

- `BuildPlanWithRoute` 是主入口；`route.go` 的 `ComplexityRouter` 在附件/论文上下文解析后做 L0–L4 路由。
- `plan_builder.go` / `plan_variants.go` 负责模板注册与 simple/standard/full 变体；`general_plan.go` 负责 General 的 direct/research/paper/literature 形状。
- L3/L4 走 skeleton + agent planner，并带一次结构化 repair；失败回退标准模板。

### scheduler

按入度执行 DAG，维护任务租约、重试、取消、预算与运行时审批。仓库相关任务还包含仓库发现（`repo_discovery.go`）、仓库准备（`repo_prepare.go`）与复现模式控制（`reproduction_mode.go`）。`runtime_gate.go` 决定镜像拉取、依赖安装、数据/模型下载、GPU 与网络访问是否暂停等待审批；`SetToolGate` 把 tool_call 节点的 allowlist/grant 判断接入同一审批门。

### tools

工具是"有副作用的代码"：`internal/tools` 提供 ToolDefinition/MCPServer/ToolGrant/ToolCallRecord 的 Memory/File 存储、JSON Schema 校验、native/HTTP/MCP stdio/MCP HTTP 传输和 `ToolInvoker`。执行期强制 `AllowedTools`、AgentProfile allowlist 与 ToolGrant；审批通过的调用写入 `PlanGraph.ToolCalls`，支持 dry-run/真实回放。

### agentprofile

`AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`。Prompt 版本不可变，`ActivatePrompt` 只移动 active 指针；`ProfileRunner` 渲染模板、解析模型并记录 `agent_profile_id/prompt_version/prompt_hash/model`，未知 profile 由计划校验直接报错。

### agent

| Agent | 文件 | 职责 |
|---|---|---|
| Chat | `chat.go` | 通用问答与轻量任务入口 |
| Librarian | `librarian.go`、`claim_evidence.go`、`literature_search.go` | 论文解析、资料检索、方法/声明提取、Claim Rubric、文献检索 |
| Benchmark | `benchmark_agent.go`、`benchmark_contract_agent.go`、`benchmark_dataset.go`、`benchmark_harness.go`、`benchmark_adapter.go` | 数据审计、split、泄漏检查、Metric/Reward 契约、隐藏指标重算 |
| Coder | `coder.go`、`coder_eino.go`、`coder_runtime.go`、`coder_runtime_flow.go`、`coder_exec.go`、`coder_python.go`、`coder_deps.go`、`coder_image.go` | 仓库发现、代码生成、绘图、runtime prepare/install/validate、依赖解析 |
| Research Coding | `research_coding_agent.go`、`reproduction_spec.go` | 论文仓库调试、Benchmark Adapter、RuntimeSpec、代码补丁 AutoResearch |
| Data | `data.go` | 指标汇总、论文声明对比、证据图判定、报告生成、视觉校验编排 |
| AutoResearch | `autoresearch.go`、`autoresearch_run.go`、`autoresearch_validation.go`、`autoresearch_spec.go`、`autoresearch_workspace.go`、`autoresearch_apply.go`、`autoresearch_helpers.go`、`autoresearch_eval.go` | ResearchSpec 冻结、候选执行、Keep/Reject、回滚、Holdout |
| Experiment Research | `experiment_research.go`、`experiment_retrieval_adapter.go`、`experiment_portable_adapter.go` | `experiment.spec/v1` 配置搜索与领域 Adapter |
| Ablation ToT | `ablation_tot.go` | 受限 Tree-of-Thoughts 消融设计 |
| Paper Debug | `paper_debug_harness.go` | 论文仓库调试状态机与受限补丁验证 |
| Visual Verifier | `visual_verifier.go` | 图像结构门禁 + 多模态视觉判定（pass/fail/inconclusive） |

### models

定义 `PlanGraph`、`Task`、`Artifact`、`ResearchSpec`、`TrialLedger`、`BenchmarkContract`、`RuntimePolicy` / `EnvironmentManifest` 等共享契约。修改这些结构时要同步检查前端 `src/contracts/`。

### store

`PlanStore` 接口有两个实现：内存存储与文件存储。文件存储采用“按计划原子快照 + 小型 append-only JSONL 索引/事件日志”：每个计划写入 `PLAN_STORE_PATH + ".d/<plan-id>.json"`，`PLAN_STORE_PATH` 只追加 `plan_saved`/`plan_updated`/`event` 小记录。启动时加载快照并回放事件；旧的全量快照 JSONL 会自动迁移为快照文件并压缩为 `.legacy-*.gz`，避免历史 O(n²) 写放大。

### sandbox

`sandbox` 是访问独立沙箱 gateway 的客户端；嵌入式 `sandboxserver` 已移除，单文件模式也必须通过 `SANDBOX_URL` 连接外部沙箱服务。

## API

除 `GET /ping` 与 `GET /api/health` 外，其余 `/api` 路由都经过 `APIAuthMiddleware`（`API_AUTH_TOKEN` 为空且 `REQUIRE_API_AUTH=false` 时放行，便于本地开发）；`GET /api/plans/:id/stream` 可用 60 秒 HMAC ticket 替代 Bearer。

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/ping` | 轻量存活检查 |
| `GET` | `/api/health` | 后端、仓库、沙箱、Optimizer 与意图服务健康状态 |
| `GET` | `/api/me` | 返回服务端派生的 user_id / session_id |
| `POST` | `/api/plan` | 创建计划 |
| `GET` | `/api/plans` | 列出计划 |
| `GET` | `/api/plans/:id` | 查询计划 |
| `POST` | `/api/plans/:id/execute` | 执行计划（可带 trigger=manual/auto） |
| `POST` | `/api/plans/:id/pause` / `resume` | 暂停/恢复派发 |
| `POST` | `/api/plans/:id/approve` | 批准计划 |
| `POST` | `/api/plans/:id/cancel` | 取消计划 |
| `DELETE` | `/api/plans/:id` / `/api/sessions/:id` | 删除计划/会话 |
| `GET` | `/api/sessions` | 列出会话 |
| `GET` | `/api/plans/:id/export` | 导出 md/json/zip 最小复现包 |
| `GET` | `/api/plans/:id/runtime-actions` | 查询运行时审批请求 |
| `POST` | `/api/plans/:id/runtime-actions/:actionId/decision` | 允许/拒绝运行时操作 |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` | 重试节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/reassign` | 重分配节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/run` | 从指定节点重置下游并重跑 |
| `POST` | `/api/plans/:id/tasks/:taskId/execute` | 仅执行计划内单个节点；输入来自 PlanGraph，不接收客户端路径 |
| `GET` | `/api/plans/:id/events` | 事件历史 |
| `GET` | `/api/plans/:id/stream-ticket` | 60 秒 HMAC SSE ticket |
| `GET` | `/api/plans/:id/stream` | SSE 事件流（支持 `?ticket=`） |
| `GET` | `/api/artifacts/:ref` | 读取 Artifact（含 blob 引用） |
| `POST` | `/api/execute` | 已禁用（410）；请使用计划内节点执行接口 |
| `POST` | `/api/uploads` | 上传附件（SHA-256 去重、配额、可选 ClamAV 扫描） |
| `GET` | `/api/uploads/:id` / `content` | 上传元数据 / 读取附件 |
| `DELETE` | `/api/uploads/:id` | 删除附件及派生文档 |
| `GET` | `/api/uploads/:id/document` | PDF 文本层摘要与页列表 |
| `GET` | `/api/uploads/:id/document/pages/:page` | 指定页全文 |
| `GET` | `/api/uploads/:id/document/search?q=...` | BM25 + 中文 bigram 文档检索 |
| `GET` | `/api/uploads/:id/document/sections/:section` | 指定章节 |
| `GET` | `/api/uploads/:id/document/quality` | per-page 质量与 OCR 统计 |
| `GET` | `/api/uploads/:id/document/pages/:page/layout` | 版面 blocks / reading order |
| `GET` | `/api/uploads/:id/document/locate` | 依据 quote 定位 block 偏移 |
| `GET` | `/api/uploads/:id/document/tables/:tableId` | 表格 JSON/CSV |
| `GET` | `/api/uploads/:id/document/figures/:figureId` | 图注与图片导出 |
| `POST` | `/api/uploads/:id/assist` | 划词翻译/解释（带页上下文） |
| `POST` | `/api/uploads/:id/translation-jobs` | 创建全文翻译任务 |
| `GET` | `/api/uploads/:id/translation-jobs` / `:jobId` / `:jobId/pages/:page` | 翻译任务列表/状态/单页结果 |
| `POST` | `/api/uploads/:id/translation-jobs/:jobId/cancel` | 取消翻译任务 |
| `GET` | `/api/literature/search` | 多 provider 文献检索；`relevance` 为跨 provider 词法+引用排序，单个 provider 超时只降级为 warning |
| `POST` | `/api/literature/import` | DOI/arXiv/PDF URL 导入 |
| `GET` | `/api/literature/records` / `:id` | 文献库（owner 隔离） |
| `POST` / `DELETE` | `/api/literature/records/:id/attach` | 加入/移出会话 |
| `GET` | `/api/literature/records/:id/bibtex` | BibTeX 导出 |
| `DELETE` | `/api/literature/records/:id` | 删除文献 |
| `GET` | `/api/literature/citations/*doi` | 引用/被引图 |
| `POST` | `/api/web/fetch` | 抽取公开网页正文 |
| `POST` | `/api/chat` | 对话 |
| `GET` | `/api/pdf-proxy` | PDF 代理 |
| `GET` / `POST` | `/api/model-profiles` | 模型 profile 列表/创建 |
| `PUT` / `DELETE` | `/api/model-profiles/:id` | 更新/删除模型 profile |
| `POST` | `/api/model-profiles/:id/test` / `probe-vision` | 连通性与视觉能力探测 |
| `GET` / `PUT` | `/api/model-routing` | 主模型/视觉/fallback 路由 |
| `GET` | `/api/metrics`（及 `/metrics`） | Prometheus 指标（Bearer 鉴权） |
| `GET` | `/api/audit/export` | 审计事件导出（调用者隔离） |
| `GET` | `/openapi.json` | OpenAPI 3.1 合同（根路径无需 Bearer；`/api/openapi.json` 同内容但走 API 鉴权） |

## 环境变量

| 变量 | 说明 |
|---|---|
| `OPENAI_API_KEY` | 必填，OpenAI-compatible API Key |
| `OPENAI_BASE_URL` | API Base URL；未设置时回退官方 DeepSeek `https://api.deepseek.com/v1` |
| `OPENAI_MODEL_NAME` | 模型名称；未设置时回退 `deepseek-chat` |
| `INTENT_SERVICE_URL` | 可选意图识别服务地址；为空时只使用规则路由 |
| `INTENT_ROUTER_MODE` | `rule` / `llm` / `hybrid` / `shadow`；代码默认 `rule`，Compose 默认 `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | 模型结果最低置信度，低于阈值回退规则（默认 0.65） |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | 调用意图服务的单次超时秒数 |
| `SANDBOX_URL` | 沙箱服务地址（默认 `http://localhost:8082`） |
| `SANDBOX_API_TOKEN` | 沙箱 Bearer Token |
| `SANDBOX_DEFAULT_IMAGE` | 论文复现默认真实运行时镜像 |
| `PLAN_STORE_PATH` | 计划快照目录（`<path>.d`）与小型 JSONL 索引/事件日志的基准路径 |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与大小限制 |
| `UPLOAD_USER_QUOTA_MB` / `UPLOAD_MAX_FILES` / `UPLOAD_TTL_DAYS` | 每用户配额、文件数上限与保留天数 |
| `BENCHMARK_PRIVATE_ROOT` | 隐藏标签与 evaluator 目录，不应挂载进沙箱 |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python 优化器 |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | API 静态 Token 与 fail-closed 开关；Compose 强制 `REQUIRE_API_AUTH=true` |
| `AUDIT_LOG_PATH` / `AUDIT_EXPORT_TOKEN` | 审计日志路径与跨用户导出 Token |
| `CORS_ALLOWED_ORIGINS` | 允许的前端 Origin |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | 任务与时长预算 |
| `PLAN_RATE_LIMIT_PER_MINUTE` / `PLAN_RATE_LIMIT_BURST` / `PLAN_MAX_ACTIVE_PER_USER` | 计划创建限流与每用户活跃计划上限 |
| `PLAN_MAX_CONCURRENT_TASKS` / `PLAN_GLOBAL_MAX_TASKS` / `PLAN_STALL_SECONDS` | 并发槽位与停滞检测 |
| `APPROVAL_TIMEOUT_SECONDS` | 运行时审批窗口 |
| `LLM_MAX_ATTEMPTS` / `LLM_ATTEMPT_TIMEOUT_SECONDS` / `LLM_RETRY_BASE_MS` / `LLM_RETRY_MAX_MS` | LLM 重试与超时 |
| `PDF_PARSE_MAX_MB` / `PDF_PARSE_TIMEOUT_SECONDS` | PDF 解析预算 |
| `PDF_OCR_MAX_PAGES` / `PDF_OCR_TIMEOUT_SECONDS` / `PDF_OCR_LANG` / `PDF_OCR_MIN_TEXT_CHARS` | OCR 预算、语言与文本阈值 |
| `DOCUMENT_CACHE_DIR` | 文档缓存目录；为空时落在 `PLAN_STORE_PATH` 目录下 |
| `LITERATURE_CACHE_DIR` / `LITERATURE_LIBRARY_DIR` / `LITERATURE_MAX_RETRIES` / `LITERATURE_PROVIDER_TIMEOUT_SECONDS` | 文献检索缓存、文献库与 provider 预算 |
| `WEB_FETCH_MAX_MB` / `WEB_FETCH_TIMEOUT_SECONDS` / `WEB_FETCH_RESPECT_ROBOTS` / `WEB_FETCH_CACHE_TTL_SECONDS` | 网页正文抽取限制 |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` / `ADMIN_TOKEN` | 运行期模型 profile 存储、加密密钥与全局 profile 管理 |
| `MODEL_PROFILE_MAX_PER_USER` / `MODEL_PROBE_RATE_LIMIT_SECONDS` | 每用户 profile 上限与探测频率 |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `VISION_MIN_EDGE_PX` / `FEATURE_VISION_VERIFICATION` | 视觉校验预算与开关 |
| `PROMPT_CONTEXT_WINDOW` / `PROMPT_MAX_OUTPUT_TOKENS` / `PROMPT_SAFETY_TOKENS` | Prompt token 预算 |
| `PLANNER_AGENT_ENABLED` | 是否启用 agent planner（E2E 覆盖会关闭以固定计划形状） |
| `REQUIRE_PLAN_APPROVAL` | 是否强制人工审批 |
| `LOG_MODEL_OUTPUT` | 是否把模型输出写入进程日志 |

## 构建与测试

```bash
go mod tidy
go build -o bin/api ./cmd/api
go build -o bin/research-assistant ./cmd/app

go test ./...
go test -race -count=1 ./internal/agent/conversation ./internal/prompts ./internal/planner ./internal/api ./internal/literature ./internal/documents ./internal/scheduler ./internal/modelprofile ./internal/modelresolver
go test -v ./evals
```

后端包含 Linux 专用的进程组、`syscall.Kill` 和 `Statfs` 调用。**Windows 原生后端不受支持**；Windows 请使用 Docker Compose。在 Linux 上可交叉构建：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false ./...
```

测试同样依赖 Linux 进程组语义，建议在 Docker 容器内运行 `go test ./...`。

单文件构建需要前端产物，并且运行时必须提供外部沙箱：

```bash
cd ../frontend && npm run build
cd ../backend && go build -o bin/research-assistant ./cmd/app
SANDBOX_URL=http://localhost:8082 ./bin/research-assistant
```

`cmd/app` 不再自动拉起嵌入式沙箱；未设置 `SANDBOX_URL` 会直接退出。

`webui/embed.go` 使用 `//go:embed dist`。仓库中保留 `webui/dist/placeholder.txt` 以便未构建前端时也能通过编译；实际发布前应把前端 `dist` 同步到该目录。

## 集成测试

`tests/` 与部分 `internal/agent` 测试需要真实 LLM、网络或 Docker，默认会跳过。按测试文件中的环境变量开启，例如：

```bash
REAL_PAPER_REPRO_TEST=1 SANDBOX_URL=http://localhost:8082 \
  go test ./tests -run TestRealPaperReproductionFlow -v -count=1
```

## 子模块说明

- [agent](internal/agent/README.md)
- [planner](internal/planner/README.md)
- [scheduler](internal/scheduler/README.md)
