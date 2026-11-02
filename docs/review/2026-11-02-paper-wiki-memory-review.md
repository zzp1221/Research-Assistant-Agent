# 《论文知识 Wiki（Paper Wiki）设计》评审

- **评审对象**：[论文知识 Wiki（Paper Wiki）设计](../design/2026-11-02-paper-wiki-memory.md)（设计稿，2026-11-02）
- **评审基准**：当前工作区代码（`research-assistant/backend`，memory 已在 `feature/memory` 落地）
- **评审日期**：2026-11-02（Asia/Shanghai）
- **评审性质**：设计可行性评审，不修改源码、不新增实现
- **关联**：[Agent 记忆系统设计](../design/2026-11-01-agent-memory-system.md)、[上下文系统审计](../audit/context-system-audit-2026-11-01.md)、[记忆系统实现对照](../improvement-2026-11-01/MEMORY-IMPLEMENTATION.md)

## 0. 结论摘要

**方向正确，但按文档原样落地的成本被显著低估；共 9 处需要修订的设计缺口，其中 3 处 P0（DOI 断链、Link 不可重建、Evidence 类型冲突）会直接导致数据分裂或验收失败，需先修订再分期实施。**

值得肯定的五点：

1. **不建第二套事实源**的正确判断——PaperPage/Link 只是读模型，Point 回到 memory，关闭开关即完全回退。这与 `internal/memory` 的现有分层完全吻合。
2. **Point 作为原语、而不是“整篇论文一条记忆”**——与现有 `Record` 的原子粒度、`ContentHash` 去重、RRF+MMR 检索天然对齐。
3. **确定性抽取优先**——`PlanGraph`/`ResearchTrialLedger`/`EnvironmentManifest`/`Artifact` 确实已是高质量结构源，文档对它们的枚举与实际代码一致。
4. **Link 必须有证据、LLM-only 默认 pending_review**——与 `memory` 的 `Trust`/`Status`/`Evidence.Class` 语义一致，做的是同一件事。
5. **分阶段 W0→W4 与回滚开关**的设计形态正确；W0 的“PaperPage + Point + 显式引用”确实是最小可交付切片。

**总体可行性评分：6.0 / 10（按架构方向加权，高于各维度算术平均；方向可信，需修订后分阶段实施；W0 可先做，但 DOI、Evidence、Link 可重建三个基建缺口必须先补）。**

| 评审维度 | 评分 | 判断 |
|---|---|---|
| 架构与现有 memory 的契合度 | 8/10 | Point 复用 Record 正确；Link 作为独立投影合理，但需明确可重建边界 |
| 数据模型完整性 | 5/10 | `Link` 缺 `owner_id`、aliases 未按 owner 分区、Evidence 形状冲突、Link 无版本字段 |
| 身份（PaperIdentity）可落地性 | 4/10 | 依赖的 DOI 在现有链路中**根本到不了 persistence** |
| 写入管道与抽取质量 | 6/10 | 确定性来源清单准确；但 ClaimRubric/LLM 抽取被误标为“确定性”，且与 `paper_parse` 的实际产物脱节 |
| 检索与注入 | 6/10 | 预算与顺序思路正确；1.5% 预算对中文 Point 偏紧，且未处理现有 prompt section 的优先级冲突 |
| Link 构建与治理 | 6/10 | 评分公式与阈值方向可行；候选生成的 O(n²) 分析不足、缺少跨 owner 的 linker 作用域 |
| 可重建性（W7） | 3/10 | 文档声称“删除 wiki 可重建”，但 Link 只存 wiki 目录、LLM 证据无法复现，rebuild 会**丢数据** |
| 分阶段与验收 | 7/10 | 顺序正确；但 W0 的 API 已返回 `link_ids` 而该阶段还没有 Link，性能指标也缺少数据集假设 |
| 安全与隔离 | 5/10 | owner 隔离写得明确；hidden benchmark 的传播路径未验证，且 `links.jsonl` 是新的泄露面 |

## 1. 方案与代码事实核对

以下核对用于确认文档不是“纸上分析”。结论：**大部分事实准确；4 处关键判断与代码不符（❌），另有 4 处需要修正的偏差（⚠️）**。

