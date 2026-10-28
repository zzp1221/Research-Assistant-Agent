# research-assistant

`research-assistant` 是 Research Assistant Agent 的产品主实现。它把 Go 后端、React 工作台、Docker 沙箱服务、Python 研究优化器和可选 AI 服务组合成一套可运行的多智能体科研执行系统。

## 服务组成

| 服务 | 目录 | 端口 | 职责 |
|---|---|---:|---|
| Backend | `backend/` | 8080 | Gin API、意图识别、Planner、Scheduler、Agent 与 SSE |
| Frontend | `frontend/` | 5173（开发）/ 80（容器） | React 工作台、DAG 可视化、PDF 与执行结果展示 |
| Docker Sandbox | `docker-sandbox/` | 8082 | 容器创建、命令执行、文件与运行时生命周期管理 |
| Research Optimizer | `research-optimizer/` | 8090 | 数据特征、候选优先级与 SQLite 跨任务经验 |
| AI Services | `ai-services/` | 18000（可选） | 真实 LLM 意图识别服务 + BERT/Qwen 训练脚本；`intent-recognition` 为 Compose 可选服务 |

## 架构与流程

- [系统架构图](../docs/diagrams/architecture.drawio)（[SVG 预览](../docs/diagrams/architecture.svg)）
- [运行流程图](../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../docs/diagrams/agent-execution-flow.svg)）

一次任务的主链路是：`POST /api/plan` → Intent Router → Planner/DAG 校验 → PlanStore → Scheduler → RoutedTaskExecutor → 专业 Agent → docker-sandbox gateway/executor → Artifact/Event → SSE → 前端。

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
| `GENERAL_PLAN_FAST_PATH` | 默认 `true`：General 计划按 direct/research/paper/literature 形状生成；`false` 回退旧 3 节点 |
| `PROMPT_CONTEXT_V2` | 默认 `true`：单一 prompt 组装（plan_context/task_instruction/task_inputs/volatile）；`false` 回退旧拼接 |
| `COMPLEXITY_ROUTER_ENABLED` | 默认 `true`：L0-L4 复杂度路由；`false` 回退按 intent_type 二选一 |
| `PLANNER_MAX_NODES_L3` / `PLANNER_MAX_NODES_L4` | agent planner 在 L3/L4 的节点上限（默认 16 / 24） |
| `PROMPT_CONTEXT_WINDOW` / `PROMPT_MAX_OUTPUT_TOKENS` / `PROMPT_SAFETY_TOKENS` | prompt token 预算默认值（128000 / 8192 / 2000）；模型 profile 的 `context_window_tokens` 优先 |

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

## 2026-10 第二轮加固（R-01 ~ R-17）

本轮按 `docs/improvement-2026-10-26/02-remaining-issues.md` 完成剩余问题修复，关键变化：

**沙箱与资源边界**
- 沙箱默认 `SANDBOX_NETWORK_MODE=none`，代码执行无公网出口；只有已批准的依赖安装/联网执行节点会临时 `docker network connect bridge`，执行后立即断开。
- `RuntimePolicy` 的镜像/下载/磁盘/时长硬限制进入执行路径：镜像预检、`docker stats` 网络计量、`du` 磁盘计量、按任务的超时与 507 超限错误；实际用量写入 `EnvironmentManifest` 与审计日志。
- `SANDBOX_SECURITY_PROFILE=strict` 提供非 root + read-only rootfs + tmpfs 的高隔离模式；`SANDBOX_RUNTIME=runsc` 可接入 gVisor（需宿主机已安装）。

**API 与调度**
- `POST /api/plan`：intent ≤ 20000 字符、附件 ≤ 8、per-user token bucket（429 + Retry-After）、`Idempotency-Key` 24h 幂等、per-user 活跃计划上限。
- Scheduler 改为事件驱动唤醒（任务完成立即唤醒），并发/每用户配额可配置；无状态变化不写快照；计划停滞超过 `PLAN_STALL_SECONDS` 发布 `plan_stalled`。
- 新增暂停/恢复/从节点重跑：`POST /api/plans/:id/pause|resume`、`POST /api/plans/:id/tasks/:taskId/run`。

