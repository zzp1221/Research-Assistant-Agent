# Research Assistant Agent 上下文系统审计

- 日期：2026-11-01
- 范围：research-assistant/backend/internal/{prompts,agent,scheduler,planner,documents,api}、frontend/src/app/hooks
- 方法：代码审计 + 隔离容器实测（确定性 mock LLM 记录真实请求消息）+ 真实 compose 栈会话行为验证
- 结论：**有需要改进的地方，且不是单点问题。** 当前实现具备 PromptBudget、Section 优先级、Repair 多轮会话、ResearchContext、两阶段文档检索等基础件，但“会话连续性、输入分配公平性、长意图去重、PDF 段落检索、可观测性”五个环节存在系统性缺口；其中聊天多轮记忆完全缺失、输入预算会被单个大输入吃光、PDF 检索对常见排版几乎失效，属于会直接影响用户结果的高优先级问题。

---

## 1. 测试环境与方法

| 项 | 说明 |
|---|---|
| 真实栈 | research-assistant compose：backend 127.0.0.1:18080、frontend 15173、sandbox 8082、intent 18000、optimizer 8090，均 healthy |
| 真实 LLM | 不可用：api.ai-pixel.online 对配置的 deepseek-v4.1-flash-1017 与 deepseek-v4.1-flash 均返回 no_eligible_routing_group；与仓库报告 reports/agent-pipeline-and-cache-review.md 记录一致 |
| 隔离审计栈 | ra-ctx-backend（同 compose 网络、独立数据卷、ALLOW_PRIVATE_MODEL_ENDPOINTS=true）+ ra-ctx-mock（确定性 OpenAI-compatible mock，把每次请求的 messages/角色/字符数写入 /log/requests.jsonl）。仅用于观察“模型实际收到什么”，不改动项目代码与真实数据 |
| 测试样本 | 3 轮连续 chat；同 session 两个 plan；18,030 字中文长意图 plan；14,543 字论文类长意图 plan；15 页 Attention PDF；13 页 Apriori PDF；276,006 字中文 chat |
| 临时 Go 探针 | zz_audit_context_test.go（inputs 预算、Session Trim）运行后已删除，未留在工作区 |
| 证据文件 | reports/context-audit/prompt_requests.jsonl（20 条真实请求记录）、reports/context-audit/mock_llm_server.py |

> 说明：真实栈的 /api/chat 因 provider 不可用而无法完成生成，但会话行为（session id、请求是否带历史）不依赖 provider，仍可在真实栈验证；prompt/消息级证据来自隔离审计栈。

---

## 2. 当前上下文系统的组成（已实现的部分）

1. **Prompt 预算**：prompts.PromptBudget = ContextWindow − MaxOutput − Safety（默认 128000−8192−2000=117808），支持环境变量与模型 profile 的 context_window_tokens 覆盖（prompts/budget.go:20-55）。
2. **Section 优先级**：system(0)/plan(1)/task(2)/primary input(3)/volatile(5)，按优先级 first-fit 分配，强制 section 超预算 fail-closed（prompts/budget.go:130-199）。
3. **单一任务 Prompt 组装**：BuildTaskPrompt 把 plan_context、task_instruction、task_inputs、volatile 各注入一次（prompts/task_prompt.go:104-155）。
4. **Repair 多轮会话**：conversation.Session 保留 system+首条 user，追加 assistant/user，按 MaxTurns=6 裁剪（agent/conversation/session.go）；Planner/PaperDebug/BenchmarkAdapter/AutoResearch/Coder 已接入。
5. **类型化 ResearchContext**：附件、文档、论文元数据、Entities、Constraints（models/context.go:6-15），由 api/context_resolver.go 构建。
6. **两阶段文档检索**：Outline 选 section → BM25 按 section 检索 → 合并并加页码/章节标签（agent/librarian.go:218-291）。
7. **输入绑定**：planner.ContextBinder 按 task.Inputs → InputBindings → RequiredArtifacts 绑定（planner/context_binder.go:54-99）。
8. **Prompt 报告**：每个任务记录 prompt_budget sections 与截断标记，并在 usage 中累计 PromptBudgetLimit/PromptTruncatedSections（scheduler/executor.go:316-336、scheduler/scheduler_dispatch.go:248-260）。