| 文档判断 | 代码事实 | 核对 |
|---|---|---|
| Point 可映射到 memory.Record 的 Kind/Tags/Entities/DerivedFrom | `memory/types.go:113-140` 的 `Record` 字段齐全；`Kind` 9 种、`Status` 5 种、`Trust` 4 种 | ✅ 准确 |
| 复用 memory 的 JSONL + snapshot 存储 | `memory/store.go:46-103`：`memory.wal.jsonl` + `memory.snapshot.json` + 目录锁 + 2000 条 compaction | ✅ 准确（但注意**并非**文档写的 `papers.jsonl`/`papers.snapshot.json` 命名） |
| Point 内容哈希去重 | `memory/types.go:181-185` 的 `ContentHash(kind, scope, title, content)`；`store.go:231-244` 命中则 `UseCount++` | ✅ 准确；但**哈希不含 Evidence**，文档 §7.3 的 `sha256(paper_id + kind + normalized(text) + evidence_ref)` 是**新造的第二套哈希**，与现有 `byHash` 去重并行，形成两套口径 |
| `Evidence` 可直接复用 memory 的 Evidence 形状 | `memory/types.go:89-95` 的 `Evidence` 只有 `Class/Confidence/SHA256/Hidden`；文档 §4 的 `Evidence` 是 `Page/Section/Quote/ArtifactRef/TaskID/PlanID/DocumentID` | ❌ **同名不同义**；文档自己在 §4 末尾承认“应复用”，但给出的类型本身就是冲突的 |
| `PlanFinalizer` 写 task/result/negative Point | `planner/semantic_validation.go:118-122`：`PlanFinalizer.Finalize` 只计算 `plan.Outcome`，**不写任何存储**；真正写 memory 的是 `scheduler.MemorySink`（`scheduler/scheduler.go:39-44`、`api/memory_sink.go:14-28`） | ❌ 接错入口 |
| `scheduler.RoutedTaskExecutor` 可选 WikiSource | `scheduler/executor.go:64-80` 只有 `Memory memory.Store` 字段；`SetMemory` 在 `executor.go:91` | ⚠️ 可加，但文档没提**必须同时改 `agent/task_prompt.go:78-84`**，否则 wiki context 到不了 prompt |
| `prompts.TaskPromptInput` 增加 `WikiContext` | `prompts/task_prompt.go:16-31` 无此字段；`BuildTaskPrompt` 的 `sections` 在 `150-165` 行硬编码 6 个 section | ⚠️ 改这里需要同步改 `prompts/budget.go:130-199` 的排序与 `PromptReport` |
| Chat citation 增加 paper/point | `api/chat_runtime.go:159-163` 返回 `citations: memCtx.Citations`，`memory.Citation` 只有 `MemoryID/Title/Kind/Date/Source/Trust` | ⚠️ 需要扩展 `Citation` 结构，不是“并列返回”就行 |
| ClaimRubric 是确定性抽取源 | `agent/claim_evidence.go:70-92`：`executeClaimRubricExtraction` **调用 LLM**（`llmclient.GenerateWithUsage`），产出由 `normalizeClaimRubric` 校验 | ❌ 文档 §7.2 第 3 条把 `ClaimRubric` 列为“确定性抽取到 claim Point”，实际是 **LLM 生成 + 结构校验**，默认应为 pending_review 而非 active |
| `PaperMetadata` 含 DOI，可做 canonical key | `models/context.go:41-52` 的 `PaperMetadata` **没有 DOI 字段**；`documents/metadata.go:699-702` 注释明确写 “models.PaperMetadata has no DOI field yet”，`ApplyProviderRecord` 把 DOI 作为**第二个返回值**单独返回，`api/context_resolver.go:71-78` 用的是 `ApplyProviderMetadata`（**丢弃 DOI**） | ❌ **硬缺口**：按当前链路，绝大多数上传 PDF 的 PaperPage 只能落到 `title+year` 这一层，`doi:` 分支几乎永远走不到 |
| `literature.Record` 有 DOI/arXiv，可作为身份源 | `literature/library.go:17-38`：`Record` 有 `DOI`、`ArxivID`、`UploadID`，且 `sameRecord`(`library.go:425-438`) 已按 DOI→arXiv→title 做过去重 | ✅ 准确，但文档**没有把 literature library 列为 PaperIdentity 的输入**，这是最应该复用的现成资产 |
| `Document.Outline` 可映射 Sections | `documents/outline.go:27-85` 返回 `SectionInfo{Title,StartPage,EndPage,Chunks,Characters}`，最多无上限 | ✅ 准确；但 `documents/store.go:192-206` 的 `looksLikeSectionHeading` 正是审计 C6 认定“对常见排版失效”的那套规则，PaperPage 的 section map 会继承同样的错误 |
| `EnvironmentManifest` 可映射 constraint Point | `models/reproduction.go:136-168` 字段齐全 | ✅ 准确 |
| `ResearchTrialLedger` 可映射 negative/procedure | `models/autoresearch.go:99-123`；`memory/extract_trial.go:28-58` 已实现 rejected→negative、kept+SearchRuns≥2→procedure | ✅ 准确，且**已经实现**，W1 不需重写 |
| 现有 memory 支持 per-owner settings | `api/memory_runtime.go:27-36` + `api/chat_runtime.go:90-93` 有 `MemorySettings().Get(ownerID)`；但 `scheduler/executor.go:288` 用的是 `memory.DefaultSettings()`，**任务注入不吃 per-owner 设置** | ⚠️ Wiki 若按 owner 配置预算，需先修这条不一致 |

