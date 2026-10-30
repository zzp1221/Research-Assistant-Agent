# N-13 Agent/工具生态（MCP）：现状、问题与实施方案

- **状态**：设计稿（待评审）
- **日期**：2026-10-30
- **范围**：工具注册与调用、MCP 接入、自定义 Agent、Prompt 版本与评测、工具调用审计/回放
- **关联**：`docs/improvement-2026-10-26/03-new-feature-proposals.md`、`research-assistant/backend/internal/scheduler/executor.go`、`planner/planner.go`、`planner/agent_planner.go`、`models/runtime.go`、`models/task.go`、`models/revision.go`、`api/plan_questions.go`、`api/runtime_actions.go`、`modelprofile/profile.go`、`frontend/src/features/settings`
- **一句话结论**：当前是“固定 7 个 Agent + 一张装饰性的 AllowedTools 清单”，没有真正的工具层。N-13 的第一步不是接 MCP，而是**把 AllowedTools 变成可执行的权限边界，再建工具注册表，最后接 MCP/自定义 Agent/Prompt 版本**。

---

## 1. 摘要

N-13 的目标是让系统从“固定产品”变成“可扩展平台”：

- 用户能注册自己的工具（MCP / HTTP / 原生），在计划中调用；
- 用户能定义自己的 Agent（Prompt + 模型 + 工具白名单）；
- Prompt 有版本、可评测、可回滚；
- 每次工具调用有参数、结果、权限、审计，可回放。

但当前代码的事实是：

- **Agent 是固定的**：`RoutedTaskExecutor` 只有 librarian/data/coder/general/research_coding/benchmark/answer 七个 runner；`resolveRunner` 是硬编码 switch。
- **AllowedTools 是装饰性的**：`planner.allowedToolsForAgent` 会写 `TaskContract.AllowedTools`，前端也会展示，但执行器创建 `runtimeTask` 时没有复制 `Contract`，更没有工具调用层去检查它。
- **没有 MCP**：全仓库没有 MCP client/server、工具注册表、工具调用记录。
- **没有自定义 Agent / Prompt 版本**：Prompt 是编译期常量；`assigned_to` 不在白名单会被 `normalizeAssignedTo` 静默改成 `general_agent`。

所以 N-13 必须分阶段：**先修“假白名单”，再做“工具层”，再做“Agent 层”，最后做“生态层”**。

---

## 2. 现状盘点

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| Agent 路由 | 固定 7 个 runner；`sandbox_agent` 复用 `coder_agent` | `scheduler/executor.go:60-90、320-353` | 无插件/自定义 Agent；新增 Agent 必须改代码 |
| Agent 名称校验 | `normalizeAssignedTo` 只接受 8 个固定名称，其他一律 `general_agent` | `planner/agent_planner.go:510-517` | 未知 Agent 不报错，静默降级，可能绕过权限/语义 |
| Tool 契约 | `TaskContract.AllowedTools` 字段存在；`allowedToolsForAgent` 生成 `repository.read`/`sandbox.command` 等逻辑名 | `models/runtime.go:8-17`、`planner/planner.go:372-389` | 只是元数据；没有工具 ID、schema、版本、权限 |
| 契约传递 | `Task` 结构体有 `Contract`，但 `RoutedTaskExecutor` 构造 `runtimeTask` 时未复制 | `models/task.go:26-45`、`scheduler/executor.go:105-128` | 执行期拿不到 AllowedTools，无法校验 |
| 工具调用 | Agent 直接调用 sandbox/内部客户端；没有统一 `ToolInvoker` | `agent/*`、`scheduler/executor.go` | 无工具注册、无参数 schema、无统一超时/审计 |
| MCP | **没有**：无 `internal/mcp`、无 `modelcontextprotocol` 依赖、无 MCP 配置/端点 | grep 无结果 | 不能接外部 MCP server |
| HTTP 工具 | **没有**：无工具注册 API；只有零散内部 HTTP 客户端 | 无 `/api/tools` | 不能注册用户 HTTP 工具 |
| 自定义 Agent | **没有**：Prompt 是 `prompts` 常量；模型由 model profile 解析 | `agent/*`、`prompts/prompts.go` | 不能配置 Prompt/模型/工具白名单 |
| Prompt 版本 | **没有**：只有 `assist.PromptVersion = "document-assist/v1"` 常量 | `internal/assist/assist.go:24` | 无版本表、无评测、无回滚、无 A/B |
| 工具审计/回放 | **没有**：只有 task 级事件/结果；无工具参数/结果记录 | `models/event.go`、`scheduler` | 无法回答“调了什么、参数是什么、结果是否可复现” |
| 现有可复用底座 | 运行期 model profile/routing；N-05 的 revision/question/review gate；runtime action 审批；sandbox 网络策略；artifact store；audit | `modelprofile`、`models/revision.go`、`api/plan_questions.go`、`api/runtime_actions.go` | 可作为工具权限/审批/模型选择的基础 |

