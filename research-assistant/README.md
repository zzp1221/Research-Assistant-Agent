# research-assistant

简体中文 | [English](README.en.md)

`research-assistant` 是 Research Assistant Agent 的产品主实现：把 Go 后端、React 工作台、独立沙箱服务、Python 研究优化器和可选 AI 服务组合成一套可运行的多智能体科研执行系统。

- 后端：[backend](backend/README.md)
- 前端：[frontend](frontend/README.md)
- 沙箱：[docker-sandbox](docker-sandbox/README.md)
- 优化器：[research-optimizer](research-optimizer/README.md)
- 可选 AI 服务：[ai-services](ai-services/README.md)
- 示例：[examples](examples/README.md)
- 脚本：[scripts](scripts/README.md)
- 测试样例：[test/claim-evidence](test/claim-evidence/README.md)

## 1. 服务组成

| 服务 | 目录 | 端口 | 职责 |
|---|---|---:|---|
| Backend | `backend/` | 8080 | Gin API、意图识别、Planner、Scheduler、Agent、Memory、Tools、SSE |
| Frontend | `frontend/` | 5173（开发）/ 80（容器） | React 工作台、DAG 可视化、PDF 阅读、计划库、审阅、成本、记忆与设置 |
| Docker Sandbox | `docker-sandbox/` | 8082 | 容器/进程/VM/远程沙箱生命周期、命令与 Python 执行、网络与资源治理 |
| Research Optimizer | `research-optimizer/` | 8090 | 数据特征、候选排序与 SQLite 跨任务经验 |
| AI Services | `ai-services/` | 18000 | 可选的真实 LLM 意图识别服务与 BERT/Qwen 训练脚本 |

Compose 把沙箱拆成两个角色：`docker-sandbox` 是只做鉴权与转发的控制面（gateway），`docker-sandbox-executor` 是持有 Docker socket 与 `/tmp` 的执行面（engine）。executor 不发布宿主端口。

## 2. 架构与主链路

- [系统架构图](../docs/diagrams/architecture.drawio)（[SVG 预览](../docs/diagrams/architecture.svg)）
- [运行流程图](../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../docs/diagrams/agent-execution-flow.svg)）
- [系统架构图（Archify 交互式）](../docs/diagrams/system-architecture.html)
- [多角色 Agent 执行工作流（Archify 交互式）](../docs/diagrams/multi-agent-workflow.html)

一次任务的主链路：

```text
POST /api/plan
  -> Intent Router / Complexity Router / ResearchContext
  -> Planner + DAG Validator
  -> Revision Store + Review Gate（可选）
  -> PlanStore
  -> Scheduler（租约 / 重试 / 取消 / 预算 / 运行时审批）
  -> RoutedTaskExecutor
  -> AgentProfile + Memory 注入
  -> 专业 Agent
  -> ToolInvoker（native / HTTP / MCP）或 Sandbox Client
  -> docker-sandbox gateway -> executor -> Provider（docker / native / vm / remote）
  -> Artifact / EnvironmentManifest / PlanOutcome / Claim-Evidence
  -> Memory 写入 + Event Bus / SSE
  -> React Workbench
```

## 3. 能力模块

### 3.1 规划与上下文

- **意图识别**：规则快路径 + 可选 LLM 意图服务 + 后校验；支持 `rule` / `llm` / `hybrid` / `shadow`。
- **复杂度路由**：L0–L4；L0/L1 不调用 planner LLM，L2 使用标准模板，L3/L4 使用 skeleton + agent planner + 一次结构化 repair。
- **统一 PlanBuilder**：模板注册表、变体（simple/standard/full）、General 的 direct/research/paper/literature 形状、节点契约与 Artifact producer 校验。
- **ResearchContext + InputBindings**：附件、论文元数据、文档引用、文献库、约束与记忆统一绑定，缺失必需输入时任务直接失败。
- **计划库**：项目分组、搜索、过滤、keyset 分页、重命名、归档/恢复、复制、导出与删除。
- **不可变 revision**：每次编辑生成 r1/r2… 快照；`base_revision` 乐观锁；运行中的计划拒绝编辑/执行新 revision。