## 2. 必须修正的问题（按严重程度）

### 2.1【P0】PaperIdentity 依赖的 DOI 在现有链路里到不了 PaperPage

**问题**：文档 §3.4 和 §8.1 把 DOI 定为最高优先级身份，但：

- `models.PaperMetadata` 没有 `DOI` 字段（`models/context.go:41-52`）；
- `documents.ApplyProviderRecord` 返回 `(PaperMetadata, string)`，DOI 在第二个返回值里（`documents/metadata.go:771-786`）；
- `api/context_resolver.go:73-78` 只调用 `ApplyProviderMetadata`，**直接把 DOI 丢掉**；
- `literature.Record`（`library.go:17-38`）里有 DOI，但 `PaperPage` 的构建路径没有引用 `LibraryStore`。

**后果**：一个上传的 Attention PDF，即使 arXiv provider 明确返回了 `10.48550/arxiv.1706.03762`，PaperPage 也只能拿到 `arxiv:1706.03762` 或 `title:attention is all you need:2017`。同一篇论文如果先按 DOI 导入、再按 PDF 上传，会生成**两个 PaperPage**——而 §8.3 的“同 DOI 直接合并”规则永远不会触发。这是**静默的数据分裂**，比显式报错更糟。

**必须修正**：

1. 在 `models.PaperMetadata` 增加 `DOI string`（这是增量字段，向后兼容）；
2. `api/context_resolver.go` 改用 `ApplyProviderRecord` 并把 DOI 写入 metadata；
3. `documents.ProviderRecord` 的 `DOI` 在 `ApplyProviderMetadata` 中也要保留（或至少提供 `WithDOI` 变体）；
4. PaperIdentity 的输入优先级明确为：`literature.Record.DOI` > `PaperMetadata.DOI` > `PaperMetadata.ArxivID` > `title+year`，**并且把 `literature.LibraryStore` 列为 alias 表的第一数据源**；
5. 接受“同一篇论文有 DOI 和 arXiv 两个 alias”是常态，`aliases.json` 的 key 不能只有 canonical id，必须支持**多对一**。

### 2.2【P0】W7“可重建”不成立：Link 的输入不都在 memory 里

**问题**：文档 §2.1 W7 与 §17 都声称“删除 wiki 目录后可从 memory + plan/document 重建 PaperPage 与 Link 索引”。但：

- Link 的候选来源 §9.1 第 1 条是“PDF references、Crossref、OpenAlex、Semantic Scholar”——这些数据**不在 memory 里**，也不在 `PlanGraph` 里；
- §9.1 第 4 条“BM25/embedding 文本相似”需要论文全文，而 `documents.Document` 虽然保存在 upload 目录，但 wiki 目录删除后，**Document 仍存在**——这点没问题；
- 但 **Link 的 `Evidence.Quote` 是 LLM 或规则从原文抽取的**，重建时如果 LLM 不可用（`WIKI_LLM_MODE=off`），这些 Link 无法复现；
- 更关键：§5.1 说 Link 存在 `wiki_links.jsonl`，不进入 memory；**删除 wiki 目录 = 删除 Link 的唯一副本**。

**后果**：`WIKI_ENABLED=false` 再打开、或用户手动删 `data/wiki/` 后，PaperPage 能重建（因为 Point 在 memory），但 **Link 全部丢失**，且 LLM-only Link 的 `pending_review` 状态、用户 confirm/reject 决策也全部丢失。这与 W7 的验收信号直接矛盾。

**必须修正**（三选一，推荐第 1 个）：

1. **Link 也进 memory**：作为 `Kind=fact`、`Tags=["wiki_link"]` 的特殊 Record，`Source.Type="wiki_link"`，正文存 `from_paper|to_paper|type` 的可读摘要，`Attributes` 存结构化字段。这样 Link 天然进入 `ContentHash` 去重、`Status` 生命周期、`DerivedFrom` 链，W7 才成立。
2. **Link 存 `links.jsonl` 同时保留一个 `link_decisions.jsonl` 的用户决策日志**，重建时回放决策；但 LLM-only Link 的原文证据仍需重新抽取，成本高。
3. **降级 W7**：明确写“PaperPage 可从 memory 重建；Link 只能重建确定性部分（cites/uses_same_*），LLM Link 需重新生成或从备份恢复”。至少不要让验收标准撒谎。

