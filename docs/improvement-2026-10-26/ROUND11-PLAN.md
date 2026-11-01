# 实施计划：N-13 Agent/工具生态（MCP）（2026-10-30 第十一轮）

- 依据：`docs/design/n13-agent-tool-ecosystem-mcp.md` + `spec.md`
- 基线：`master @ 1282a57` + 前十轮未提交成果（容器内 build/vet 基线通过）
- 测试环境：容器内（`golang:1.26.1-bookworm` / `node:22-bookworm-slim` / 运行中的 compose 栈）
- 完成定义：`spec.md` 第 5 节；每阶段结束跑容器内构建/单测，最后统一 E2E + 回归。
- 状态：已完成（2026-10-30）；容器证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND11.md`。

## 阶段 P0 — 基线与规格
1. 复核设计文档与实际代码差异，写 `spec.md` / `plan.md` / `tasks.md`（差异见 spec 第 8 节）。
2. 归档第十轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND9-*.md`。
3. 容器内基线：`go build ./...`、`go vet ./...`（确认起点无编译错误）。

## 阶段 P1 — 真白名单与 native 工具（A1/A2/A3）
1. `models/runtime.go`：确认 TaskContract；`models/tools.go` 新增 ToolDefinition/MCPServer/ToolGrant/ToolCallRecord；`models/agent_profile.go` 新增 AgentProfile/PromptVersion。
2. `models/graph.go`：PlanGraph 增加 ToolCalls、AgentSnapshot；TaskExecutionResult 增加 ToolCall。
3. `scheduler/executor.go`：`runtimeTask.Contract = task.Contract`；新增 tool_call 分支；注入 ToolInvoker/ProfileRegistry。
4. `planner/agent_planner.go`：`normalizeAssignedTo` 只对空值默认 general_agent，未知非空值保留并报错；`normalizePlannerAssignedTo` 支持 tool_call。
5. `planner/taskregistry.go`：新增 `tool_call` 类型（allowed: tool_executor + profile:*）；`allowsAgent` 支持 profile:*（LLM 类型）。
6. `internal/tools`：Store/Registry/schema/native/invoker；内置 native:echo/write_file/fail/sleep/big_output。
7. 单测：白名单拒绝、未知 agent、schema、native 调用、record。

## 阶段 P2 — 工具注册 API 与审批（A6）
1. `api/tools.go`：GET/POST/PUT/DELETE/test/schema + tool-grants。
2. `scheduler/runtime_gate.go`：新增 toolGate 钩子；`models/reproduction.go` 增加 RuntimeActionToolCall。
3. `api/runtime_actions.go`：tool_call 拒绝/批准后更新 ToolCallRecord。
4. `scheduler/scheduler_state.go`：markTaskCompleted/markTaskFailed 合并 ToolCallRecord。
5. 单测：pending→reject→不调用；pending→approve→调用；record.ApprovalID。

## 阶段 P3 — MCP（A4/A5）
1. `internal/tools/mcp_stdio.go`：JSON-RPC over stdio、initialize、tools/list、tools/call、超时/进程组 kill。
2. `internal/tools/mcp_http.go`：Streamable HTTP/SSE、SSRF、allowlist、auth header、MaxOutputBytes。
3. `cmd/mockmcp/main.go`：stdio + HTTP mock server（echo/slow/big/fail）。
4. `backend/Dockerfile`：构建并复制 mockmcp。
5. `api/tools.go`：MCP server 注册握手、fingerprint、stale 检测。
6. 单测：stdio helper 进程、HTTP httptest、SSRF、截断。

## 阶段 P4 — AgentProfile/PromptVersion（A7/A8）
1. `internal/agentprofile`：Store（Memory/File）、Registry（builtin seed）、render、eval、ProfileRunner。
2. `api/agent_profiles.go`：profile CRUD + prompt CRUD + activate + eval。
3. `scheduler/executor.go`：resolveRunner 支持 profile:<id>；ProfileRunner 调用模型。
4. `api/plan_import.go`：POST /api/plans/from-graph；创建时写 AgentSnapshot。
5. `api/plan_export.go`：导出包含 ToolCalls/AgentSnapshot。
6. 单测：profile 行为变化（fake completer）、版本不可变、回滚、eval。

## 阶段 P5 — 回放（A9）
1. `api/tool_calls.go`：GET tool-calls、POST replay（dry-run / 审批）。
2. `internal/tools/invoker.go`：DryRun 支持。
3. 单测：hash 一致、真实重放需审批。

## 阶段 P6 — 前端
1. `contracts/api.ts`：Tool/MCP/Grant/AgentProfile/PromptVersion/ToolCall 类型。
2. `services/api/researchAssistantApi.ts`：工具/Agent/Prompt/tool-call/replay 方法。
3. `features/settings/ToolsSettingsPanel.tsx`、`AgentSettingsPanel.tsx`；`SettingsPanel` tab 容器。
4. `features/plan-graph`：tool_call 节点样式 + 侧栏参数/结果/审批/耗时。
5. `features/review/ApprovalInboxPanel.tsx`：tool_call kind。
6. 组件测试。

## 阶段 P7 — 容器回归、E2E、清理
1. 后端全量：`go build ./... && go vet ./... && go test ./...`；新增包 `-race`。
2. 前端容器：`npm ci && npm test && npm run build && npm run lint`。
3. 重建 backend/frontend 镜像 + compose 起栈。
4. 新增 E2E `tool-ecosystem.mjs`（A1–A10）。
5. 回归既有 E2E。
6. 清理临时容器/镜像/卷/脚本/产物；删除死代码。
7. 更新 README/OpenAPI/FIX-REPORT-ROUND11/spec/plan/tasks。

## 风险与对策
- ToolCallRecord 与 plan 双写：由 scheduler 在状态迁移内合并，executor 只返回结果。
- MCP stdio 子进程：context 超时 + Setpgid + kill 进程组；每次调用独立进程，避免泄漏。
- MCP HTTP SSE：严格按 id 匹配响应；限制总字节与事件数量。
- SSRF：DNS 解析后拒绝私网；allowlist 精确 host；仅管理员 allow_private_hosts。
- 未知 Agent 改动：只对非空未知值报错，空值仍默认 general_agent。
- 旧计划兼容：ToolCalls 零值不影响执行；AgentSnapshot 零值不校验。
- 前端设置页：拆三个面板，复用 ModelSettingsPanel 样式；组件测试覆盖。
- 容器资源：测试用 --rm；结束后清理 dangling 镜像/卷，不删除 compose 数据卷。