**可观测性与成本**
- `GET /api/metrics` 与 `GET /metrics`（Prometheus 文本格式）：HTTP、计划/任务、LLM token/延迟、沙箱、活跃计划与停滞指标。
- 计划与节点记录 LLM prompt/completion tokens、费用（`LLM_PRICE_*_PER_1K_USD`）与沙箱耗时；无 provider usage 时按字符估算并标记 `estimated`。
- 审计日志（`AUDIT_LOG_PATH`）与 `GET /api/audit/export`；OpenAPI 3.1 文档在 `/openapi.json`；CLI 在 `scripts/ra.mjs`。

**检索、文献与数据**
- `documents` 使用真正的 BM25（IDF/k1/b/长度归一化）、页码/章节过滤与两阶段 Librarian 检索；逐页混合 OCR（文本页保留文本层，仅低文本页 OCR；`PDF_OCR_MAX_PAGES` 是 OCR 预算，`PDF_OCR_LANG` 默认 `eng+chi_sim`）；`partial` 状态明确列出跳过/失败页。
- 文档智能：`pdftotext -bbox-layout` blocks/reading order（双栏左→右）、页眉页脚标记、表格行对齐抽取（CSV）、图注绑定与图片导出、per-page 质量报告（OCR 置信度/失败/跳过）；API `/document/quality`、`/document/pages/:page/layout`、`/document/tables/:tableId`、`/document/figures/:figureId`。
- `GET /api/literature/search` 支持 arXiv/OpenAlex/Crossref/GitHub/Semantic Scholar/PubMed/DBLP，支持 relevance/citations/year 排序与 rank/rank_reason；429/503 按 Retry-After 退避。
- 文献库：`GET/POST/DELETE /api/literature/records`、`/records/:id/attach`、`/records/:id/bibtex`（owner 隔离、标签、BibTeX、加入会话）；导入自动落库并按 DOI/arXiv/标题去重；`GET /api/literature/citations/:doi` 返回引用/被引。
- `POST /api/web/fetch` 抽取公开网页正文（SSRF/robots/content-type/大小/超时/缓存/段落链接）。
- planner 新增 `literature_search` 节点：真实检索 → top K 导入 PDF → metadata-only 写 `pdf_unavailable` missing requirement。
- `POST /api/literature/import` 支持 arXiv URL/ID、DOI、PDF URL 导入并自动落盘为论文附件；计划意图中的 arXiv/DOI 会自动导入。
- 上传按 SHA-256 去重、per-user 配额、可选 ClamAV 扫描、`DELETE /api/uploads/:id`、`DELETE /api/sessions/:id`、`GET /api/plans/:id/export?format=md|json|zip`（最小复现包）。
- PDF 解析限制输入大小与超时（`PDF_PARSE_MAX_MB` / `PDF_PARSE_TIMEOUT_SECONDS`），解析失败可见。

**前端**
- 计划审批与运行时审批严格分离；运行时标题按计划上下文生成；自动运行开关（默认关闭）与触发方式展示。
- SSE 断线指数退避重连、刷新 ticket、按 `after_seq` 增量续传；创建计划带幂等键。
- 结构化错误码 + 修复建议；执行日志级别过滤/窗口化/下载；窄屏可打开节点详情。


## 2026-10-27 第四轮：N-04 文献检索与 N-07 文档智能

按 `docs/design/n04-n07-literature-and-document-intelligence.md` 落地，详见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND4.md`。

**N-04 文献与网页**
- provider 扩展：Semantic Scholar、PubMed、DBLP、Unpaywall；搜索支持 `sort=relevance|citations|year`，结果附 `rank/score/rank_reason`；429/503 使用 Retry-After + 指数退避。
- 文献库：owner 隔离 JSON 存储、标签、BibTeX、加入会话；导入/元数据导入自动 Upsert，重复 DOI/arXiv/标题合并。
- 引用图谱：OpenAlex references/cited_by。
- 网页正文：readability 评分、SSRF、robots、content-type、2MB/15s 限制与 URL 缓存。
- 计划：`literature_search` 真实检索节点（Librarian），top K 导入 PDF，失败写 `pdf_unavailable`。

**N-07 文档智能**
- 逐页 OCR：文本页/扫描页/混合页；`PDF_OCR_MAX_PAGES` 为预算，保留 `total_pages`，预算不足 `partial`。
- 版面：bbox blocks、两栏 reading order、页眉/页脚/标题；OCR 置信度来自 Tesseract TSV。
- 结构：表格抽取 + CSV、图注 + 图片导出、BM25 table/figure chunks、LocateQuote block 偏移。
- 质量：per-page source/chars/OCR 置信度/失败/跳过 + `OCRStats`；前端摘要 badge、低置信度提示、表格/图注面板。
- 缓存：`sha256(pdf)+ocr_config` → document.json。

## 2026-10-27 第五轮：Agent 链路优化与意图复杂度路由

按 `docs/design/2026-10-27-agent-chain-optimization.md` 与 `docs/design/intent-complexity-routing.md` 落地，详见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND5.md`。

