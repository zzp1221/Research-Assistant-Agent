# 任务清单：N-13 Agent/工具生态（MCP）（2026-10-30 第十一轮）

## P0 基线与规格
- [x] 复核设计文档与实际代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 归档第十轮 spec/plan/tasks（ROUND9-*）
- [x] 容器内基线 go build/vet

## P1 真白名单与 native 工具
- [x] models/tools.go（ToolDefinition/MCPServer/ToolGrant/ToolCallRecord）
- [x] models/agent_profile.go（AgentProfile/PromptVersion）
- [x] PlanGraph.ToolCalls/AgentSnapshot；TaskExecutionResult.ToolCall
- [x] runtimeTask.Contract 复制
- [x] normalizeAssignedTo 未知值不降级
- [x] taskregistry tool_call + profile:*
- [x] internal/tools Store/Registry/schema/native/invoker
- [x] 内置 native 工具 seed
- [x] 单测 A1/A2/A3

## P2 工具 API 与审批
- [x] api/tools.go CRUD/test/schema
- [x] api/tool_grants
- [x] RuntimeActionToolCall + toolGate
- [x] runtime action 拒绝/批准联动 ToolCallRecord
- [x] scheduler 合并 ToolCallRecord
- [x] 单测 A6

## P3 MCP
- [x] mcp_stdio client
- [x] mcp_http client（SSE）
- [x] cmd/mockmcp（stdio/http）
- [x] Dockerfile 构建 mockmcp
- [x] MCP 注册握手/fingerprint/stale
- [x] 单测 A4/A5

## P4 AgentProfile/PromptVersion
- [x] internal/agentprofile Store/Registry/render/eval/runner
- [x] api/agent_profiles.go + prompt versions + activate + eval
- [x] resolveRunner profile:*
- [x] POST /api/plans/from-graph
- [x] 导出包含 ToolCalls/AgentSnapshot
- [x] 单测 A7/A8

## P5 回放
- [x] GET /api/plans/:id/tool-calls
- [x] POST replay dry-run/审批
- [x] 单测 A9

## P6 前端
- [x] contracts/api.ts 类型
- [x] api client 方法
- [x] ToolsSettingsPanel
- [x] AgentSettingsPanel + SettingsPanel tabs
- [x] GraphPanel tool_call 展示
- [x] ApprovalInbox tool_call
- [x] 组件测试

## P7 回归、E2E、清理
- [x] 后端容器 build/vet/test 全绿
- [x] 前端容器 test/build/lint 全绿
- [x] 重建镜像 + compose 起栈
- [x] 新增 E2E tool-ecosystem.mjs（44/44）
- [x] 既有 E2E 回归
- [x] 清理临时产物/镜像
- [x] FIX-REPORT-ROUND11

## 未实现（明确记录）
- [ ] 公开工具市场/计费/评级（设计 Phase 3 可选）
- [ ] 任意用户代码上传执行（非目标）
- [ ] LLM 自由 function calling（非目标；仅显式 tool_call 节点）