### 2.3【P0】`Point.Evidence` 与 `memory.Evidence` 同名不同义

**问题**：文档 §4 的 `Evidence` 有 `Page/Section/Quote/ArtifactRef/TaskID/PlanID/DocumentID` 七个字段；`memory.Evidence` 只有 `Class/Confidence/SHA256/Hidden`。文档 §4 末尾自己写“应复用 internal/memory 的 Source/Evidence/Validity/Entity 形状”，但给出的类型与之冲突。

**后果**：如果按文档原样实现，会出现**两套 Evidence 类型**，`ValidateRecord`（`memory/redact.go:37-77`）只认识 `memory.Evidence`，于是 Wiki 侧的 `Page/Section/Quote` 永远不会被校验，也不会进入 `IsHiddenSource` 检查——这是**安全漏洞**，不只是类型洁癖。

**必须修正**：

- `Point` 不定义新 `Evidence`，而是：
  - `memory.Evidence` 保留 `Class/Confidence/SHA256/Hidden`；
  - 新增 `memory.SourceRef` 结构（`Page/Section/Quote/ArtifactRef/TaskID/PlanID/DocumentID`），挂在 `Record.Attributes` 或新字段 `Record.Refs []SourceRef`；
  - `Record.Source` 仍指“产生这条记录的来源”，`Refs` 指“这条记录引用的证据”。
- 或者更简单：**给 `memory.Record` 增加 `Attributes map[string]any`**（文档 §5.2 已提到），把 `page/section/quote` 放进去，并让 `ValidateRecord` 扫描 `Attributes` 里的 `Hidden`/`secret` 字段。

### 2.4【P1】`Link` 缺 `owner_id`/`project_id`，`aliases.json` 未按 owner 分区，与“owner 隔离是硬边界”矛盾

**问题**：文档 §4 的 `PaperPage` 有 `OwnerID` 和 `ProjectID`，但 `Link` 只有 `FromPaper/ToPaper/Type/...`，**没有 owner/project**。§6.1 才补一句“Link 需要 owner 字段”，但 §4 的类型定义没有。

**后果**：

- `byLinkFrom`/`byLinkTo` 索引是全局的，跨 owner 的 link 会出现在别人的 PaperPage 上；
- `GET /api/wiki/papers/:id/links` 如果只按 paper id 过滤，会泄露其他 owner 的论文标题（即使只返回 link type 和 confidence）；
- `aliases.json` 是**全局单文件**，而文档 §6.1 说“PaperPage 按 owner/project 分区”——alias 表如果不分区，两个用户上传同一篇论文会互相覆盖 alias，甚至把 A 的 canonical id 指到 B 的 PaperPage。

**必须修正**：

- `Link` 增加 `OwnerID`、`ProjectID`（至少 OwnerID）；
- `aliases.json` 按 owner 分文件（`data/wiki/owners/<hash>/aliases.json`），与 `literature.LibraryStore.ownerDir`（`library.go:73-76`）保持一致；
- 所有 `GET /api/wiki/*` 在 handler 层用 `resolveUserID(c)` 注入 owner，**store 层再校验一次**（参考 `memory/store.go:285` 的 `Get` 双校验）。

### 2.5【P1】§7.2 的“确定性抽取”清单有 3 项不准确

| 文档条目 | 实际情况 | 影响 |
|---|---|---|
| “ClaimRubric 或 paper_parse output 到 claim Point” | `claim_rubric_extract` 是 **LLM 调用**（`agent/claim_evidence.go:70-92`），产出 `ClaimRubric` 由 `normalizeClaimRubric` 做结构校验 | claim Point 应默认 `pending_review`，不能直接 `active` |
| “PlanGraph 到 task Point：节点名、类型、状态、error 前 300 字、result 摘要、artifact key” | 现有 `memory.RecordTask`（`extract.go:208-259`）只在 **failed** 或 **completed+RunCount>1** 时写记录，**正常完成的 task 不写** | Wiki 若按文档“每个节点都生成 task Point”，**失败节点**会与现有 `mem-task-*` 记录重复（同一 task 两条），需要明确“Wiki Point 是否复用这条 Record” |
| “Artifact metrics 到 result Point” | `PlanGraph.Artifacts` 是 `map[string]Artifact`（`models/graph.go:141`），`Artifact.Value` 是 string（`models/artifact.go:16`），metrics 没有结构化 schema | 需要先定义 `Artifact.Kind=metrics` 的 JSON schema，否则只能把整个 value 当文本塞进 result Point |

**必须修正**：

