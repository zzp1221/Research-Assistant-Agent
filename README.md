# Research Assistant Agent

**面向论文复现、数据评测与预算受限自动研究的多智能体科研执行系统**

简体中文 | [English](README.en.md)

> 把自然语言研究目标转换为可验证的执行计划：理解任务 → 检索资料 → 固化评测契约 → 生成 DAG → 隔离执行 → 以 Artifact、指标、环境清单与证据图给出可审计结果。

Research Assistant Agent 由 DeepSeek 构建与维护，当前版本 **0.9.0**。项目同时提供 **Windows 桌面客户端**、**Docker Compose 服务端** 与 **源码开发** 三种使用形态，覆盖从单机科研到团队服务器的不同场景。

---

## 目录

- [0. 项目定位](#0-项目定位)
- [1. 核心能力](#1-核心能力)
- [2. 系统架构](#2-系统架构)
- [3. Agent 矩阵](#3-agent-矩阵)
- [4. 沙箱与运行时](#4-沙箱与运行时)
- [5. 记忆系统](#5-记忆系统)
- [6. 工具生态与 MCP](#6-工具生态与-mcp)
- [7. 桌面客户端](#7-桌面客户端)
- [8. 快速开始](#8-快速开始)
- [9. 配置](#9-配置)
- [10. API 摘要](#10-api-摘要)
- [11. 仓库结构](#11-仓库结构)
- [12. 测试与质量门禁](#12-测试与质量门禁)
- [13. 运行边界与安全](#13-运行边界与安全)
- [14. 文档索引](#14-文档索引)

---

## 0. 项目定位

科研任务和普通问答的差别在于：**答案必须可执行、可复现、可验证**。一个研究目标通常包含论文、代码仓库、数据集、运行环境、评测指标和预算约束，任何一环缺失都会让“看起来完成了”变成不可信的结果。

Research Assistant Agent 把这类任务拆成一条可审计的链路：

1. **理解**：意图识别 + 类型化 `ResearchContext`（附件、论文元数据、文档引用、文献库、约束、记忆）。
2. **规划**：统一 PlanBuilder 生成 DAG，`InputBindings` 绑定上下文/Artifact，DAG Validator 校验依赖、产物 producer 与节点契约。
3. **审阅**：不可变 revision + 计划审阅门 + 结构化提问，让重要计划在执行前可被人工检查、修改、批准或拒绝。
4. **执行**：Scheduler 以租约、重试、取消、预算和运行时审批调度节点；专业 Agent 在隔离沙箱中真实执行代码或实验。
5. **验收**：输出 Artifact、EnvironmentManifest、PlanOutcome 与逐主张证据图，明确区分 `completed`、`partial`、`smoke`、`blocked`、`failed` 与 `needs_review`。

系统不把“节点跑完”当成成功，也不允许 mock 结果冒充真实实验。

### 0.1 三种使用形态

| 形态 | 入口 | 适用场景 | 依赖 |
|---|---|---|---|
| Windows 桌面客户端 | `ResearchAssistant.exe`（NSIS 安装包） | 个人科研、单机运行、零 Docker 起步 | Windows 10/11 + WebView2 |
| Docker Compose 服务端 | `http://localhost:5173` | 团队共享、Linux 服务器、完整容器隔离 | Docker Engine 20.10+ / Compose v2 |
| 源码开发 | `make run-sandbox` / `make run-backend` / `make run-frontend` | 二次开发、调试、扩展 Agent | Go 1.27.3、Node.js 22+、Docker（可选） |

### 0.2 设计原则

- **真实执行**：代码、命令、依赖安装、评测都在沙箱中运行，模型不能自报最终指标。
- **诚实降级**：能力不足时返回 `blocked` 与 `missing_requirements`，不静默降级为 mock 或 smoke。
- **证据优先**：论文主张必须绑定可定位的 Artifact、页码、章节或运行记录。
- **默认收紧**：沙箱默认断网、镜像 allowlist、资源上限、租约与审批；重要操作默认需要确认。
- **可回滚**：计划 revision、记忆开关、Prompt 版本、模型 profile 与工具授权都支持显式回滚。

---

## 1. 核心能力

### 1.1 研究任务与规划

| 能力 | 说明 |
|---|---|
| 多类型研究任务 | 论文复现、代码执行、框架对比、数据评测、预算受限 AutoResearch、通用研究问答 |
| 意图识别 | `rule` / `llm` / `hybrid` / `shadow` 四种模式；规则快路径 + 可选 Python 意图服务 + 后校验 |
| 复杂度路由 | L0–L4：L0/L1 走 fast path，L2 标准模板，L3/L4 使用 skeleton + agent planner，并带一次结构化 repair |
| 统一 DAG 规划 | 模板注册表、simple/standard/full 变体、General 的 direct/research/paper/literature 形状、节点契约与 Artifact 校验 |
| 类型化上下文 | `ResearchContext` + `InputBindings` 统一附件、论文元数据、文档、文献库、约束与记忆，避免模板手工拼 Inputs |
| 计划库 | 项目分组、标题/意图搜索、状态/类型过滤、keyset 分页、重命名、归档/恢复、复制、导出与删除 |
| 不可变 revision | 每次编辑生成 r1/r2… 快照；`base_revision` 乐观锁；运行中的计划拒绝编辑/执行新 revision |

### 1.2 执行与验收

| 能力 | 说明 |
|---|---|
| 专业 Agent 路由 | 按 `assigned_to` 路由到 Chat、Librarian、Benchmark、Coder、Research Coding、Data、AutoResearch、Experiment Research、Ablation ToT、Paper Debug、Visual Verifier、Answer |
| 真实隔离执行 | 通过独立 Go 沙箱服务调用 Docker / Native / VM / Remote Provider；持久工作区、流式日志、产物回传 |
| 运行时审批 | `confirm_important`（默认）在镜像拉取、依赖安装、数据/模型下载、GPU、网络访问前暂停；`full_access` 在边界内自动执行并审计 |
| 论文复现运行时 | RuntimeSpec、镜像/依赖探测、真实安装、`EnvironmentManifest`（镜像 digest、Python/pip freeze、GPU、数据 hash、命令、工作目录） |
| 仓库优先复现 | 发现或使用指定 Git 仓库，固定 revision，生成 RuntimeSpec、安装依赖并执行受控最小/完整实验 |
| 预算受限 AutoResearch | 冻结 ResearchSpec 与指标，在受限文件范围内生成代码补丁或参数候选，真实执行后 Keep/Reject、退化回滚、隐藏 Holdout 复验 |
| 可信 Benchmark | 数据审计、可复现 split、泄漏检查、Metric/Reward 契约、隐藏标签后端重算 |
| 分层候选搜索 | 受限穷举、路线 UCB、Top-K Beam、UCT-style 参数树、Virtual visit 与隐藏 Holdout |
| 逐主张证据 | 冻结分层 Rubric，把论文主张、判定准则与真实 Artifact 绑定为证据图，计算覆盖率 |
| 语义结果状态 | `PlanOutcome` 区分 completed / partial / smoke / mock / blocked / failed / needs_review，并展示缺失项 |
| 视觉校验 | 图像结构门禁 + 多模态视觉判定（pass / fail / inconclusive），视觉 fail 不伪造 completed |
| 可复现导出 | `manifest.json` + `checksums.sha256` + `environment/*` + `provenance/*`；缺关键证据时标记 partial 并列出 missing |

### 1.3 记忆、工具与治理

| 能力 | 说明 |
|---|---|
| Agent 记忆 | 分层记忆（profile/fact/episode/procedure/constraint/negative/document/working/reflection）、BM25 + 实体索引、RRF/MMR、core/retrieved 预算、引用与信任级别、owner 隔离、脱敏与注入扫描 |
| 聊天记忆 | 服务端 Session + ChatSession + 轮次摘要压缩 + citations；重启后仍可恢复 |
| 计划/任务记忆 | 计划 episode、任务失败/重试经验、文档记忆、AutoResearch trial 记忆与 consolidation |
| Paper Wiki | 论文页 PaperPage、Point、带证据 Link；W3 自动摘要、W2.5 PDF references、owner 隔离与可重建 |
| 工具注册表 | native / HTTP / MCP stdio / MCP HTTP；JSON Schema 校验、连接测试、schema fingerprint |
| 工具权限 | `AllowedTools` + AgentProfile `ToolAllowlist` + `ToolGrant`；越权返回 `tool_forbidden` 且无副作用 |
| 工具审批与审计 | `SideEffect` 或审批策略触发 `tool_call` RuntimeAction；`ToolCallRecord` 记录参数 hash、结果 hash、耗时、审批 ID；支持 dry-run 与真实回放 |
| AgentProfile / PromptVersion | Prompt 不可变、active 指针回滚、模板渲染、评测与 profile runner；未知 profile 在计划校验阶段报错 |
| 成本与预算 | token / USD / sandbox / download 上限；soft 超限暂停、hard 取消；80% 预警、100% 拒绝；按模型 profile 计价 |
| 可观测性 | Prometheus 指标、结构化审计事件、SSE 计划/节点/日志/Artifact 事件、诊断包与日志轮转 |

---

## 2. 系统架构

### 2.1 架构图

| 图 | draw.io 源文件 | SVG 预览 | 说明 |
|---|---|---|---|
| 系统架构图 | [architecture.drawio](docs/diagrams/architecture.drawio) | [architecture.svg](docs/diagrams/architecture.svg) | 客户端、桌面外壳、React 工作台、Go 后端、Agent、沙箱 Provider、记忆与持久化、外部依赖的分层关系 |
| 运行流程图 | [runtime-flow.drawio](docs/diagrams/runtime-flow.drawio) | [runtime-flow.svg](docs/diagrams/runtime-flow.svg) | 从研究目标到计划、审阅、调度、沙箱执行、Artifact、记忆与 SSE 的完整时序 |
| Agent 执行流程图 | [agent-execution-flow.drawio](docs/diagrams/agent-execution-flow.drawio) | [agent-execution-flow.svg](docs/diagrams/agent-execution-flow.svg) | RoutedTaskExecutor 的 Agent 路由、记忆注入、工具调用、沙箱执行与证据/搜索子循环 |
| 记忆系统架构图 | [memory-system.drawio](docs/diagrams/memory-system.drawio) | [memory-system.svg](docs/diagrams/memory-system.svg) | 记忆类型、写入管道、检索/注入、W3 摘要、W2/W2.5 Link 构建与 owner-scoped 持久化 |

Archify 交互式图（自包含 HTML，浏览器直接打开，支持明暗主题、缩放与导出）：

- [系统架构图](docs/diagrams/system-architecture.html)
- [多角色 Agent 执行工作流](docs/diagrams/multi-agent-workflow.html)
- [记忆系统架构图](docs/diagrams/memory-system.html)

![系统架构图](docs/diagrams/architecture.svg)

### 2.2 分层说明

```text
Researcher / Paper / Repository / Dataset
                    │
        ┌───────────┴────────────┐
        │                        │
Windows Desktop (MyGo)      Web Browser / API Client
  ├─ MyGo window            └─ nginx BFF (静态资源 + /api 代理)
  ├─ bridge (mygo.Bind)
  ├─ supervisor (backend.exe / sandbox.exe)
  ├─ loopback proxy (SSE / 上传 / 下载)
  ├─ settings + DPAPI
  ├─ updater + diagnostics
  └─ tray / single instance
        │
        └──────────► React Workbench (embedded or served)
                    │ REST / Upload / SSE
                    ▼
        Go Backend (API + Orchestration)
        ├─ API 层：Auth / CORS / Rate limit / OpenAPI
        ├─ 认知层：Intent Router → Complexity Router → Planner + DAG Validator
        ├─ 治理层：Plan Store / Revision / Review gate / Budget / Audit
        ├─ 调度层：Scheduler (lease / retry / cancel / budget / approval)
        ├─ 执行层：RoutedTaskExecutor → 专业 Agent
        ├─ 记忆层：Memory Store + Retrieval/Injection + Paper Wiki
        ├─ 工具层：ToolRegistry / ToolInvoker (native / HTTP / MCP)
        └─ 事件层：Event Bus → SSE
                    │
                    ▼
        Sandbox Client → docker-sandbox gateway → executor
                    ├─ Docker Provider (容器 / 镜像 / GPU / 网络)
                    ├─ Native Provider (uv CPython + venv + AppContainer/lowil)
                    ├─ VM Provider (WSL2 / Docker in VM)
                    └─ Remote Provider (OpenSandbox)
                    │
                    ▼
        Artifact / EnvironmentManifest / PlanOutcome / Claim-Evidence
                    │
                    ▼
        Event Bus / SSE → React Workbench
```

| 层 | 主要组件 | 职责 |
|---|---|---|
| 客户端层 | Windows 桌面外壳、Web 前端、API/CLI | 输入研究目标、上传资料、查看 DAG、审批、阅读结果 |
| 接入层 | Gin API、认证、CORS、限流、OpenAPI | 统一 REST/SSE 契约、身份与配额 |
| 认知层 | Intent Router、Complexity Router、Planner、DAG Validator | 把意图变成合法、可执行的 PlanGraph |
| 治理层 | Plan Store、Revision Store、Review Gate、Budget、Audit | 计划持久化、版本、审阅、成本与审计 |
| 调度层 | Scheduler、Lease、Runtime Gate、Retry/Cancel | 安全、可恢复地推进 DAG |
| 执行层 | RoutedTaskExecutor、专业 Agent、ToolInvoker | 选择 Agent、注入记忆、调用工具与沙箱 |
| 沙箱层 | gateway、executor、docker/native/vm/remote Provider | 真实隔离执行与能力协商 |
| 状态层 | Plan/Revision/Memory/Upload/Document/Literature/Budget/Audit | 持久化与可回放状态 |
| 外部依赖 | LLM Provider、Research Optimizer、文献数据源 | 模型、候选排序、文献检索 |

### 2.3 一次请求的完整路径

1. **输入与上下文**：用户输入目标、上传 PDF/CSV/仓库链接；后端解析 `AttachmentRef` / `DocumentRef` / `PaperMetadata`，写入 `ResearchContext`，并检索相关记忆。
2. **意图与复杂度**：Intent Router 输出意图类型与置信度；Complexity Router 在上下文解析后做 L0–L4 最终路由，选择模板变体或 agent planner。
3. **计划生成**：PlanBuilder 生成节点、依赖、`assigned_to`、`InputBindings` 与 Artifact 契约；DAG Validator 校验无环、依赖存在、producer 唯一、关键节点完整。
4. **revision 与审阅**：计划写入 Plan Store 并生成 revision；满足审阅门（`PLAN_REVIEW_MODE=always`、成本阈值、高风险节点、缺需求、L4）时进入 `awaiting_review`，用户可编辑、校验、diff、批准或拒绝。
5. **执行**：Scheduler 计算入度、派发就绪节点、获取租约；运行时审批门在镜像/依赖/GPU/网络等操作前生成 `RuntimeActionRequest`。
6. **Agent 执行**：RoutedTaskExecutor 解析 AgentProfile（PromptVersion + ModelProfile + ToolAllowlist + MaxSteps），注入 core/retrieved memory，调用 Agent；Agent 通过 ToolInvoker 调用工具或通过 Sandbox Client 执行代码。
7. **沙箱执行**：gateway 鉴权转发；executor 选择 Provider；Provider 按 `Capabilities` 执行并回传 stdout/stderr/用量/产物；能力不足时返回结构化 `blocked`。
8. **验收**：Artifact 校验（非空、类型、producer 绑定）；PlanFinalizer 汇总节点状态、EnvironmentManifest、TrialLedger、Claim-Evidence，产出 `PlanOutcome`；记忆写入计划/任务/trial 经验。
9. **回传**：事件总线持久化并推送到 SSE；前端更新 DAG、执行侧栏、证据图与结果面板；计划可导出为可复现包。

---

## 3. Agent 矩阵

| Agent | 主要职责 | 典型任务类型 |
|---|---|---|
| **Chat / General** | 通用对话、范围澄清、非论文类请求、轻量研究问答 | `general_process`、`general_synthesis` |
| **Librarian** | 论文解析、`paper_metadata`、DocumentStore 检索、文献检索、Claim Rubric | `paper_parse`、`literature_search`、`claim_rubric_extract` |
| **Benchmark** | 数据审计、泄漏安全 split、Metric/Reward 契约、隐藏标签重算 | `dataset_audit`、`split_materialize`、`contract_freeze`、`benchmark_execute` |
| **Coder** | 代码生成、绘图、runtime prepare/install/validate、依赖解析、受限修复 | `generate_code`、`render_plot`、`runtime_prepare`、`install_dependencies` |
| **Research Coding** | 仓库准备、Benchmark Adapter、论文代码执行/调试、AutoResearch | `repo_prepare`、`paper_code_execute`、`autoresearch_run` |
| **Data** | 指标汇总、论文声明对比、证据图构建、报告与图表、视觉校验编排 | `result_analysis`、`paper_compare`、`claim_evidence_build` |
| **AutoResearch** | ResearchSpec 冻结、候选执行、Keep/Reject、回滚、Holdout | `autoresearch_spec`、`autoresearch_search`、`autoresearch_validate` |
| **Experiment Research** | `experiment.spec/v1` 配置搜索与领域 Adapter | `experiment_design`、`experiment_search` |
| **Ablation ToT** | 受限 Tree-of-Thoughts 消融设计 | `ablation_design` |
| **Paper Debug** | 论文仓库调试状态机与受限补丁验证 | `paper_debug` |
| **Visual Verifier** | 图像结构门禁与多模态视觉判定（pass/fail/inconclusive） | `verify_result` |
| **Answer** | 面向用户的最终回答合成与引用整理 | `final_answer` |

Agent 之间不直接共享可变内存；所有跨 Agent 数据通过 Artifact、InputBindings、事件与记忆引用传递。

---

## 4. 沙箱与运行时

### 4.1 Provider 矩阵

沙箱服务把执行能力抽象为统一 `Provider` 接口，由 `SANDBOX_PROVIDER` 选择；`auto` 在 Docker 不可用时优先选择 native。

| Provider | 隔离级别 | 镜像/环境 | 网络控制 | GPU | 适用场景 |
|---|---|---|---|---|---|
| `docker` | 容器（container） | OCI 镜像 + digest | OS 级（默认离线 bridge） | NVIDIA runtime 透传 | 服务端、已有 Docker、需要镜像级复现 |
| `native` | 进程（AppContainer / lowil / Job Object） | uv 管理的 CPython + 每计划 venv | AppContainer 双 profile（离线/在线） | 宿主 GPU 直接使用 | Windows 桌面、零 Docker 起步、纯 Python 任务 |
| `vm` | 虚拟机（WSL2 等） | 自带 Docker Engine / OCI | VM 网络 | VM 内 GPU（视配置） | 无 Docker Desktop 但需要完整 OCI 语义 |
| `remote` | 远程沙箱服务 | 由服务方提供 | 由服务方提供 | 由服务方提供 | GPU、大内存、长时间任务 |

Provider 通过 `GET /api/v1/capabilities` 返回真实能力矩阵（镜像、网络、GPU、Python 版本、探针结果）。任务需要的能力不满足时，系统返回 `blocked` + `missing_requirements=[sandbox_capability]`，并给出可操作建议，而不是静默降级。

### 4.2 运行时链

论文复现主链：

```text
reproduction_spec -> runtime_probe -> image_prepare -> prepare_runtime
-> install_dependencies -> runtime_validate -> paper_code_execute
```

- `runtime_validate` 输出 `EnvironmentManifest`：镜像 digest（或 native runtime hash）、Python/pip freeze、GPU、数据 hash、运行命令与工作目录。
- 完整复现资源不足时不会用 smoke 冒充成功；`outcome` 明确标记 `smoke`、`partial` 或 `blocked`。
- 运行时权限只决定“是否自动执行重要操作”，不会绕过镜像 allowlist、资源上限、沙箱隔离或计划所有权检查。

### 4.3 安全边界

- Compose 中 gateway 不挂载 Docker socket、不挂载宿主工作区；只有不发布端口的内部 executor 持有 socket。
- 容器默认 `network=none`（内部离线网络）；联网需显式审批并临时打开，命令结束立即恢复。
- 默认限制 CPU、内存、PID、capability、镜像 allowlist、挂载路径与 TTL；`strict` 档启用非 root、只读 rootfs 与 tmpfs。
- native Provider 使用 AppContainer / lowil + Job Object 限额；隔离强度低于容器，建议保持 `confirm_important`，不要对不可信代码使用 `full_access`。
- 桌面设置中的 API Key 使用 Windows DPAPI 加密，child process 通过环境变量接收，不落盘、不出现在命令行。

---

## 5. 记忆系统

Agent 记忆系统由两层组成：

- **分层 Memory（M0–M3）**：负责可复用经验的存储、检索与注入；
- **Paper Wiki（W0–W3 + W2.5）**：在 memory 之上提供以论文为单位的科研知识读模型（PaperPage / Point / Link）。

两者共享 owner 隔离、trust/status 生命周期、token 预算与安全边界；关闭开关后完全回退，不影响既有聊天、计划与任务。

### 5.1 记忆类型

| 类型 | 内容 |
|---|---|
| `profile` | 用户偏好、研究领域、输出风格 |
| `fact` | 论文元数据、指标、环境事实 |
| `episode` | 一次计划/任务的经过与结论 |
| `procedure` | 失败后修复成功的具体步骤 |
| `constraint` | provider、镜像、Python、GPU、网络等环境约束 |
| `negative` | 复现失败、候选被拒、不可行路径 |
| `document` | 文档页数、章节、解析状态与引用 |
| `working` | 当前会话的短期工作记忆 |
| `reflection` | 由多条 episode/negative 归纳的反思（默认 pending_review） |

### 5.2 写入管道

| 触发 | 写入内容 |
|---|---|
| 聊天轮次 | `ChatSessionStore` 保存 user/assistant 轮次、摘要与裁剪窗口 |
| 计划创建/终态 | `RecordPlan` 生成 plan episode、paper metadata fact、environment constraint、negative；取消路径同样补写 |
| 任务完成/失败 | `RecordTask` 生成失败 episode 与“失败后修复成功”的 procedure |
| AutoResearch trial | `RecordTrial` 生成 rejected→negative、kept+多轮→procedure |
| 文档解析 | `RecordDocument` 保存页数、章节、解析状态与引用 |
| 用户编辑 | user_note / user_explicit 记忆 |
| Paper Wiki W3 | LLM 生成 TLDR、method / limitation / open_question 摘要，默认 `pending_review` |
| Paper Wiki W2.5 | 解析 PDF References，生成 `cites` Link 候选（默认关闭） |

所有写入先经过 `ValidateRecord`：owner 必填、secret/私钥/大 base64 拒绝、hidden benchmark 拒绝、Refs quote ≤500 字并做 injection 扫描。

### 5.3 检索与注入

- **索引**：中英混合 tokenizer（CJK bigram）、BM25、实体 / kind / status 倒排。
- **召回**：BM25 + 实体 + 时间召回，RRF 融合，trust / importance / recency / confidence / scope 加权，MMR 去冗余。
- **预算**：core memory 稳定前缀 + retrieved memory 动态块；按 token 预算裁剪，不挤占任务输入。
- **顺序**：`core_memory`（priority 1）→ `plan_context` / `task_instruction`（2）→ `retrieved_memory` → `wiki_context`（同为 3，retrieved 在前）→ `task_inputs`（4）→ `volatile`（5）。
- **Wiki 预算**：`wikiBudget = min(max(PromptLimit×2%, 300), 1500)`，并受 `PromptLimit×3%` 硬上限约束；低于 150 token 不注入；与 retrieved 共享预算。
- **引用**：注入记忆带 citation；wiki 注入额外返回 `wiki_citations`（paper/point/page/section/quote）。
- **安全**：owner 硬隔离；记忆内容作为数据而非指令；hidden benchmark、secret、injection 在写入与注入两侧过滤。

### 5.4 Paper Wiki

Paper Wiki 不建第二套事实源：**PaperPage 是可删除、可重建的投影缓存，Point 和 Link 都是 `memory.Record` 的视图**。

| 概念 | 说明 |
|---|---|
| PaperPage | 论文页：标题/作者/年份/venue/DOI/arXiv、TLDR、章节地图、Point/Link id、复现状态、revision |
| Point | 原子记忆点：claim / method / result / limitation / task / procedure / negative / open_question / user_note / constraint |
| Link | 带证据的跨论文关系：cites / extends / contradicts / uses_same_dataset / uses_same_method / same_repo / reproduces / related / supersedes |

- **身份**：DOI > arXiv > 规范化标题+年份；`paper_id` 含 owner 哈希，alias 表按 owner 分区，支持多对一合并。
- **W3 自动摘要**：`WIKI_LLM_MODE=summary|all` 时自动生成 TLDR 与摘要 Point；严格 JSON，解析失败最多重试 2 次；所有 quote 必须通过 `documents.LocateQuote`，失败则丢弃 quote 并保持 `pending_review`。
- **W2.5 PDF references**：`WIKI_PDF_REFS=true` 时解析 PDF References，与已知 PaperPage 按 DOI/arXiv/标题匹配，生成 `cites` Link；默认关闭。
- **可重建**：删除 `data/wiki` 后，PaperPage 从 memory 的 paper/document fact 重建，Point/Link 从 memory 恢复，用户 confirm/reject 决策保留；TLDR 持久化为 `wiki_tldr` memory 记录，重建不重新调用 LLM。
- **治理**：LLM-only Point/Link 默认 `pending_review`；Link 支持 confirm/reject、revision、supersede；Point 支持 pin、编辑、软删除与 point_key supersede。

### 5.5 开关与回滚

| 变量 | 默认 | 说明 |
|---|---|---|
| `MEMORY_ENABLED` | `true` | `false` 时完全不初始化、不注入、不写入 |
| `MEMORY_MODE` | `on` | `shadow` 只检索不注入，便于 A/B |
| `MEMORY_DIR` | `<PLAN_STORE_PATH 目录>/memory` | 独立记忆目录，便于隔离实验 |
| `MEMORY_CONSOLIDATE_INTERVAL_SECONDS` | 空（关闭） | 后台 consolidation 周期；`0` 关闭 |
| `WIKI_ENABLED` | `true` | `false` 时不注册 wiki 路由、不写、不注入 |
| `WIKI_MODE` | `on` | `shadow` 只构建不注入 |
| `WIKI_DIR` | `<PLAN_STORE_PATH 目录>/wiki` | Paper Wiki 投影目录 |
| `WIKI_LLM_MODE` | `summary` | `off|summary|links|all`：TLDR/摘要与 Link 裁决开关 |
| `WIKI_LINK_MODE` | `explicit` | `explicit|hybrid|llm`（W4 语义消融预留） |
| `WIKI_PDF_REFS` | `false` | 是否解析 PDF References 生成 cites |
| `WIKI_BUDGET_TOKENS` | 空 | 覆盖 wiki 预算（仍受 3% 硬上限） |

前端「设置 → 记忆」提供列表、搜索、新建、pin、删除、设置与导出/导入；「设置 → 论文」提供 PaperPage 列表、Point 分组、Link 确认/拒绝与重建；计划详情展示本次使用的记忆引用。

### 5.6 记忆系统架构图

| 图 | draw.io 源文件 | SVG 预览 | 说明 |
|---|---|---|---|
| 记忆系统架构图 | [memory-system.drawio](docs/diagrams/memory-system.drawio) | [memory-system.svg](docs/diagrams/memory-system.svg) | 记忆类型、写入管道、检索/注入、W3 摘要、W2/W2.5 Link 构建与 owner-scoped 持久化 |
| 交互式记忆架构 | — | [memory-system.html](docs/diagrams/memory-system.html) | Archify 生成的交互式 HTML（明暗主题、缩放、导出），节点带源码证据 |

![记忆系统架构图](docs/diagrams/memory-system.svg)

---

## 6. 工具生态与 MCP

N-13 把“工具”从 Prompt 里的自由文本变成有 schema、有权限、有审计的受控能力。

- **注册表**：`GET/POST /api/tools`、`GET/PUT/DELETE /api/tools/:id`、`POST /api/tools/:id/test`、`GET /api/tools/:id/schema`；支持 native / HTTP / MCP stdio / MCP HTTP。
- **MCP 接入**：注册时执行 `initialize` + `tools/list`，保存 schema fingerprint；MCP HTTP 默认拒绝私网，需显式 allowlist。
- **权限边界**：执行期强制节点 `AllowedTools`、AgentProfile `ToolAllowlist` 与 `ToolGrant`；越权返回 `tool_forbidden` 且无副作用。
- **审批**：`SideEffect=true` 或 `Approval=always/write` 的工具在 dispatch 前创建 `tool_call` RuntimeAction；拒绝不调用，批准后执行并关联 `ApprovalID`。
- **审计/回放**：每次调用写入 `PlanGraph.ToolCalls`（参数 hash、结果 hash、耗时、审批 ID、错误码）；`GET /api/plans/:id/tool-calls`、`POST .../tool-calls/:callId/replay` 支持 dry-run hash 对比与需审批的真实重放。
- **AgentProfile/PromptVersion**：`AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`；Prompt 不可变、active 指针回滚；设置页提供模型/工具/Agent/运行模式/记忆/论文六个 tab。

---

## 7. 桌面客户端

Windows 桌面客户端是 0.9.0 的首发交付形态，位于 `desktop/`（独立 Go module）。

### 7.1 组成

| 包 | 职责 |
|---|---|
| `desktop/paths` | 每用户安装/数据/日志目录 |
| `desktop/settings` | `settings.json`、环境变量注入、DPAPI 密钥存储 |
| `desktop/supervisor` | 单实例锁、OS 分配端口、子进程启动与健康检查、Windows Job Object 回收 |
| `desktop/proxy` | 固定 loopback origin 的反向代理，SSE `FlushInterval=-1`，Host/Origin 守卫与 SPA 回退 |
| `desktop/shell` | settings → 端口 → token → supervisor → proxy 编排、崩溃重启、`/desktop/state` / `diagnostics` / `retry` 与错误页 |
| `desktop/diagnostics` | 脱敏诊断包（日志尾部、设置、健康、系统信息、失败计划） |
| `desktop/updater` | `latest.json` 检查、semver 比较、SHA-256 + Ed25519 校验、分阶段下载 |
| `desktop/onboarding` | 首次启动状态、凭据校验与最小完成测试 |
| `desktop/bridge` | `mygo.Bind` 暴露的桌面 API；Wails 回退仍暴露 `window.go.main.API` |
| `desktop/mygo` | MyGo 适配层（托盘、对话框、通知、Shell），业务只依赖这一层 |
| `desktop/tray` | 托盘/通知契约（MyGo 驱动、systray 回退、测试用 memory 驱动） |

### 7.2 运行目录

```text
%LOCALAPPDATA%\Programs\ResearchAssistant\   ResearchAssistant.exe backend.exe sandbox.exe uv.exe
%LOCALAPPDATA%\ResearchAssistant\            settings.json secrets.dat lock data\ runtimes\ workspaces\ logs\
```

### 7.3 构建

```powershell
# 一键构建（前端 + backend.exe + sandbox.exe + MyGo exe + NSIS 安装包）
powershell -ExecutionPolicy Bypass -File desktop/build/build.ps1 -Version 0.9.0

# 仅纯逻辑测试
cd desktop; go test ./...

# Windows 交叉编译（默认 MyGo 入口）
GOOS=windows GOARCH=amd64 go build ./...
```

默认入口为 `desktop/main_mygo.go`（build tag `!wails && !headless`）；Wails 回退为 `-tags "desktop wails"`；headless stub 为 `-tags headless`。构建产物默认写入 `desktop/dist/ResearchAssistant-<version>-Setup.exe`。

### 7.4 更新与诊断

- `RA_UPDATE_ENDPOINT` + `RA_UPDATE_PUBLIC_KEY` 启用签名更新；安装包与更新清单使用 SHA-256 + Ed25519。
- 崩溃转储通过 WER LocalDumps 注册到 `%LOCALAPPDATA%\ResearchAssistant\crashes`。
- 「导出诊断」生成脱敏 zip，包含日志、设置（密钥已脱敏）、健康检查与失败计划摘要。

---

## 8. 快速开始

### 8.1 前置条件

- **桌面客户端**：Windows 10/11 + WebView2 Runtime（Win11 自带）。
- **Docker Compose**：Docker Engine 20.10+、Docker Compose v2、Git。
- **源码开发**：Go 1.27.3、Node.js 22+、Python 3.12（可选 AI 服务/优化器）、Docker（沙箱）。
- 一个 OpenAI-compatible LLM API Key（默认指向 DeepSeek）。

### 8.2 方式 A：Windows 桌面客户端

1. 运行 `desktop/dist/ResearchAssistant-0.9.0-Setup.exe`，按向导选择安装位置与可选 PDF 关联。
2. 首次启动完成 Onboarding：填写 Base URL、模型名与 API Key（保存到 DPAPI）。
3. 在「设置 → 运行模式」选择 Sandbox Provider：`auto` 推荐；无 Docker 时自动使用 native。
4. 新建对话，输入研究目标或上传 PDF/CSV，选择运行权限后发送。

> 安装包必须在普通用户目录（如 Desktop/Downloads）运行；某些受 ACL 限制的工作区目录会因 NSIS 临时文件检查失败。

### 8.3 方式 B：Docker Compose

```bash
cd research-assistant
cp .env.example .env
# 编辑 .env：设置随机 SANDBOX_API_TOKEN、RESEARCH_OPTIMIZER_API_TOKEN 与 API_AUTH_TOKEN
cp backend.env.example backend.env
# 编辑 backend.env：至少填写 OPENAI_API_KEY
docker compose up --build -d
```

启动后访问（端口可通过 `.env` 的 `*_HOST_PORT` 覆盖）：

| 服务 | 地址 |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health Check | http://localhost:8080/api/health（与 `/ping` 无需 Bearer） |
| Sandbox API | http://localhost:8082 |
| Research Optimizer | http://localhost:8090 |

```bash
curl -s http://localhost:8080/api/health
# 期望：backend.ok=true、repository.ok=true、sandbox.ok=true、research_optimizer.ok=true
docker compose logs -f
docker compose down
```

### 8.4 方式 C：本地开发

```bash
cd research-assistant
make install
export SANDBOX_API_TOKEN=<random-token>
make run-sandbox     # 终端 1：本地 engine 模式沙箱
make run-backend     # 终端 2：需要 SANDBOX_URL 与 API_AUTH_TOKEN
make run-frontend    # 终端 3：Vite 开发服务器
```

Windows 开发者优先使用桌面客户端或 Docker Compose。后端已通过 `procgroup` / `sysinfo` 抽象支持 Windows 构建，桌面版以 `backend.exe` 子进程运行；但部分依赖 POSIX 的测试/功能（symlink、文件权限、PDF OCR 外部工具）在 Windows 上受限，服务端仍推荐 Linux 容器。

### 8.5 典型任务

#### 论文复现

在 Web UI 或桌面端输入：

```text
请使用 https://github.com/harvardnlp/annotated-transformer 复现
Attention Is All You Need，使用 smoke 模式运行轻量注意力消融，
不要执行 WMT14 完整训练。
```

发送前选择「重要操作需确认」或「完全权限」。系统会生成并执行“解析论文 → 冻结主张 Rubric → 检索仓库 → 准备工作区 → 解析依赖 → 生成 RuntimeSpec → 探测 sandbox 能力 → 准备镜像 → 准备运行时 → 安装依赖 → 校验运行时/写 EnvironmentManifest → 执行实验 → 对比论文声明 → 主张证据图”的主链。

#### 自有数据评测

上传 CSV/TSV/JSON/JSONL 后输入：

```text
用 https://github.com/OWNER/REPOSITORY 跑 benchmark，
输入列是 review，标签列是 label，最多运行 500 条样本。
```

Benchmark Agent 会审计列映射、生成泄漏安全的 train/validation/test 划分、冻结 Metric/Reward 契约，并在模型不可见的隐藏标签上重算最终指标。

#### 预算受限 AutoResearch

```text
用 https://github.com/OWNER/REPOSITORY 做 AutoResearch，
按 autoresearch.json 运行，最多 3 轮，总时长不超过 15 分钟，最终验证 3 次。
```

系统冻结 ResearchSpec 与仓库 revision，在受限文件范围内生成候选，按真实指标执行 Keep/Reject、退化回滚与隐藏 Holdout 复验。

---

## 9. 配置

完整变量见 [backend.env.example](research-assistant/backend.env.example)、[docker-compose.yml](research-assistant/docker-compose.yml) 与 [backend/README.md](research-assistant/backend/README.md)。常用项按用途分组：

### 9.1 LLM 与模型

| 变量 | 必填 | 说明 |
|---|---:|---|
| `OPENAI_API_KEY` | 是 | OpenAI-compatible API 密钥 |
| `OPENAI_BASE_URL` | 否 | API Base URL；未设置时回退 `https://api.deepseek.com/v1` |
| `OPENAI_MODEL_NAME` | 否 | 模型名称；未设置时回退 `deepseek-chat` |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` | 否 | 运行期模型 profile 存储与加密；未配置密钥时禁止保存 profile |
| `ADMIN_TOKEN` | 否 | 可创建/修改全局模型 profile 的运营 Token |
| `LLM_MAX_ATTEMPTS` / `LLM_ATTEMPT_TIMEOUT_SECONDS` / `LLM_RETRY_BASE_MS` / `LLM_RETRY_MAX_MS` | 否 | LLM 重试与超时 |

### 9.2 意图与规划

| 变量 | 说明 |
|---|---|
| `INTENT_SERVICE_URL` | 可选意图服务地址；为空时只使用规则路由 |
| `INTENT_ROUTER_MODE` | `rule` / `llm` / `hybrid` / `shadow`；Compose 默认 `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | 模型结果最低置信度，低于阈值回退规则（默认 0.65） |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | 调用意图服务的单次超时秒数 |
| `GENERAL_PLAN_FAST_PATH` | 默认 `true`：General 按 direct/research/paper/literature 形状生成 |
| `COMPLEXITY_ROUTER_ENABLED` | 默认 `true`：启用 L0–L4 复杂度路由 |
| `PLANNER_MAX_NODES_L3` / `PLANNER_MAX_NODES_L4` | agent planner 在 L3/L4 的节点上限（默认 16 / 24） |

### 9.3 沙箱与运行时

| 变量 | 说明 |
|---|---|
| `SANDBOX_URL` / `SANDBOX_API_TOKEN` | 沙箱地址与 Bearer Token；gateway/executor 均 fail-closed |
| `SANDBOX_PROVIDER` | `auto` / `docker` / `native` / `vm` / `remote` |
| `SANDBOX_DEFAULT_IMAGE` / `SANDBOX_IMAGE_ALLOWLIST` | 默认运行时镜像与允许的镜像前缀 |
| `SANDBOX_NETWORK_MODE` / `SANDBOX_ALLOW_NETWORK` | 默认网络模式（`none`）与是否允许审批后联网 |
| `SANDBOX_SECURITY_PROFILE` | `standard` / `strict`（非 root + 只读 rootfs + tmpfs） |
| `SANDBOX_WINDOWS_RUNNER` | `auto` / `appcontainer` / `lowil` / `raw`（native Provider） |
| `SANDBOX_DEFAULT_PYTHON` | native Provider 默认 Python 版本 |
| `SANDBOX_WORKSPACE_ROOTS` | 允许挂载的工作区根目录 |
| `SANDBOX_TTL_MINUTES` / `SANDBOX_REAPER_INTERVAL_SECONDS` | 沙箱最长存活时间与回收扫描间隔 |
| `OPEN_SANDBOX_URL` / `ENABLE_OPENSANDBOX_FALLBACK` | OpenSandbox 地址与 fallback 开关 |

### 9.4 存储与记忆

| 变量 | 说明 |
|---|---|
| `PLAN_STORE_PATH` | 计划快照目录（`<path>.d`）与 JSONL 索引/事件日志的基准路径 |
| `REVISION_STORE_PATH` | 不可变 revision 存储；默认 `<PLAN_STORE_PATH>.revisions.json` |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | 上传目录与单文件大小限制 |
| `UPLOAD_USER_QUOTA_MB` / `UPLOAD_MAX_FILES` / `UPLOAD_TTL_DAYS` | 每用户配额、文件数上限与保留天数 |
| `BLOB_STORE_PATH` | Artifact 内容寻址存储 |
| `MEMORY_ENABLED` / `MEMORY_MODE` / `MEMORY_DIR` | 记忆开关、模式与目录 |
| `MEMORY_CONSOLIDATE_INTERVAL_SECONDS` | 后台 consolidation 周期；空为关闭 |
| `WIKI_ENABLED` / `WIKI_MODE` / `WIKI_DIR` | Paper Wiki 开关、模式与投影目录 |
| `WIKI_LLM_MODE` / `WIKI_LINK_MODE` / `WIKI_PDF_REFS` / `WIKI_BUDGET_TOKENS` | 摘要/Link 裁决/PDF references/预算覆盖 |
| `DOCUMENT_CACHE_DIR` / `LITERATURE_CACHE_DIR` / `LITERATURE_LIBRARY_DIR` | 文档缓存、文献缓存与文献库 |
| `PROJECT_STORE_PATH` / `BUDGET_STORE_PATH` | 计划项目与成本台账；默认在 `PLAN_STORE_PATH` 旁生成 |

### 9.5 审批、预算与安全

| 变量 | 说明 |
|---|---|
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | API 静态 Bearer；Compose 强制 `REQUIRE_API_AUTH=true`，Token 为空时 fail-closed |
| `CORS_ALLOWED_ORIGINS` | 允许访问后端的前端 Origin 列表 |
| `APPROVAL_TIMEOUT_SECONDS` | 运行时审批窗口 |
| `REQUIRE_PLAN_APPROVAL` / `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` | 计划审批与审阅门 |
| `QUESTION_TIMEOUT_SECONDS` | 结构化提问默认超时（默认 900 秒） |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | 计划级预算；soft / hard（默认 soft） |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | 任务尝试次数与时长预算 |
| `PLAN_RATE_LIMIT_PER_MINUTE` / `PLAN_RATE_LIMIT_BURST` / `PLAN_MAX_ACTIVE_PER_USER` | 计划创建限流与每用户活跃计划上限 |
| `EXPORT_MAX_FILE_MB` / `EXPORT_MAX_TOTAL_MB` | 可复现导出包单文件与总大小上限（默认 16 / 128 MB） |

### 9.6 文献与文档

| 变量 | 说明 |
|---|---|
| `LITERATURE_PROVIDER_TIMEOUT_SECONDS` / `LITERATURE_MAX_RETRIES` | 文献 provider 超时与重试 |
| `WEB_FETCH_MAX_MB` / `WEB_FETCH_TIMEOUT_SECONDS` / `WEB_FETCH_RESPECT_ROBOTS` | 网页正文抽取限制 |
| `PDF_PARSE_MAX_MB` / `PDF_PARSE_TIMEOUT_SECONDS` | PDF 解析预算 |
| `PDF_OCR_MAX_PAGES` / `PDF_OCR_LANG` / `PDF_OCR_TIMEOUT_SECONDS` / `PDF_OCR_MIN_TEXT_CHARS` | OCR 预算、语言与文本阈值 |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `FEATURE_VISION_VERIFICATION` | 视觉校验预算与开关 |

> `VITE_API_AUTH_TOKEN` 只在本地 Vite 开发态使用；Compose 生产构建不把 Token 编进前端 bundle，浏览器请求经 nginx 反向代理时由 BFF 注入 `Authorization`。

---

## 10. API 摘要

Backend API 默认 `:8080`。除 `GET /ping`、`GET /api/health` 与 `GET /openapi.json` 外，其余 `/api` 路由经过 `APIAuthMiddleware`；`GET /api/plans/:id/stream` 可用 60 秒 HMAC ticket 替代 Bearer。完整契约见 [OpenAPI](research-assistant/backend/internal/api/openapi.json) 与 [backend/README.md](research-assistant/backend/README.md)。

### 10.1 健康、身份与元数据

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/ping` / `/api/health` | 存活检查 / 后端、仓库、沙箱、Optimizer、意图服务健康状态 |
| `GET` | `/api/me` | 服务端派生的 user_id / session_id |
| `GET` | `/openapi.json` | OpenAPI 3.1 合同（根路径无需 Bearer） |
| `GET` | `/api/sandbox/capabilities` | 当前沙箱 Provider 能力矩阵 |
| `GET` | `/api/metrics`（及 `/metrics`） | Prometheus 文本指标（Bearer 鉴权） |
| `GET` | `/api/audit/export` | 审计事件导出（调用者隔离） |

### 10.2 计划、revision 与审阅

| Method | Endpoint | 说明 |
|---|---|---|
| `POST` | `/api/plan` | 创建计划（意图、附件、运行权限、复杂度） |
| `GET` | `/api/plans` / `/api/plans/:id` | 列出/查询计划、节点、产物与结果 |
| `POST` | `/api/plans/:id/execute` / `pause` / `resume` / `cancel` | 执行/暂停/恢复/取消 |
| `POST` | `/api/plans/:id/approve` | 批准需要人工确认的计划 |
| `POST` | `/api/plans/:id/duplicate` / `PATCH` / `DELETE` | 复制/重命名/删除计划 |
| `GET` / `POST` | `/api/plans/:id/revisions` | 列出/创建不可变 revision |
| `GET` | `/api/plans/:id/revisions/:revision` / `diff` | 查询 revision / 与当前版本 diff |
| `POST` | `.../revisions/:revision/validate` / `approve` / `reject` / `execute` | 校验/批准/拒绝/执行 revision |
| `POST` | `/api/plans/:id/questions` / `.../answer` | 结构化提问与回答 |
| `GET` | `/api/approvals` / `POST /api/approvals/bulk` | 审批收件箱 / 低风险批量决策 |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` / `reassign` / `run` / `execute` | 重试/改派/从节点重跑/仅执行节点 |
| `GET` | `/api/plans/:id/export?format=md` / `json` / `zip` | 导出最小复现包 |
| `POST` | `/api/plans/:id/reproduce/verify` | 静态校验复现包完整性 |

### 10.3 事件、运行时与工具

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/plans/:id/events` | 事件历史 |
| `GET` | `/api/plans/:id/stream-ticket` | 60 秒 HMAC SSE ticket |
| `GET` | `/api/plans/:id/stream` | SSE 事件流（支持 `?ticket=`） |
| `GET` | `/api/plans/:id/runtime-actions` | 运行时审批请求 |
| `POST` | `.../runtime-actions/:actionId/decision` | 允许/拒绝运行时操作 |
| `GET` / `POST` | `/api/tools`、`/api/tools/:id`、`/api/tools/:id/test`、`/api/tools/:id/schema` | 工具注册、测试与 schema |
| `GET` / `POST` / `DELETE` | `/api/tool-grants` | 工具授权 |
| `GET` / `POST` | `/api/agent-profiles`、`/api/prompt-versions` | AgentProfile 与 PromptVersion |
| `POST` | `.../prompt-versions/:id/activate` / `eval` | 激活/评测 Prompt |
| `GET` | `/api/plans/:id/tool-calls` / `POST .../replay` | 工具调用审计与回放 |

### 10.4 记忆、会话与上传

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` / `POST` / `PATCH` / `DELETE` | `/api/memories`（及 `/:id`、`/search`、`/stats`、`/export`、`/import`、`/settings`、`/consolidate`、`/feedback`） | 记忆 CRUD、检索、统计、导出导入、设置与整合 |
| `GET` / `POST` / `PATCH` / `DELETE` | `/api/wiki/papers`（及 `/:id`、`/:id/points`、`/:id/links`） | PaperPage、Point、Link 查询与治理 |
| `POST` | `/api/wiki/links/:linkID/confirm` / `reject` | Link 确认/拒绝 |
| `POST` / `GET` | `/api/wiki/build`、`/api/wiki/build/:jobID` | 增量/全量重建与进度 |
| `GET` | `/api/wiki/stats` | 论文、Point、Link 与待审核统计 |
| `GET` / `POST` / `DELETE` | `/api/sessions`（及 `/:id/activate`） | 服务端会话 |
| `POST` | `/api/uploads` | 上传附件（SHA-256 去重、配额、可选 ClamAV） |
| `GET` | `/api/uploads/:id` / `content` / `document` | 元数据 / 内容 / PDF 文本层摘要 |
| `GET` | `/api/uploads/:id/document/pages/:page` / `sections/:section` / `search` | 页/章节/BM25 检索 |
| `GET` | `/api/uploads/:id/document/quality` / `pages/:page/layout` / `locate` | 质量报告 / 版面 / quote 定位 |
| `GET` | `/api/uploads/:id/document/tables/:tableId` / `figures/:figureId` | 表格 JSON/CSV / 图注与图片 |
| `POST` | `/api/uploads/:id/assist` / `translation-jobs` | 划词翻译/解释 / 全文翻译任务 |
| `DELETE` | `/api/uploads/:id` | 删除上传及派生文档 |

### 10.5 文献、模型与网页

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/literature/search` | 多 provider 检索（arXiv/OpenAlex/Crossref/GitHub/Semantic Scholar/PubMed/DBLP） |
| `POST` | `/api/literature/import` | arXiv ID/URL、DOI、PDF URL 导入并解析 |
| `GET` / `POST` / `DELETE` | `/api/literature/records`（及 `/:id`、`/attach`、`/bibtex`） | 文献库、标签、BibTeX、加入会话 |
| `GET` | `/api/literature/citations/:doi` | 引用/被引图 |
| `POST` | `/api/web/fetch` | 抽取公开网页正文（SSRF/robots/content-type/大小/超时/缓存） |
| `POST` | `/api/chat` | 多轮对话（记忆、引用、会话） |
| `GET` / `POST` / `PUT` / `DELETE` | `/api/model-profiles`（及 `/:id/test`、`/probe-vision`）、`/api/model-routing` | 运行期模型 profile 与 routing |
| `GET` / `POST` | `/api/usage/summary` / `timeseries` / `/api/plans/:id/usage` / `/api/budgets` | 用量与预算 |

Sandbox 服务（默认 `:8082`）另提供 `/api/v1/health`、`/capabilities`、`/images/ensure`、`/sandboxes`、`/sandboxes/:id/python`、`/commands`、`/network`、`/usage`、`/runtime/status|install|cleanup` 等接口，详见 [docker-sandbox/README.md](research-assistant/docker-sandbox/README.md)。

---

## 11. 仓库结构

```text
Research Assistant Agent/
├── README.md                     # 本文件（简体中文）
├── README.en.md                  # English version
├── .github/workflows/ci.yml      # Go / 前端 / Python / Docker / Compose CI
├── docs/                         # 架构图、设计、ADR、CHANGELOG、验证状态
│   ├── diagrams/                 # draw.io / SVG / Archify HTML
│   ├── design/                   # 设计文档（含未实现草案）
│   ├── audit/                    # 上下文系统审计
│   └── improvement-*/            # 逐轮修复与测试报告
├── desktop/                      # Windows 桌面客户端（独立 Go module）
│   ├── mygo/                     # MyGo 适配层
│   ├── shell/ supervisor/ proxy/ settings/ bridge/ tray/
│   ├── diagnostics/ updater/ onboarding/ crashdump/ paths/
│   └── build/                    # MyGo + NSIS 打包流水线
├── docker-core/                  # Docker/gVisor 沙箱内核实验模块
│   ├── checkpoint/               # 快照、回滚与负反馈注入
│   ├── config/                   # TOML 配置加载
│   ├── executor/                 # DAG 调度、节点竞速与资源网关
│   ├── sandbox/                  # 持久化 TTY 沙箱
│   └── watchdog/                 # 语义断路器与超时防护
├── research-assistant/           # 产品主实现
│   ├── backend/                  # Go API、Planner、Scheduler、Agent、Memory、Tools
│   ├── frontend/                 # React + TypeScript 工作台
│   ├── docker-sandbox/           # 独立 Go 沙箱服务（gateway / engine / providers）
│   ├── research-optimizer/       # Python 候选策略与经验服务
│   ├── ai-services/              # 可选 Python AI 服务（意图识别）
│   ├── examples/                 # 可运行示例与验收脚本
│   ├── test/                     # E2E、Golden test 与 fixtures
│   ├── scripts/                  # Unix / Windows 启动与维护脚本
│   ├── backend.env.example
│   ├── docker-compose.yml
│   └── Makefile
└── tool/                         # 零依赖 Go Function Calling 示例
```

---

## 12. 测试与质量门禁

### 12.1 本地命令

```bash
# 后端（Linux 或容器内）
cd research-assistant/backend
go test ./...
go vet ./...

# 前端
cd research-assistant/frontend
npm test
npm run lint
npm run build
npm run check:bundle

# 沙箱与 docker-core
cd research-assistant/docker-sandbox && go test ./...
cd docker-core && go test ./...

# 桌面
cd desktop && go test ./...
GOOS=windows GOARCH=amd64 go build ./...

# Python
cd research-assistant/research-optimizer && python -m unittest discover -s tests -p 'test_*.py'
```

### 12.2 CI 覆盖

- **Go**：Linux/Windows 矩阵；gofmt、后端测试/race/安全回归/覆盖率门禁、沙箱与 docker-core 测试、Windows 交叉构建。
- **Desktop**：Linux 纯逻辑测试 + Windows 交叉构建；Windows MyGo 构建、生成客户端一致性检查与安装包产物。
- **Frontend**：构建、bundle 预算、ESLint、单元/组件测试、CLI 语法、无障碍与视觉回归。
- **Python**：Research Optimizer、论文复现示例、AutoResearch 示例。
- **Docker**：backend/sandbox/frontend 镜像构建；主分支 Compose 冒烟。
- **Hygiene**：UTF-8/mojibake 检查、mock 审计。

最近一次验证状态（2026-11-02）与已知缺陷见 [docs/verification.md](docs/verification.md)。

### 12.3 E2E

E2E 脚本位于 `research-assistant/test/e2e/`，使用 `RA_API_URL` / `RA_API_TOKEN`。覆盖：

- 基础：`http-smoke`、`routing-and-prompts`、`literature`、`literature-plan`、`document-intelligence`、`model-profiles`、`verification`、`sandbox-network`、`round6-*`
- 计划治理：`plan-library`、`repro-bundle`、`cost-budget`、`budget-recovery`、`plan-revisions`、`review-hitl`
- 工具生态：`tool-ecosystem`
- 深度：`round7-api-coverage`、`round7-real-scenarios`、`round7-paper-reproduction`、`round7-autoresearch`、`round7-task-execute`、`round7-doc-assist`、`round7-sandbox-deep`
- 桌面/命令：`desktop-smoke`、`sandbox-command`

---

## 13. 运行边界与安全

- 本项目是研究原型，接口和数据结构可能调整，不承诺向后兼容。
- 单机持久化基于“按计划原子快照 + 小型 append-only JSONL 索引/事件日志”；多副本部署需要共享事务数据库与 leader election。
- 已支持静态 API Token、计划所有权与计划级审批，但不是 OIDC、RBAC 或生产级多租户认证。
- Compose 中 gateway 不挂载 Docker socket 或宿主工作区；只有内部 executor 持有 socket，仍属高权限执行边界。
- 默认运行时镜像是 CPU/通用 Python 镜像；GPU 透传需要 NVIDIA Container Toolkit 与 `SANDBOX_DOCKER_GPUS=all`。
- native Provider 的隔离强度低于容器；不应对不可信代码使用 `full_access`，也不要在多租户环境共享同一桌面数据目录。
- 论文复现重点是轻量 smoke 与结构消融，不包含大规模数据集训练；资源不足时返回 `blocked/partial`。
- Framework_Evaluation 保留 deterministic offline baseline；它不是真实 LangChain/LlamaIndex 性能 benchmark。
- 候选搜索是真实 Validation Reward 驱动的分层 UCB、Top-K Beam 与 UCT-style 搜索，不是完整 MCTS 或通用强化学习。
- 记忆系统默认只做确定性抽取；LLM 抽取与 embedding/图扩展默认关闭，需显式开启并评估。

---

## 14. 文档索引

### 14.1 模块 README

| 模块 | 中文 | English |
|---|---|---|
| 产品主实现 | [research-assistant](research-assistant/README.md) | [English](research-assistant/README.en.md) |
| Go 后端 | [backend](research-assistant/backend/README.md) | [English](research-assistant/backend/README.en.md) |
| React 工作台 | [frontend](research-assistant/frontend/README.md) | [English](research-assistant/frontend/README.en.md) |
| 沙箱服务 | [docker-sandbox](research-assistant/docker-sandbox/README.md) | [English](research-assistant/docker-sandbox/README.en.md) |
| Python 优化器 | [research-optimizer](research-assistant/research-optimizer/README.md) | [English](research-assistant/research-optimizer/README.en.md) |
| AI 服务 | [ai-services](research-assistant/ai-services/README.md) | [English](research-assistant/ai-services/README.en.md) |
| 示例 | [examples](research-assistant/examples/README.md) | [English](research-assistant/examples/README.en.md) |
| 脚本 | [scripts](research-assistant/scripts/README.md) | [English](research-assistant/scripts/README.en.md) |
| 桌面客户端 | [desktop](desktop/README.md) | [English](desktop/README.en.md) |
| 沙箱内核实验 | [docker-core](docker-core/README.md) | [English](docker-core/README.en.md) |
| 工具调用示例 | [tool](tool/README.md) | [English](tool/README.en.md) |

### 14.2 项目文档

- [docs/README.md](docs/README.md)：架构图、设计、ADR 与轮次报告索引
- [docs/diagrams/README.md](docs/diagrams/README.md)：图形文件与编辑方式
- [docs/CHANGELOG.md](docs/CHANGELOG.md)：按提交整理的变更记录
- [docs/verification.md](docs/verification.md)：最近验证状态与已知缺陷
- [reports/README.md](reports/README.md)：本地验收报告目录说明

---

<p align="center">
  <sub>Research Assistant Agent · 由 DeepSeek 构建与维护 · 0.9.0</sub>
</p>
