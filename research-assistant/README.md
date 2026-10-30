# research-assistant

`research-assistant` 是 Research Assistant Agent 的产品主实现：把 Go 后端、React 工作台、Docker 沙箱服务、Python 研究优化器和可选 AI 服务组合成一套可运行的多智能体科研执行系统。

- 后端：[backend](backend/README.md)
- 前端：[frontend](frontend/README.md)
- 沙箱：[docker-sandbox](docker-sandbox/README.md)
- 优化器：[research-optimizer](research-optimizer/README.md)
- 可选 AI 服务：[ai-services](ai-services/README.md)
- 示例：[examples](examples/README.md)
- 脚本：[scripts](scripts/README.md)

## 服务组成

| 服务 | 目录 | 端口 | 职责 |
|---|---|---:|---|
| Backend | `backend/` | 8080 | Gin API、意图识别、Planner、Scheduler、Agent 与 SSE |
| Frontend | `frontend/` | 5173（开发）/ 80（容器） | React 工作台、DAG 可视化、PDF 阅读与执行结果展示 |
| Docker Sandbox | `docker-sandbox/` | 8082 | 容器生命周期、命令/Python 执行、网络与资源治理 |
| Research Optimizer | `research-optimizer/` | 8090 | 数据特征、候选排序与 SQLite 跨任务经验 |
| AI Services | `ai-services/` | 18000 | 可选的真实 LLM 意图识别服务与 BERT/Qwen 训练脚本 |

Compose 把沙箱拆成两个角色：`docker-sandbox` 是只做鉴权与转发的控制面（gateway），`docker-sandbox-executor` 是持有 Docker socket 与 `/tmp` 的执行面（engine）。executor 不发布宿主端口。

## 架构与流程

- [系统架构图](../docs/diagrams/architecture.drawio)（[SVG 预览](../docs/diagrams/architecture.svg)）
- [运行流程图](../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../docs/diagrams/agent-execution-flow.svg)）
- [系统架构图（Archify 交互式）](../docs/diagrams/system-architecture.html)
- [多角色 Agent 执行工作流（Archify 交互式）](../docs/diagrams/multi-agent-workflow.html)

一次任务的主链路：

```text
POST /api/plan
  -> Intent Router / ResearchContext
  -> Planner + DAG Validator
  -> PlanStore
  -> Scheduler（租约 / 重试 / 取消 / 预算 / 审批）
  -> RoutedTaskExecutor
  -> 专业 Agent
  -> docker-sandbox gateway -> executor -> Native Docker
  -> Artifact / EnvironmentManifest / PlanOutcome
  -> Event Bus / SSE
  -> React Workbench
```

| Agent | 主要职责 |
|---|---|
| Chat / General | 通用对话、范围澄清、非论文类请求 |
| Librarian | 论文解析、paper_metadata、DocumentStore 检索、Claim Rubric |
| Benchmark | 数据审计、split、Metric/Reward 契约、隐藏标签重算 |
| Coder | 代码生成、绘图、runtime prepare/install/validate、sandbox 命令 |
| Research Coding | 仓库准备、Benchmark Adapter、论文代码执行/调试 |
| Data | 对比报告、评估报告、Claim-Evidence 构建 |
| AutoResearch | ResearchSpec 冻结、候选执行、Keep/Reject、回滚与 Holdout |
| Experiment Research | `experiment.spec/v1` 配置搜索与领域 Adapter |
| Ablation ToT | 受限 Tree-of-Thoughts 消融设计 |
| Literature Search | 多 provider 文献检索、去重、排序与导入 |
| Visual Verifier | 图像结构门禁与多模态视觉判定（pass/fail/inconclusive） |
| Answer | 面向用户的最终回答合成 |

## 运行时权限与论文复现

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
- 拒绝重要操作会立即终止计划：`status=failed`、`outcome=blocked`，并写入 `missing_requirements=[user_rejected]`；审批超时同理标记 `approval_timeout`。
- 运行时链为 `reproduction_spec -> runtime_probe -> image_prepare -> prepare_runtime -> install_dependencies -> runtime_validate -> paper_code_execute`。
- `runtime_validate` 输出 `EnvironmentManifest`：镜像 digest、Python/pip freeze、GPU、数据 hash、运行命令与工作目录。
- 完整复现资源不足时不会用 smoke 冒充成功；`outcome` 会明确标记 `smoke`、`partial` 或 `blocked`。

## 计划库、复现包与成本控制台