- 明确 `Point` 与 `Record` 的关系：**Point 不是 Record 的替代，而是 Record 上的一个“wiki 视图标签”**。W0 先做 `Record.Tags += ["wiki_point","paper:<id>"]`，不新建 `Point` 存储；等 W2 真正需要 `Link` 时再抽象。
- `ClaimRubric` → Point 必须 `Trust=TrustInferred`、`Status=StatusPending`，并且 `Evidence.Class="llm_inference"`，与 `memory/consolidate.go:87` 的 reflection 一致。

### 2.6【P1】`WikiContext` 注入的预算与现有 prompt 组装冲突

**问题**：文档 §10.3 建议 `wikiBudget = clamp(promptBudget * 0.015, 800, 2000)`，§13.2 又说 `budget 为 800 到 2000 tokens`。但：

- `prompts.BuildTaskPrompt`（`task_prompt.go:121-173`）里 `limit = budget.PromptLimit()`，真正给 section 的是 `limit - systemTokens`（`task_prompt.go:166`）；
- `task_inputs` 在 `task_prompt.go:159` 用这个**整段预算**独立预渲染，完全不知道 `plan_context`/`task_instruction`/`retrieved_memory` 已经占掉多少；随后 `BuildSectionsWithLimit`（`task_prompt.go:166`）再按真实剩余空间 first-fit 二次截断——`task_inputs` 是“先渲染、后压缩”的一个大 section；
- `BuildSectionsWithLimit`（`budget.go:163-169`）对 `Priority<=2` 的 section 是**强制全量**；priority 3 的 `retrieved_memory` 会先于 priority 4 的 `task_inputs` 拿预算。`wiki_context` 若也设为 priority 4，只能排在 `task_inputs` 前或后：排前会抢走 `task_inputs` 的配额（而 `task_inputs` 里可能有用户输入的关键参数），排后则基本拿不到预算。

**后果**：文档说“与 task_inputs 同级或紧随 retrieved_memory”，但没有给出 `wiki_context` 与 `task_inputs` 争预算时的取舍规则；`task_inputs` 承载用户输入，不能被无限压缩，而 priority 3 的 `retrieved_memory` 已经会先吃掉一块预算。

**必须修正**：

- `wiki_context` 设为 **priority 3，且在 `retrieved_memory` 之前**（更稳定、更接近论文事实），或与 `retrieved_memory` 合并为一个 `memory_context` section；
- 在 `PromptReport` 中单独记录 `wiki_tokens`，并在 §16.4 的“Wiki 注入 token 不超过单次 prompt 的 3%”中明确**分母是 `PromptLimit`，不是 `ContextWindow`**；
- `promptBudget * 0.015` 在 128k 模型上约 1,920，与 §10.3 的“PaperPage 摘要加 5 到 10 个 Point”大致匹配；但在 **小模型（如 8k context）** 上会降到 120，低于 `800` 下限，此时 `clamp` 会**超过 1.5%**，需要写明“小模型优先保 wiki 还是保 task_inputs”。

### 2.7【P1】`Link` 构建的 O(n²) 分析不完整

**问题**：文档 §9.2 说“先按实体倒排取候选；再按引用邻接表；最后 BM25 topK”，这确实能避免全量 O(n²)。但：

- `byEntity` 索引是**全局的**（§6.2 的 `byEntity map[entityKey]map[paperID]struct{}`），如果同一实体（如 `dataset:WMT14`）被 10,000 篇论文使用，单次查询就返回 10,000 个候选，**仍然是 O(n²)**；
- 文档没有定义 `Link` 的**方向性**：`uses_same_dataset` 是对称关系，但 `cites`/`extends`/`reproduces` 是单向的；如果按“每篇论文生成最多 200 条候选”，对称关系会被**重复生成两次**（A→B 和 B→A）；
- `Link` 没有 `revision`/`version` 字段，但 §9.4 说“不删除历史，只 archived 或 rejected”——没有版本号就无法区分“同一条 Link 被重新生成”和“用户 reject 过的旧 Link”。

**必须修正**：

- `byEntity` 按 owner 分区，并对单个实体的候选数设上限（如 500），超出时按 `paper.importance` 截断；
- 对称关系（`uses_same_dataset`/`uses_same_method`/`same_repo`/`related`）在写入时**规范化方向**（如 `from_paper < to_paper` 的字典序），避免重复；
- `Link` 增加 `Revision int` 和 `SupersededBy string`，与 `memory.Record` 的 `Version`/`Validity.SupersededBy` 对齐。

### 2.8【P2】`GET /api/wiki/papers/:paperID/links?depth=1` 缺少分页与截断