---

## 3. 问题在哪

### 3.1 固定 7 个 Agent，无法覆盖真实研究工具

- 研究工具是长尾的：私有数据库、实验室 API、内部实验平台、领域脚本、数据清洗器、模型服务。
- 当前只能改 Go 代码、重新编译、重新部署；用户/研究员无法自助接入。
- 结果：系统只能做“内置的几类研究”，无法成为平台。

### 3.2 AllowedTools 是“假白名单”，给出错误的安全感

- `allowedToolsForAgent` 生成的 `repository.read`/`sandbox.command`/`dataset.audit` 等只是字符串；
- `runtimeTask` 没有 `Contract`，执行器没有检查；Agent 也没有按这个列表调用工具（因为根本没有工具层）。
- 后果：
  - 审计上看起来“限制了工具”，实际没有；
  - 未来接入工具时，如果继续沿用这个字段但不执行检查，会成为权限绕过点；
  - 用户无法知道一个 Agent 到底能做什么。

### 3.3 没有工具 schema/注册表，planner 无法正确选择工具

- 当前 planner 只认识 task type 和 agent 名称；没有“工具存在/参数 schema/输出 schema/版本”的概念。
- 后果：LLM 只能编造工具名，或 planner 只能写死；工具参数无法校验；工具升级会破坏计划。

### 3.4 没有工具级权限/审批

- 现有 `RuntimeActionKind` 是资源级：image pull、dependency install、dataset/model download、GPU、network。
- 但工具调用可能是：读数据库、写文件、发 HTTP、执行 shell、调用内部实验平台；风险粒度不同。
- 后果：无法按工具/参数审批；无法“只读自动、写入需批”；无法拒绝单个工具而保留计划。

### 3.5 自定义 Agent 不存在，未知 Agent 还会静默降级

- `normalizeAssignedTo` 对未知值返回 `general_agent`，而不是报错。
- 后果：用户以为创建了“自定义 Agent”，实际所有未知节点都跑通用 Agent；工具白名单/模型/Prompt 全部失效。
- 这是比“没有自定义 Agent”更危险的行为：失败不显式。

### 3.6 Prompt 没有版本/评测，改 Prompt 等于赌博

- Prompt 是编译期常量；改动需要发版；没有版本号、diff、评测集、回滚。
- 后果：无法判断“新 Prompt 是更好还是更差”；不同计划无法复现当时 Prompt；A/B 测试不可能。

### 3.7 工具调用不可审计/回放

- 现在只有 task 级 `Result`/事件；没有 `tool_name`、`arguments`、`tool_version`、`result_hash`、`approval_id`。
- 后果：无法定位“哪个工具返回了脏数据”；无法回放；无法在导出包中证明工具调用链。

### 3.8 MCP 不是“加个客户端”那么简单

- MCP server 可能是本地 stdio 进程、远程 HTTP/SSE 服务；涉及进程生命周期、环境变量、secret、网络、超时、输出大小、schema 漂移、工具投毒。
- 如果没有工具注册/权限/审计底座，直接接 MCP 会把任意外部工具暴露给 Agent，风险不可控。

---

## 4. 为什么这么做

### 4.1 工具是“有副作用的代码”，必须一等公民化

- 工具调用不是普通文本生成：它读文件、发网络、写数据库、跑 shell。
- 因此必须有：唯一 ID、版本、输入/输出 schema、权限 scope、超时、最大输出、审批策略、审计记录。
- 只有“工具调用记录”成为 PlanGraph/Event 的一部分，才能复现和追责。

### 4.2 先 native，后 MCP，最后市场

- Native 工具（Go 函数/内部服务）最容易做权限、schema、审计，是先验证模型的最小闭环。
- MCP 是标准协议，但引入外部进程/服务，必须建立在 native 工具层之上。
- 工具市场需要信任、签名、评级、计费、租户隔离，过早做会放大风险；先做“私有工具注册”。

### 4.3 AllowedTools 必须在执行器强制，而不是 planner 声明