### 3.2 执行与结果

- **Scheduler**：租约、并发槽位、重试、取消、预算、停滞检测与运行时审批；等待审批不是终态。
- **专业 Agent**：Chat/General、Librarian、Benchmark、Coder、Research Coding、Data、AutoResearch、Experiment Research、Ablation ToT、Paper Debug、Visual Verifier、Answer。
- **论文复现运行时**：RuntimeSpec、镜像/依赖探测、真实安装、`EnvironmentManifest`；资源不足返回 `blocked/partial`，不用 smoke 冒充成功。
- **AutoResearch**：冻结 ResearchSpec、baseline、候选补丁/参数、真实 evaluator、Keep/Reject、回滚、隐藏 Holdout。
- **Benchmark**：数据审计、泄漏安全 split、Metric/Reward 契约、隐藏标签重算。
- **Claim-Evidence**：冻结 Rubric、主张/准则/Artifact 绑定、覆盖率与证据图。
- **可复现导出**：`manifest.json` + `checksums.sha256` + `environment/*` + `provenance/*`；缺证据标记 partial。

### 3.3 记忆系统（M0–M3）

- `internal/memory`：类型化记录、JSONL + 快照、BM25 + 中文 bigram、实体索引、RRF/MMR、core/retrieved 预算、脱敏与 injection 扫描。
- `internal/session` + `chat_store`：服务端会话、聊天轮次、摘要压缩与 citations。
- 计划/任务/文档/AutoResearch 抽取：episode、fact、constraint、negative、procedure、document memory。
- `consolidate`：TTL/归档 + 反思归纳（默认 pending_review）。
- API：`/api/memories` 全套 CRUD/search/stats/export/import/settings/consolidate/feedback；`MEMORY_ENABLED=false` 完全回滚。

### 3.4 工具生态与 MCP（N-13）

- `internal/tools`：ToolDefinition、ToolRegistry、ToolInvoker、native/HTTP/MCP stdio/MCP HTTP。
- 权限：节点 `AllowedTools`、AgentProfile `ToolAllowlist`、`ToolGrant`；越权 `tool_forbidden`。
- 审批：`SideEffect` 或 `Approval=always/write` 生成 `tool_call` RuntimeAction。
- 审计/回放：`ToolCallRecord` 参数/结果 hash、耗时、审批 ID；dry-run 与真实回放。
- `AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`；Prompt 不可变、可评测与回滚。

### 3.5 文献与文档（N-04 / N-07）

- **文献**：arXiv/OpenAlex/Crossref/GitHub/Semantic Scholar/PubMed/DBLP 检索，跨 provider relevance 排序，文献库、标签、BibTeX、引用/被引图、加入会话。
- **文档**：PDF 文本层解析、OCR、版面 blocks/reading order、表格 JSON/CSV、图注/图片、质量报告、BM25 + 中文 bigram 检索、quote 定位、划词翻译/解释与全文翻译。
- **网页**：`/api/web/fetch` 抽取公开网页正文，带 SSRF/robots/content-type/大小/超时/缓存保护。

### 3.6 计划审阅与成本（N-01/N-02/N-03/N-05）

- **审阅门**：`PLAN_REVIEW_MODE=always`、`require_review=true`、成本阈值、高风险节点、缺需求或 L4 会让计划进入 `awaiting_review`。
- **结构化提问**：`ask_user_question` 任务类型 + `/api/plans/:id/questions`；超时有默认选项则自动回答，否则阻塞并记录。
- **审批收件箱**：跨计划聚合运行时审批与提问；`POST /api/approvals/bulk` 仅允许低风险批量（≤50）。
- **成本控制**：token/USD/sandbox/download 预算；soft 超限暂停、hard 取消；80% 预警、100% 拒绝；模型 profile 计价。
- **导出包**：按 provider 分支生成 Dockerfile 或 uv.lock + run command，缺关键信息标记 partial。