**问题**：§10.2 说“跨论文扩展只扩展 1 跳，最多 10 条 Link”，但 §11.1 的 API 没有 `limit/cursor` 参数。一个高被引论文可能有 200+ 条 incoming cites，前端一次性拉取会卡顿。

**必须修正**：API 增加 `limit`（默认 50，上限 200）和 `cursor`，并在响应中返回 `next_cursor`；UI 侧 §12.2 的“1-hop 图”应只展示 top 20，其余折叠。

### 2.9【P2】`PaperPage` 的 `Sections` 与 `documents.SectionInfo` 字段重复定义

**问题**：§4 的 `Section` 类型文档没给出定义，但 §11.3 的 JSON 示例是 `{"title","start_page","end_page"}`，与 `documents.SectionInfo`（`outline.go:12-18`）的 `{Title,StartPage,EndPage,Chunks,Characters}` 几乎一样，却少了 `Chunks/Characters`。

**后果**：两套类型，未来 `documents` 改字段（如增加 `Level`/`Number`，审计 P1-3 已建议）时 Wiki 不会跟着改。

**必须修正**：`PaperPage.Sections []documents.SectionInfo`，直接复用；如果怕包依赖，把 `SectionInfo` 提到 `models` 或 `documents` 的公共子包。

## 3. 逐阶段可行性评估

### W0：PaperPage + Point 最小切片 — 可行，但要先补 3 个基建

**可行点**：

- `internal/wiki` 新建 package、复用 `memory.FileStore` 模式（WAL + snapshot + 锁）是低风险选择；
- PaperPage 从 `Document` + `PaperMetadata` 生成，输入明确；
- Point 从 `paper_parse` + `PlanGraph` 确定性生成（注意：`ClaimRubric` 是 LLM 产物，不属于 W0）；
- owner 隔离的单测可以直接照 `memory/memory_test.go:101-129` 的写法。

**必须先补**：

1. `PaperMetadata.DOI` 字段（见 2.1），否则 W0 的“身份去重”验收通不过；
2. `memory.Record.Attributes map[string]any`（或 `Refs []SourceRef`），否则 `page/section/quote` 无处可放（见 2.3）；
3. `api/context_resolver.go` 改用 `ApplyProviderRecord`，并让 `literature` 的 DOI 流入 `ResearchContext.Paper`。

**W0 验收建议补充**：

- 同一 PDF 通过“上传”和“文献库导入”两条路径进入时，必须命中同一个 `paper_id`；
- `WIKI_ENABLED=false` 时，`GET /api/wiki/papers` 返回 404/空，而不是 500。

### W1：任务与实验 Point — 大部分已存在，W1 实际只需“挂接”

**可行点**：`memory/extract.go` 已实现 `RecordPlan`/`RecordTask`，`memory/extract_trial.go` 已实现 trial→negative/procedure。W1 不需要重写抽取器，只需：

- 在 `memory.RecordTask` 的现有记录上加 `Tags=["wiki_point","paper:<paper_id>"]`；
- 在 `api/memory_sink.go` 的 `RecordTask` 之后调用 `wiki.BuildForPlan(ctx, plan)`；
- 在 `scheduler/scheduler_execute.go:189-192` 的 `memorySink.RecordPlan` 之后触发 `wiki.RefreshPaper(planID)`。

**注意**：`scheduler_execute.go` 的 `memorySink` 只在**正常终态**调用（第 190 行），**取消路径 `CancelPlanWithActor`（`scheduler_control.go:17-73`）不调用**。如果 Wiki 依赖“计划终态”触发，取消的计划不会生成 negative Point——这与 §7.1“Plan 完成/失败 → negative Point”不符，需要把 `RecordPlan` 挂到 `onTerminal` 或 `CancelPlanWithActor`。

### W2：显式 Link — 可行，但依赖 3 个前置

1. `literature` 的引用图能力（`literature/citations.go:26-108` 已有 OpenAlex references/cited_by），可以直接复用生成 `cites`；
2. `documents` 的参考文献解析**不存在**——文档 §9.1 说“PDF references”是候选源，但当前 `documents` 包只做 section/BM25，**没有 references 抽取**；W2 要么新增解析器，要么只用 OpenAlex/Crossref；
3. `literature.Record` 的 `DOI`/`ArxivID` 可以作为 `cites` 的目标，但需要先把 `literature.Result` 的 `URL/PDFURL` 归一化到 canonical paper id——这正是 2.1 的 alias 表要解决的问题。

**建议**：W2 先只做 `cites`（OpenAlex）+ `uses_same_*`（entity overlap），把 `PDF references` 放到 W2.5 或 W3，避免一上来就写 PDF 参考文献解析器。

### W3：LLM 摘要与待审核 Link — 方向正确，但要先定义“LLM 输出如何进 memory”