- Planner 产出的 `AllowedTools` 只是“意图”；执行器才是权限边界。
- 正确做法：`runtimeTask.Contract = task.Contract`，`ToolInvoker` 在每次调用前检查 `tool_id ∈ AllowedTools` 且 `ToolGrant` 允许。
- 未知 Agent/未知工具必须返回结构化错误，禁止静默降级。

### 4.4 自定义 Agent 是“配置”，不是“代码”

- AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps + OutputSchema。
- 内置 Agent 也走同一套 profile（builtin profile），避免两套逻辑。
- 这样用户可创建、评测、回滚，而无需发版。

### 4.5 Prompt 版本必须不可变 + 可评测

- Prompt 是研究质量的核心变量；修改必须可比较。
- 版本不可变，计划快照记录 `prompt_version`；评测集覆盖典型 intent/任务；A/B 结果决定是否 active。
- 回滚只是切换 active 指针，不改历史。

### 4.6 复用已有底座，不重造

- 模型选择：复用 `modelprofile` 的 per-user profile/routing。
- 人工审批：复用 N-05 的 revision/question/review gate 与 `RuntimeAction`；新增 `tool_call` kind。
- 执行隔离：复用 sandbox 网络/资源策略；MCP stdio 进程也放进受限环境。
- 审计/导出：复用 audit event、artifact store、plan export。

---

## 5. 怎么做

### 5.1 目标架构

```text
Settings / API
  ├─ Tool Registry（native / HTTP / MCP）
  ├─ Agent Profiles（PromptVersion + ModelProfile + ToolAllowlist）
  └─ Tool Grants（owner/agent/tool/scopes/approval）
        │
        ▼
Planner
  ├─ 只生成已注册的 tool_call 节点 / agent_profile 节点
  └─ 校验 tool_id、version、schema、allowed_tools
        │
        ▼
Scheduler / RoutedTaskExecutor
  ├─ resolve AgentProfile（内置/自定义）
  ├─ ToolInvoker
  │    ├─ schema validate
  │    ├─ permission check
  │    ├─ approval gate（RuntimeAction）
  │    ├─ transport call（native/HTTP/MCP）
  │    └─ ToolCallRecord + artifact
  └─ PlanGraph / events / export
```

### 5.2 数据模型

```go
type ToolDefinition struct {
    ID            string   // native:echo / mcp:arxiv/search / http:lab/query
    Name          string
    Version       string   // semver；schema/行为变化必须升版本
    Transport     string   // native | http | mcp_stdio | mcp_http
    Description   string
    InputSchema   string   // JSON Schema
    OutputSchema  string   // JSON Schema
    Scopes        []string // fs.read / fs.write / net / db / shell / gpu
    SideEffect    bool
    TimeoutSec    int
    MaxOutputBytes int64
    Approval      string   // never | always | write | network
    Enabled       bool
}

type ToolGrant struct {
    OwnerID       string
    AgentProfileID string
    ToolID        string
    Scopes        []string
    Approval      string
    CreatedBy     string
    CreatedAt     time.Time
}

type ToolCallRecord struct {
    ID            string
    PlanID        string
    TaskID        string
    ToolID        string
    ToolVersion   string
    Arguments     map[string]any
    ArgumentsHash string
    ResultHash    string
    Status        string // pending | approved | running | completed | failed | rejected
    ApprovalID    string
    Error         string
    StartedAt     time.Time
    FinishedAt    *time.Time
    DurationMS    int64
}

type AgentProfile struct {
    ID             string
    OwnerID        string
    Name           string
    PromptVersionID string
    ModelProfileID string
    ToolAllowlist  []string
    MaxSteps       int
    OutputSchema   string
    Builtin        bool
    Enabled        bool
}

type PromptVersion struct {
    ID          string
    Name        string
    Version     int
    Template    string
    Variables   []string
    Status      string // draft | active | archived
    EvalScore   float64
    CreatedBy   string
    CreatedAt   time.Time
}
```

约束：

- `ToolDefinition.ID` 全局唯一；版本不可变；同 ID 新版本 = 新记录。
- `ToolGrant` 默认拒绝；owner/agent/tool 三维隔离；全局工具只允许管理员授权。
- `AgentProfile` 内置 Agent 也建 builtin profile；自定义 profile 必须有 PromptVersion + ModelProfile。
- `PromptVersion` 不可变；`Status=active` 只有一个；回滚 = 切 active。