- **计划库**：顶栏"计划库"打开独立页面；支持项目分组、标题/意图搜索、状态/类型/项目过滤、稳定 keyset 分页、重命名、移动、归档/恢复、复制、导出与删除（运行中拒绝）。旧计划自动归入 Inbox。
- **可复现导出包**：`GET /api/plans/:id/export?format=zip` 生成 `manifest.json` + `checksums.sha256` + `environment/{requirements.lock,system-packages.txt,Dockerfile,run.sh}` + `provenance/*`；可用 `sha256sum -c` 校验，缺镜像 digest/run command/artifact 时标记 `partial` 并给出 `missing` 清单；导出前脱敏 API key/token；`POST /api/plans/:id/reproduce/verify` 做静态校验。
- **成本与预算**：`GET /api/usage/summary`、`/api/usage/timeseries`、`/api/plans/:id/usage`、`GET/PUT /api/budgets`、`/api/budgets/alerts`；计划预算支持 token/USD/sandbox/download 上限，soft 超限暂停、hard 取消，80% 预警、100% 拒绝或暂停；模型 profile 的单价进入 usage 计费，未配置时标记 estimated/unknown。

## 计划审阅与人在回路（N-05）

- **不可变 revision**：每次编辑生成新的 revision 快照（r1、r2…），历史版本永不覆盖；`GET/POST /api/plans/:id/revisions`、`GET /api/plans/:id/revisions/:revision`、`POST .../validate|approve|reject|execute`；`base_revision` 乐观锁，过期返回 409 + 当前 revision；运行中的计划拒绝编辑/执行新 revision。
- **审阅门**：`PLAN_REVIEW_MODE=always`、`require_review=true`、`REVIEW_COST_THRESHOLD_USD` 超阈值、高风险节点、缺需求或 L4 复杂度都会让计划进入 `awaiting_review`，不自动执行；审阅页支持节点 inspector 编辑、依赖多选、预算调整、校验、diff、保存草稿、批准并运行、拒绝。
- **结构化提问**：`ask_user_question` 任务类型 + `POST /api/plans/:id/questions` 工具端点；Agent 需要用户决策时暂停为 `awaiting_input`，前端提供选项/多选/自由文本；`POST .../questions/:id/answer` 恢复任务；超时有默认选项则自动回答（`auto_answered`），否则阻塞并记录 `question_timeout`。
- **审批收件箱**：`GET /api/approvals?status=pending&kind=approval|question|all` 跨计划聚合运行时审批与提问；`POST /api/approvals/bulk` 仅允许低风险批量（≤50，高风险逐条）；顶栏铃铛显示待处理数量。
- **审计**：`plan_revision_created`、`plan_revision_executed`、`plan_reviewed`、`question_requested`、`question_answered`、`question_expired`、`approval_bulk_decided` 事件与 audit 均可查询。
- **持久化**：revision 存于 `REVISION_STORE_PATH`（默认 `<PLAN_STORE_PATH>.revisions.json`）。

## 工具生态与 MCP（N-13）

- **工具注册表**：`GET/POST /api/tools`、`GET/PUT/DELETE /api/tools/:id`、`POST /api/tools/:id/test`、`GET /api/tools/:id/schema`；支持 native / HTTP / MCP stdio / MCP HTTP。MCP 注册时执行 `initialize` + `tools/list` 并保存 schema fingerprint。
- **权限边界**：`ToolInvoker` 在执行期强制节点 `AllowedTools`、AgentProfile `ToolAllowlist` 与 `ToolGrant`；越权返回 `tool_forbidden` 且无副作用。未知 Agent/Profile 在计划导入/校验时直接报错，不再静默降级为 general_agent。
- **审批**：`SideEffect=true` 或 `Approval=always/write` 的工具在 dispatch 前创建 `tool_call` RuntimeAction；拒绝不调用，批准后执行并关联 `ApprovalID`。审批门会跳过必然被 allowlist/grant 拒绝的调用。
- **审计/回放**：每次调用写 `PlanGraph.ToolCalls`（参数 hash、结果 hash、耗时、审批 ID、错误码）；`GET /api/plans/:id/tool-calls`、`POST .../tool-calls/:callId/replay` 支持 dry-run hash 对比与需审批的真实重放。
- **AgentProfile/PromptVersion**：`GET/POST /api/agent-profiles`、`GET/POST /api/prompt-versions`、`POST .../activate|eval`；Prompt 不可变、active 指针回滚；`POST /api/plans/from-graph` 支持显式 `tool_call` 节点与 `profile:<id>` 节点。
- **前端**：设置页分为模型/工具/Agent 三个 tab；工具页注册 MCP/HTTP 工具、测试连接、管理授权；Agent 页管理 Prompt 版本、AgentProfile 与评测；计划图显示 tool_call 工具名/版本，执行侧栏显示参数与错误码。