这些基础件方向正确，问题主要出在“怎么分配、怎么保留、怎么检索、怎么让用户看见”。

---

## 3. 主要问题（按优先级）

### C1【P0】Chat 完全没有多轮记忆：前端有历史，后端只收到当前一句

- **代码事实**：ChatPayload 只有 Message 一个字段（backend/internal/api/routes.go:46-48）；/api/chat 每次把 payload.Message 传给 ChatAgent.Answer（routes.go:171-188）；ChatAgent 的 Prompt_Builder 固定 system+当前 user 两条消息（agent/chat.go:59-65）。前端虽然把 chatHistory 存在 localStorage 并完整渲染，但发送时只调用 chat(userPrompt, identity)（useResearchAssistantChatFlow.ts:600-604、researchAssistantApi.ts:256-259），从不回传历史。
- **实测证据**：同一 X-User-Id/X-Session-Id 连续 3 轮 chat，mock 收到 3 次请求，每次都是 messages=2（system+user），没有任何历史；第二轮问“我刚才说我喜欢的颜色是什么？”时，请求仍只有 system+当前问题。用 {"messages":[...]} 调用直接 400（ChatPayload.Message required）。
- **影响**：用户看到“多轮对话”的 UI，但模型实际上每轮失忆；追问、指代消解、逐步修改全部失效；也无法利用对话前缀缓存。这是上下文系统最直接的缺口。
- **相关**：chat 历史只存在浏览器 localStorage，清缓存/换设备即丢失；服务端没有 chat session 存储。

### C2【P0】前端 session 与后端 session 是两套 ID，服务端会话分组失真

- **代码事实**：前端自己生成 session.id 并只存在 localStorage（useResearchAssistantChatFlow.ts:140-152），请求时 buildIdentityHeaders 实际返回空对象（researchAssistantApi.ts:58-61）；后端 resolveSessionID 优先使用签名的 ra_session cookie，X-Session-Id 仅在显式 legacy 测试开关下生效（api/cookies.go:160-183）。
- **实测证据**：真实栈上同一 cookie jar、两次请求分别带 X-Session-Id: ui-session-AAA 与 ui-session-BBB 创建 plan，返回的两个 plan session_id 都是同一个服务端值 b8c71ce3-ab28-41a5-86d7-0569df9b46d8；/api/sessions 只返回这一个 session（plan_count=2），两个 UI 会话被合并。
- **影响**：UI 的“切换会话”只是前端视图切换；服务端按 session 分组的计划、历史与缓存都会混在一起。若未来把 chat 历史落到服务端，必须先统一 session 身份，否则会继续错位。

### C3【P1】输入预算“先到先得”：一个超大输入可以饿死后面所有小输入

- **代码事实**：FormatTaskInputsForPromptWithPolicy 把每个 input 包成 Priority: 3 的 Section，并按 key 字典序排序（prompts/inputs.go:38-59）；BuildSectionsWithLimit 对同优先级 section 按顺序 first-fit 分配（prompts/budget.go:144, 157-195）。没有任何 per-section 最小值/配额/按重要性排序。
- **实测证据**（临时 Go 探针）：{"a_huge": 200000 个字符, "z_important": "MUST-KEEP-PARAM"}、预算 2000 token → a_huge 占满 1982 token（truncated=true），z_important 被 Dropped=true，尽管它只需要 8 token。最终 prompt 里完全看不到 MUST-KEEP-PARAM。
- **影响**：上游日志/长 artifact 一大，用户问题中的关键参数、短约束、小证据就会静默消失；且因为按 key 排序，不是按重要性，结果具有“字母序决定上下文”的不可解释性。

### C4【P1】结构化输入被截断成非法 JSON，模型收到坏数据

- **代码事实**：renderPromptValue 对 []any/map[string]any/[]string 先 json.Marshal，超过 maxRunes 后直接 TruncateRunes(text, maxRunes) + "...[truncated]"（prompts/inputs.go:85-94）。这是对“JSON 字符串”做字符截断，不是对 JSON 内部字段做截断。
- **实测证据**（临时 Go 探针）：3 个 uploaded_files、每个 20,000 字符 text_excerpt，默认每值 24,000 runes → 渲染结果 24,064 字符，尾部为 ...[truncated]；json.Unmarshal 解析失败：invalid character 'm' looking for beginning of value。真实 plan 中 paper_parse 节点的 uploaded_files 就带 24,015 字符 text_excerpt，已接近这个边界。
- **影响**：模型看到的是半截 JSON，无法可靠解析附件列表/参数；多附件时经常触发。比单纯“少一些上下文”更糟，因为它是坏数据。