### 5.3 Tool Registry API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/tools` | 当前用户可见的工具（内置 + 自己的） |
| POST | `/api/tools` | 注册 native/HTTP/MCP 工具 |
| GET | `/api/tools/:id` | 工具详情/schema/版本 |
| PUT | `/api/tools/:id` | 更新配置（生成新版本） |
| DELETE | `/api/tools/:id` | 停用（不物理删除历史） |
| POST | `/api/tools/:id/test` | 用测试参数调用一次（dry-run/审批） |
| GET | `/api/tools/:id/schema` | JSON Schema |
| GET/POST | `/api/tool-grants` | 授权 agent/profile 使用工具 |
| GET | `/api/agent-profiles` | 内置 + 自定义 Agent |
| POST/PUT | `/api/agent-profiles` | 创建/修改 profile |
| GET/POST | `/api/prompt-versions` | Prompt 版本列表/创建 |
| POST | `/api/prompt-versions/:id/eval` | 运行评测 |
| POST | `/api/agent-profiles/:id/eval` | 评测 Agent |

### 5.4 MCP 接入

- 传输：
  - `mcp_stdio`：启动本地进程，JSON-RPC over stdin/stdout；必须限制 command/args/env/working dir，禁止 shell 解释，进程组超时 kill。
  - `mcp_http`：Streamable HTTP / SSE；必须 HTTPS、allowlist host、SSRF 防护、auth header secret ref、最大响应。
- 生命周期：
  - 注册时 `initialize` + `tools/list`，保存 server fingerprint/tool schema；
  - 调用前检查 server 健康；失败重连一次；
  - 工具 schema 变化 → 标记 `stale`，要求重新审核，不自动信任。
- 调用：
  - `tools/call` 参数按 `ToolDefinition.InputSchema` 校验；
  - 结果支持 text/image/resource；文本截断、图片转 artifact；
  - 工具输出作为**不可信数据**，包装成 `tool_result`，禁止当指令执行。
- 安全：
  - MCP server 默认不信任；只允许管理员/owner 注册；禁止自动安装；
  - stdio 进程无网络（默认）、只读根、资源限制；HTTP 走 egress allowlist；
  - secret 用加密引用，不写入 plan/export/log。

### 5.5 工具调用节点

第一阶段不要做“LLM 自由 function calling”，先做**显式 tool_call 节点**：

```json
{
  "id": "tool-1",
  "type": "tool_call",
  "assigned_to": "tool_executor",
  "inputs": {
    "tool_id": "mcp:arxiv/search",
    "tool_version": "1.2.0",
    "arguments": {"query": "graph neural network", "limit": 5}
  },
  "output_artifacts": ["tool_result"]
}
```

执行流程：

1. Planner 校验 `tool_id` 存在、版本匹配、`tool_id ∈ AgentProfile.ToolAllowlist`；
2. Scheduler 创建 `ToolCallRecord`（pending）；
3. `ToolInvoker` 校验 schema → 权限 → 审批（若 `SideEffect`/`Approval=always`）；
4. 调用 transport；结果写 artifact + `ToolCallRecord`；
5. 事件 `tool_call_started/completed/failed`；失败按 retry/timeout 处理。

后续阶段再允许 AgentProfile 的 LLM 通过 function calling 产生 tool_call，但仍走同一 `ToolInvoker` 和审计。

### 5.6 自定义 Agent

- `AgentProfile` 作为 `assigned_to` 的目标：
  - 内置：`builtin:coder_agent` 等，保持兼容；
  - 自定义：`profile:<uuid>`，planner 只接受已注册 ID。
- `RoutedTaskExecutor.resolveRunner`：
  1. 先查 AgentProfileRegistry；找到则用 profile 的 prompt/model/tools；
  2. 否则回退内置固定 runner；
  3. 未知 profile 返回错误，**不再静默变成 general_agent**。
- Profile 执行：
  - 渲染 PromptVersion 模板（变量来自 plan/context）；
  - 用 ModelProfile 解析模型（复用 `modelresolver`）；
  - 限制 `MaxSteps`、`ToolAllowlist`、`OutputSchema`；
  - 结果按 schema 校验后写 task。
- Prompt 版本：
  - `PromptVersion` 不可变；创建 draft → eval → active；
  - 计划创建时记录 `agent_profile_id + prompt_version + model_profile_revision`；
  - 回滚只切 active，不修改历史。

### 5.7 权限与审批

- Scope：
  - `fs.read`/`fs.write`/`net`/`db`/`shell`/`gpu`；
  - 只读工具默认 allow；写/网络/shell/db 默认 require approval。
