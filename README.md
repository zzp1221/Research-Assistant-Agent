# Research Assistant Agent

**面向论文复现、数据评测与预算受限自动研究的多智能体科研执行系统**

Research Assistant Agent 把自然语言研究目标转换为可验证的执行计划：系统先理解任务、检索资料并冻结评测契约，再由 Planner 生成 DAG，Scheduler 将节点路由给专业 Agent，在隔离沙箱中真实执行代码或实验，最后通过 Artifact、指标和证据图给出可审计的结果。

项目由三个相对独立的源码部分组成：

- `research-assistant/`：完整产品实现，包含 Go 后端、React 工作台、Docker 沙箱服务、Python 研究优化器和可选 AI 服务。
- `docker-core/`：面向 Docker / gVisor 的底层沙箱内核实验模块，可独立构建和测试。
- `tool/`：一个零依赖的 Go 工具调用示例，演示 LLM Function Calling 的注册、路由与执行循环。

## 核心能力

| 能力 | 说明 |
|---|---|
| 研究任务规划 | 将论文复现、代码执行、框架对比、数据评测等目标拆解为带依赖关系的 DAG |
| 专业 Agent 路由 | 按任务类型路由到 Chat、Librarian、Benchmark、Coder、Research Coding 和 Data |
| 真实隔离执行 | 通过独立 Go 沙箱服务调用原生 Docker，支持持久工作区、流式日志和产物回传 |
| 仓库优先的论文复现 | 发现或使用指定 Git 仓库，准备依赖并运行受控 smoke 实验 |
| 预算受限的 AutoResearch | 在冻结的指标、数据和预算内搜索代码补丁或方法/超参数候选，真实执行后 Keep/Reject |
| 可信 Benchmark | 数据审计、可复现 split、泄漏检查、Metric/Reward 契约与隐藏标签重算 |
| 分层候选搜索 | 受限穷举、路线 UCB、Top-K Beam、UCT-style 参数树和隐藏 Holdout 复验 |
| 逐主张证据 | 冻结分层 Rubric，把论文主张、判定准则和真实 Artifact 绑定为证据图 |
| 实时可观测 | SSE 推送计划、节点、日志和 Artifact 事件，前端同步展示执行状态 |
| 持久化与治理 | 计划与事件持久化、任务租约、取消/重试、预算限制和人工审批 |

## 系统架构

```text
Researcher / Paper / Repository / Dataset
                    |
                    v
             React Workbench
                    |
            REST / Upload / SSE
                    |
                    v
    API -> Intent Router -> Planner + DAG Validator
                               |
                 Validated PlanGraph + Plan Store
                               |
          Scheduler (lease / retry / cancel / budget)
                               |
                    Routed Task Executor
          +--------+----------+----------+-----------------+------+
          |        |          |          |                 |      |
         Chat  Librarian  Benchmark   Coder       Research Coding  Data
                     |       /    \       |          /          \     |
              Claim Rubric Split Metric Runtime  Paper Debug  AutoResearch
                               \ Contract         /       \       |
                                Hidden Eval   Repository Adapter  |
                                       \        |     +----+----------------+
                                        Go Policy Gate  Code Patch   Config Search
                                                                Domain Adapter
                                                                     |
                                                   Python Research Optimizer
                                       Context / Route UCB / Beam + UCT / Experience
                                    |
               Sandbox Client -> docker-sandbox -> Native Docker
                                    |
            stdout / metrics / files / TrialLedger / evidence graph
                                    |
                          Artifact + Event -> SSE
```

## 仓库结构

```text
Research Assistant Agent/
├── README.md
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

## 快速开始

### 前置条件

- Docker Engine 20.10+ 与 Docker Compose v2
- Git
- 一个 OpenAI-compatible LLM API Key
- 本地开发需要 Go 1.26.1（backend/docker-sandbox）与 Node.js 22+
- 后端依赖 Linux 进程组与 `Statfs`，Windows 原生后端不受支持；Windows 请使用 Docker Compose

### 使用 Docker Compose

```bash
cd research-assistant
cp .env.example .env
# 编辑 .env，设置随机 SANDBOX_API_TOKEN 与 RESEARCH_OPTIMIZER_API_TOKEN
cp backend.env.example backend.env
# 编辑 backend.env，至少填写 OPENAI_API_KEY，并保持 SANDBOX_API_TOKEN 与上面的 .env 一致