### C5【P1】长意图被复制进每个节点，plan_context 反而被去重成空，计划体积和 token 双膨胀

- **代码事实**：模板所有节点都用 newNode(..., context)，context = intent.RawIntent（如 planner/planner_templates.go:390-430）；newNode 把 prompts.TaskDescription(displayName, context) 写入 TaskNode.Description（planner/planner.go:293-310、prompts/prompts.go:798-800）。同时 PlanContextForTask 用 strings.Contains(taskDescription, intent) 判断重复，命中后把 plan_context 中的 User intent 删掉（prompts/task_prompt.go:68-88）。
- **实测证据**：
  - 18,030 字中文长意图、General research 计划仅 2 个节点，但每个节点 description ≈18,078 字；两次执行请求分别 18,615/18,739 字符。prompt_budget 显示每个节点 plan_context 只有 7 token、task_instruction 9,497 token——plan 级稳定上下文基本为空，全部意图塞在 task 里。
  - 14,543 字论文类长意图、16 节点 Paper_Reproduction 计划：节点 description 总计 219,442 字符（估算 ≈138k token），整个计划 JSON 815,514 字节；单节点最大 ≈9,379 token。
- **影响**：同一段用户意图在 N 个节点重复 N 次，既浪费 token/存储，又破坏缓存前缀的稳定性（每个 task 的 instruction 都不同，plan_context 只有 7 token）；长意图时计划文件可达 MB 级，导出/历史/SSE 都会变重。MAX_INTENT_CHARS=20000 只限制入口字符，没有限制“意图在计划中的放大倍数”。

### C6【P1】PDF 段落识别对常见排版失效，论文检索上下文几乎为空

- **代码事实**：looksLikeSectionHeading 判定“首字符是数字且含空格/点”或“全大写且长度≥4且不含点”即 heading（documents/store.go:192-203）；buildChunks 把该行作为后续所有 chunk 的 section，包括表格和图片（store.go:112-189）。selectLibrarianSections 再用关键词/描述重叠打分（agent/librarian.go:343-397），retrieveDocumentContext 只对这些 section 做 BM25，没有有效 section 时退回全文档（librarian.go:238-290）。
- **实测证据**：
  - 15 页 Attention PDF（53,558 字符）检测出 126 个“heading”，包括 23.75、QK T、BLEU、1.0 · 10 20；实际 3.1/3.2 没带标题，Introduction（Title Case）根本不被识别。
  - 执行 paper_parse 时事件显示 [retrieval] sections=1 chunks=1 queries=1 sections_list="1: Long Papers), pages 434–443. ACL, August 2013."；prompt_budget.volatile 仅 108 token——模型实际上几乎没读到论文正文，却被要求“解析并总结论文”。
  - 13 页 Apriori PDF 同样出现 2. Use the large itemsets...、1) L1 = ... 等正文行被当作 section。
- **根因之二**：retrieveDocumentContext 把整个 task.Description 当 BM25 查询（librarian.go:140），而 Description 里是“任务目标/具体要求/完整用户意图”，包含大量非检索词，进一步稀释 BM25；librarianRetrievalQueries 又在 base 后拼接 method/experiment 等词，base 本身已很长（librarian.go:295-320）。
- **影响**：这是“上下文系统”里对结果影响最大的检索缺陷：关于论文的任务可能只拿到 1 个错误 chunk（108 token）就开始生成分析，结果必然泛化/编造；无论 prompt 预算多大都无法补救。

### C7【P1】多轮 Session 只按“轮数”裁剪，不按 token；旧 trial 被直接丢弃且没有摘要，repair_session 也没落盘