**General 计划形状（A）**
- `classifyGeneralPlanShape` 确定性选择 direct（1 节点）/ research（2 节点）/ paper（4 节点）/ literature（3 节点）；`GENERAL_PLAN_FAST_PATH=false` 回退旧 3 节点。
- `plan.Meta.GeneralPlanShape` 与 `plan_general_shape_total` / `plan_llm_nodes_total` 指标。

**单一 Prompt 组装（B）**
- `prompts.BuildTaskPrompt`：plan_context → task_instruction → task_inputs → volatile，同一 artifact 只出现一次；executor 不再追加 Global user intent / 500 字 artifact 摘要。
- `PROMPT_CONTEXT_V2=false` 可回退。

**Repair 会话（C）**
- `internal/agent/conversation.Session`：保留 system + first user，assistant 上一轮输出作为对话历史，只追加反馈；Planner / PaperDebug / BenchmarkAdapter / AutoResearch / Coder self-correction 已接入；`task.Metadata["repair_session"]` 记录轮数与 prefix hash。

**Token / Context 预算（D）**
- `prompts.PromptBudget` / `Section` / `PromptReport`：section 级预算、rune-safe 截断、超预算 fail-closed；AutoResearch / PaperDebug / Claim / Benchmark / Visual 均有 token 上限。
- 模型 profile 新增 `context_window_tokens`（默认 128000）；`TaskUsage.prompt_budget_limit` / `prompt_truncated_sections` 与 `llm_prompt_truncated_sections_total` 指标可见。

**复杂度路由（L0-L4）**
- Python intent 服务一次返回 `complexity/capabilities/route_hint/complexity_confidence/complexity_reason`；Go `ComplexityRouter` 在附件/论文上下文之后二次修正，LLM 不得降级。
- 模板变体：General / Code_Execution / Framework_Evaluation / Paper_Reproduction 支持 simple/standard/full（Paper 为 light/standard/full）。
- L0/L1 不调用 planner LLM；L2 标准模板；L3/L4 skeleton + agent 增强（`PLANNER_MAX_NODES_L3/L4`）；失败回退 standard。
- `plan.Meta` 新增 `planning_route/complexity/complexity_source/template_variant`，SSE `plan_created`、`/api/plans/:id` 与 `/api/metrics` 可见；golden set `backend/evals/route_eval_dataset.json`。

## 2026-10-27 第三轮：运行期模型配置与通用视觉校验