- `ToolGrant` 是白名单；`AllowedTools` 是二次约束（AgentProfile 级）。
- 审批：
  - 新增 `RuntimeActionKind = "tool_call"`；
  - `Details` 包含 tool_id、参数摘要、目标、预计影响；
  - approve 才调用；reject 将 task 置 `blocked`，不产生副作用；
  - 审批事件进入 audit，ToolCallRecord 关联 `ApprovalID`。
- 租户：
  - 工具/授权按 owner 隔离；全局工具只读；跨租户不可见；
  - secret 加密；导出/日志脱敏。

### 5.8 审计与回放

- `ToolCallRecord` 持久化在 plan（或独立 store），包含参数/结果 hash、版本、耗时、审批 ID。
- 事件：
  - `tool_call_requested`、`tool_call_started`、`tool_call_completed`、`tool_call_failed`、`tool_call_rejected`。
- 回放：
  - `POST /api/plans/:id/tool-calls/:callId/replay`；
  - 默认 `dry_run`：用记录参数重放，比较 result hash；不产生副作用；
  - 真实重放需显式 `allow_side_effects=true` + 审批；
  - 导出包包含 tool-call 记录，支持离线复现。
- 指标：
  - 工具调用次数/失败率/延迟/输出字节/审批等待；
  - 按 tool/agent/tenant 维度。

### 5.9 前端

- 设置 → 工具：
  - 工具列表：来源（native/HTTP/MCP）、版本、scope、启用状态、最近测试；
  - 添加 MCP server：stdio command/args/env 或 HTTP URL/auth；测试连接；查看 `tools/list`；
  - 工具详情：schema、示例、权限、调用统计。
- 设置 → Agent：
  - Profile 列表：内置/自定义；Prompt 版本、模型、工具白名单、MaxSteps；
  - Prompt 编辑器 + 版本 diff + eval 结果 + 激活/回滚。
- 计划图：
  - `tool_call` 节点显示工具名/版本；执行侧栏显示参数、结果、审批、耗时；
  - 被拒绝/失败的调用有明确错误码。
- 审批收件箱：
  - `tool_call` 与 runtime action 一起展示；高风险工具逐条确认。

### 5.10 实施阶段

| 阶段 | 周期 | 内容 | 交付 |
|---|---|---|---|
| Phase 0：真白名单 | 1–2 周 | 复制 Contract 到 runtimeTask；ToolInvoker 检查 AllowedTools；未知 Agent 报错；native echo 工具 + ToolCallRecord | 工具白名单真实生效；基础审计 |
| Phase 1：MCP | 3–4 周 | MCP stdio/HTTP client、工具注册 API、tool_call 节点、审批、schema 校验、超时/输出限制 | 注册 MCP 工具并在计划中调用 |
| Phase 2：Agent 层 | 2–3 周 | AgentProfile、PromptVersion、模型绑定、planner 支持 profile、prompt eval/rollback | 自定义 Agent 可创建/评测/回滚 |
| Phase 3：回放/生态 | 2–3 周 | ToolCall replay、工具市场（可选）、模板/评分/签名 | 调用可回放；生态可选 |

---

## 6. 验收标准（容器内）

1. **白名单真实生效**：AgentProfile 只允许 `native:echo`；调用 `native:write_file` 返回 `tool_forbidden`，无副作用，审计有记录。
2. **未知 Agent 不降级**：创建 `assigned_to=profile:unknown` 的计划返回 400/validation error，不变成 `general_agent`。
3. **Native 工具**：注册 `native:echo`，计划 `tool_call` 调用，输出 artifact；`ToolCallRecord` 含参数/结果/耗时。
4. **MCP stdio**：启动 mock MCP server，`tools/list` 返回 echo；计划调用成功；server 超时/退出返回结构化 `tool_timeout`/`tool_unavailable`。
5. **MCP HTTP**：mock HTTP MCP 工具；SSRF 私网 URL 被拒绝；响应超限被截断并标记。
6. **权限/审批**：`SideEffect=true` 的工具创建 `tool_call` pending；拒绝后不调用；批准后调用；审批事件与 ToolCallRecord 关联。
7. **自定义 Agent**：创建 profile（Prompt v1 + model profile + echo 工具）；计划使用；切 Prompt v2 后行为变化；回滚 v1 恢复；eval 结果可见。
8. **Prompt 版本**：v1/v2 不可变；计划快照记录版本；导出包含版本；回滚只切 active。
9. **回放**：对已完成 tool call 做 dry-run replay，result hash 一致；真实重放需审批。
10. **租户/密钥**：owner B 看不到 owner A 的工具/授权；导出/日志不含 API key。