- **代码事实**：Session.Trim 只比较 len(Messages) 与 MaxTurns（默认 6），保留 system+first user 和最后 10 条消息，中间轮次直接丢弃（conversation/session.go:72-89）；没有 token 计数、没有摘要、没有单条消息长度上限。Session.Record 把快照写进 Task.Metadata["repair_session"]（session.go:137-146），但 promptMetadataFromTask 的白名单没有 repair_session（scheduler/executor.go:318-336），所以执行结束后不会持久化到 PlanNode。
- **实测证据**：8 轮 trial feedback 后 messages=13 turns=6，保留的 user turns 是 trial 4–8，trial 1–3 被静默丢弃（无摘要、无记录）。设计文档 docs/design/2026-10-27-agent-chain-optimization.md:599-605 明确要求“中间轮次压缩成摘要”，实现只做了截断。
- **影响**：AutoResearch 最多 8 trial、PaperDebug 2 次 repair，模型会忘掉早期假设/失败原因，容易重复同一错误；同时 6 轮中若某条 assistant 输出很大，仍可能超 context（无 token 上限）。Round5 报告声称“Task.Metadata[repair_session] 记录 session_id/turns/prompt_hashes”，实际只在运行时对象上存在，计划持久化后不可审计。

### C8【P2】上下文预算与截断对用户不可见，缓存命中也没有被测量

- **代码事实**：TaskUsage 有 PromptBudgetLimit/PromptTruncatedSections（models/runtime.go:69-71），但前端 TaskUsage 契约没有这两个字段（frontend/src/contracts/api.ts:299-311），任务详情只显示 llm_calls/prompt+completion tokens/cost（features/execution/ExecutionSidebar.tsx:549）。RecordUsage 只读 PromptTokens/CompletionTokens，完全忽略 provider 的 prompt_tokens_details.cached_tokens（llmclient/generate.go:75-100）。
- **影响**：材料被截断/丢弃时用户看不到，只会觉得“回答变差”；也无法评估上下文改造对 prefix cache 的效果（而 reports/agent-pipeline-and-cache-review.md 已实测同一 11k prompt 因顺序不同命中率 0% vs 79.2%，说明缓存观测对上下文系统非常关键）。

### C9【P2】跨 plan 的会话上下文缺失：追问会变成全新计划

- **实测证据**：同一 X-Session-Id 下创建两个 plan：第一个“解释 Transformer 的注意力机制”，第二个“再详细一点，补充它和 RNN 的对比”。第二个 plan 的 user_intent 只有后半句，context 为 null，没有任何对前一个 plan 的引用；两个 plan 只有 session_id 相同，没有 parent_plan_id/context_ref。
- **影响**：用户在同一个会话里追问、改需求，系统每次都从零开始；前一轮的论文/附件/结论不会进入后一轮上下文。若要做真正的多轮任务式对话，需要 plan 级上下文链或显式“引用的 plan/artifact”。

### C10【P2】类型化 ResearchContext 的 Entities/Constraints 实际为空，且论文标题抽取有误

- **代码事实**：buildResearchContext 初始化 Entities: map[string]any{}, Constraints: map[string]any{}（api/context_resolver.go:15-23），但整个函数从未填充它们；plan 响应中 context.entities 为 null（实测 p1/p2/paper plan 均如此）。ResearchContext 注释称其为“shared by every planner branch and agent”，但下游拿不到 intent 里的 needs_plot/paper_search_fields/uploaded_files 等结构化实体。
- **同源问题**：Apriori PDF 的 paper_metadata.title 被抽成 "for Mining"（真标题是 “Fast Algorithms for Mining Association Rules”），于是 plan 的 parse ... & extract method 节点、paper_title 检索字段都用了错误标题（实测 paper_title="for Mining"）。selectTitle 逐行打分后 mergeWrappedTitle 只向后合并，导致两行标题只取到第二行（documents/metadata.go:118-150, 191-208）。
- **影响**：ResearchContext 没有成为真正的“结构化上下文总线”；论文检索/仓库发现会被错误标题带偏。

### C11【P3】AutoResearch 的 prompt 顺序仍是“易变内容在稳定内容之前”，且 per-file 最小配额可能超总预算

- **代码事实**：AutoResearchCandidateUserPrompt(spec, ledger, editableSource, readOnlySource, rejectedCandidate) 的顺序是 spec → ledger（每 trial 变化）→ source（稳定）→ readOnly → feedback（易变）（prompts/prompts.go:199-220；调用 agent/autoresearch_spec.go:313-322）。reports/agent-pipeline-and-cache-review.md 的实测正是这个顺序：11,143 token prompt 命中 0%，把 stable source 提前后 79.2%。
- **次要点**：autoResearchSourceContext 的 perFile = budget / len(snapshots)，但低于 2000 时强制 2000（agent/autoresearch_apply.go:229-240）；若 AUTORESEARCH_SOURCE_MAX_TOKENS 配得较小（如 5000）且有 8 个文件，实际可发 16,000 token，超过配置预算。JSON 数组外壳/路径字符也未计入。
- **影响**：跨 run 的 prefix cache 基本无法命中；极端配置下上下文超预算。

