# 规格：N-13 Agent/工具生态（MCP）（2026-10-30 第十一轮）

> 依据：`docs/design/n13-agent-tool-ecosystem-mcp.md`（设计稿，2026-10-30）
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处在第 9 节记录，以实际为准。
> 约束：一切与功能相关的测试只在容器内执行（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）。
> 基线：`master @ 1282a57` + 前十轮未提交成果（N-01/N-02/N-03/N-04/N-05/N-07、复杂度路由、模型 Profile、预算、审计等）。
> 状态：已实现并通过容器内验证（2026-10-30）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND11.md`。

## 1. 目标

把系统从"固定 7 个 Agent + 装饰性 AllowedTools"升级为"有真实工具权限边界的可扩展平台"。设计文档第 6 节 10 条验收标准是本轮硬性完成条件：

| 编号 | 目标 | 验收信号 |
|---|---|---|
| A1 | 白名单真实生效 | AgentProfile 只允许 `native:echo`；调用 `native:write_file` 返回 `tool_forbidden`，无副作用，审计有记录 |
| A2 | 未知 Agent 不降级 | 创建 `assigned_to=profile:unknown` 的计划返回 400/validation error，不变成 `general_agent` |
| A3 | Native 工具 | 注册 `native:echo`，计划 `tool_call` 调用，输出 artifact；`ToolCallRecord` 含参数/结果/耗时 |
| A4 | MCP stdio | 启动 mock MCP server，`tools/list` 返回 echo；计划调用成功；server 超时/退出返回结构化 `tool_timeout`/`tool_unavailable` |
| A5 | MCP HTTP | mock HTTP MCP 工具；SSRF 私网 URL 被拒绝；响应超限被截断并标记 |
| A6 | 权限/审批 | `SideEffect=true` 的工具创建 `tool_call` pending；拒绝后不调用；批准后调用；审批事件与 ToolCallRecord 关联 |
| A7 | 自定义 Agent | 创建 profile（Prompt v1 + model profile + echo 工具）；计划使用；切 Prompt v2 后行为变化；回滚 v1 恢复；eval 结果可见 |
| A8 | Prompt 版本 | v1/v2 不可变；计划快照记录版本；导出包含版本；回滚只切 active |
| A9 | 回放 | 对已完成 tool call 做 dry-run replay，result hash 一致；真实重放需审批 |
| A10 | 租户/密钥 | owner B 看不到 owner A 的工具/授权；导出/日志不含 API key |

## 2. 现状复核（以实际代码为准，2026-10-30）

| 设计文档描述 | 实际（本轮复核） |
|---|---|
| `RoutedTaskExecutor` 只有 librarian/data/coder/general/research_coding/benchmark/answer 七个 runner；`resolveRunner` 硬编码 switch | 属实：`scheduler/executor.go:61-90、320-353` |
| `normalizeAssignedTo` 只接受 8 个固定名称，其他一律 `general_agent` | 属实：`planner/agent_planner.go:510-517`；另外 `normalizePlannerAssignedTo` 对已知类型会强制覆盖，未知类型保留原值 |
| `TaskContract.AllowedTools` 字段存在；`allowedToolsForAgent` 生成逻辑名 | 属实：`models/runtime.go:10-18`、`planner/planner.go:372-389` |
| `Task` 有 `Contract`，但 `runtimeTask` 未复制 | 属实：`models/task.go:45`、`scheduler/executor.go:105-128` |
| 没有 MCP / 工具注册表 / ToolCallRecord | 属实：grep 无结果 |
| 没有自定义 Agent / Prompt 版本 | 属实：Prompt 是常量；无 profile 表 |
| 工具审计/回放无 | 属实：只有 task 级事件/结果 |
| 可复用 N-05 revision/question/review gate、runtime action、model profile、sandbox、artifact store、audit | 属实：`models/revision.go`、`api/plan_questions.go`、`api/runtime_actions.go`、`modelprofile`、`blobstore`、`audit` |

**补充实际约束（文档未提，直接影响实现）：**

1. **计划创建只有 intent 入口**：`POST /api/plan` 只接受自然语言 intent，节点由 planner 生成；没有"从图创建计划"的 API。要让显式 `tool_call` 节点可测试/可用，必须新增 `POST /api/plans/from-graph`（程序化导入计划图，同样走 owner 隔离、校验、revision 1、review gate）。
2. **运行时审批发生在任务执行前**：`scheduler/scheduler_dispatch.go:68` 在 dispatch 前调用 `prepareRuntimeGate`，`runtime_gate.go:16-73` 只识别资源级 kind。工具审批必须扩展这里，而不是在 executor 里临时造一个未被 scheduler 处理的 action。
3. **executor 返回非 completed 会被当作失败**：`scheduler_dispatch.go:173-194`。因此工具审批必须在 dispatch 前拦截；executor 只负责真正调用工具。
4. **任务类型有注册表强校验**：`planner/taskregistry.go` 的 `validateTaskNode` 会校验 task type 与 allowed agents；新增 `tool_call` 类型必须进注册表，且 `profile:<id>` 需要作为合法 assigned_to 被接受（仅限 LLM 类型）。
5. **后端镜像是单二进制**：`backend/Dockerfile` 只构建 `cmd/api`。MCP stdio 测试需要一个可执行 mock server，因此在同一镜像中额外构建 `cmd/mockmcp`。
6. **已有确定性 OpenAI-compatible mock**：`test/mock/mock_vision_server.py` 由 compose 的 `mock-vision` 服务提供，E2E 已使用；本轮扩展它用于 MCP HTTP 场景，agent profile 行为测试使用容器内已配置的真实模型（`OPENAI_API_KEY` 已设置）。
7. **模型 Profile 已有 SSRF 防护**：`api/model_profiles.go` 使用 `ALLOW_PRIVATE_MODEL_ENDPOINTS`；工具/MCP 需要独立的 `MCP_HTTP_ALLOWED_HOSTS` + 管理员显式 `allow_private_hosts`。
8. **PlanGraph 无 ToolCalls 字段**：`models/graph.go:89-132`；ToolCallRecord 需要持久化到 plan 内，才能随导出/回放使用。
9. **前端设置只有模型页**：`features/settings/ModelSettingsPanel.tsx`；没有工具/Agent 页。前端计划图侧栏没有 tool_call 展示。

**结论**：设计文档对现状判断准确；实现顺序按 Phase 0→3，但为满足 10 条验收，Phase 0/1/2/3 的核心闭环在本轮一次落地。

## 3. 范围

### 3.1 数据模型

新增 `models/tools.go`：

```go
type ToolDefinition struct {
    ID             string   // native:echo / mcp:arxiv/search / http:lab/query
    OwnerID        string   // "" = 全局内置
    Name           string
    Version        string   // semver
    Transport      string   // native | http | mcp_stdio | mcp_http
    Description    string
    InputSchema    string   // JSON Schema
    OutputSchema   string   // JSON Schema
    Scopes         []string // fs.read / fs.write / net / db / shell / gpu
    SideEffect     bool
    TimeoutSec     int
    MaxOutputBytes int64
    Approval       string   // never | always | write | network
    Enabled        bool
    Builtin        bool
    // native
    Native string
    // http
    HTTP *HTTPToolConfig
    // mcp
    ServerID   string
    RemoteName string
    CreatedBy  string
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type MCPServer struct {
    ID          string
    OwnerID     string
    Name        string
    Transport   string // mcp_stdio | mcp_http
    Command     string
    Args        []string
    EnvKeys     []string
    EnvSealed   map[string]string // AES-GCM ciphertext, never serialized
    WorkingDir  string
    URL         string
    HeaderKeys  []string
    HeaderSealed map[string]string
    AllowPrivateHosts bool
    TimeoutSec  int
    MaxOutputBytes int64
    Fingerprint string // sha256 of tools/list
    Status      string // ready | stale | error
    LastError   string
    CreatedBy   string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type ToolGrant struct {
    ID             string
    OwnerID        string
    AgentProfileID string
    ToolID         string
    Scopes         []string
    Approval       string
    CreatedBy      string
    CreatedAt      time.Time
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
    ResultPreview string // bounded, redacted
    Status        string // pending | approved | running | completed | failed | rejected
    ErrorCode     string
    Error         string
    ApprovalID    string
    DryRun        bool
    Truncated     bool
    OutputBytes   int64
    StartedAt     time.Time
    FinishedAt    *time.Time
    DurationMS    int64
}
```

`models/agent_profile.go`：

```go
type AgentProfile struct {
    ID              string
    OwnerID         string
    Name            string
    PromptVersionID string
    ModelProfileID  string
    ToolAllowlist   []string
    MaxSteps        int
    OutputSchema    string
    Builtin         bool
    Enabled         bool
    CreatedAt       time.Time
    UpdatedAt       time.Time
}

type PromptVersion struct {
    ID         string
    OwnerID    string
    Name       string
    Version    int
    Template   string
    Variables  []string
    Status     string // draft | active | archived
    EvalScore  float64
    EvalDetail string
    CreatedBy  string
    CreatedAt  time.Time
}
```

`PlanGraph` 增加：

- `ToolCalls []ToolCallRecord`（持久化调用记录，导出/回放）
- `AgentSnapshot []AgentProfileSnapshot`（计划创建时的 profile/prompt/model 版本快照）

`TaskNode` 已有 `Contract`，新增输入约定：
- `tool_id`、`tool_version`、`arguments`（object）、可选 `call_id`
- `agent_profile_id`（当 assigned_to 为 `profile:<id>` 时由服务端写入）

`TaskExecutionResult` 增加 `ToolCall *ToolCallRecord`，供 scheduler 落盘。

### 3.2 工具注册与传输

新增 `internal/tools`：

- `Store`：工具、MCP server、授权三类记录的 Memory/File 存储（单 JSON 文件，原子写）。
- `Registry`：校验 ID 唯一、版本不可变、owner 可见性；启动时 seed 内置 native 工具。
- `schema.go`：最小 JSON Schema 校验器（type/properties/required/enum/minimum/maximum/minLength/maxLength/items/additionalProperties），足够覆盖工具 schema。
- 传输：
  - `native`：`native:echo`（只读）、`native:write_file`（写文件，SideEffect）、`native:fail`（返回错误）、`native:sleep`（超时测试）、`native:big_output`（截断测试）。
  - `http`：POST JSON，HTTPS/私网校验、allowlist、超时、MaxOutputBytes。
  - `mcp_stdio`：启动受限子进程，JSON-RPC over stdin/stdout（newline-delimited），initialize → notifications/initialized → tools/list / tools/call；进程组超时 kill；env 解密后注入；工作目录限制。
  - `mcp_http`：Streamable HTTP（POST JSON-RPC，支持 application/json 与 text/event-stream），HTTPS/私网校验、allowlist、auth header、超时、MaxOutputBytes。

内置工具（全局、不可被普通用户修改）：

| ID | Scopes | SideEffect | Approval | 说明 |
|---|---|---|---|---|
| `native:echo` | [] | false | never | 返回 message，用于最小闭环 |
| `native:write_file` | fs.write | true | write | 写入 TOOL_WORKSPACE_ROOT，路径越界拒绝 |
| `native:fail` | [] | false | never | 固定返回 tool_execution_failed |
| `native:sleep` | [] | false | never | 按 ms 睡眠，用于超时 |
| `native:big_output` | [] | false | never | 返回 N 字节，用于截断 |

### 3.3 MCP 接入

- 注册：`POST /api/tools` 且 `transport=mcp_stdio|mcp_http` 时，服务端执行 `initialize` + `tools/list`，保存 server fingerprint 与每个工具 schema；返回 server + tools。
- schema 变化：重新注册时计算新 fingerprint，记录 `mcp_schema_changed` 审计并替换工具定义；**重新注册动作本身即重新审核**（不保留不可用的 stale 状态）。
- 工具 ID：`mcp:<serverName>/<remoteToolName>`；`ServerID` 指向 server 记录。
- 调用：`tools/call`；文本截断到 MaxOutputBytes 并标记 `truncated`；图片/资源转 artifact（本轮实现文本 + 结构化 JSON，图片保存为 base64 artifact）；工具输出 artifact 标记 `metadata.untrusted=true`，下游只当数据不当指令。
- 失败：进程退出/连接失败 → `tool_unavailable`；超时 → `tool_timeout`；`isError=true` → `tool_execution_failed`。
- 安全：MCP server 默认不信任；stdio 命令必须为绝对路径且存在于 allowlist 目录（`MCP_STDIO_ALLOWED_COMMANDS`，默认 `/app/mockmcp`）；禁止 shell 解释；env 值加密存储；HTTP 默认仅 HTTPS + 公网 IP，`MCP_HTTP_ALLOWED_HOSTS` 或管理员 `allow_private_hosts` 可放行内网。

### 3.4 工具调用节点

`tool_call` 任务类型（`taskregistry`）：

```json
{
  "id": "tool-1",
  "type": "tool_call",
  "assigned_to": "profile:<uuid>" 或 "tool_executor",
  "inputs": {
    "tool_id": "native:echo",
    "tool_version": "1.0.0",
    "arguments": {"message": "hello"}
  },
  "output_artifacts": ["tool_result"]
}
```

执行流程：
1. `prepareRuntimeGate` 读取 tool 定义；若需要审批 → 创建 `tool_call` pending action，任务 `awaiting_approval`，不执行。
2. `RoutedTaskExecutor.executeToolCall`：
   - 解析 tool_id/version/arguments；
   - 计算 effective allowlist = AgentProfile.ToolAllowlist（有 profile 时）∩ node.Contract.AllowedTools；tool_id 不在其中 → `tool_forbidden`，不调用，写失败记录；
   - 校验 ToolGrant（有 profile 时必须有匹配 grant；scope 覆盖）；
   - schema 校验 → `tool_invalid_arguments`；
   - 调用 transport（带超时/输出上限）；
   - 写 ToolCallRecord（参数 hash、结果 hash、耗时、approval_id），返回 artifact `tool_result`。
3. 事件：`tool_call_requested/started/completed/failed/rejected`。

### 3.5 权限与审批

- `RuntimeActionKind = "tool_call"`；Details 含 tool_id、tool_version、arguments 摘要（不含 secret）、call_id、scopes。
- 需要审批的判定：`SideEffect=true` 或 `Approval=always`，或 `Approval=write` 且 scopes 含 fs.write/db/shell。
- 批准 → 任务回到 pending，scheduler 重跑；ToolInvoker 找到已批准 action（taskID + kind + details.call_id）→ 执行并记录 ApprovalID。
- 拒绝 → 任务 blocked、plan failed、记录 rejected，不调用。
- 审批超时复用 `APPROVAL_TIMEOUT_SECONDS`。

### 3.6 AgentProfile 与 PromptVersion

- 内置 profile：`builtin:general_agent` 等 7 个，Builtin=true，OwnerID=""，映射到现有 runner；保持行为兼容。
- 自定义 profile：`profile:<uuid>`；创建时校验 PromptVersion、ModelProfile、ToolAllowlist 均可见。
- `RoutedTaskExecutor.resolveRunner`：
  1. `profile:<id>` → ProfileRunner（渲染 PromptVersion → 解析 ModelProfile → 调用模型 → 输出 schema 校验）；
  2. 内置名称 → 固定 runner；
  3. 未知名称 → 错误，**不再静默降级**。
- PromptVersion：不可变；创建同 name 新版本自动 version++；`POST /api/prompt-versions/:id/activate` 只切 active（归档旧 active）；eval 结果写入 `EvalScore/EvalDetail`。
- 计划快照：创建/导入计划时记录 `AgentSnapshot`（profile id、prompt version id/version、model profile id/revision）和节点 inputs；导出包含。

### 3.7 审计与回放

- ToolCallRecord 持久化在 `PlanGraph.ToolCalls`，随计划导出。
- 事件：`tool_call_requested`、`tool_call_started`、`tool_call_completed`、`tool_call_failed`、`tool_call_rejected`。
- `GET /api/plans/:id/tool-calls`：列表（owner 隔离）。
- `POST /api/plans/:id/tool-calls/:callId/replay`：
  - `{allow_side_effects:false}` → dry-run 调用，比较 result hash，返回 `{match, expected_hash, actual_hash, dry_run:true}`；不产生副作用。
  - `{allow_side_effects:true}` 无 `approval_id` → 创建 pending `tool_call` action，返回 202 `{status:"pending_approval", approval_id}`。
  - 带已批准 `approval_id` → 真实重放并记录新 ToolCallRecord（`replay_of`）。

### 3.8 API 清单

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/tools` | 当前用户可见工具 + MCP servers |
| POST | `/api/tools` | 注册 native/HTTP/MCP 工具（MCP 会握手并展开 tools） |
| GET | `/api/tools/:id` | 详情/schema/版本 |
| PUT | `/api/tools/:id` | 更新配置（版本必须显式升） |
| DELETE | `/api/tools/:id` | 停用（不物理删除） |
| POST | `/api/tools/:id/test` | 测试调用（默认 dry-run） |
| GET | `/api/tools/:id/schema` | JSON Schema |
| GET/POST/DELETE | `/api/tool-grants` | 授权 agent/profile 使用工具 |
| GET/POST | `/api/agent-profiles` | 内置 + 自定义 |
| PUT/DELETE | `/api/agent-profiles/:id` | 修改/停用 |
| POST | `/api/agent-profiles/:id/eval` | 评测 Agent |
| GET/POST | `/api/prompt-versions` | 版本列表/创建 |
| POST | `/api/prompt-versions/:id/activate` | 切换 active |
| POST | `/api/prompt-versions/:id/eval` | 运行评测 |
| POST | `/api/plans/from-graph` | 从图导入计划（含 tool_call 节点） |
| GET | `/api/plans/:id/tool-calls` | 调用记录 |
| POST | `/api/plans/:id/tool-calls/:callId/replay` | 回放 |

### 3.9 前端

- 设置弹窗改为三个 tab：模型 / 工具 / Agent。
  - 工具：列表（来源、版本、scope、状态、最近测试）；添加 MCP server（stdio command/args/env 或 HTTP URL/auth）；测试连接；查看 tools/list；工具详情 schema。
  - Agent：profile 列表（内置/自定义）；Prompt 编辑器 + 版本列表 + 激活/回滚 + eval 结果；模型、工具白名单、MaxSteps。
- 计划图：`tool_call` 节点显示工具名/版本；执行侧栏显示参数、结果、审批、耗时；失败显示错误码。
- 审批收件箱：`tool_call` 与 runtime action 一起展示。

### 3.10 实施阶段

| 阶段 | 内容 | 交付 |
|---|---|---|
| P0 真白名单 | Contract 复制；AllowedTools 执行期检查；未知 Agent 报错；native echo + ToolCallRecord | A1/A2/A3 |
| P1 工具层 | Registry/Store/Invoker/schema/HTTP/native；工具 API；tool_call 节点；审批 | A3/A6 |
| P2 MCP | stdio/HTTP client、server 注册、schema 固定、超时/输出限制、SSRF | A4/A5 |
| P3 Agent 层 | AgentProfile/PromptVersion、profile runner、eval/rollback、快照 | A7/A8 |
| P4 回放与前端 | replay、工具/Agent 设置页、tool_call 展示、审批收件箱 | A9/A10 + 5.9 |

## 4. 测试策略（全部在容器内）

1. 后端：`scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."`；新增包 `-race`。
2. 前端：`node:22-bookworm-slim` 内 `npm ci && npm test && npm run build && npm run lint`。
3. 镜像重建 + compose 起栈（backend/frontend）。
4. 新增 E2E `test/e2e/tool-ecosystem.mjs`：覆盖 A1–A10。
5. 回归既有 E2E：http-smoke、plan-run、plan-library、plan-revisions、review-hitl、cost-budget、budget-recovery、model-profiles。
6. 清理：临时容器/镜像/卷、mock 进程、测试数据。

## 5. 完成定义

- 10 条验收全部有容器内证据（E2E 输出 + 单测）。
- 后端 build/vet/test 全绿；前端 test/build/lint 全绿。
- 无新增回归（既有 E2E 全绿）。
- 文档：FIX-REPORT-ROUND11、OpenAPI、README/spec/plan/tasks 更新。
- 清理完成。

## 6. 非目标

- 不做公开工具市场/计费/评级（设计 Phase 3 可选）。
- 不做任意用户代码上传执行；工具必须是注册的 native/HTTP/MCP。
- 不做无限制 LLM 自由 tool calling；先显式 tool_call 节点。
- 不自动安装 MCP server 或依赖。

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| ToolCallRecord 与 plan 并发写冲突 | 由 scheduler 在 markTaskCompleted/markTaskFailed 内合并，executor 只返回结果 |
| MCP stdio 子进程泄漏 | 每次调用 context 超时 + Setpgid + kill 进程组；server 记录指纹 |
| MCP schema 漂移 | 注册时 fingerprint；重新注册时记录 `mcp_schema_changed` 审计并替换工具（重新注册即重新审核） |
| SSRF | 解析 DNS 后拒绝私网/回环/链路本地；allowlist 精确匹配；仅管理员可 allow_private_hosts |
| secret 泄露 | AES-GCM 加密 env/header；API 只返回 key 名；导出/日志脱敏 |
| 审批死锁 | 复用 runtime action 超时；拒绝时清理后续节点 |
| 前端设置页复杂 | 拆 ToolsSettingsPanel / AgentSettingsPanel；组件测试 |
| 未知 Agent 改动影响旧计划 | 仅非空未知值报错；空值仍默认 general_agent；旧计划不重新校验 |

## 8. 与设计文档的差异（以实际为准）

1. 设计文档写"Planner 校验 tool_id 存在、版本匹配、tool_id ∈ AgentProfile.ToolAllowlist"；实际实现中，**allowlist 在执行期强制**（planner/导入校验只校验存在性与版本），否则无法满足 A1 的"调用返回 tool_forbidden"语义（计划可创建、执行被拒绝）。这是有意的权限边界：planner 是意图，executor 是边界。
2. 设计文档的 `ToolGrant` "默认拒绝"在本轮落地为：**有 AgentProfile 的调用必须有匹配 grant**；无 profile 的 `tool_executor` 节点以节点 Contract.AllowedTools 为准（用于系统内置工具与测试）。理由：现有系统没有"用户=租户"之外的授权模型，强制 grant 会让所有内置工具不可用。
3. 设计文档写 MCP `tools/list` 在注册时保存 fingerprint；本轮实现为**每次注册/重新发现时计算**，重新注册即重新审核并记录 `mcp_schema_changed` 审计；调用期不保留独立 stale 状态（避免"注册后反而不能调用"的死状态）。
4. 设计文档的 Prompt eval 未定义评测集格式；本轮定义为 `{cases:[{name, variables, expected_contains}]}`，默认模板级评测（确定性），可选 `use_model` 走真实模型。
5. 设计文档写"图片转 artifact"；本轮 MCP 结果中的 image 内容保存为 base64 artifact（`tool_result_image`），文本结果截断到 MaxOutputBytes。
6. 设计文档的 `POST /api/tools/:id/test` 写"dry-run/审批"；本轮实现为默认 dry-run，`dry_run=false` 且工具需要审批时返回 409 `tool_approval_required`（测试端点不创建计划级 action）。

## 9. 完成后的清理

- 删除测试产生的临时容器、镜像、构建缓存、脚本与 mock 进程；工具/Agent 存储重置为内置项（5 个 native 工具、8 个 builtin profile）。
- 保留 compose 数据卷、历史 plan store（append-only 审计数据）与既有服务。