---

## 7. 与旧提案的差异

| 项 | 旧提案 | 本文改进版 |
|---|---|---|
| AllowedTools | 未提及；假设从零做工具注册 | **字段已存在但未执行**；Phase 0 先把它变成真权限 |
| N-05 依赖 | 依赖 HITL | 当前工作区已有 revision/question/review gate（`models/revision.go`、`api/plan_questions.go`），直接复用 |
| MCP | 直接做 MCP/HTTP 注册 | 先 native 工具层，再 MCP；MCP 需要 server 生命周期/安全 |
| 自定义 Agent | Prompt/工具/模型配置 | 以 AgentProfile + PromptVersion + ModelProfile 组合实现；内置 Agent 也 profile 化 |
| 工具市场 | 核心能力之一 | 放到 Phase 3 可选；先私有工具注册 |
| 工作量 | L（4+ 周） | 拆成 4 阶段；Phase 0 1–2 周即可产生安全价值 |

---

## 8. 关键文件索引

| 层 | 文件 | 说明 |
|---|---|---|
| Agent 路由 | `backend/internal/scheduler/executor.go:60-90、320-353` | 固定 7 runner；resolveRunner switch |
| Agent 归一化 | `backend/internal/planner/agent_planner.go:510-517` | 未知 Agent → general_agent |
| 工具白名单 | `backend/internal/planner/planner.go:372-389` | allowedToolsForAgent |
| 任务契约 | `backend/internal/models/runtime.go:8-17` | TaskContract.AllowedTools |
| Task 模型 | `backend/internal/models/task.go:26-45` | Task 有 Contract，但 runtimeTask 未复制 |
| 执行器 | `backend/internal/scheduler/executor.go:105-128` | runtimeTask 构造缺 Contract |
| 运行审批 | `backend/internal/api/runtime_actions.go` | 可扩展 tool_call kind |
| 结构化提问 | `backend/internal/api/plan_questions.go` | 可复用 question 机制 |
| Revision/Review | `backend/internal/models/revision.go` | N-05 已落地 |
| 模型 Profile | `backend/internal/modelprofile/profile.go` | 可复用模型/价格/角色 |
| 前端设置 | `frontend/src/features/settings` | 目前只有模型设置，无工具/Agent |

---

## 9. 非目标与风险

### 非目标

- 不做任意用户代码上传执行；工具必须是注册的 native/HTTP/MCP；
- 不做公开工具市场/计费/评级（Phase 3 可选）；
- 不自动安装 MCP server 或依赖；
- 不做无限制的 LLM 自由 tool calling；先显式 tool_call 节点；
- 不替代内置 Agent；内置 Agent 也 profile 化，但保持行为兼容。

### 风险与缓解

| 风险 | 缓解 |
|---|---|
| 工具投毒/恶意 MCP | 只允许可信注册；server fingerprint/schema diff 重审；默认拒绝 |
| Prompt injection（工具输出） | 工具结果作为不可信数据；不执行其中的指令；内容/大小限制 |
| SSRF/私网访问 | HTTP MCP/工具走 egress allowlist + 私网 IP 拒绝 |
| secret 泄露 | 加密 secret ref；不写 plan/export/log；最小权限 |
| 工具 schema 漂移 | 版本固定；schema 变化需新版本 + 重新授权 |
| 工具输出过大 | MaxOutputBytes + 截断 + artifact 化 |
| 自定义 Agent 质量差 | Prompt eval/评测集；灰度 active；可回滚 |
| 未知 Agent 静默降级 | 改为显式 validation error；禁止 fallback |
| 工具调用成本/延迟 | 超时/重试/并发限制；指标与预算（N-03）联动 |

---

## 10. 结论

- **N-13 的第一步不是 MCP，而是让 AllowedTools 真正生效**：当前它只是 planner 生成的展示字段，执行器没有复制/检查；这是必须先修的安全缺口。
- **第二步是 native 工具注册 + ToolCallRecord**：先把 schema、权限、审计、审批跑通，再接 MCP。
- **第三步是 MCP**：stdio/HTTP 接入、server 生命周期、schema 固定、输出限制、SSRF/secret 防护。
- **第四步是 AgentProfile + PromptVersion**：让用户配置 Prompt/模型/工具，内置 Agent 也 profile 化；Prompt 评测/回滚。
- 工具市场、公开生态放到最后；没有权限/审计/回放底座之前，市场只会放大风险。