### C12【P3】Chat 与文档划词没有统一 token 预算

- **代码事实**：/api/chat 仅用 http.MaxBytesReader(..., 1<<20) 限制请求体（routes.go:171-188），没有 token 估算/上下文预算；实测 276,006 个中文字符（约 180,004 估算 token）被原样发给 mock，真实 128k 模型会直接 context length 400。assist 对 selected text 限制 12,000 runes，但 page context 由 ContextForPage(document, page, radius) 生成，radius 最大 3 → 最多 7 页 × MaxPageChars=6000 = 42,000 字符，再加 selected text，没有总 token 预算（assist/assist.go:23-27, 137-152；documents/document.go:53-56）。
- **影响**：短请求路径没有预算保护；长 chat 可能“看起来发送成功”，实际在 provider 端 400；assist 长页面也可能超。

---

## 4. 建议的改进方案（按实施顺序）

### P0-1 让 chat 真正多轮（最高优先，用户可感知）

1. **协议**：ChatPayload 增加可选 messages[]（role/content）与 session_id；保留 message 兼容旧客户端。
2. **服务端会话存储**：新增 chat_sessions store（文件或 SQLite），按 owner_id + session_id 存 [{role,text,created_at,plan_id?}]；/api/chat 读取最近 N 轮（默认 20 条/32k token），把当前 user 追加后按 PromptBudget 组装。
3. **摘要压缩**：超出预算时保留 system + 最近 8 轮 + 旧轮次摘要（“用户先问了 X，结论 Y；随后要求 Z”），摘要写入 session 元数据；不要直接丢中间轮。
4. **前端**：把当前 activeSessionId 作为 session_id 发送；刷新/切设备时从 GET /api/chat/sessions/:id 恢复；localStorage 只做缓存，不是唯一事实源。
5. **验收**：3 轮 chat 中第 3 轮请求必须包含前两轮 role/text；问“我刚才说喜欢什么颜色”能答出；清除 localStorage 后刷新仍能恢复。

### P0-2 统一会话身份（否则 P0-1 会错位）

- 方案 A（推荐）：由后端签发 session id（POST /api/sessions 返回 id + 签名 cookie），前端所有请求带这个 id，后端把 plan/chat 都挂到同一 id；X-Session-Id 不再被忽略。
- 方案 B（过渡）：保留服务端 cookie，但前端创建 UI 会话时调用后端创建；前端不再自己生成 UUID。
- 验收：两个 UI 会话创建 plan 后，/api/sessions 返回两个不同 session；切换会话后 /api/plans?session_id=... 只返回该会话的计划。

### P1-1 输入分配器：优先级 + 最小值 + JSON 安全截断

1. **Section 增加 Priority 与 MinTokens**：inputs 按业务重要性而不是 key 字母序排序（例如 question/constraints/paper_metadata 优先，logs/raw_artifacts 其次）。
2. **两阶段分配**：先给每个 section 保留 MinTokens（如 128–512 token），剩余预算按权重分配；大输入先截断，小关键输入不因预算被 drop。
3. **JSON 专用渲染**：不要对 json.Marshal 结果做 rune 截断。对 uploaded_files 做专用投影：只保留 id/name/role/page_count/document_id，text_excerpt 每文件单独 cap（建议 2–4k runes），总长再按预算压缩；其他 map/list 同理，先截叶字段再 marshal，保证输出合法 JSON。
4. **报告**：每个 input 记录 original_tokens/included_tokens/dropped/truncated，并显式写出 [omitted: key=... reason=budget]。
5. **验收**：新增单测 TestInputBudgetKeepsSmallCriticalInput、TestLargeUploadedFilesRendersValidJSON；200k 大输入 + 8 token 关键输入时关键输入必须保留。

### P1-2 长意图治理：计划级上下文只放一次

