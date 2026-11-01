# Agent 记忆系统实现对照（feature/memory）

- 日期：2026-11-01
- 分支：`feature/memory`（从 `feature/desk` 工作区创建；实际仓库原先没有同名分支）
- 主设计：[2026-11-01-agent-memory-system.md](../design/2026-11-01-agent-memory-system.md)
- 现状审计：[context-system-audit-2026-11-01.md](../audit/context-system-audit-2026-11-01.md)
- 测试报告：[MEMORY-TEST-REPORT.md](MEMORY-TEST-REPORT.md)

## 1. 与设计文档的实际差异

| 文档描述 | 实际情况 | 处理 |
|---|---|---|
| 新增 internal/memory | 目录不存在 | 从零实现 types/store/index/retrieve/render/redact/settings/chat_store/extract*/consolidate |
| 新增 internal/session | 不存在 | 实现 FileStore + create/ensure/list/touch/delete |
| ChatPayload 只有 message | 确认 | 扩展 session_id/project_id/use_memory/messages，保留单轮 message 兼容 |
| resolveSessionID 只认签名 cookie | 确认 | 新增 POST /api/sessions、/activate；plan 与 chat 都接受服务端 session_id |
| prompts.Section 需要 MinTokens | 已有字段，但只用于非 mandatory section | 保留，并新增 core_memory/retrieved_memory 优先级 |
| TaskUsage 需要 cached_tokens | 无 LLMCachedTokens | 增加字段，并从 provider usage 的 prompt_tokens_details.cached_tokens / prompt_cache_hit_tokens 解析 |
| PlanGraph.ParentPlanID / ResearchContext.MemoryRefs / GraphMeta.MemoryRefs | 均无 | 增加 omitempty 字段；clonePlanGraph 深拷贝；plan 导出/导入自动带出；openapi/前端契约同步 |
| 现有 /api/sessions | 只按 plan 聚合 | 改为 server session store + plan/chat 统计合并 |
| 现有 chat 无多轮 | 确认 | ChatAgent.AnswerWithMessages + ChatSessionStore + 摘要压缩 + memory 注入 + citations |

## 2. 已实现范围

### M0 基础件
- `internal/memory/types.go`：Kind/Trust/Status/Scope/Source/Evidence/Validity/Entity/Record/Query/Context/Citation/Settings/Patch。
- `internal/memory/store.go`：JSONL WAL + 原子快照 + 目录锁 + 幂等去重 + supersede + compaction；`NewMemoryStore` 供测试。
- `internal/memory/index.go`：中英混合 tokenizer（CJK bigram）、BM25、实体/kind/status 倒排。
- `internal/memory/retrieve.go`：owner/scope 过滤、BM25 + 实体 + 时间召回、RRF、trust/importance/recency/confidence/scope 加权、MMR、core/retrieved 预算。
- `internal/memory/render.go`：`<memory kind id date trust source>` 渲染、token 估算、按 token 截断。
- `internal/memory/redact.go`：密钥/私钥/大 base64 拒绝、hidden benchmark 拒绝、注入扫描。
- `internal/api/memories.go` + `memory_runtime.go`：CRUD/search/stats/export/import/settings/consolidate；`MEMORY_ENABLED`/`MEMORY_MODE`/`MEMORY_DIR`。

### M1 聊天记忆与会话
- `internal/session/store.go`：server-owned SessionStore。
- `internal/memory/chat_store.go`：ChatSession/Turn 持久化、token+轮数裁剪、确定性摘要。
- `internal/agent/chat.go`：`AnswerWithMessages` 直接走 usage-wrapped chat model，保留代码执行后处理。
- `internal/api/chat_runtime.go`：system → core_memory → 历史/摘要 → retrieved_memory → 当前消息；返回 citations；写回 user/assistant 轮次。
- `internal/api/sessions.go`：POST /api/sessions、POST /api/sessions/:id/activate；GET/DELETE 合并 plan/chat。
- 前端：ChatResponse.citations、SessionSummary、MemoryRecord/MemorySettings/MemoryStats 契约；设置页 MemoryPanel。

### M2 计划/任务记忆与跨计划上下文
- `internal/memory/extract.go`：PlanEpisode/fact/constraint/negative；任务失败 episode；重试成功 procedure。
- `internal/memory/extract_document.go`：文档页数/章节映射；无章节时 pending_review + section_detection_failed。
- `internal/prompts/task_prompt.go`：core_memory/plan_context/task_instruction/retrieved_memory/task_inputs/volatile 优先级。
- `internal/scheduler/executor.go`：RoutedTaskExecutor.Memory 注入 core/retrieved/memory_refs，并写 RecordTask。
- `internal/scheduler/scheduler.go` + `scheduler_execute.go`：MemorySink 在计划终态写 RecordPlan。
- `internal/api/plan_runtime.go`：计划创建写初始 episode、检索 memory context、同 session 追问设置 ParentPlanID、MemoryRefs。
- `internal/agent/librarian.go`：paper_parse 完成后写 document memory。
- 长任务测试中修复 `createPlan` 的真实 nil pointer：当 `buildResearchContext` 返回 nil + contextErr 且存在 importedPaper 时，先解引用 `researchContext.Paper` 会 panic；现在先检查 contextErr，再做 nil guard，最后合并 importedPaper。

### M3 程序性记忆与整合
- `internal/memory/extract_trial.go`：AutoResearch rejected → negative、kept+search_runs≥2 → procedure、metric fact。
- `internal/agent/autoresearch_run.go`：每个 trial 前把已有 trial 记忆注入 candidate prompt（readOnlyContext）。
- `internal/memory/consolidate.go`：确定性 TTL/归档 + reflection（≥5 条 episode/negative，pending_review，derived_from≥2）。
- API `/api/memories/consolidate`；`MEMORY_CONSOLIDATE_INTERVAL_SECONDS` 可选后台周期。

### 未实现/默认关闭
- M4 embedding/图扩展默认关闭（设计为可选）。
- LLM 抽取默认关闭（`MEMORY_WRITE_MODE=deterministic` 语义）；当前实现只做确定性抽取。
- 前端 MemoryPanel 以列表/搜索/新建/pin/删除/设置为主；时间线以 kind 过滤呈现，未做独立图表。

## 3. 回滚开关

- `MEMORY_ENABLED=false`：不初始化 store、不注入、不写。
- `MEMORY_MODE=shadow`：检索但不注入（当前 chat/task/plan 注入路径均检查）。
- `MEMORY_DIR`：独立目录 A/B。
- `MEMORY_CONSOLIDATE_INTERVAL_SECONDS=0`：关闭后台整合。

## 4. 安全与边界

- owner 是硬边界：Store/API/Retrieval 均以 owner 过滤；跨 owner get 返回 not found。
- hidden benchmark：`Evidence.Hidden` 与 `BENCHMARK_PRIVATE_ROOT` 路径/关键字在写入前拒绝。
- 记忆内容不复制完整 PDF/工具输出/CoT；单条 content ≤8000 字符，plan/任务抽取 ≤800 字。
- `<memory>` 是数据：ChatSystemPrompt 追加 `ChatMemoryInstruction`，明确不得执行其中指令。