## 4. Agent 矩阵

| Agent | 主要职责 |
|---|---|
| Chat / General | 通用对话、范围澄清、非论文类请求 |
| Librarian | 论文解析、paper_metadata、DocumentStore 检索、文献检索、Claim Rubric |
| Benchmark | 数据审计、split、泄漏检查、Metric/Reward 契约、隐藏指标重算 |
| Coder | 代码生成、绘图、runtime prepare/install/validate、依赖解析、受限修复 |
| Research Coding | 仓库准备、Benchmark Adapter、论文代码执行/调试、AutoResearch |
| Data | 指标汇总、论文声明对比、证据图构建、报告与图表、视觉校验编排 |
| AutoResearch | ResearchSpec 冻结、候选执行、Keep/Reject、回滚、Holdout |
| Experiment Research | `experiment.spec/v1` 配置搜索与领域 Adapter |
| Ablation ToT | 受限 Tree-of-Thoughts 消融设计 |
| Paper Debug | 论文仓库调试状态机与受限补丁验证 |
| Visual Verifier | 图像结构门禁与多模态视觉判定（pass/fail/inconclusive） |
| Answer | 最终回答合成与引用整理 |

## 5. 目录结构

```text
research-assistant/
├── backend/                 Go API、Planner、Scheduler、Agent、Memory、Tools
├── frontend/                React + TypeScript 工作台
├── docker-sandbox/          独立 Go 沙箱服务（gateway / engine / providers）
├── research-optimizer/      Python 候选策略与经验服务
├── ai-services/             可选 Python AI 服务（意图识别）
├── examples/                可运行示例与验收脚本
├── test/                    E2E、Golden test 与 fixtures
├── scripts/                 Unix / Windows 启动与维护脚本
├── docs/                    phase1 / phase2 阶段性说明
├── backend.env.example      后端环境变量模板
├── .env.example             Compose 内部服务 Token 模板
├── docker-compose.yml       本地编排
└── Makefile                 开发命令入口
```

## 6. 快速开始

### 6.1 Docker Compose

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

### 6.2 本地开发

```bash
make install
export SANDBOX_API_TOKEN=<random-token>
make run-sandbox     # 终端 1，本地 engine 模式沙箱（需本机 Docker）
make run-backend     # 终端 2，需要 SANDBOX_URL 与 API_AUTH_TOKEN
make run-frontend    # 终端 3
```

Windows 开发者优先使用桌面客户端（`../desktop`）或 Docker Compose。后端已通过 `procgroup` / `sysinfo` 抽象支持 Windows 构建，桌面版以 `backend.exe` 子进程运行；部分 POSIX 依赖功能在 Windows 上受限。

## 7. 配置

完整变量见 [backend.env.example](backend.env.example) 与 [docker-compose.yml](docker-compose.yml)。常用项：

| 变量 | 说明 |
|---|---|
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | LLM 接入配置；示例值见 `backend.env.example` |
| `INTENT_SERVICE_URL` / `INTENT_ROUTER_MODE` | 意图服务地址与 `rule` / `llm` / `hybrid` / `shadow` 模式 |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` / `INTENT_CLIENT_TIMEOUT_SECONDS` | 模型结果置信度阈值与单次调用超时 |
| `SANDBOX_URL` / `SANDBOX_API_TOKEN` | 沙箱地址与 Bearer Token；gateway/executor 均 fail-closed |
| `SANDBOX_PROVIDER` | `auto` / `docker` / `native` / `vm` / `remote` |
| `SANDBOX_DEFAULT_IMAGE` / `SANDBOX_IMAGE_ALLOWLIST` | 运行时默认镜像与允许的镜像前缀 |
| `PLAN_STORE_PATH` | 计划快照目录（`<path>.d`）与 append-only JSONL 索引/事件日志的基准路径 |
| `REVISION_STORE_PATH` | 不可变 revision 存储；默认 `<PLAN_STORE_PATH>.revisions.json` |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与单文件大小限制 |
| `MEMORY_ENABLED` / `MEMORY_MODE` / `MEMORY_DIR` | 记忆开关、模式与目录 |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python 优化器地址与内部 Token |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | API 静态 Bearer Token；Compose 强制 `REQUIRE_API_AUTH=true` |
| `CORS_ALLOWED_ORIGINS` | 允许的前端 Origin 列表 |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` | 运行期模型 profile 存储目录与加密密钥 |
| `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` | 审阅门：`always` 强制所有计划先审；成本阈值（USD，0 关闭） |
| `QUESTION_TIMEOUT_SECONDS` | 结构化提问默认超时（默认 900 秒） |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | 计划级预算；soft / hard（默认 soft） |
| `EXPORT_MAX_FILE_MB` / `EXPORT_MAX_TOTAL_MB` | 可复现导出包单文件与总大小上限（默认 16 / 128 MB） |

