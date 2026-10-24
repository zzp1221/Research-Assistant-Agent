# Research Assistant Agent

**面向论文复现、数据评测与预算受限自动研究的多智能体科研执行系统**

Research Assistant Agent 把自然语言研究目标转换为可验证的执行计划：系统先理解任务、检索资料并固化评测契约，再由 Planner 生成 DAG，Scheduler 将节点路由给专业 Agent，在隔离沙箱中真实执行代码或实验，最后通过 Artifact、指标、EnvironmentManifest 和证据图给出可审计的结果。

项目由三个相对独立的源码部分组成：

- `research-assistant/`：完整产品实现，包含 Go 后端、React 工作台、Docker 沙箱服务、Python 研究优化器和可选 AI 服务。
- `docker-core/`：面向 Docker / gVisor 的底层沙箱内核实验模块，可独立构建和测试。
- `tool/`：一个零依赖的 Go 工具调用示例，演示 LLM Function Calling 的注册、路由与执行循环。

本文档分为三部分：**[1. 整体架构与 Agent 运行说明](#1-整体架构与-agent-运行说明)**、**[2. 快速开始](#2-快速开始)**、**[3. 本地开发](#3-本地开发)**。

## 1. 整体架构与 Agent 运行说明

### 1.1 设计目标与核心能力

系统把“研究意图”拆成可验证的执行链：

1. **理解**：意图识别 + 类型化 ResearchContext（附件、论文元数据、文档、约束）。
2. **规划**：统一 PlanBuilder 生成 DAG，InputBindings 绑定上下文/Artifact，DAG Validator 校验依赖与产物。
3. **授权**：按计划级运行权限决定重要操作是自动执行还是逐项审批。
4. **执行**：Scheduler 以租约、重试、取消和预算调度节点，专业 Agent 在 sandbox 中落地执行。
5. **验收**：输出 Artifact、EnvironmentManifest、PlanOutcome 与逐主张证据图，而不是只看“节点是否完成”。

| 能力 | 说明 |
|---|---|
| 研究任务规划 | 将论文复现、代码执行、框架对比、数据评测等目标拆解为带依赖关系的 DAG |
| 专业 Agent 路由 | 按任务类型路由到 Chat、Librarian、Benchmark、Coder、Research Coding、Data 和 Answer Agent |
| 类型化研究上下文 | ResearchContext + InputBindings 统一附件、论文元数据、文档与约束；不再依赖各模板手工注入 |
| 全文文档检索 | DocumentStore 支持 BM25 + 中文 bigram、按页/按节检索与页码引用；长 PDF 不再只读前几页 |
| 论文复现运行时 | RuntimeSpec、镜像/依赖探测、真实安装与 EnvironmentManifest；支持自动执行或逐项审批 |
| 真实隔离执行 | 通过独立 Go 沙箱服务调用原生 Docker，支持持久工作区、流式日志和产物回传 |
| 仓库优先的论文复现 | 发现或使用指定 Git 仓库，生成 RuntimeSpec、安装依赖并执行受控最小/完整实验 |
| 预算受限的 AutoResearch | 在冻结的指标、数据和预算内搜索代码补丁或方法/超参数候选，真实执行后 Keep/Reject |
| 可信 Benchmark | 数据审计、可复现 split、泄漏检查、Metric/Reward 契约与隐藏标签重算 |
| 分层候选搜索 | 受限穷举、路线 UCB、Top-K Beam、UCT-style 参数树和隐藏 Holdout 复验 |
| 逐主张证据 | 冻结分层 Rubric，把论文主张、判定准则和真实 Artifact 绑定为证据图 |
| 语义结果状态 | PlanOutcome 区分 completed / partial / smoke / mock / blocked / failed / needs_review，并展示缺失项 |
| 实时可观测 | SSE 推送计划、节点、日志、RuntimeAction 和 Artifact 事件，前端同步展示执行状态 |
| 持久化与治理 | 计划与事件持久化、任务租约、取消/重试、预算限制、计划审批与运行时操作审批 |

### 1.2 系统架构

```text
Researcher / Paper / Repository / Dataset
                    |
                    v
        React Workbench
  (permission selector / DAG / runtime action cards / outcome)
                    |
            REST / Upload / SSE
                    |
                    v
 API -> Intent Router -> ResearchContext
        (attachments, paper_metadata, document refs, constraints)
                    |
                    v
 Planner + InputBindings -> validated PlanGraph + Plan Store
                    |
                    v
 Scheduler (lease / retry / cancel / budget / awaiting_approval)
                    |
                    v
 Routed Task Executor
   Chat / Librarian / Answer / Benchmark / Coder / Research Coding / Data
                    |
                    v
 RuntimeGate (confirm_important / full_access)
                    |
                    v
 Sandbox Client -> docker-sandbox gateway -> executor -> Native Docker
                    |
                    v
 stdout / metrics / EnvironmentManifest / TrialLedger / claim evidence
                    |
                    v
 Artifact + PlanOutcome -> SSE -> Workbench
```

| 图 | draw.io 源文件 | SVG 预览 |
|---|---|---|
| 系统架构图 | [architecture.drawio](docs/diagrams/architecture.drawio) | [architecture.svg](docs/diagrams/architecture.svg) |
| 运行流程图 | [runtime-flow.drawio](docs/diagrams/runtime-flow.drawio) | [runtime-flow.svg](docs/diagrams/runtime-flow.svg) |
| Agent 执行流程图 | [agent-execution-flow.drawio](docs/diagrams/agent-execution-flow.drawio) | [agent-execution-flow.svg](docs/diagrams/agent-execution-flow.svg) |

图形说明与编辑方式见 [docs/README.md](docs/README.md) 和 [docs/diagrams/README.md](docs/diagrams/README.md)。

### 1.3 Agent 运行说明

一次请求的完整执行路径如下：

1. **意图与上下文**
   - 意图服务把自然语言分类为 `Code_Execution`、`Paper_Reproduction`、`Framework_Evaluation`、`Custom_Benchmark`、`AutoResearch` 或 `General`。
   - 附件被解析为 `AttachmentRef` / `DocumentRef`；PDF 生成 `paper_metadata`，并写入 `ResearchContext`。
   - 计划级不变量检查附件/文档是否被节点引用，避免“上传了但没被消费”。

2. **计划生成**
   - 统一 PlanBuilder 生成节点，节点通过 `InputBindings` 声明 `context_attachment`、`context_document` 或 `artifact` 来源。
   - Scheduler 在执行前解析绑定；必需绑定缺失时任务直接失败，不继续空跑。
   - DAG Validator 检查依赖、产物 producer、循环和关键节点契约。

3. **运行权限**
   - 计划创建时选择 `confirm_important`（默认）或 `full_access`。
   - `confirm_important`：镜像拉取、依赖安装、数据/模型下载、GPU、网络访问前暂停，生成 `RuntimeActionRequest`；只有计划所有者能允许或拒绝。
   - `full_access`：在镜像 allowlist、磁盘/时长预算和 sandbox 安全边界内自动执行，并记录 `auto_approved=true` 审计动作。

4. **调度**
   - Scheduler 负责租约、并发槽位、重试、取消、预算和 `awaiting_approval` 暂停/恢复。
   - 等待审批的任务不会被视为终态；审批通过后从原节点继续，拒绝则终止计划并标记 `outcome=blocked`。

5. **Agent 执行**
   - 节点按 `assigned_to` 路由到专业 Agent。
   - 所有跨 Agent 数据通过 Artifact/InputBindings 传递，不再依赖模板手工拼 `Inputs`。

6. **Sandbox 执行**
   - 运行时先探测宿主能力（Docker、GPU、CPU、内存、磁盘、镜像 allowlist），再准备镜像。
   - 依赖安装、运行命令和 import 检查都在 sandbox 中真实执行；输出 `EnvironmentManifest`。

7. **结果验收**
   - PlanFinalizer 根据证据把结果标记为 `completed`、`partial`、`smoke`、`mock`、`blocked`、`failed` 或 `needs_review`。
   - Claim-Evidence 图把论文主张、判定准则和真实 Artifact 绑定；前端显示 OutcomeBadge 与 missing requirements。

| Agent | 主要职责 |
|---|---|
| Chat / General | 通用对话、范围澄清、非论文类请求 |
| Librarian | 论文解析、paper_metadata、DocumentStore 检索、Claim Rubric |
| Benchmark | 数据审计、split、Metric/Reward 契约、隐藏标签验证 |
| Coder | 代码生成、绘图、runtime prepare/install/validate、sandbox 命令 |
| Research Coding | 仓库准备、benchmark adapter、论文代码执行/调试、AutoResearch |
| Data | 对比报告、评估报告、Claim-Evidence 构建 |
| Answer | 面向用户的最终回答合成 |

### 1.4 运行时权限与论文复现运行时

论文复现请求在创建计划时传入：

```json
{
  "intent": "复现我上传论文的核心方法并跑通最小测试",
  "attachments": ["<upload-id>"],
  "runtime_permission_mode": "confirm_important",
  "runtime_permission_acknowledged": false
}
```

- **重要操作需确认**（`confirm_important`，默认）：拉取镜像、安装依赖、下载公开数据/模型、使用 GPU 或访问网络前，计划暂停并生成 `RuntimeAction`；只有计划所有者可以允许或拒绝。
- **完全权限**（`full_access`）：上述操作在镜像 allowlist、磁盘/时长预算与 sandbox 安全边界内自动执行；创建计划时必须显式传 `runtime_permission_acknowledged=true`。
- 拒绝重要操作会立即终止计划：`status=failed`、`outcome=blocked`，并写入 `missing_requirements=[user_rejected]`。
- 运行时链为 `reproduction_spec -> runtime_probe -> image_prepare -> prepare_runtime -> install_dependencies -> runtime_validate -> paper_code_execute`。
- `runtime_validate` 输出 `EnvironmentManifest`：镜像 digest、Python/pip freeze、GPU、数据 hash、运行命令与工作目录。
- 完整复现资源不足时不会用 smoke 冒充成功；`outcome` 会明确标记 `smoke`、`partial` 或 `blocked`。

### 1.5 仓库结构

```text
Research Assistant Agent/
├── README.md
├── .github/workflows/ci.yml     # Linux/Windows CI：Go、前端、Python、Docker build
├── docs/                        # 架构图、流程图与项目级文档
├── docker-core/                 # 底层 Docker/gVisor 沙箱内核实验模块
│   ├── checkpoint/              # 快照、回滚与负反馈注入
│   ├── config/                  # TOML 配置加载
│   ├── executor/                # DAG 调度、节点竞速与资源网关
│   ├── sandbox/                 # 持久化 TTY 沙箱
│   ├── watchdog/                # 语义断路器与超时防护
│   └── tests/                   # 真实 Docker 集成测试
├── research-assistant/          # 产品主实现
│   ├── backend/                 # Go API、Planner、Scheduler、Agent
│   ├── frontend/                # React + TypeScript 工作台
│   ├── docker-sandbox/          # 独立 Go 沙箱服务
│   ├── research-optimizer/      # Python 候选策略与经验存储
│   ├── ai-services/             # 可选 Python AI 服务
│   ├── examples/                # 可运行示例与验收脚本
│   ├── test/                    # Golden test 数据与运行器
│   ├── scripts/                 # Unix / Windows 启动脚本
│   ├── backend.env.example
│   ├── docker-compose.yml
│   └── Makefile
└── tool/                        # Go 工具调用框架示例
```

### 1.6 运行边界

- 本项目是研究原型，接口和数据结构可能调整，不承诺向后兼容。
- 单机持久化基于 append-only JSONL 事件日志；多副本部署需要共享事务数据库与 leader election。
- 已支持静态 API token 和计划所有权检查，但不是 OIDC、RBAC 或生产级多租户认证。
- `docker-sandbox` gateway 不挂载 Docker socket，也不挂载宿主工作区；只有内部 `docker-sandbox-executor` 持有 socket 和 `/tmp` 挂载。executor 不发布端口，仍是受控的高权限执行边界。
- 默认运行时镜像是 CPU/通用 Python 镜像；GPU 透传需要额外配置。
- 运行时权限只决定“是否自动执行重要操作”，不会绕过镜像 allowlist、资源上限、sandbox 隔离或计划所有权检查。
- 当前复现重点是轻量 smoke 与结构消融，不包含大规模数据集训练；资源不足时返回 `blocked/partial`，不会用 mock 冒充成功。
- Framework_Evaluation 仍保留 deterministic offline baseline；它不是真实 LangChain/LlamaIndex 性能 benchmark，真实框架评测需独立专项。
- 候选搜索是真实 Validation Reward 驱动的分层 UCB、Top-K Beam 与 UCT-style 搜索，不是完整 MCTS 或通用强化学习策略。

## 2. 快速开始

### 2.1 前置条件

- Docker Engine 20.10+ 与 Docker Compose v2
- Git
- 一个 OpenAI-compatible LLM API Key
- 本地开发需要 Go 1.26.1（backend/docker-sandbox）与 Node.js 22+
- 后端依赖 Linux 进程组与 `Statfs`，Windows 原生后端不受支持；Windows 请使用 Docker Compose

### 2.2 使用 Docker Compose

```bash
cd research-assistant
cp .env.example .env
# 编辑 .env，设置随机 SANDBOX_API_TOKEN、RESEARCH_OPTIMIZER_API_TOKEN 与 API_AUTH_TOKEN
# API_AUTH_TOKEN 会同时注入 backend、sandbox 认证和前端构建；Compose 不会使用默认 token。
cp backend.env.example backend.env
# 编辑 backend.env，至少填写 OPENAI_API_KEY

docker compose up --build -d
```

启动后访问（端口可通过 `.env` 的 `*_HOST_PORT` 覆盖，以下为默认值）：

| 服务 | 地址 |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health Check | http://localhost:8080/api/health（唯一无需 Bearer 的 API） |
| Sandbox API | http://localhost:8082 |
| Research Optimizer | http://localhost:8090 |

确认后端与沙箱均已就绪：

```bash
curl -s http://localhost:8080/api/health
```

期望响应中同时出现 `backend.ok=true`、`repository.ok=true`、`sandbox.ok=true` 与 `research_optimizer.ok=true`。查看日志或停止服务：

```bash
docker compose logs -f
docker compose down
```

### 2.3 典型任务

#### 论文复现

在 Web UI 输入：

```text
请使用 https://github.com/harvardnlp/annotated-transformer 复现
Attention Is All You Need，使用 smoke 模式运行轻量注意力消融，
不要执行 WMT14 完整训练。
```

在发送前选择“重要操作需确认”或“完全权限”。系统会生成并执行“解析论文 -> 冻结主张 Rubric -> 检索仓库 -> 准备工作区 -> 解析依赖 -> 生成 RuntimeSpec -> 探测 sandbox 能力 -> 准备镜像 -> 准备运行时 -> 安装依赖 -> 校验运行时/写 EnvironmentManifest -> 执行实验 -> 对比论文声明 -> 主张证据图”的主链。

如果选择“重要操作需确认”，镜像拉取和依赖安装会分别暂停，等待在右侧计划面板中允许或拒绝；拒绝后计划立即终止并标记 `outcome=blocked`。

#### 自有数据评测

上传 CSV、TSV、JSON 或 JSONL 后输入：

```text
用 https://github.com/OWNER/REPOSITORY 跑 benchmark，
输入列是 review，标签列是 label，最多运行 500 条样本。
```

Benchmark Agent 会审计列映射、生成泄漏安全的 train/validation/test 划分，冻结 Metric/Reward 契约，并在模型不可见的隐藏标签上重算最终指标。

#### 预算受限 AutoResearch

```text
用 https://github.com/OWNER/REPOSITORY 做 AutoResearch，
按 autoresearch.json 运行，最多 3 轮，总时长不超过 15 分钟，最终验证 3 次。
```

系统冻结 ResearchSpec 与仓库 revision，在受限文件范围内生成候选，按真实指标执行 Keep/Reject、退化回滚和隐藏 Holdout 复验。

## 3. 本地开发

### 3.1 启动本地服务

```bash
cd research-assistant
make install
export SANDBOX_API_TOKEN=<random-token>
make run-sandbox     # 终端 1，gateway + executor 的本地等价物
make run-backend     # 终端 2，需要 SANDBOX_URL 与 API_AUTH_TOKEN
make run-frontend    # 终端 3
```

Windows 用户请使用 Docker Compose；`scripts/windows/start-backend.ps1` 会直接提示 Docker Compose，后端原生 Windows 编译仍不受支持。CI 的 Windows job 覆盖 docker-sandbox 测试与 docker-core 构建，backend 保持 Linux-only；前端在 Linux job 中构建与测试。详见 [scripts/README.md](research-assistant/scripts/README.md)。

### 3.2 配置

复制 `research-assistant/backend.env.example` 为 `backend.env`。常用变量：

| 变量 | 必填 | 说明 |
|---|---:|---|
| `OPENAI_API_KEY` | 是 | OpenAI-compatible API 密钥 |
| `OPENAI_BASE_URL` | 否 | 默认示例为官方 DeepSeek `https://api.deepseek.com/v1` |
| `OPENAI_MODEL_NAME` | 否 | 默认示例为 `deepseek-flash` |
| `INTENT_SERVICE_URL` | 否 | 可选 LLM 意图服务；Compose 默认 `http://intent-recognition:8000` |
| `INTENT_ROUTER_MODE` | 否 | `rule` / `llm` / `hybrid` / `shadow`；Compose 默认 `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | 否 | 模型结果最低置信度，低于阈值回退规则 |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | 否 | 调用意图服务的单次超时秒数 |
| `SANDBOX_URL` | 否 | 本地默认 `http://localhost:8082`；Compose 会覆盖为服务地址 |
| `SANDBOX_DEFAULT_IMAGE` | 否 | 默认 Python 运行时镜像 |
| `SANDBOX_IMAGE_ALLOWLIST` | 否 | 允许 sandbox 使用的镜像前缀/名称；运行时镜像必须命中 allowlist |
| `PLAN_STORE_PATH` | 否 | 计划与事件 append-only JSONL 日志路径；旧 JSON 快照会自动迁移 |
| `RESEARCH_OPTIMIZER_URL` | 否 | Python Research Optimizer 地址；不配置时使用确定性 Go fallback |
| `RESEARCH_OPTIMIZER_API_TOKEN` | 否 | Backend 与 Optimizer 之间的 Bearer Token |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | 否 | 计划尝试次数与时长预算 |
| `REQUIRE_PLAN_APPROVAL` | 否 | 执行前强制人工审批 |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | Compose 必填 | API 静态 Bearer 保护；Compose 强制 `REQUIRE_API_AUTH=true`，token 为空时拒绝启动请求 |
| `VITE_API_AUTH_TOKEN` | 自动 | Compose 用 `API_AUTH_TOKEN` 构建前端；不再需要单独设置 |
| `SANDBOX_API_TOKEN` | 是 | Compose 必须通过 `.env` 设置；gateway 与 executor 均 fail-closed |
| `CORS_ALLOWED_ORIGINS` | 否 | 允许访问后端的前端 Origin 列表 |

后端镜像构建还支持 `DEBIAN_MIRROR` 与 `DEBIAN_SECURITY_MIRROR`，由执行 `docker compose` 的 shell 或项目 `.env` 读取。GPU 透传需要宿主机安装 NVIDIA Container Toolkit，并设置 `SANDBOX_DOCKER_GPUS=all`。

### 3.3 API 摘要

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/health` | 检查后端、仓库、沙箱与 Optimizer 状态 |
| `POST` | `/api/plan` | 根据用户意图创建并保存 DAG |
| `GET` | `/api/plans/:id` | 查询计划、节点状态和产物 |
| `POST` | `/api/plans/:id/execute` | 启动整张计划图 |
| `POST` | `/api/plans/:id/approve` | 批准需要人工确认的计划 |
| `GET` | `/api/plans/:id/runtime-actions` | 查询运行时审批请求（镜像/依赖/GPU/网络等） |
| `POST` | `/api/plans/:id/runtime-actions/:actionId/decision` | 允许或拒绝一次运行时操作；拒绝会终止计划 |
| `POST` | `/api/plans/:id/cancel` | 取消计划与未完成节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` | 重试失败、阻塞或取消节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/reassign` | 重分配节点并使旧执行租约失效 |
| `POST` | `/api/plans/:id/tasks/:taskId/execute` | 仅执行计划内节点；输入来自 PlanGraph/Artifact，不接收客户端路径 |
| `GET` | `/api/plans/:id/events` | 获取计划事件历史 |
| `GET` | `/api/plans/:id/stream-ticket` | 获取 60 秒 HMAC SSE ticket；启用 API_AUTH_TOKEN 时前端使用 |
| `GET` | `/api/plans/:id/stream` | 订阅计划级 SSE 事件流；可携带 `?ticket=` |
| `POST` | `/api/execute` | 已禁用（410）；旧直接执行入口不再接受任意 `Inputs` |
| `POST` | `/api/uploads` | 上传论文、配置或小型 Benchmark 数据 |
| `GET` | `/api/uploads/:id/content` | 按用户所有权读取上传内容 |
| `GET` | `/api/uploads/:id/document` | 读取 PDF 文本层摘要与页列表 |
| `GET` | `/api/uploads/:id/document/pages/:page` | 读取指定页全文 |
| `GET` | `/api/uploads/:id/document/search?q=...` | 在文档中检索相关片段（BM25 + 中文 bigram） |
| `GET` | `/api/uploads/:id/document/sections/:section` | 读取指定章节 |
| `POST` | `/api/chat` | 通用对话接口 |
| `GET` | `/api/pdf-proxy?url=...` | 代理读取远端 PDF |
| `GET` | `/api/v1/capabilities` | Sandbox 宿主能力：Docker、GPU、CPU、内存、磁盘、镜像 allowlist |
| `POST` | `/api/v1/images/ensure` | 校验并确保运行时镜像存在，返回 digest/size |

完整路由和请求结构见 [backend/README.md](research-assistant/backend/README.md)。

### 3.4 测试与质量门禁

后端（Linux 或容器内）：

```bash
cd research-assistant/backend
go test ./...
go vet ./...
```

前端：

```bash
cd research-assistant/frontend
npm test
npm run lint
npm run build
npm run check:bundle
```

Sandbox：

```bash
cd research-assistant/docker-sandbox
go test ./...
```

### 3.5 开发产物与报告

- `plan.md`、`spec.md`、`tasks.md` 是开发过程中的计划/规格/任务记录，默认不进入版本库。
- `reports/` 下的验收报告、E2E JSON、截图和下载的 PDF fixture 属于本地运行产物；`.gitignore` 只保留 `reports/README.md` 作为目录说明。
- 需要长期保留的架构说明放在 `docs/`，需要长期保留的测试证据请先脱敏再移入文档，而不是直接提交原始运行日志。

### 3.6 模块说明

- [docs](docs/README.md)：架构图、流程图与项目级文档
- [research-assistant](research-assistant/README.md)：产品主实现与启动方式
- [backend](research-assistant/backend/README.md)：Go API、规划、调度与 Agent
- [frontend](research-assistant/frontend/README.md)：React 工作台
- [docker-sandbox](research-assistant/docker-sandbox/README.md)：独立沙箱服务
- [research-optimizer](research-assistant/research-optimizer/README.md)：Python 候选策略服务
- [ai-services](research-assistant/ai-services/README.md)：可选 AI 服务与意图识别
- [examples](research-assistant/examples/README.md)：可运行示例
- [docker-core](docker-core/README.md)：底层沙箱内核实验模块
- [tool](tool/README.md)：Go 工具调用框架示例