**问题**：文档 §7.4 说“输出严格 JSON（point/link 加 evidence_quote）”“schema 校验失败直接丢弃，不重试超过 2 次”——这与 `agent/claim_evidence.go:89-95` 的 `cleanJSONResponse` + `json.Unmarshal` 模式一致。但：

- `memory.ValidateRecord`（`redact.go:37-77`）会**拒绝含 secret 的 Point**，LLM 生成的 quote 如果包含 `password=` 之类，会整条丢弃，而不是“截断 quote”。需要明确：是丢弃整条，还是把 quote 脱敏后保留；
- `ScanInjection`（`redact.go:122-130`）目前**只被测试调用**，没有被 `ValidateRecord` 使用。Wiki 的 `Point.Text` 来自论文原文，如果论文里含“ignore previous instructions”，按 §10.3 会直接注入 prompt——**建议在 `wiki.Point` 写入前调用 `ScanInjection`，命中则降级为 `TrustUntrusted` 且不参与注入**（与 memory 的 `ChatMemoryInstruction` 配合）。

### W4：语义和图增强 — 可延后，但文档应写明“不做的理由”

文档 §2.2 说“不默认启用 embedding 或图数据库”，这是对的。但 §15 的 W4 描述“A-MEM 式链接、跨项目提升”过于笼统，建议在 W4 前先写一个**消融计划**：在 50 篇 fixture 上比较 `WIKI_LINK_MODE=explicit|hybrid|llm` 的 Link Precision，只有 hybrid 显著优于 explicit 才做 W4。否则 W4 会变成“为了技术而技术”。

## 4. 安全与治理缺口

### 4.1 hidden benchmark 的传播链没有验证

文档 §14 说“paper 来源路径在 BENCHMARK_PRIVATE_ROOT 下时不入 Wiki；Evidence.Hidden 为 true 时拒绝”。但：

- `memory.IsHiddenSource`（`redact.go:82-102`）只检查 `Source.Ref` 和 `Source.URL`，**不检查 `PaperPage.Title/Authors/Abstract`**；如果 hidden benchmark 的论文标题出现在 `Abstract` 里，会绕过检查；
- `BENCHMARK_PRIVATE_ROOT` 只在 `memory`（`redact.go:83`）和 `agent/benchmark_contract_agent.go:957` 中使用，`documents`/`literature` 包**不知道**这个变量；
- Wiki 的 `PaperPage` 是**新的持久化面**，如果 hidden 论文的 `document_id` 被写进 `PaperPage.SourceRefs`，即使 Point 被拒绝，PaperPage 仍可能泄露标题。

**必须修正**：

- `wiki` 在写入 PaperPage 前，对 `Title/Abstract/Authors` 全部调用 `memory.IsHiddenSource` 的文本版（或新增 `redact.ContainsHiddenMarker(text)`）；
- `PaperPage` 不存 `document_id` 的原始路径，只存 `document_id`（已经是 UUID，安全）；
- 在 `GET /api/wiki/papers` 的列表响应里，对 hidden 论文返回 404，而不是空对象。

### 4.2 `links.jsonl` 是新的泄露面

- `Link.Evidence[].Quote` 可能包含论文原文片段（甚至 hidden benchmark 的题目/摘要），而 `Link` 不进 memory，**不受 `ValidateRecord` 的 secret/hidden 检查**；
- 建议 `Link` 写入前调用 `memory.ScanInjection` + 新增 `memory.RedactText`，并让 `Link.Evidence` 的 `Quote` 最长 500 字符（与 `agent/claim_evidence.go:630-635` 的 `claimSourceLocator` 一致）。

### 4.3 owner 隔离的单测要覆盖“跨 owner 的 Link”

文档 §16.1 说“Security: owner 隔离”，但没给出具体 case。建议加：

- `TestLinkCrossOwnerInvisible`：A 的 PaperPage 查询不返回 B 的 Link；
- `TestAliasOwnerScoped`：A 和 B 上传同一篇论文，各自有独立 canonical id；
- `TestHiddenPaperNotInWiki`：`BENCHMARK_PRIVATE_ROOT` 下的 PDF 上传后，`GET /api/wiki/papers` 不返回。

## 5. 与现有记忆系统的重复建设风险

文档 §5.1 说“Point 是 memory.Record，PaperPage/Link 是 wiki 投影”，这句话是对的，但 §4 又定义了独立的 `Point/Evidence/PaperPage` 类型，§6.1 又定义了独立的 `papers.jsonl/links.jsonl/aliases.json` 存储。这容易让实现者**真的新建一套平行存储**。

**建议在 W0 就明确三条硬约束**：