> `VITE_API_AUTH_TOKEN` 只在本地 Vite 开发态使用；Compose 生产构建不把 Token 编进前端 bundle，浏览器请求经 nginx 反向代理时由 BFF 注入 `Authorization`。

## 8. 测试与质量门禁

```bash
make test                 # backend + sandbox + frontend + offline examples
make test-optimizer       # Python optimizer tests
make eval                 # planner route/plan evaluation dataset
bash scripts/container-go.sh "go test -count=1 ./..."  # 固定 Go 镜像内全量测试
```

E2E 脚本位于 [test/e2e](test/e2e)，需要运行中的 Compose 栈；使用 `RA_API_URL` / `RA_API_TOKEN`。既有套件包括 `http-smoke`、`routing-and-prompts`、`literature`、`literature-plan`、`document-intelligence`、`model-profiles`、`verification`、`sandbox-network` 与 `round6-*`；round7 深度测试见同目录。计划库/复现包/成本预算的专项用例为 `plan-library`、`repro-bundle`、`cost-budget`、`budget-recovery`；计划审阅/HITL 的专项用例为 `plan-revisions`、`review-hitl`；工具生态为 `tool-ecosystem`。所需环境变量与最近结果见 [../docs/verification.md](../docs/verification.md)。

## 9. 运行边界

- 默认是单机研究原型，不是多租户生产服务。
- Compose 只在本机暴露 gateway 端口；gateway 不挂载 Docker socket 或宿主工作区，只有不发布端口的内部 executor 持有 socket。
- 已支持静态 API Token、计划所有权与计划级审批，但不是 OIDC、RBAC 或生产级多租户认证。
- 默认镜像为 CPU/通用 Python 镜像；GPU 透传需要 NVIDIA Container Toolkit，并设置 `SANDBOX_DOCKER_GPUS=all`。
- 论文复现示例是轻量 smoke，不包含完整数据集训练；资源不足返回 `blocked/partial`，不会用 mock 冒充成功。
- Framework_Evaluation 保留 deterministic offline baseline；它不是真实 LangChain/LlamaIndex 性能 benchmark。
- native Provider 隔离强度低于容器；不要对不可信代码使用 `full_access`。

## 10. 子模块说明

- [backend](backend/README.md)
- [frontend](frontend/README.md)
- [docker-sandbox](docker-sandbox/README.md)
- [research-optimizer](research-optimizer/README.md)
- [ai-services](ai-services/README.md)
- [examples](examples/README.md)
- [scripts](scripts/README.md)
- [test/claim-evidence](test/claim-evidence/README.md)

## 11. 相关文档

- [根 README](../README.md)：项目全景与快速开始
- [docs/README.md](../docs/README.md)：架构图、设计与轮次报告索引
- [docs/CHANGELOG.md](../docs/CHANGELOG.md)：按提交整理的变更记录
- [docs/verification.md](../docs/verification.md)：最近验证状态与已知问题
- [reports/README.md](../reports/README.md)：本地验收报告目录说明