## 目录结构

| 目录 / 文件 | 说明 |
|---|---|
| `backend/` | Go API、Planner、Scheduler 与 Agent |
| `frontend/` | React + TypeScript 工作台 |
| `docker-sandbox/` | 独立 Go 沙箱服务（gateway / engine） |
| `research-optimizer/` | Python 候选策略与经验服务 |
| `ai-services/` | 可选 Python AI 服务 |
| `examples/` | 可运行示例与验收脚本 |
| `test/` | E2E、Golden test 与测试 fixtures |
| `scripts/` | Unix / Windows 启动与维护脚本 |
| `docs/` | phase1 / phase2 阶段性说明 |
| `backend.env.example` | 后端环境变量模板 |
| `.env.example` | Compose 内部服务 Token 模板 |
| `docker-compose.yml` | 本地编排 |
| `Makefile` | 开发命令入口 |

## 快速开始

### Docker Compose

```bash
cd research-assistant
cp .env.example .env
# 编辑 .env：设置随机 SANDBOX_API_TOKEN、RESEARCH_OPTIMIZER_API_TOKEN 与 API_AUTH_TOKEN
cp backend.env.example backend.env
# 编辑 backend.env：至少填写 OPENAI_API_KEY；SANDBOX_API_TOKEN 必须与 .env 一致
docker compose up --build -d
```

访问：

| 服务 | 地址 |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health Check | http://localhost:8080/api/health（与 `/ping` 无需 Bearer） |
| Sandbox API | http://localhost:8082 |
| Research Optimizer | http://localhost:8090 |

健康检查：

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
make install
make run-sandbox     # 终端 1，本地 engine 模式沙箱（需本机 Docker）
make run-backend     # 终端 2，需要 SANDBOX_URL 与 API_AUTH_TOKEN
make run-frontend    # 终端 3
```

Windows 用户请使用 Docker Compose；后端依赖 Linux 进程组与 `Statfs`，原生 Windows 后端不受支持。Windows 启动脚本会直接提示使用 Compose。详见 [scripts/README.md](scripts/README.md) 与 [../README.md](../README.md)。

## 配置

完整变量见 [backend.env.example](backend.env.example) 与 [docker-compose.yml](docker-compose.yml)。常用项：

| 变量 | 说明 |
|---|---|
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | LLM 接入配置；示例值见 `backend.env.example` |
| `INTENT_SERVICE_URL` / `INTENT_ROUTER_MODE` | 意图服务地址与 `rule` / `llm` / `hybrid` / `shadow` 模式；Compose 默认 `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` / `INTENT_CLIENT_TIMEOUT_SECONDS` | 模型结果置信度阈值与单次调用超时 |
| `SANDBOX_URL` / `SANDBOX_API_TOKEN` | 沙箱地址与 Bearer Token；gateway/executor 均 fail-closed |
| `SANDBOX_DEFAULT_IMAGE` / `SANDBOX_IMAGE_ALLOWLIST` | 运行时默认镜像与允许的镜像前缀 |
| `PLAN_STORE_PATH` | 计划快照目录（`<path>.d`）与 append-only JSONL 索引/事件日志的基准路径 |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与单文件大小限制 |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python 优化器地址与内部 Token |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | API 静态 Bearer Token；Compose 强制 `REQUIRE_API_AUTH=true`，Token 为空时 fail-closed |
| `CORS_ALLOWED_ORIGINS` | 允许的前端 Origin 列表 |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` | 运行期模型 profile 存储目录与加密密钥；未配置密钥时禁止保存 profile |
| `ADMIN_TOKEN` | 可创建/修改全局模型 profile 的运营 Token |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `FEATURE_VISION_VERIFICATION` | 视觉校验预算与开关 |
| `PDF_OCR_MAX_PAGES` / `PDF_OCR_LANG` / `PDF_OCR_TIMEOUT_SECONDS` | 文档 OCR 预算、语言与超时 |
| `LITERATURE_PROVIDER_TIMEOUT_SECONDS` / `LITERATURE_MAX_RETRIES` | 文献 provider 超时与重试 |
| `GENERAL_PLAN_FAST_PATH` | 默认 `true`：General 计划按 direct/research/paper/literature 形状生成；`false` 回退旧 3 节点 |
| `PROMPT_CONTEXT_V2` | 默认 `true`：单一 prompt 组装；`false` 回退旧拼接 |
| `COMPLEXITY_ROUTER_ENABLED` | 默认 `true`：L0-L4 复杂度路由；`false` 回退按 intent_type 二选一 |
| `PLANNER_MAX_NODES_L3` / `PLANNER_MAX_NODES_L4` | agent planner 在 L3/L4 的节点上限（默认 16 / 24） |
| `PROMPT_CONTEXT_WINDOW` / `PROMPT_MAX_OUTPUT_TOKENS` / `PROMPT_SAFETY_TOKENS` | prompt token 预算默认值（128000 / 8192 / 2000）；模型 profile 的 `context_window_tokens` 优先 |
| `PROJECT_STORE_PATH` / `BUDGET_STORE_PATH` | 计划项目与成本台账的 JSON 持久化路径；默认在 `PLAN_STORE_PATH` 旁生成 `.projects.json` / `.budget.json` |
| `REVISION_STORE_PATH` | 不可变计划 revision 的 JSON 持久化路径；默认 `<PLAN_STORE_PATH>.revisions.json` |
| `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` | 审阅门：`always` 强制所有计划先审；成本阈值（USD，0 关闭） |
| `QUESTION_TIMEOUT_SECONDS` | 结构化提问的默认超时（默认 900 秒） |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | 计划级成本/Token/沙箱/下载预算默认值；`PLAN_BUDGET_POLICY=soft|hard`（默认 soft，超限暂停等待人工） |
| `EXPORT_MAX_FILE_MB` / `EXPORT_MAX_TOTAL_MB` | 可复现导出包单文件与总大小上限（默认 16 / 128 MB），超限 artifact 记入 manifest `omitted` |

