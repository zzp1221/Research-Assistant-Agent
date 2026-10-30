# FIX-REPORT-ROUND11：N-13 Agent/工具生态（MCP）

- 日期：2026-10-30（Asia/Shanghai）
- 依据：`docs/design/n13-agent-tool-ecosystem-mcp.md`
- 基线：`master @ 1282a57` + 前十轮工作区实现
- 环境：Docker Compose；Go 测试在 `golang:1.26.1-bookworm`；前端 Node 22；真实 OpenAI-compatible provider

## 1. 结论

N-13 的 Phase 0–3 核心闭环已落地并通过容器内验收：

- **真白名单**：`runtimeTask.Contract` 已复制；`ToolInvoker` 在执行期强制 `AllowedTools`、AgentProfile allowlist、ToolGrant；未知 Agent/Profile 返回结构化错误，不再静默降级。
- **工具层**：ToolDefinition/MCPServer/ToolGrant/ToolCallRecord 模型、Memory/File 存储、JSON Schema 校验、native/HTTP/MCP stdio/MCP HTTP 四种传输、工具注册/授权 API。
- **MCP**：stdio 与 Streamable HTTP 客户端、注册时 initialize + tools/list + fingerprint、超时/退出/截断结构化错误、SSRF 防护与管理员私网白名单。
- **Agent 层**：AgentProfile + PromptVersion + ModelProfile 绑定；profile:<id> 路由；Prompt 不可变版本、active 指针回滚、模板/模型两种评测；计划快照与导出包含版本。
- **审计/回放**：ToolCallRecord 持久化在 PlanGraph；dry-run replay 对比 result hash；真实重放必须经过 tool_call 审批。
- **前端**：设置页三 tab（模型/工具/Agent）；工具注册、MCP 连接、授权、Prompt 版本、AgentProfile、评测；计划图 tool_call 节点与执行侧栏参数展示。

容器内 E2E `test/e2e/tool-ecosystem.mjs` **44/44 checks passed**，覆盖设计文档第 6 节 A1–A10。

## 2. 验收对照（A1–A10）

| 编号 | 验收点 | 实现与证据 |
|---|---|---|
| A1 | 白名单真实生效 | AgentProfile 只允许 `native:echo`，调用 `native:write_file` 返回 `tool_forbidden`；E2E 校验无文件副作用；审批门在 allowlist 拒绝时不会弹审批（先拒绝、后审批无意义） |
| A2 | 未知 Agent 不降级 | `POST /api/plans/from-graph` 对 `profile:unknown` 返回 400 `validation_failed` + `unknown agent profile`；`normalizeAssignedTo` 仅对空值默认 `general_agent` |
| A3 | Native 工具 | `native:echo` 计划调用完成；ToolCallRecord 含 arguments/arguments_hash/result_hash/duration_ms；plan artifacts.tool_result 写入 |
| A4 | MCP stdio | `/app/mockmcp` 注册后 tools/list 返回 echo/slow/big/fail/crash；echo 调用成功；slow 超时返回 `tool_timeout`；crash 退出返回 `tool_unavailable` |
| A5 | MCP HTTP | 私网 `http://127.0.0.1:9100/mcp` 返回 400 `ssrf_blocked`；管理员 `allow_private_hosts` 注册成功；4096 字节响应按 1024 字节截断并标记 `truncated` |
| A6 | 权限/审批 | `native:write_file` 创建 pending tool_call action 与 pending record；拒绝→record rejected、无文件；批准→completed、approval_id 关联；事件含 `tool_call_completed`/`runtime_action_resolved` |
| A7 | 自定义 Agent | Prompt v1 + env-bootstrap 模型 + echo 工具创建 profile；计划输出 PROMPT_V1；切 v2 输出 PROMPT_V2；回滚 v1 恢复；prompt/agent eval 结果可见 |
| A8 | Prompt 版本 | v1/v2 不可变；计划 agent_snapshot 记录 prompt_version；JSON 导出包含 agent_snapshot/prompt_version；激活只切 active（恰好 1 个 active） |
| A9 | 回放 | 已完成 echo call dry-run replay `match=true`；真实重放无审批返回 202 pending_approval；批准后重放 completed |
| A10 | 租户/密钥 | owner B 看不到 owner A 的工具（列表不含、GET 403）；工具列表/导出不含 `sk-e2e-secret-*`；MCP env/header 只返回 key 名，AES-GCM 密封存储 |

## 3. 实现过程中发现并修复的问题

