# research-assistant

`research-assistant` 是 Research Assistant Agent 的产品主实现。它把 Go 后端、React 工作台、Docker 沙箱服务、Python 研究优化器和可选 AI 服务组合成一套可运行的多智能体科研执行系统。

## 服务组成

| 服务 | 目录 | 端口 | 职责 |
|---|---|---:|---|
| Backend | `backend/` | 8080 | Gin API、意图识别、Planner、Scheduler、Agent 与 SSE |
| Frontend | `frontend/` | 5173（开发）/ 80（容器） | React 工作台、DAG 可视化、PDF 与执行结果展示 |
| Docker Sandbox | `docker-sandbox/` | 8082 | 容器创建、命令执行、文件与运行时生命周期管理 |
| Research Optimizer | `research-optimizer/` | 8090 | 数据特征、候选优先级与 SQLite 跨任务经验 |
| AI Services | `ai-services/` | 8000（可选） | 意图识别服务与模型训练脚本 |

## 快速开始

### Docker Compose

```bash
cp .env.example .env
# 编辑 .env，设置随机 SANDBOX_API_TOKEN / RESEARCH_OPTIMIZER_API_TOKEN
cp backend.env.example backend.env
# 编辑 backend.env，至少填写 OPENAI_API_KEY；SANDBOX_API_TOKEN 必须与 .env 一致
docker compose up --build -d
```

访问：

- Web UI: http://localhost:5173
- Backend API: http://localhost:8080
- Sandbox API: http://localhost:8082
- Research Optimizer: http://localhost:8090

健康检查：

```bash
curl -s http://localhost:8080/api/health
```

### 本地开发

```bash
make install
make run-sandbox
make run-backend
make run-frontend
```

`make help` 会列出全部目标。

## 目录结构

```text
research-assistant/
├── backend/                 Go API、Planner、Scheduler 与 Agent
├── frontend/                React + TypeScript 工作台
├── docker-sandbox/          独立 Go 沙箱服务
├── research-optimizer/      Python 候选策略服务
├── ai-services/             可选 Python AI 服务
├── examples/                可运行示例与验收脚本
├── test/                    Golden test 数据与运行器
├── scripts/                 Unix / Windows 启动脚本
├── backend.env.example      后端环境变量模板
├── .env.example             Compose 内部服务 Token 模板
├── docker-compose.yml       本地编排
└── Makefile                 开发命令入口
```

## 配置

所有后端变量见 [backend.env.example](backend.env.example)。关键项：

| 变量 | 说明 |
|---|---|
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | LLM 接入配置 |
| `SANDBOX_URL` | 沙箱服务地址，Compose 中为 `http://docker-sandbox:8082` |
| `PLAN_STORE_PATH` | append-only JSONL 计划/事件日志；旧 JSON 快照自动迁移 |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与单文件大小限制 |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python 优化器地址与内部 Token |
| `API_AUTH_TOKEN` | API 静态 Bearer Token；SSE 通过 60 秒 HMAC ticket 鉴权 |
| `VITE_API_AUTH_TOKEN` | 由 Compose 使用 `API_AUTH_TOKEN` 自动注入前端构建 |
| `REQUIRE_API_AUTH` | Compose 强制 `true`；Token 为空时 API fail-closed |
| `CORS_ALLOWED_ORIGINS` | 允许的前端 Origin 列表 |
| `SANDBOX_API_TOKEN` | 后端与沙箱之间的 Bearer Token；gateway/executor 均 fail-closed |

## 常用命令

```bash
make install          # 安装前端依赖并整理 Go modules
make lint             # 前端 ESLint
make build            # 构建前端、后端与沙箱
make package          # 构建带嵌入式前端的单文件服务（Linux 目标）
make test             # 后端、沙箱、前端与离线示例测试
make test-optimizer   # Python Research Optimizer 测试
make eval             # Agent 规划评测数据集
make docker-up        # Docker Compose 启动
make docker-down      # Docker Compose 停止
```

## 架构要点

1. 用户在工作台提交目标，前端调用 `POST /api/plan`。
2. Backend 的 Intent Router 与 Planner 生成经过校验的 DAG，并写入 Plan Store。
3. Scheduler 按依赖关系调度节点，通过类型化 Artifact 路由给专业 Agent。
4. Agent 通过 `internal/sandbox` 客户端调用 `docker-sandbox` gateway；gateway 只做鉴权与转发，不持有 Docker socket。内部 executor 才持有 socket 和 `/tmp`，在 Docker 中执行代码并回传日志、指标和文件。
5. 事件与 Artifact 通过 SSE 推送到前端；前端展示 DAG、节点日志、代码、报告和证据图。
6. AutoResearch 场景下，Go Harness 负责候选合法性、预算、Keep/Reject、回滚与 Holdout 验收，Python Optimizer 只提供候选排序建议。

## 运行边界

- 默认是单机研究原型，不是多租户生产服务。
- Compose 只在本机暴露 gateway 端口；gateway 不挂载 Docker socket 或宿主工作区，只有不发布端口的内部 executor 持有 socket，权限仍高但边界更小。
- 默认镜像为 CPU/通用 Python 镜像；GPU 透传需要 NVIDIA Container Toolkit。
- 论文复现示例是轻量 smoke，不包含完整数据集训练。

## 子模块说明

- [backend](backend/README.md)
- [frontend](frontend/README.md)
- [docker-sandbox](docker-sandbox/README.md)
- [research-optimizer](research-optimizer/README.md)
- [ai-services](ai-services/README.md)
- [examples](examples/README.md)
- [scripts](scripts/README.md)
- [test/claim-evidence](test/claim-evidence/README.md)
