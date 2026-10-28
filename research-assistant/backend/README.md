# backend

Go 后端是 Research Assistant Agent 的业务核心，负责 HTTP API、意图识别、任务规划、DAG 调度、Agent 编排、沙箱调用、上传管理与 SSE 事件推送。

模块路径：`research-assistant-backend`

## 架构与流程

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

一次计划执行的生命周期：

```text
HTTP API -> Intent Router -> Planner + DAG Validator -> PlanStore
          -> Scheduler (lease/retry/cancel/budget)
          -> RoutedTaskExecutor -> 专业 Agent -> Sandbox gateway/executor
          -> Artifact 校验 -> Event Bus -> SSE
```

## 入口

| 入口 | 说明 |
|---|---|
| `cmd/api/main.go` | 纯 API 服务，监听 `:8080`，适合与独立沙箱服务配合 |
| `cmd/app/main.go` | 单文件应用，内嵌前端静态资源；运行时必须配置 `SANDBOX_URL`，不会自动拉起本地沙箱 |

## 目录结构

```text
backend/
├── cmd/
│   ├── api/                 纯 API 入口
│   └── app/                 单文件应用入口
├── internal/
│   ├── api/                 HTTP 路由、认证、上传、计划运行时
│   ├── planner/             意图到 DAG 的规划器
│   ├── scheduler/           DAG 执行、租约、仓库发现与准备
│   ├── agent/               Chat/Librarian/Benchmark/Coder/Research Coding/Data/Visual Verifier
│   ├── documents/           PDF 解析、OCR、版面/表格/图注、quote locate、质量报告
│   ├── modelprofile/        per-user/全局模型 profile、加密存储、routing、视觉探测
│   ├── modelresolver/       按 owner/role 解析模型 profile 与 revision 缓存
│   ├── literature/          arXiv/OpenAlex/Crossref 等 provider、文献库、BibTeX、引用图
│   ├── uploadstore/         上传配额、去重、生命周期
│   ├── webread/             网页正文抽取与 SSRF/robots 防护
│   ├── metrics/             Prometheus 指标
│   ├── audit/               审计事件导出
│   ├── models/              计划、任务、Artifact、AutoResearch 契约
│   ├── prompts/             各 Agent 的系统提示词与 token 预算
│   ├── store/               计划与事件存储（文件/内存）
│   ├── sandbox/             沙箱 gateway HTTP 客户端
│   ├── appconfig/           LLM 配置加载
│   ├── events/              事件总线
│   └── tools/               工具实现（ask_user_question）
├── evals/                   规划评测数据集与测试
├── tests/                   跨模块集成测试
└── webui/                   前端嵌入入口
```

## 主要包职责

### planner

把用户意图、附件和上下文转换为 `PlanGraph`。规划器负责节点类型、依赖关系、输入输出契约和基础校验，确保 Scheduler 只接收合法 DAG。

### scheduler

按入度执行 DAG，维护任务租约、重试、取消、预算和事件。仓库相关任务还包含仓库发现（`repo_discovery.go`）、仓库准备（`repo_prepare.go`）与复现模式控制。

### agent

| Agent | 文件 | 职责 |
|---|---|---|
| Chat | `chat.go` | 通用问答与轻量任务入口 |
| Librarian | `librarian.go` | 论文解析、资料检索、方法/声明提取、Rubric 冻结 |
| Benchmark | `benchmark_agent.go` 等 | 数据审计、split、泄漏检查、Metric/Reward 契约 |
| Coder | `coder.go` | 仓库发现、代码准备、依赖分析与修复 |
| Research Coding | `research_coding_agent.go` | 论文仓库调试、Benchmark 适配、代码补丁 AutoResearch |
| Data | `data.go` | 指标汇总、论文声明对比、证据图判定、报告生成 |
| AutoResearch | `autoresearch.go` | ResearchSpec 冻结、候选执行、Keep/Reject、回滚、Holdout |
| Experiment Research | `experiment_research.go` | `experiment.spec/v1` 配置搜索与 Domain Adapter |
| Ablation ToT | `ablation_tot.go` | 受限 Tree-of-Thoughts 消融设计 |
| Literature Search | `literature_search.go` | 多 provider 文献检索、去重、排序与导入 |
| Visual Verifier | `visual_verifier.go` | 图像结构门禁 + 多模态视觉判定（pass/fail/inconclusive） |

### models

定义 `PlanGraph`、`Task`、`Artifact`、`ResearchSpec`、`TrialLedger`、`BenchmarkContract` 等共享契约。修改这些结构时要同步检查前端 `src/contracts/`。

### store

`PlanStore` 接口有两个实现：内存存储与文件存储。文件存储采用“按计划原子快照 + 小型 append-only JSONL 索引/事件日志”：每个计划写入 `PLAN_STORE_PATH + ".d/<plan-id>.json"`，`PLAN_STORE_PATH` 只追加 `plan_saved`/`plan_updated`/`event` 小记录。启动时加载快照并回放事件；旧的全量快照 JSONL 会自动迁移为快照文件并压缩为 `.legacy-*.gz`，避免历史 O(n²) 写放大。

### sandbox