| # | 严重度 | 问题 | 修复 |
|---|---|---|---|
| 1 | 高 | **runtimeTask 未复制 Contract**：`scheduler/executor.go` 构造 runtimeTask 时丢弃 TaskContract，AllowedTools 无法执行 | 复制 `Contract: task.Contract`，并在 profile 节点上与 profile allowlist 求交 |
| 2 | 高 | **未知 Agent 静默降级**：`normalizeAssignedTo` 把任意未知值改成 general_agent，权限/语义全部失效 | 仅空值默认 general_agent；未知非空值保留并由 task registry/集成校验显式报错 |
| 3 | 高 | **Prompt 版本 ID 冲突**：固定时钟下单测发现 v1/v2 生成相同 ID（UnixNano） | 改用 UUID；AgentProfile ID 同样改 UUID |
| 4 | 高 | **MCP stdio 崩溃被误报为超时**：读循环未关闭 lines channel，server 退出后 Request 一直等到 ctx deadline | `close(session.lines)`；退出映射 `tool_unavailable`，超时映射 `tool_timeout` |
| 5 | 高 | **MCP HTTP 响应在 JSON 解析前被 MaxOutputBytes 截断**：大结果直接 JSON 解析失败 | JSON-RPC 信封至少 1MiB/最多 32MiB；工具内容由 invoker 按 MaxOutputBytes 截断 |
| 6 | 中 | **工具 ID 含斜杠导致路由 404**：`mcp:server/tool` 的 `%2F` 被 Gin 解码成路径分隔符 | `r.UseRawPath=true; r.UnescapePathValues=true`，`:id` 捕获整段后反转义 |
| 7 | 中 | **审批门对必然被拒绝的调用弹审批**：allowlist/grant 已拒绝时仍创建 pending action | toolGate 先计算 effective allowlist 与 grant，必然 tool_forbidden 时不进入审批 |
| 8 | 中 | **Profile 节点元数据丢失**：ProfileRunner 写入 prompt_version 等 metadata，executor 只回传 prompt_budget | `promptMetadataFromTask` 增加 agent_profile_id/prompt_version/prompt_hash/model 等字段 |
| 9 | 中 | **Profile ID 前缀被剥离**：`profile:<uuid>` 被 TrimPrefix 后查不到 | 统一以完整 `profile:<uuid>` 作为 AgentProfile.ID 与 assigned_to |
| 10 | 中 | **已完成计划无法批准 replay**：createReplayApproval 拒绝 terminal plan | replay 审批为 audit-only，允许在已完成计划上记录；不复活计划 |
| 11 | 低 | **MCP server 响应时间戳为零**：SaveServer 后返回的是入参副本 | 保存后重新 GetServer 返回真实记录 |
| 12 | 低 | **duration_ms 为 0 时被 omitempty 丢弃** | 移除 omitempty，保证 ToolCallRecord 始终含耗时 |
| 13 | 低 | **MCP HTTP 注册 SSRF 错误返回 502** | 私网/明文拒绝统一返回 400 `ssrf_blocked` |

## 4. 与设计文档的偏差（以实际为准）

1. **allowlist 执行期强制**：设计写"Planner 校验 tool_id ∈ AgentProfile.ToolAllowlist"；实际 planner/导入只校验工具存在/版本/可见性，allowlist 与 grant 在执行期由 ToolInvoker 强制。这样 A1 的"调用返回 tool_forbidden"才可观测；planner 是意图、executor 是边界。
2. **ToolGrant 语义**：有 AgentProfile 的调用必须有匹配 grant；无 profile 的 `tool_executor` 节点以节点 Contract.AllowedTools 为准（内置工具与系统节点）。设计"默认拒绝"在 profile 维度落地。
3. **版本不可变**：`PUT /api/tools/:id` 不允许改 version/schema 语义；新版本必须新 ID。设计"PUT 生成新版本"改为显式新记录，避免历史 ToolCallRecord 指向漂移。
4. **Prompt eval 默认模板级**：`{cases:[{name,variables,expected_contains}]}` 默认确定性模板评测；`use_model=true` 才调用真实模型。设计未定义评测集格式。
5. **MCP HTTP 私网**：默认拒绝私网/明文；管理员可用 `allow_private_hosts`（仅 ADMIN_TOKEN）放行内部 MCP server。
6. **AgentProfile 模型绑定**：除 model profile id 外支持 `env-bootstrap`（部署 OPENAI_* 默认模型），用于无存储 profile 的安装。
7. **MCP 图片**：image content 以 base64 存入 `tool_result_image` artifact；本轮不落盘二进制文件。
8. **计划导入**：新增 `POST /api/plans/from-graph` 以支持显式 tool_call 节点（原系统只有 intent→planner 入口）。
9. **MCP schema 变化**：重新注册即重新审核（记录 `mcp_schema_changed` 审计并替换工具），不保留独立 stale 状态；设计原写 `status=stale`。