1. **节点 description 只放 node-local 指令**：newNode 不再接收 intent.RawIntent 作为 context；改为 NodeInstruction{goal, constraints, input_refs}，默认 200–500 token。
2. **PlanContext 承担稳定上下文**：完整 user intent、intent type、paper metadata、附件索引只放在 plan_context 一次；PlanContextForTask 只在“完全相同的稳定块”时去重，不要用 strings.Contains 猜。
3. **长意图摘要**：createPlan 时生成 ResearchContext.Summary（目标/约束/输出/关键实体），节点使用 summary；原文作为 artifact/blob 引用，按需读取。
4. **计划体积上限**：PlanGraph JSON 设上限（如 1 MB）与单 node description 上限（如 4k 字符）；超限时拒绝创建并返回 actionable error，而不是默默生成 815KB 的计划。
5. **验收**：20k 字符意图 + 16 节点计划 JSON < 256KB；每个 node description < 4k；plan_context 在每节点出现且只出现一次完整意图/摘要。

### P1-3 文档检索：先修 section 检测，再修 query

1. **重写 heading 判定**：使用 ^\d+(\.\d+){0,3}\s+[A-Z][A-Za-z] 的数字标题；全大写需长度 4–80、字母占比高、不含数字/货币/百分比；排除 references/acknowledgements 之后的内容；不要把 table/figure caption 继承为 section title，caption 自己作为 kind=figure/table chunk。
2. **保留标题层级**：SectionInfo 增加 Level/Number，允许 3.1 Encoder and Decoder Stacks 这种“编号+标题”识别；无编号的 Introduction 用 Title Case + 位置/字号提示识别。
3. **检索 query 只用任务意图**：retrieveDocumentContext 改为 task.Name + 从 ResearchContext 提取的 topic/entities，剔除“任务目标/具体要求/expected outputs”；query 截到 200–300 字符，中文 bigram 只保留去重后的前 20 个。
4. **fallback 改成分层采样**：无 section 时按 Abstract→Intro→Method→Results→Conclusion 各取一段，而不是从全文开头截 24k 字符。
5. **验收**：Attention PDF 至少识别 Introduction/3.1/3.2/Results/Conclusion；paper_parse 的 volatile ≥ 2,000 token；Apriori PDF 标题/章节不再出现 2. Use the large itemsets...。

### P1-4 多轮 Session：token 预算 + 摘要 + 持久化

1. Session 增加 MaxTokens，按估算 token 保留；每条消息设上限（如 8k token）。
2. 裁剪时把被丢弃的中间轮次压成一条 [earlier turns summary]（可用确定性摘要：trial number/status/hypothesis/decision/reason；或小模型摘要），放在 system 与最近轮次之间。
3. repair_session 加入 promptMetadataFromTask 白名单并写入 task events；重启后可从 PlanNode 恢复或至少可审计。
4. AutoResearch 的 MaxTurns 至少 = MaxTrials + 2（或使用摘要），避免 8 trial 丢前 3 个。
5. **验收**：8 trial 后 summary 中包含 trial 1–8 的编号/结论；Session 的 token 总量不超过预算；PlanNode 能看到 repair_session。

### P2-1 可观测性：把“截断”和“缓存”亮出来

1. RecordUsage 解析 prompt_tokens_details.cached_tokens，新增 LLMCachedTokens 与 llm_cached_prompt_tokens_total；任务详情显示 cached/total。
2. 前端 TaskUsage 增加 prompt_budget_limit/prompt_truncated_sections；执行侧栏在任务卡上显示“上下文 117k / 已截断 2 个 section”，点击可看 section 报告。
3. 每次 LLM 调用记录 prompt_prefix_hash（已有 Session.PrefixHash 可复用）与 model/revision；用于比较改造前后缓存命中。
4. 验收：构造超预算任务时 UI 必须显示截断；mock/真实 provider 的 cached_tokens 能进入 usage。

### P2-2 AutoResearch prompt 重排

- 顺序改为：system → spec → editable source → readOnly source → ledger（最近 trial）→ feedback。这样稳定内容形成前缀，ledger/feedback 只在末尾变化。
- 单测：同一 spec 的两次 trial 请求，前 60% 字节相同；PrefixHash 在首条消息后保持稳定。
- 同时修复 perFile 最小配额：perFile = max(1, budget/len) 且总和不超过 budget；或对文件按优先级排序，最后一个文件可截断。