> `VITE_API_AUTH_TOKEN` 只在本地 Vite 开发态使用；Compose 生产构建不把 Token 编进前端 bundle，浏览器请求经 nginx 反向代理时由 BFF 注入 `Authorization`。

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

## 测试与质量门禁

```bash
make test                 # backend + sandbox + frontend + offline examples
make test-optimizer       # Python optimizer tests
make eval                 # planner route/plan evaluation dataset
bash scripts/container-go.sh "go test -count=1 ./..."  # 固定 Go 镜像内全量测试
```

E2E 脚本位于 [test/e2e](test/e2e)，需要运行中的 Compose 栈；使用 `RA_API_URL` / `RA_API_TOKEN`。既有套件包括 `http-smoke`、`routing-and-prompts`、`literature`、`literature-plan`、`document-intelligence`、`model-profiles`、`verification`、`sandbox-network` 与 `round6-*`；round7 深度测试见同目录。计划库/复现包/成本预算的专项用例为 `plan-library`、`repro-bundle`、`cost-budget`；计划审阅/HITL 的专项用例为 `plan-revisions`、`review-hitl`。所需环境变量与最近结果见 [../docs/verification.md](../docs/verification.md)。

## 运行边界

- 默认是单机研究原型，不是多租户生产服务。
- Compose 只在本机暴露 gateway 端口；gateway 不挂载 Docker socket 或宿主工作区，只有不发布端口的内部 executor 持有 socket，权限仍高但边界更小。
- 已支持静态 API Token、计划所有权与计划级审批，但不是 OIDC、RBAC 或生产级多租户认证。
- 默认镜像为 CPU/通用 Python 镜像；GPU 透传需要 NVIDIA Container Toolkit，并设置 `SANDBOX_DOCKER_GPUS=all`。
- 论文复现示例是轻量 smoke，不包含完整数据集训练；资源不足返回 `blocked/partial`，不会用 mock 冒充成功。
- Framework_Evaluation 仍保留 deterministic offline baseline；它不是真实 LangChain/LlamaIndex 性能 benchmark。

## 相关文档

- [根 README](../README.md)：项目全景与快速开始
- [docs/README.md](../docs/README.md)：架构图、设计与轮次报告索引
- [docs/CHANGELOG.md](../docs/CHANGELOG.md)：按提交整理的变更记录
- [docs/verification.md](../docs/verification.md)：最近验证状态与已知问题
- [reports/README.md](../reports/README.md)：本地验收报告目录说明

## 子模块说明

- [backend](backend/README.md)
- [frontend](frontend/README.md)
- [docker-sandbox](docker-sandbox/README.md)
- [research-optimizer](research-optimizer/README.md)
- [ai-services](ai-services/README.md)
- [examples](examples/README.md)
- [scripts](scripts/README.md)
- [test/claim-evidence](test/claim-evidence/README.md)