按 `docs/design/runtime-model-config-and-visual-verification.md` 落地，详见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND3.md`。

**运行期模型配置**
- 新增 `internal/modelprofile`（AES-256-GCM 加密、原子文件存储、per-user + 全局 profile、routing、红/蓝视觉探测）与 `internal/modelresolver`（按 owner/role 解析、revision 缓存、保存即热更新）。
- `GET/POST /api/model-profiles`、`PUT/DELETE /api/model-profiles/:id`、`POST /api/model-profiles/:id/test`、`POST /api/model-profiles/:id/probe-vision`、`GET/PUT /api/model-routing`。
- API Key 只写不回显；无 `MODEL_PROFILE_ENCRYPTION_KEY` 时拒绝保存（fail closed）；全局 profile 需要 `ADMIN_TOKEN`；`base_url` 默认要求 https 且拒绝私网（测试可用 `ALLOW_PRIVATE_MODEL_ENDPOINTS`）。
- 计划创建时冻结 `model_snapshot`（profile id + revision）；运行中的计划不受后续编辑影响。
- Eino 链不再在启动时固化模型：`dynamicChatModel` / `runtimePlannerModel` 在每次调用时解析当前 profile，因此修改配置无需重启。

**verify_result 通用视觉校验**
- 结构门禁（`internal/agent/visual_verifier.go`）：PNG/JPEG 解码、尺寸、颜色方差/熵/边缘密度/透明比例；纯色、全透明、1×1、损坏图直接 fail(structural)。
- 视觉路由：主模型 `supports_vision=yes` 优先；否则按 routing 的 vision 列表依次尝试；全部失败/无候选返回 `inconclusive`，绝不伪装 pass。
- 多模态调用走 OpenAI-compatible `image_url` data URI；最长边缩放到 `VISION_MAX_EDGE_PX`；严格 JSON verdict（`pass|fail|inconclusive`）；解析失败一次修复重试；按 `VISION_MAX_IMAGES` 限制张数；结果按图像 sha256 + rubric + profile revision + intent 缓存。
- 汇总：任一图 fail -> fail(visual)；无 fail 但有 inconclusive -> inconclusive；全部 pass -> pass(visual)；无图像 -> skipped。
- 视觉 fail 时计划仍 `completed`，但 `plan.verification / node.verification / artifact.metadata.verification` 与 `verification_failed` 事件、`verification_total` 指标同时标记（advisory）。
- 文本报告 prompt 注入权威 verdict，要求不得把 inconclusive 说成 pass。
- 前端：工作台“模型设置”面板（profile 列表/编辑/测试/探测/删除/routing/成本与隐私提示）；计划头与节点详情展示校验徽标、issues、置信度与判图模型。

## 2026-10-28 第六/七轮：测试与验证

本轮在容器内完成全量回归与新增深度测试，测试脚本统一位于 `test/e2e/`：

- 既有回归：`http-smoke.mjs`、`routing-and-prompts.mjs`、`literature.mjs`、`literature-plan.mjs`、`document-intelligence.mjs`、`model-profiles.mjs`、`verification.mjs`、`sandbox-network.mjs`、`round6-*.mjs`。
- 新增：`round7-api-coverage.mjs`、`round7-real-scenarios.mjs`、`round7-paper-reproduction.mjs`、`round7-autoresearch.mjs`、`round7-task-execute.mjs`、`round7-doc-assist.mjs`、`round7-sandbox-deep.mjs`。
- 2026-10-28 结果：基线构建/单测全绿；既有 E2E 全部通过；deep API 58/59；真实 LLM 场景 18/18；AutoResearch 8/8 节点且 hidden holdout 3/3；论文复现 14/15 节点完成，暴露 claim rubric JSON 解析缺陷；Playwright 10/11（AI 翻译浮层遮挡缩放按钮）。
- 测试环境：`test/docker-compose.e2e.yml` 提供 e2e profile；`model-profiles`/`verification` 需要 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true`，`document-intelligence` 全量 OCR 需要 `PDF_OCR_MAX_PAGES=20`。
- 完整报告（本地生成，不提交）：`reports/eval-2026-10-28/REPORT.md`。

常用测试命令：

```bash
make test                 # backend + sandbox + frontend + offline examples
make test-optimizer       # Python optimizer tests
make eval                 # planner route/plan evaluation dataset
bash scripts/container-go.sh "go test -count=1 ./..."  # 固定 Go 镜像内全量测试
```

**新增配置**
| 变量 | 说明 |
|---|---|
| `MODEL_PROFILE_DIR` | profile 存储目录；默认 `<PLAN_STORE_PATH>.model-profiles` |
| `MODEL_PROFILE_ENCRYPTION_KEY` | 32 字节 base64 密钥；未配置时禁止保存 profile |
| `ADMIN_TOKEN` | 可创建/修改全局 profile 的运营 token |
| `MODEL_PROFILE_MAX_PER_USER` / `MODEL_PROBE_RATE_LIMIT_SECONDS` | 每用户 profile 上限 / probe/test 频率限制 |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `VISION_MIN_EDGE_PX` | 视觉预处理与预算 |
| `FEATURE_VISION_VERIFICATION` | `false` 时退回纯文本 verify（保留字段） |
| `ALLOW_PRIVATE_MODEL_ENDPOINTS` | 仅本地/容器测试：允许 mock provider 的私网地址 |

## 运行边界

- 默认是单机研究原型，不是多租户生产服务。
- Compose 只在本机暴露 gateway 端口；gateway 不挂载 Docker socket 或宿主工作区，只有不发布端口的内部 executor 持有 socket，权限仍高但边界更小。
- 默认镜像为 CPU/通用 Python 镜像；GPU 透传需要 NVIDIA Container Toolkit。
- 论文复现示例是轻量 smoke，不包含完整数据集训练。

## 相关文档

- [根 README](../README.md)：项目全景与快速开始
- [docs/README.md](../docs/README.md)：架构图、流程图与文档索引
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