1. `Point` 不落盘，只是 `memory.Record` 的查询视图（`List(Query{Tags:["wiki_point"]})`）；
2. `PaperPage` 不存正文，只存 `paper_id` + `point_ids` + `link_ids` + 从 `Document`/`PaperMetadata` 可重建的字段；
3. `Link` 如果按 2.2 的方案 1 进 memory，则 `links.jsonl` 只是**索引缓存**，可随时从 memory 重建。

如果做不到这三条，W7 的“可重建”就是空话。

## 6. 建议的修订顺序

| 顺序 | 动作 | 对应问题 | 工作量 |
|---|---|---|---|
| 0 | 修 `models.PaperMetadata` 加 `DOI`，`context_resolver` 用 `ApplyProviderRecord` | 2.1 | 0.5 天 |
| 0 | `memory.Record` 加 `Attributes map[string]any`（或 `Refs []SourceRef`） | 2.3 | 0.5 天 |
| 0 | 明确 `Point=Record 视图`，不新建 `Point` 存储 | 2.5 / 5 | 设计文档修订，0.5 天 |
| 1 | W0：PaperPage 从 `Document`+`PaperMetadata` 生成，Point 打标签 | W0 | 2–3 天 |
| 2 | W1：复用 `extract.go`/`extract_trial.go`，把 Wiki 刷新挂到 `MemorySink` + `onTerminal` | W1 | 1–2 天 |
| 3 | W2：`cites`（OpenAlex）+ `uses_same_*`（entity），Link 进 memory | W2 | 3–4 天 |
| 4 | W3：LLM TLDR + pending_review Link，先补 `ScanInjection` 到 `wiki.Point` | W3 | 3–4 天 |
| 5 | W4：仅当消融证明 hybrid 优于 explicit 才做 | W4 | 可选 |

**总评**：这份设计在“做什么”上是对的，在“怎么接进现有代码”上还停留在类型草图阶段。建议先把 2.1（DOI）、2.2（Link 可重建）、2.3（Evidence 形状）三个 P0 缺口修掉，再开 W0；否则 W0 的“身份去重”和 W7 的“可重建”都会在验收时失败。W1 的实际工作量远小于文档估计（因为 `extract.go` 已存在），可以把省下的时间投到 W0 的 identity 与 store 上。

## 7. 附：文档自身的前后矛盾清单

| # | 位置 | 矛盾 |
|---|---|---|
| 1 | §1 vs §2.1 W7 | 说“可重建”，但 Link 只存 `links.jsonl`，不在 memory |
| 2 | §4 vs §5.1 | §4 定义独立 `Point/Evidence`，§5.1 说 Point 是 memory.Record |
| 3 | §10.3 vs §16.4 | §10.3 说 `wikiBudget = clamp(1.5%, 800, 2000)`，§16.4 说“不超过单次 prompt 的 3%”；两个数字不一致（且小模型上 clamp 的下限会突破 1.5%） |
| 4 | §13.2 vs §10.4 vs 代码 | §13.2 说 `wiki_context` priority 4；`retrieved_memory` 在代码里是 priority 3、`task_inputs` 是 priority 4。priority 4 的 wiki 若插在 `task_inputs` 之前会先吃掉预算、把 `task_inputs` 挤掉；插在其后则 wiki 永远拿不到预算。§10.4 的“retrieved_memory → wiki_context → task_inputs”顺序无法用现有 priority 实现 |
| 5 | §7.2 vs 代码 | §7.2 说从 `PaperMetadata` 抽 DOI，但 `models.PaperMetadata`（`models/context.go:41-52`）没有 `DOI` 字段，`documents` 包的 DOI 是 `ApplyProviderRecord` 的第二个返回值且未被 `context_resolver` 使用 |
| 6 | §2.1 W4 vs §3.3 | W4 说“至少支持 6 类 Link”，§3.3 的表格列了 9 类（多出 `reproduces/related/supersedes`） |
| 7 | §5.1 vs §6.1 | §5.1 写的是 `wiki_links.jsonl` / `wiki_links.snapshot.json`，§6.1 的目录布局写的是 `links.jsonl` / `links.snapshot.json`；同一份设计两个文件名 |
| 8 | §6.1 vs §6.2 | §6.1 的目录布局有 `papers.jsonl`/`links.jsonl`/`aliases.json`，§6.2 的索引只有内存结构；没有说 `papers.jsonl` 与 `memory` 的 `WAL` 如何保持一致 |
| 9 | §15 W0 vs §16.2 | W0 的验收说“单测：身份去重、Point 抽取、owner 隔离”，但 §16.2 的 Golden 评测要求“50 篇论文 fixture”，W0 阶段还没有 Link，无法测“交叉引用” |