### P3 其他

- **ResearchContext**：在 buildResearchContext 中复制 intent.Entities/Constraints 到 ctx.Entities/Constraints；PlanContext 使用它们；补测试。
- **论文标题**：selectTitle 支持“上一行+当前行”合并候选，mergeWrappedTitle 双向合并；用 Apriori fixture 断言标题为 Fast Algorithms for Mining Association Rules。
- **Chat/assist 预算**：/api/chat 用 PromptBudget 组装 system+history+new，超限返回 413 + 建议；assist 对 page context 按“选中页 > 相邻页”优先级 token 预算。
- **跨 plan 上下文**：PlanGraph 增加 ParentPlanID/ContextRefs；同 session 的追问可引用上一 plan 的 ResearchContext 与关键 artifacts；至少支持“继续上一个计划”显式引用。

---

## 5. 建议的验收清单

| 编号 | 验收项 | 通过标准 |
|---|---|---|
| A1 | 多轮 chat | 第 3 轮请求含前 2 轮消息；追问能答出前文事实；刷新后仍能恢复 |
| A2 | 会话身份 | 两个 UI 会话对应两个 server session；/api/plans?session_id 隔离正确 |
| A3 | 输入分配 | 200k 大输入 + 8 token 关键输入，关键输入保留；JSON 可解析 |
| A4 | 长意图 | 20k 字意图、16 节点计划 JSON < 256KB；每节点 description < 4k |
| A5 | PDF 检索 | Attention PDF 识别 ≥5 个真实章节；paper_parse volatile ≥2k token |
| A6 | Session 裁剪 | 8 trial 摘要包含全部 trial；token 不超预算；repair_session 持久化 |
| A7 | 可观测 | UI 显示 prompt 截断；usage 含 cached_tokens |
| A8 | 缓存顺序 | AutoResearch 两次 trial 请求稳定前缀 ≥60%；cached_tokens >0（真实 provider） |

---

## 6. 关键证据索引

| 证据 | 路径/位置 |
|---|---|
| Mock LLM 请求记录（20 条） | reports/context-audit/prompt_requests.jsonl |
| Mock LLM 实现 | reports/context-audit/mock_llm_server.py |
| 3 轮 chat 每次 messages=2 | prompt_requests.jsonl seq 1-3 |
| 长意图 plan 节点描述/体积 | reports/context-audit/plan-long.json、plan-long-after.json |
| 14.5k 字论文计划 16 节点 815KB | reports/context-audit/plan-paper.json |
| PDF 检索 trace（1 section/1 chunk） | reports/context-audit/events.json、plan-pdf-after.json |
| 输入预算饥饿（临时测试输出） | 本报告 C3，测试已删除 |
| JSON 截断非法（临时测试输出） | 本报告 C4，测试已删除 |
| Session 裁剪丢 3/8 trials（临时测试输出） | 本报告 C7，测试已删除 |
| 真实栈 session 合并 | 本报告 C2，plan 24aacebf.../c48972e... 已删除 |
| Apriori 标题 “for Mining” | reports/context-audit/plan-repro.json |

---

## 7. 结论与建议

**结论：需要改进，且应优先修“会话连续性”和“输入/检索质量”，而不是先调大上下文窗口。** 当前系统已经有不少正确的抽象（PromptBudget、Section、Session、ResearchContext、两阶段检索），主要问题是这些抽象之间没有真正串起来：

1. **会话层**：chat 无历史、session 两套 ID、plan 无上下文链；
2. **任务上下文层**：输入按字母序 first-fit、JSON 截断坏数据、长意图 N 倍复制、plan_context 被去重为空；
3. **检索层**：heading 检测对真实 PDF 失效、query 被整段任务描述稀释、fallback 只看开头；
4. **可观测层**：截断不可见、cache 未计量。

建议实施顺序：**P0-1 chat 多轮 + P0-2 会话身份 → P1-1 输入分配/JSON 安全 → P1-2 长意图治理 → P1-3 文档检索 → P1-4 Session 预算/摘要 → P2 可观测/缓存顺序**。前三项是用户可感知、风险低、可以在一到两天内落地的；P1-3 需要 PDF fixture 回归，但对论文任务的结果提升最大。

如果你希望，我可以从 P0-1/P0-2 开始直接改代码并补测试。