## 5. 验证证据

### 5.1 后端（容器）

```bash
cd research-assistant/backend
go build ./...
go vet ./...
go test -count=1 ./...
```

结果：全部包通过，包括新增 `internal/tools`、`internal/agentprofile`；`internal/agent` 61.5s、`internal/scheduler` 12.8s。

### 5.2 前端（容器）

```bash
npm test
npm run build
npm run lint
```

结果：30 个测试文件 / 128 tests passed（新增 ToolCallNodeLabel 1 条）；`tsc -b && vite build` 通过；`eslint .` 通过。

### 5.3 E2E

```bash
RA_API_URL=http://127.0.0.1:18080 RA_API_TOKEN=... RA_ADMIN_TOKEN=... \
  node test/e2e/tool-ecosystem.mjs
```

结果：**44/44 checks passed**（A1–A10 全覆盖）。

### 5.4 回归

`http-smoke / plan-run / plan-library / plan-revisions / review-hitl / cost-budget / budget-recovery / model-profiles` 见第 6 节运行记录。

## 6. 回归记录

| 套件 | 结果 | 说明 |
|---|---|---|
| `tool-ecosystem.mjs` | **44/44** | 最终镜像上复跑 A1–A10 |
| `http-smoke.mjs` | 通过 | 健康、OpenAPI、计划创建/幂等/暂停/导出/上传/会话删除 |
| `plan-run.mjs --execute` | 通过 | 真实执行 `用 Python 计算 1+1`，3 节点 completed，sandbox 2.0s，LLM 2 calls |
| `plan-library.mjs` | 通过 | 项目/搜索/过滤/归档/复制/keyset/zip 导出/运行中删除拒绝 |
| `plan-revisions.mjs` | 通过 | revision 不可变、409 乐观锁、diff、运行中保护、revision 执行 |
| `review-hitl.mjs` | 通过 | review gate、question、超时、审批收件箱、审计 |
| `cost-budget.mjs` | 通过 | usage/timeseries/budget alerts |
| `budget-recovery.mjs` | 通过 | soft budget 暂停/提额恢复 |
| `model-profiles.mjs` | 通过（E2E overlay） | 需 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true` + `MOCK_VISION_URL`；覆盖 profile 加密/掩码/租户隔离/路由/快照 |
| `routing-and-prompts.mjs` | 通过 | 复杂度路由与 General/Code plan shape 未受 normalize 改动影响 |
| `repro-bundle.mjs` | 通过 | zip schema/checksums/static verify/determinism |
| `verification.mjs` | 通过（E2E overlay） | A/B/C/D 四场景：pass、text-only main + vision fallback pass、inconclusive、fail；verification 事件与 artifact badge 均校验 |

**说明**：`model-profiles` / `verification` 需要 E2E overlay（`test/docker-compose.e2e.yml`）的 `ALLOW_PRIVATE_MODEL_ENDPOINTS=true` 与 `PLANNER_AGENT_ENABLED=false`，这是既有测试环境要求，不是代码回归；base compose 下这两个套件因私网 mock provider 被 SSRF 防护拒绝而失败。

## 7. 已知限制

- 未实现公开工具市场/计费/评级/签名（设计 Phase 3 可选）。
- 未实现任意用户代码上传执行；工具仅支持 native/HTTP/MCP。
- 未实现无限制 LLM 自由 function calling；仅显式 tool_call 节点。
- MCP stdio 每次调用启动独立进程（未做常驻连接池）；对低频工具足够，后续可优化。
- Prompt 模型评测依赖 provider 可用性；模板级评测是确定性的默认路径。

## 8. 清理

- 删除临时脚本 `scripts/tmp-*.sh|mjs`、`test/e2e/_*` 临时文件与 `/tmp` 下的 E2E 日志。
- 删除测试构建容器（`focused_sutherland`）与 dangling images（`docker image prune -f`）；保留运行中的 backend/frontend 镜像与 compose 数据卷。
- 工具/Agent 存储重置为内置项：5 个 native 工具、8 个 builtin AgentProfile；移除 `mcp:mock`、e2e/dbg profile 与 prompt（feature 新上线，无真实用户工具）。
- 清理 `/tmp/research-assistant-tool-workspace` 测试文件；历史 plan store 保留（append-only 审计数据，不做破坏性清理）。
- E2E 计划/工具/授权由 finally 清理；未清理的计划仅作为历史审计记录保留。