docker compose up --build -d
```

启动后访问：

| 服务 | 地址 |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health Check | http://localhost:8080/api/health |
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

### 本地开发

```bash
cd research-assistant
make install
make run-sandbox     # 终端 1
make run-backend     # 终端 2
make run-frontend    # 终端 3
```

Windows 用户请使用 Docker Compose；`scripts/windows/start-backend.ps1` 仍会尝试原生 `go run`，但后端并不支持 Windows 原生编译。详见 [scripts/README.md](research-assistant/scripts/README.md)。

## 配置

复制 `research-assistant/backend.env.example` 为 `backend.env`。常用变量：

| 变量 | 必填 | 说明 |
|---|---:|---|
| `OPENAI_API_KEY` | 是 | OpenAI-compatible API 密钥 |
| `OPENAI_BASE_URL` | 否 | 默认示例为 DashScope compatible endpoint |
| `OPENAI_MODEL_NAME` | 否 | 默认示例为 `qwen3-coder-plus` |
| `SANDBOX_URL` | 否 | 本地默认 `http://localhost:8082`；Compose 会覆盖为服务地址 |
| `SANDBOX_DEFAULT_IMAGE` | 否 | 默认 Python 运行时镜像 |
| `PLAN_STORE_PATH` | 否 | 计划与事件 JSON 存储路径 |
| `RESEARCH_OPTIMIZER_URL` | 否 | Python Research Optimizer 地址；不配置时使用确定性 Go fallback |
| `RESEARCH_OPTIMIZER_API_TOKEN` | 否 | Backend 与 Optimizer 之间的 Bearer Token |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | 否 | 计划尝试次数与时长预算 |
| `REQUIRE_PLAN_APPROVAL` | 否 | 执行前强制人工审批 |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | 否 | API 静态 Bearer 保护；`REQUIRE_API_AUTH=true` 且 token 为空时 fail-closed |
| `VITE_API_AUTH_TOKEN` | 否 | 前端构建时注入的 Bearer Token；必须与 `API_AUTH_TOKEN` 一致，仅用于可信静态部署 |
| `SANDBOX_API_TOKEN` | 是 | Compose 必须通过 `.env` 设置；不再使用可预测默认值 |
| `CORS_ALLOWED_ORIGINS` | 否 | 允许访问后端的前端 Origin 列表 |

后端镜像构建还支持 `DEBIAN_MIRROR` 与 `DEBIAN_SECURITY_MIRROR`，由执行 `docker compose` 的 shell 或项目 `.env` 读取。GPU 透传需要宿主机安装 NVIDIA Container Toolkit，并设置 `SANDBOX_DOCKER_GPUS=all`。

## API 摘要

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/health` | 检查后端、仓库、沙箱与 Optimizer 状态 |
| `POST` | `/api/plan` | 根据用户意图创建并保存 DAG |
| `GET` | `/api/plans/:id` | 查询计划、节点状态和产物 |
| `POST` | `/api/plans/:id/execute` | 启动整张计划图 |
| `POST` | `/api/plans/:id/approve` | 批准需要人工确认的计划 |
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
| `POST` | `/api/chat` | 通用对话接口 |
| `GET` | `/api/pdf-proxy?url=...` | 代理读取远端 PDF |

完整路由和请求结构见 [backend/README.md](research-assistant/backend/README.md)。

## 典型任务

### 论文复现

在 Web UI 输入：

```text
请使用 https://github.com/harvardnlp/annotated-transformer 复现
Attention Is All You Need，使用 smoke 模式运行轻量注意力消融，
不要执行 WMT14 完整训练。
```

系统会生成并执行“解析论文 -> 冻结主张 Rubric -> 检索仓库 -> 准备工作区 -> 解析依赖 -> 准备运行时 -> 安装依赖 -> 执行实验 -> 对比论文声明 -> 主张证据图”的主链。

### 自有数据评测

上传 CSV、TSV、JSON 或 JSONL 后输入：

```text
用 https://github.com/OWNER/REPOSITORY 跑 benchmark，
输入列是 review，标签列是 label，最多运行 500 条样本。
```

Benchmark Agent 会审计列映射、生成泄漏安全的 train/validation/test 划分，冻结 Metric/Reward 契约，并在模型不可见的隐藏标签上重算最终指标。

### 预算受限 AutoResearch

```text
用 https://github.com/OWNER/REPOSITORY 做 AutoResearch，
按 autoresearch.json 运行，最多 3 轮，总时长不超过 15 分钟，最终验证 3 次。
```

系统冻结 ResearchSpec 与仓库 revision，在受限文件范围内生成候选，按真实指标执行 Keep/Reject、退化回滚和隐藏 Holdout 复验。

## 运行边界

- 本项目是研究原型，接口和数据结构可能调整，不承诺向后兼容。
- 单机持久化基于 JSON 文件；多副本部署需要共享事务数据库与 leader election。
- 已支持静态 API token 和计划所有权检查，但不是 OIDC、RBAC 或生产级多租户认证。
- 沙箱具备 CPU、内存、PID、capability、镜像和挂载限制，但 Docker socket 仍等同于较高宿主机权限。
- 默认运行时镜像是 CPU/通用 Python 镜像；GPU 透传需要额外配置。
- 当前复现重点是轻量 smoke 与结构消融，不包含大规模数据集训练。
- 候选搜索是真实 Validation Reward 驱动的分层 UCB、Top-K Beam 与 UCT-style 搜索，不是完整 MCTS 或通用强化学习策略。

## 模块说明

- [research-assistant](research-assistant/README.md)：产品主实现与启动方式
- [backend](research-assistant/backend/README.md)：Go API、规划、调度与 Agent
- [frontend](research-assistant/frontend/README.md)：React 工作台
- [docker-sandbox](research-assistant/docker-sandbox/README.md)：独立沙箱服务
- [research-optimizer](research-assistant/research-optimizer/README.md)：Python 候选策略服务
- [ai-services](research-assistant/ai-services/README.md)：可选 AI 服务与意图识别
- [examples](research-assistant/examples/README.md)：可运行示例
- [docker-core](docker-core/README.md)：底层沙箱内核实验模块
- [tool](tool/README.md)：Go 工具调用框架示例