`sandbox` 是访问独立沙箱 gateway 的客户端；嵌入式 `sandboxserver` 已移除，单文件模式也必须通过 `SANDBOX_URL` 连接外部沙箱服务。

## API

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/ping` | 轻量存活检查 |
| `GET` | `/api/health` | 后端、仓库、沙箱与 Optimizer 健康状态 |
| `POST` | `/api/plan` | 创建计划 |
| `GET` | `/api/plans/:id` | 查询计划 |
| `POST` | `/api/plans/:id/execute` | 执行计划 |
| `POST` | `/api/plans/:id/approve` | 批准计划 |
| `POST` | `/api/plans/:id/cancel` | 取消计划 |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` | 重试节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/reassign` | 重分配节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/execute` | 执行计划内单个节点；忽略客户端 `inputs` |
| `GET` | `/api/plans/:id/events` | 事件历史 |
| `GET` | `/api/plans/:id/stream-ticket` | 60 秒 HMAC SSE ticket |
| `GET` | `/api/plans/:id/stream` | SSE 事件流（支持 `?ticket=`） |
| `POST` | `/api/execute` | 已禁用（410）；请使用上面的计划内节点执行接口 |
| `POST` | `/api/uploads` | 上传附件 |
| `GET` | `/api/uploads/:id/content` | 读取附件 |
| `POST` | `/api/chat` | 对话 |
| `GET` | `/api/pdf-proxy` | PDF 代理 |
| `GET` | `/api/uploads/:id/document` | PDF 文本层摘要与页列表 |
| `GET` | `/api/uploads/:id/document/pages/:page` | 指定页全文 |
| `GET` | `/api/uploads/:id/document/search?q=...` | BM25 + 中文 bigram 文档检索 |
| `GET` | `/api/uploads/:id/document/quality` | per-page 质量与 OCR 统计 |
| `GET` | `/api/uploads/:id/document/pages/:page/layout` | 版面 blocks / reading order |
| `GET` | `/api/uploads/:id/document/tables/:tableId` | 表格 JSON/CSV |
| `GET` | `/api/uploads/:id/document/figures/:figureId` | 图注与图片导出 |
| `POST` | `/api/uploads/:id/assist` | 划词翻译/解释（带页上下文） |
| `GET` | `/api/literature/search` | 多 provider 文献检索 |
| `POST` | `/api/literature/import` | DOI/arXiv/PDF URL 导入 |
| `GET` | `/api/literature/records` | 文献库（owner 隔离） |
| `GET` | `/api/literature/records/:id/bibtex` | BibTeX 导出 |
| `GET` | `/api/literature/citations/:doi` | 引用/被引图 |
| `GET` | `/api/model-profiles` / `POST` | 模型 profile 列表/创建 |
| `PUT` | `/api/model-profiles/:id` / `DELETE` | 更新/删除模型 profile |
| `POST` | `/api/model-profiles/:id/test` / `probe-vision` | 连通性与视觉能力探测 |
| `GET` | `/api/model-routing` / `PUT` | 主模型/视觉/fallback 路由 |
| `GET` | `/api/metrics` | Prometheus 指标（Bearer 鉴权） |
| `GET` | `/api/audit/export` | 审计事件导出（调用者隔离） |

## 环境变量

| 变量 | 说明 |
|---|---|
| `OPENAI_API_KEY` | 必填，OpenAI-compatible API Key |
| `OPENAI_BASE_URL` | API Base URL（官方 DeepSeek 为 `https://api.deepseek.com/v1`） |
| `OPENAI_MODEL_NAME` | 模型名称（官方示例 `deepseek-flash`） |
| `INTENT_SERVICE_URL` | 可选意图识别服务地址；为空时只使用规则路由 |
| `INTENT_ROUTER_MODE` | `rule` / `llm` / `hybrid` / `shadow`；Compose 默认 `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | 模型结果最低置信度，低于阈值回退规则 |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | 调用意图服务的单次超时秒数 |
| `SANDBOX_URL` | 沙箱服务地址 |
| `SANDBOX_API_TOKEN` | 沙箱 Bearer Token |
| `PLAN_STORE_PATH` | 计划快照目录（`<path>.d`）与小型 JSONL 索引/事件日志的基准路径 |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与大小限制 |
| `BENCHMARK_PRIVATE_ROOT` | 隐藏标签与 evaluator 目录，不应挂载进沙箱 |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python 优化器 |
| `API_AUTH_TOKEN` | API 静态 Token；Compose 必填 |
| `REQUIRE_API_AUTH` | `true` 时 token 为空将 fail-closed；Compose 强制为 `true` |
| `CORS_ALLOWED_ORIGINS` | 允许的前端 Origin |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | 任务与时长预算 |
| `LLM_MAX_ATTEMPTS` | LLM HTTP 最大尝试次数（默认 3，1–8） |
| `LLM_ATTEMPT_TIMEOUT_SECONDS` | LLM 单次尝试超时秒数（默认 180） |
| `LLM_RETRY_BASE_MS` / `LLM_RETRY_MAX_MS` | 指数退避初始/最大毫秒数（默认 500/8000） |
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
- [ask_user_question](internal/tools/ask_user_question/README.md)