# 论文知识 Wiki（Paper Wiki）设计：以论文为单位的科研记忆视图与交叉索引

- 状态：修订稿 v1.1（依据 [评审报告](../review/2026-11-02-paper-wiki-memory-review.md) 修订；v1.0 见 git 历史）
- 日期：2026-11-02
- 修订：v1.1（2026-11-02）——修复 DOI 断链、Link 不可重建、Evidence 类型冲突、owner 隔离等 9 项问题，修订对照见附录 D
- 范围：internal/wiki（新增）、internal/memory、internal/models、internal/api、internal/planner、internal/scheduler、internal/documents、internal/literature、frontend；不改 sandbox/executor 协议
- 关联：
  - [Agent 记忆系统设计](2026-11-01-agent-memory-system.md)
  - [上下文系统审计](../audit/context-system-audit-2026-11-01.md)
  - [记忆系统实现对照](../improvement-2026-11-01/MEMORY-IMPLEMENTATION.md)
- 一句话：在现有分层记忆之上增加 PaperPage（论文页）— Point（原子记忆点）— Link（带证据的跨论文关系）的可重建视图；PaperPage 给人看，Point 给检索和注入，Link 给跨论文推理。

---

## 0. 结论先行

1. 不新建孤立的 LLM Wiki 服务，也不引入外部数据库。Paper Wiki 是现有 memory 的科研知识读模型；**Point 和 Link 都是 memory.Record**，PaperPage 是可删除、可重建的投影缓存。Wiki 不成为第二套事实源。
2. 以论文为聚合单位，但不以论文全文为一条记忆。论文页是 UI 和聚合视图，真正参与检索、注入和引用的是 Point；Point 是 memory.Record 的 wiki 视图，不另建存储。
3. **Link 也必须进 memory**（`Kind=fact`、`Tags=["wiki_link"]`），否则“删除 wiki 可重建”不成立；`papers.jsonl` 只是投影缓存，Link 不是。
4. 论文身份必须先补 DOI 链路：`models.PaperMetadata` 增加 `DOI`，`api/context_resolver.go` 改用 `documents.ApplyProviderRecord`，并把 `literature.LibraryStore` 作为 alias 的第一数据源；否则 DOI 分支永远走不到。
5. 证据字段不另造 `Evidence` 类型：在 `memory.Record` 增加 `Refs []memory.SourceRef`（page/section/quote/artifact/task/plan/document）和 `Attributes map[string]any`（结构化扩展），并让 `ValidateRecord` 对 `Refs` 做 secret/hidden/injection 检查。
6. 确定性抽取优先。PaperMetadata、Document.Outline、PlanGraph、ResearchTrialLedger、EnvironmentManifest、Artifact 直接映射；**LLM 只负责 TLDR、非结构化段落摘要和 Link 类型裁决**，LLM-only 内容默认 pending_review。`ClaimRubric` 是 LLM 产物，不属于确定性抽取。
7. 先做 PaperPage + Point + 显式引用（W0/W1），再做 typed Link（W2），最后做 LLM 摘要与语义候选（W3/W4）。W1 复用现有 `memory/extract.go` 与 `memory/extract_trial.go`，不重写抽取器。

---

## 1. 背景与问题

现有 [Agent 记忆系统](2026-11-01-agent-memory-system.md) 已经提供了分层记忆、JSONL + snapshot、BM25/实体索引、RRF/MMR、core/retrieved 预算、引用与 trust/evidence、计划/任务/文档/AutoResearch 抽取、ChatSession 与长对话摘要压缩。

但在科研助手场景中，用户真正需要的不是一堆按时间排列的 Record，而是：

1. 这篇论文到底是什么：标题、作者、年份、venue、arXiv/DOI、TLDR、章节地图；
2. 这篇论文有哪些可复用结论：方法、实验、指标、局限、claim、open question；
3. 我在这个项目里对这篇论文做过什么：解析、复现、失败、修复、评测；
4. 这篇论文和其他论文是什么关系：引用、改进、矛盾、同数据集、同方法、同仓库；
5. 回答时能不能给出精确证据：paper/page/section/claim/artifact/task。

当前 Record 结构能存这些内容，但缺少一个面向论文的聚合层和关系层。用户看到的是零散记忆，而不是论文知识页加交叉索引。

---

## 2. 目标与非目标

### 2.1 目标

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 论文页可读 | 输入 arXiv/DOI/标题或上传 PDF 后生成 PaperPage：元数据、TLDR、章节、Point 列表 |
| G2 | Point 可检索可引用 | 每个 Point 有 kind、文本、evidence、source、status；可被 chat/task 检索并返回 citation |
| G3 | 任务经验挂到论文 | Plan/Task/Trial 的 episode/negative/procedure 自动生成 task/result 类型 Point，引用 plan_id/task_id |
| G4 | 论文交叉索引 | 至少支持 cites/extends/contradicts/uses_same_dataset/uses_same_method/same_repo 六类 Link；`reproduces/related/supersedes` 按证据逐步放开 |
| G5 | 交叉可治理 | 每条 Link 有 evidence、confidence、status、revision；LLM-only 链接 pending_review，可人工确认/拒绝 |
| G6 | 不破坏现有记忆 | Paper Wiki 不改变 core/retrieved 预算与缓存顺序；不复制全文；关闭开关后完全回退 |
| G7 | 可重建 | 删除 wiki 目录或 `WIKI_ENABLED=false` 后，PaperPage 从 memory + plan/document 重建，**Link 从 memory 重建（因为 Link 本身是 memory.Record）**，用户 confirm/reject 决策保留 |

> 目标编号使用 G1–G7，实施阶段使用 W0–W4，二者分离；v1.0 中 W1/W2 同时指代目标与阶段，容易混淆。

### 2.2 非目标

- 不替换 PlanGraph、Artifact、DocumentStore；只做索引、摘要、引用与关系。
- 不把论文全文、完整工具输出、CoT、base64 图片写入 Wiki 或 memory。
- 不默认启用 embedding 或图数据库；W4 可选，默认关闭。
- 不做跨用户或团队共享；owner 隔离是硬边界，alias 表按 owner 分区。
- 不把 LLM 生成的结论直接当事实；所有 LLM-only 内容默认 pending_review。
- 不做 PDF 参考文献解析的强依赖：W2 用 OpenAlex/Crossref 的引用图；PDF references 作为 W2.5 可选项。

---

## 3. 核心概念

### 3.1 PaperPage

PaperPage 是聚合视图，不是事实源。它由三类数据物化而成：

1. memory.Record 中与该论文关联的 Point（Tags 含 `wiki_point` 与 `paper:<paper_id>`）；
2. PlanGraph/Artifact/ResearchTrialLedger 中的任务与实验证据（经 memory 记录后引用）；
3. documents.Document 的章节、页数、元数据，只存引用与摘要。

PaperPage 至少包含：owner_id、project_id、canonical paper identity、bibliographic metadata（含 DOI）、tldr（标记 inferred）、section map（章节标题和页码范围，不复制正文）、point ids、link ids、reproduction status、revision、更新时间。

**可重建性契约**：删除 `data/wiki/` 后，PaperPage 可从 memory + documents + literature library 完整重建；重建不重新解析 PDF、不重新调用 LLM。

### 3.2 Point

Point 是原子记忆点，是检索、注入、引用、评估的最小单位。**Point 不落盘为独立实体，它就是 memory.Record 的一个查询视图**：

```text
Point = memory.Record
  Tags: ["wiki_point", "paper:<paper_id>", "point:<kind>"]
  Entities: [{type:"paper", value:<paper_id>}, ...]
  Refs: []memory.SourceRef   // page/section/quote/artifact/task/plan/document
  Attributes: {point_key, subject, attribute, ...}
```

| Point kind | 中文 | 典型内容 | 来源 | 默认 trust/status |
|---|---|---|---|---|
| claim | 论文结论 | 论文声称某方法在 X 上达到 Y | ClaimRubric（LLM） | inferred / pending_review |
| method | 方法要点 | 模型结构、训练目标、关键算法 | paper_parse（LLM）+ quote 校验 | inferred / pending_review |
| result | 实验结果 | 指标、基线、消融、数据集 | artifact metrics（确定性） | verified / active |
| limitation | 局限 | 论文自述或复现发现的限制 | paper_parse / user | inferred / pending 或 user_explicit |
| task | 任务点 | 某次 Plan 的任务、状态、产出 | TaskNode 终态（复用 RecordTask） | verified / active |
| procedure | 程序性经验 | 失败后修复成功的具体动作 | TaskNode / Trial（已有 extract） | verified / active |
| negative | 负结果 | 复现失败、候选被拒、方法不可行 | PlanOutcome / Trial（已有 extract） | verified / active |
| open_question | 待验证问题 | 需要后续实验确认的点 | LLM/用户 | inferred / pending_review |
| user_note | 用户笔记 | 用户显式补充或修正 | 用户编辑 | user_explicit / active |
| constraint | 环境约束 | provider/image/python/GPU/network | EnvironmentManifest | verified / active |

每个 Point 必含：`paper_id`、`kind`、`text`（结论在前，建议 300 字以内，单条不超过 800 字）、`refs`（至少一项：page/section/quote/artifact_ref/task_id/plan_id/document_id）、`source`、`trust`、`status`、`confidence`、`valid_from/valid_to`（可选）、`derived_from`（如果是归纳或 reflection）。

**引文校验**：凡是带 `quote` 的 Point，写入前必须用 `documents.LocateQuote`（`documents/quote.go:34`）在对应 Document 中定位；定位失败则降级为 pending_review 并清空 quote，不允许“模型编造页码”。

### 3.3 Link

Link 是论文之间的类型化、带证据关系。**Link 也是 memory.Record**，不是独立文件：

```text
Link = memory.Record
  Kind: fact
  Tags: ["wiki_link", "link:<type>"]
  Entities: [{type:"paper", value:<from>}, {type:"paper", value:<to>}]
  Refs: []memory.SourceRef（quote/doi/artifact/task 证据）
  Attributes: {from_paper, to_paper, link_type, source, confidence}
  // revision 用 Record.Version；superseded_by 用 Validity.SupersededBy
```

| Link type | 含义 | 最低证据 | 对称性 |
|---|---|---|---|
| cites | A 引用 B | 参考文献条目 / DOI / arXiv 引用 | 单向 |
| extends | A 在 B 基础上改进 | 引用段落 + 方法描述 | 单向 |
| contradicts | A 与 B 结论或指标冲突 | 两篇论文的 claim/result Point | 对称 |
| uses_same_dataset | 使用同一数据集 | 数据集实体 + 页面或章节证据 | 对称 |
| uses_same_method | 使用同一方法或模型 | 方法实体 + 文本证据 | 对称 |
| same_repo | 使用同一代码仓库 | repo URL / commit | 对称 |
| reproduces | 本项目的复现任务针对该论文 | Plan/Task/Artifact ref | 单向 |
| related | 一般相关 | BM25/实体候选，仅 pending_review | 对称 |
| supersedes | 新结论替代旧结论 | Point 的 supersede 链 | 单向 |

原则：`cites` 可以由参考文献/OpenAlex 确定性生成并直接 active；`uses_same_dataset/method/repo` 由实体重合生成，可 active（至少一个实体加一个来源）；`extends/contradicts/related` 如果来自 LLM，默认 pending_review；任何 Link 都不能只有向量相似度作为唯一证据。

### 3.4 PaperIdentity

论文身份是 Wiki 质量的关键。优先级：**DOI > arXiv ID > 规范化标题+年份**。canonical id 使用 `paper-` 加 canonical_key 的哈希（**哈希输入必须包含 owner_id**，避免跨 owner 冲突）：

```text
canonical_key =
  DOI 存在 ? "doi:" + lower(trim(doi))
  : arXiv 存在 ? "arxiv:" + lower(paperid.StripVersion(arxiv))
  : "title:" + literature.normalizeTitle(title) + ":" + year
paper_id = "paper-" + sha256(owner_id + "|" + canonical_key)[:24]
```

alias 表保存 doi、arxiv、title、openalex、semantic_scholar、library record id 到 canonical id 的映射，**按 owner 分区**。同一 owner 下，标题规范化后完全相同的别名合并；作者或年份冲突时进入 pending_review，不自动合并。用户可手动 merge/split，merge 产生 merged_from 记录并保留历史。

**前置依赖（v1.1 新增）**：当前 `models.PaperMetadata` 没有 `DOI` 字段，`documents.ApplyProviderRecord` 把 DOI 作为第二个返回值单独返回，而 `api/context_resolver.go` 只调用 `ApplyProviderMetadata` 并把 DOI 丢掉。W0 前必须完成：

1. `models.PaperMetadata` 增加 `DOI string`（json tag `doi,omitempty`）；
2. `documents.ApplyProviderMetadata` 保留 doi（或新增 `ApplyProviderRecord` 调用点）；
3. `api/context_resolver.go` 改用 `ApplyProviderRecord` 并把 DOI 写入 `ResearchContext.Paper`；
4. `literature.LibraryStore`（已有 DOI/ArxivID 与 `sameRecord` 去重）作为 alias 的第一数据源；
5. `literature.normalizeTitle` 当前包私有（`literature/literature.go:617`），需导出为 `literature.NormalizeTitle` 供 Wiki 使用。

---

## 4. 数据模型

### 4.1 memory 扩展（`internal/memory/types.go`，向后兼容）

```go
// SourceRef 是一条记忆引用的外部证据（与 Evidence 分离：Evidence 描述
// "这条记录有多可信"，SourceRef 描述 "这条记录引用了哪段原文/产物"）。
type SourceRef struct {
    DocumentID  string
    BlockID     string
    Page        int
    Section     string
    Quote       string
    ArtifactRef string
    TaskID      string
    PlanID      string
}

type Record struct {
    // ...现有字段保持不变...
    Refs       []SourceRef
    Attributes map[string]any
}
```

（实现时按项目惯例补 json tag；Quote 建议 `json:"quote,omitempty"`。）

`ValidateRecord`（`memory/redact.go:37`）增加：

- `Refs` 的 `Quote` 单条 ≤ 500 字符；
- 对 `Refs` 的 `Quote` 调用 `ScanInjection`（现有 `memory.ScanInjection` 目前只被测试调用），命中则要求调用方降级 trust 或拒绝；
- `Refs` 的 `DocumentID`/`ArtifactRef` 需要经过 `IsHiddenSource`；`Quote` 也要扫描 hidden 标记。

### 4.2 Wiki 读模型（`internal/wiki/types.go`）

```go
type PointKind string
const (
    PointClaim        PointKind = "claim"
    PointMethod       PointKind = "method"
    PointResult       PointKind = "result"
    PointLimitation   PointKind = "limitation"
    PointTask         PointKind = "task"
    PointProcedure    PointKind = "procedure"
    PointNegative     PointKind = "negative"
    PointOpenQuestion PointKind = "open_question"
    PointUserNote     PointKind = "user_note"
    PointConstraint   PointKind = "constraint"
)

// Point 是 memory.Record 的只读视图，不单独持久化。
type Point struct {
    Record  memory.Record
    PaperID string
    Kind    PointKind
    Refs    []memory.SourceRef
}

type LinkType string
const (
    LinkCites       LinkType = "cites"
    LinkExtends     LinkType = "extends"
    LinkContradicts LinkType = "contradicts"
    LinkSameDataset LinkType = "uses_same_dataset"
    LinkSameMethod  LinkType = "uses_same_method"
    LinkSameRepo    LinkType = "same_repo"
    LinkReproduces  LinkType = "reproduces"
    LinkRelated     LinkType = "related"
    LinkSupersedes  LinkType = "supersedes"
)

// Link 映射自 memory.Record；OwnerID/ProjectID/Revision/SupersededBy 是
// 从 Record.Scope / Record.Version / Validity.SupersededBy 解出的读模型。
type Link struct {
    ID           string
    OwnerID      string
    ProjectID    string
    FromPaper    string
    ToPaper      string
    Type         LinkType
    Evidence     []memory.SourceRef
    Source       string
    Confidence   float64
    Status       memory.Status
    Revision     int
    SupersededBy string
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type PaperPage struct {
    PaperID      string
    OwnerID      string
    ProjectID    string
    Title        string
    Authors      []string
    Year         int
    Venue        string
    ArxivID      string
    DOI          string
    Abstract     string
    TLDR         string
    TLDRInferred bool
    // Sections 直接复用 documents.SectionInfo，避免两套章节类型漂移。
    Sections     []documents.SectionInfo
    PointIDs     []string
    LinkIDs      []string
    Reproduction ReproductionStatus
    Revision     int
    UpdatedAt    time.Time
}
```

说明：

- `Point`/`Link` 不定义自己的 `Evidence` 类型，统一用 `memory.SourceRef` + `memory.Evidence`。
- `PaperPage.Sections` 直接复用 `documents.SectionInfo`（`documents/outline.go:12`）；如果 `documents` 后续增加 Level/Number（审计 P1-3），Wiki 自动跟随。
- `PointRefs` 不复制正文，只存引用。

---

## 5. 与现有 memory 系统的关系

### 5.1 Point 与 Link 都存 memory，PaperPage 存投影

- **Point**：`memory.Record`，Kind 按 §3.2 映射，Tags 加 `wiki_point`、`paper:<paper_id>`、`point:<kind>`，Entities 加 paper/repo/dataset/metric/model，`Refs` 存证据，`Attributes` 存 point_key/subject/attribute。
- **Link**：`memory.Record`，`Kind=fact`，`Tags=["wiki_link","link:<type>"]`，Entities 存 from/to paper，`Refs` 存证据，`Attributes` 存结构化字段。Link 进入 memory 后自然获得 `ContentHash` 去重、Status 生命周期、`Validity.SupersededBy`、`DerivedFrom` 与 owner 隔离。
- **PaperPage**：物化缓存，由 Point、Document、Plan 数据生成，可删除重建；只存 `paper_id` + `point_ids` + `link_ids` + 可从源头重建的元数据。

这样关闭 Wiki 时现有 memory 仍然工作；删除 Wiki 不丢 Point，也不丢 Link；重建 Wiki 不需要重新解析 PDF，也不需要重新调用 LLM。

### 5.2 与 Record 字段的映射

| Wiki 字段 | memory.Record 映射 |
|---|---|
| paper_id | Entities 中 `{Type:"paper", Value:paperID}` + `Tags paper:<id>` |
| point kind | `Tags point:<kind>`（读模型解析） |
| text | `Title` 加 `Content`，结论在前 |
| evidence | `Refs []SourceRef` 加 `Evidence.Class/Confidence` |
| trust/status/confidence | 同名或等价字段（`Trust`/`Status`/`Confidence`） |
| derived_from | `DerivedFrom` |
| page/section/quote | `Refs`（不再需要 side table） |
| link from/to/type | `Attributes` + `Entities` |
| link revision/superseded_by | `Record.Version` / `Validity.SupersededBy` |

**不再采用 v1.0 的 PointMeta 侧表方案**：`Refs` 进入主记录后，side table 与主记录不一致的问题消失。

---

## 6. 存储与索引

### 6.1 目录布局（v1.1）

```text
data/wiki/
  owners/<sha256(owner_id)[:16]>/
    papers.jsonl
    papers.snapshot.json
    aliases.json
    settings.json
```

- `papers.jsonl` + `papers.snapshot.json`：PaperPage 投影缓存（与 memory 的 WAL/snapshot 同构，但**不是事实源**，删除可重建）。
- `aliases.json`：该 owner 的 alias 表；DOI/arXiv 直接映射，标题+年份作为低置信别名；支持多对一。
- `settings.json`：该 owner 的 Wiki 设置（对比 `memory.SettingsStore`）。
- **没有 links.jsonl**：Link 存在 `MEMORY_DIR` 的 memory store 中（`Kind=fact`、`Tags=["wiki_link"]`），由 `memory.FileStore` 的 WAL/snapshot 保证持久化。
- PaperPage 按 owner/project 分区；Link 的 owner 来自 `Record.Scope.OwnerID`。

### 6.2 索引

```text
byOwner      map[string][]paperID          // 从 papers.snapshot 重建
byPaperPoint map[paperID][]pointID         // 从 memory Tags 重建
byPointPaper map[pointID]paperID
byEntity     map[ownerID]map[entityKey]map[paperID]struct{}
byLinkFrom   map[ownerID]map[paperID][]linkID
byLinkTo     map[ownerID]map[paperID][]linkID
byLinkType   map[ownerID]map[LinkType][]linkID
byPointKey   map[ownerID]map[string]pointID // 用于同一 subject/attribute 的 supersede
```

BM25 继续使用 memory 的倒排索引检索 Point；Wiki 只增加 paper/entity/link 导航，不重复实现全文检索。

### 6.3 物化策略

PaperPage 不在每次查询时实时拼装，由 `WikiBuilder` 增量更新。触发包括 Point 新增/更新、Document 解析完成、Plan 终态、Trial 结束、用户编辑。单个 PaperPage 的 point_ids 按 importance 和 updated_at 排序；页面最多展示 100 个 Point，超出分页。

**一致性**：`WikiBuilder` 以 `memory.Record.UpdatedAt` 为水位；PaperPage 的 `revision` 取该论文下所有 Point/Link 的最大 `Record.Version` 的摘要值。重建时从 memory 全量扫描，产出与增量一致的 `revision`。

---

## 7. 写入管道

### 7.1 触发源

| 触发 | 同步/异步 | 产出 |
|---|---|---|
| 文档上传/paper_parse | 异步 | PaperPage 元数据、section map、paper summary（pending） |
| ClaimRubric 抽取 | 异步 | claim Point（pending_review，LLM） |
| Plan 创建 | 同步（现有行为：`plan_runtime.go:555-557` 已调用 `memory.RecordPlan`） | 初始 episode + paper metadata fact + env constraint；Wiki 只刷新 PaperPage，不重复写 |
| Plan 完成/失败/取消 | 异步（onTerminal + CancelPlanWithActor） | task/result/negative Point，reproduction 状态 |
| Task 完成/失败 | 异步（现有 RecordTask） | task/procedure/negative Point |
| AutoResearch trial | 异步（现有 RecordTrial） | negative/procedure/result Point |
| 用户编辑 | 同步 | user_note 或 user_explicit Point |
| 文献库导入/更新 | 同步 | alias 表更新 |
| Link builder | 异步 | Link（memory.Record）与 pending_review 候选 |

**接线位置（v1.1 修正）**：

- 写 task/plan Point 的现有入口有两处：
  1. **计划创建时**：`api/plan_runtime.go:555-557` 直接调用 `memory.RecordPlan`（生成初始 episode、paper metadata fact、env constraint）；
  2. **计划终态时**：`scheduler.MemorySink`（`scheduler/scheduler.go:39-44`、`api/memory_sink.go:14-28`）再调用一次 `memory.RecordPlan`，内容按 `PlanGraph` 状态覆盖。
  不是 `planner.PlanFinalizer`（后者只计算 outcome，`planner/semantic_validation.go:118-122`）。
- `scheduler_execute.go:189-192` 只在**正常终态**调用 `memorySink.RecordPlan`；`CancelPlanWithActor`（`scheduler/scheduler_control.go:17-73`）不调用。**但计划创建时已写过一次**（`plan_runtime.go:555-557`），所以取消的计划不会完全没有记录，只是不会更新为最终 outcome。W1 仍应在取消路径补一次 RecordPlan/Wiki 刷新，让 reproduction status 与 negative Point 反映“已取消”。
- `api/memory_sink.go` 的 RecordTask/RecordPlan 之后调用 `wiki.RefreshPaper(ctx, plan)`；刷新失败只记日志，不影响计划结果。

### 7.2 抽取规则（v1.1 修正）

1. PaperMetadata 到 PaperPage：title/authors/year/venue/arXiv/DOI/abstract；不复制 PDF 全文。DOI 必须来自 `ApplyProviderRecord` 或 `literature.Record`。
2. Document.Outline 到 Sections：章节标题、页码、字符数，最多 20 个 section；直接使用 `documents.SectionInfo`。
3. **ClaimRubric 不是确定性源**：`claim_rubric_extract` 调用 LLM（`agent/claim_evidence.go:70-92`），claim Point 默认 `Trust=TrustInferred`、`Status=StatusPending`、`Evidence.Class="llm_inference"`；用户确认后转 active。
4. paper_parse output 到 method/limitation summary：同样是 LLM 产物，默认 pending；带 quote 的需 `documents.LocateQuote` 校验。
5. PlanGraph 到 task Point：**复用现有 `memory.RecordTask`**（`memory/extract.go:208-259`），仅对 failed 或 completed+RunCount>1 生成记录；不要为正常完成的每个节点额外生成 Point，避免与现有 mem-task-* 重复。
6. Artifact metrics 到 result Point：要求 `Artifact.Kind=="metrics"` 且 `Artifact.Payload` 是合法 JSON（`models/artifact.go:11-27`），否则不生成；保留 artifact_ref。
7. EnvironmentManifest 到 constraint Point：provider/image/python/GPU/network；TTL 90 天。
8. ResearchTrialLedger 到 negative/procedure Point：**复用现有 `memory.RecordTrial`**（`memory/extract_trial.go:14-71`）。
9. PlanOutcome failed 或 blocked 到 negative Point：importance 8 到 9（现有 `memory.RecordPlan` 已覆盖）。

### 7.3 去重与冲突（v1.1 修正）

- **复用 memory 的 `ContentHash`**（`memory/types.go:181`）作为第一道去重；完全重复时 `UseCount++`，不新增记录。
- 第二道去重：同一 `paper_id + kind + subject + attribute` 的 Point，用 `Attributes.point_key` 合并；新结论 supersede 旧结论（`Status=StatusSuperseded`，`Validity.SupersededBy` 指向新记录）。注意 `Put` 的覆盖语义（`store.go:249-269`）：同 ID 覆盖不会自动生成 supersede 链，所以第二道合并必须**新建 point id**（`point-<point_key>-<seq>`）并显式把旧记录标为 superseded，不能复用旧 ID。**不再使用 v1.0 的独立 sha256(paper_id+kind+text+evidence_ref) 哈希**，避免与 memory 的 byHash 两套口径。
- LLM-only Point 默认 pending_review；用户确认或第二次证据命中后 active。
- Link 去重：**稳定 key** = `sha256(owner|from|to|type)[:24]`；**记录 ID 带 revision**：`link-<key>-r<N>`。注意 `memory.FileStore.Put`（`store.go:249-269`）对同 ID 更新是原地覆盖（旧版本只在 WAL 里），不会保留 supersede 链；因此每次 Link 变更必须用新 ID（r2、r3…）并把旧的 `Validity.SupersededBy` 指向新 ID，才能实现“保留历史 revision”。
- Link 冲突：同一 from/to/type 只保留最新 active；旧 link 归档为 superseded，不物理删除。

### 7.4 LLM 使用边界

- 只用于 TLDR、非结构化段落摘要、Link 类型歧义裁决。
- 输入必须带原文片段或证据；输出严格 JSON（point/link 加 `evidence_quote`）。
- 不允许 LLM 生成整篇论文的事实而无 quote/page。
- 输出 schema 校验失败直接丢弃，不重试超过 2 次。
- `WIKI_LLM_MODE` 支持 off/summary/links/all，默认 summary。
- **新增**：任何 LLM 产出的 Quote 进入 `Refs` 前必须通过 `documents.LocateQuote`；定位失败则丢弃 quote 并降级 `StatusPending`。

---

## 8. 论文身份与去重

### 8.1 Canonical ID

```text
canonical_key =
  DOI 存在 ? "doi:" + lower(trim(doi))
  : arXiv 存在 ? "arxiv:" + lower(paperid.StripVersion(arxiv_id))
  : "title:" + literature.NormalizeTitle(title) + ":" + year
paper_id = "paper-" + sha256(owner_id + "|" + canonical_key)[:24]
```

- **owner_id 必须参与哈希**：v1.0 没有 owner，导致两个用户上传同一篇论文会生成相同 paper_id，跨 owner 时语义混乱（虽然查询能隔离，但 alias 表会互相覆盖）。
- 标题规范化：`literature.normalizeTitle`（`literature/literature.go:617`）**当前是包私有函数**，W0 需把它导出为 `literature.NormalizeTitle`（或下沉到 `internal/textnorm`），Wiki 直接调用，不另写一套。
- arXiv 规范化复用 `paperid.StripVersion`（`paperid/arxiv.go:20`，已导出）。

### 8.2 别名（按 owner 分区）

```json
{
  "paper-abc123": {
    "canonical_key": "arxiv:1706.03762",
    "aliases": ["arxiv:1706.03762", "doi:10.48550/arxiv.1706.03762", "title:attention is all you need:2017", "library:5f2c..."],
    "title": "Attention Is All You Need",
    "merged_from": []
  }
}
```

每个 owner 一份 `aliases.json`，路径 `data/wiki/owners/<hash>/aliases.json`。key 必须是“所有别名 → canonical id”的多对一映射，而不是只按 canonical id 索引。

### 8.3 合并规则

- 同 DOI 或同 arXiv 直接合并；
- 标题规范化后相同且年份差不超过 1：候选合并，pending_review；
- 作者集合重叠且标题相似度至少 0.9：候选；
- `literature.LibraryStore` 中 `sameRecord`（`library.go:425`）命中：直接合并（同一 owner）；
- 用户手动 merge 优先，记录 merged_from 和审计事件；
- 合并后旧 PaperPage 保留 archived 引用，不物理删除。

---

## 9. 交叉链接构建

### 9.1 候选来源（v1.1 修正）

1. **引用图**：优先复用 `literature.Client.Citations`（`literature/citations.go:26-108`，OpenAlex references/cited_by），生成 cites，confidence 0.95。
2. **文献库**：同一 owner 下 `literature.Record` 的 DOI/arXiv 映射到 canonical paper id，生成候选。
3. **实体**：repo/dataset/metric/method/model 相同；生成 uses_same_* 或 related 候选，confidence 0.6 到 0.8。
4. **任务**：同一 Plan 或同一 repo 的复现任务；生成 reproduces，confidence 0.9。
5. **文本相似**：BM25 或 embedding 只生成 related 候选，默认 pending_review，confidence 不高于 0.5。
6. **LLM 裁决**：对候选做类型判断（extends/contradicts/related），必须输出 evidence_quote；默认 pending_review。
7. **PDF references（W2.5 可选）**：当前 `documents` 包没有参考文献解析（只有 section/BM25），需要新增专用解析器后再接入；W2 不依赖它。

### 9.2 避免 O(n²)（v1.1 补充）

- 先按实体倒排取候选：`byEntity` 按 owner 分区；单个实体候选数上限 500，超出按 paper.importance 截断；
- 再按引用邻接表取候选；
- 最后才做 BM25 topK（K 不超过 20）的语义候选；
- 每篇论文每次构建最多生成 200 条候选 Link，超出按 confidence 和 importance 截断；
- **对称关系规范化方向**：uses_same_dataset/uses_same_method/same_repo/related 写入时按 `from_paper < to_paper` 的字典序统一，避免 A→B 与 B→A 重复生成；
- **Link 的 owner/project** 来自 `Record.Scope`，所有索引按 owner 分区；跨 owner 的 Link 在入库时直接拒绝。

### 9.3 Link 评分

```text
score =
  sourceWeight        // explicit=1.0, entity=0.8, task=0.9, llm=0.5
  * evidenceWeight    // quote/page/artifact 完整度（0..1）
  * confidence        // 0..1
  * typeWeight        // cites=1.0, contradicts=0.9, related=0.5
```

- score 至少 0.75 且 evidence 完整：active（非 LLM 类型）；
- score 在 0.5 到 0.75：pending_review；
- score 小于 0.5：不写入或 archived candidate。

### 9.4 Link 审查

- UI 提供相关论文面板：type、confidence、evidence、status、revision；
- pending_review 的 Link 可由用户 confirm 或 reject；
- reject 记录原因，避免同一候选反复出现（稳定 Link ID 保证同一候选不会因重建而重新出现）；
- Link 不删除历史，只 archived/rejected/superseded。

---

## 10. 检索与注入

### 10.1 Query 类型

| 场景 | Query | 返回 |
|---|---|---|
| 论文页 | paper_id 或标题 | PaperPage + top points + 1-hop links |
| 任务执行 | task name/description + entities | 相关 Point（claim/method/procedure/negative） |
| 跨论文问答 | 两篇 paper_id + 问题 | 两页 top points + 两页之间的 links 与 evidence |
| claim 验证 | claim 文本 + paper_id | claim Point + evidence + 冲突 links |
| 相关工作 | paper_id + depth=1 | 1-hop typed links，按 type/score 排序 |

### 10.2 排序

- Point：BM25 + entity + 时间 + importance + confidence，沿用 memory 的 RRF 思路；
- Link：typeWeight + evidence + confidence + recency；
- 跨论文扩展只扩展 1 跳，最多 10 条 Link，避免图爆炸；
- 同分按 point_id 或 link_id 排序，保证稳定。

### 10.3 注入格式与预算（v1.1 修正）

```text
<wiki_paper id="paper-..." title="...">
  <wiki_summary>...</wiki_summary>
  <wiki_point id="..." kind="claim" page="3" section="3.2" trust="verified">...</wiki_point>
  <wiki_link type="uses_same_dataset" to="paper-..." confidence="0.82">...</wiki_link>
</wiki_paper>
```

- 继续保留 memory 是数据不是指令的 system 约束；
- Wiki 块放在 `retrieved_memory` 之后、`task_inputs` 之前；
- 预算公式（v1.1 统一）：
```text
base   = floor(PromptLimit * 0.02)          // 目标 2%
hard   = floor(PromptLimit * 0.03)          // 3% 硬上限
wikiBudget = min(max(base, 300), 1500)      // 目标区间 300–1500
if wikiBudget > hard:  wikiBudget = hard
if wikiBudget < 150:   wiki_context 不注入    // 小模型上宁可不给，也不给碎片
```
  其中 `PromptLimit = ContextWindow - MaxOutput - Safety`（`prompts/budget.go:30`）。128k 模型上 base≈2.3k，clamp 到 1500，低于 hard≈3.5k；8k 模型上 hard≈30，低于 150，直接不注入——避免“50 个 token 的论文片段”污染 prompt 且破坏 `PromptReport` 的可解释性。
- **与 retrieved_memory 共享预算**：先从 `settings.RetrievedBudgetTokens`（默认 2000）中划出 wikiBudget，剩余给 retrieved；即 `retrievedBudget' = max(0, RetrievedBudgetTokens - wikiTokens)`，避免两个动态块叠加超预算。
- PaperPage 摘要加 5 到 10 个 Point 加最多 5 条 Link；Point 按 importance 排序，同分按 updated_at。
- 注入的 Point 必须 `Status=StatusActive`、`Evidence.Hidden=false`、`Trust != TrustUntrusted`。

### 10.4 Cache 顺序

```text
system
→ core_memory（稳定）
→ plan_context（稳定）
→ task_instruction（稳定）
→ retrieved_memory（动态）
→ wiki_context（动态）
→ task_inputs
→ volatile
```

Wiki 不进入 core；core 只放项目级稳定事实，避免论文页破坏前缀缓存。retrieved_memory 与 wiki_context 都在动态区，二者的先后不影响稳定前缀。

---

## 11. API 设计

### 11.1 论文

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/wiki/papers | 列表，支持 q/project/status/limit/cursor；limit 默认 50，最大 200 |
| GET | /api/wiki/papers/:paperID | PaperPage 详情（含 point_ids/link_ids） |
| POST | /api/wiki/papers | 手动创建或导入论文页（title/arxiv/doi） |
| PATCH | /api/wiki/papers/:paperID | 修改 title/tldr/reproduction/archived |
| DELETE | /api/wiki/papers/:paperID | 归档，不物理删除 Point |
| GET | /api/wiki/papers/:paperID/points | 分页查询 Point（limit/cursor/kind） |
| POST | /api/wiki/papers/:paperID/points | 手动添加 user_note Point |
| GET | /api/wiki/papers/:paperID/links | 1-hop 关系图，支持 depth（默认 1，最大 1）、type、status、limit（默认 50，最大 200）、cursor；返回 next_cursor |

### 11.2 Point 和 Link 治理

| 方法 | 路径 | 说明 |
|---|---|---|
| PATCH | /api/wiki/points/:pointID | 编辑、确认、supersede |
| DELETE | /api/wiki/points/:pointID | 软删除（写 memory Status=deleted） |
| GET | /api/wiki/links | pending_review 或 type 筛选，limit/cursor |
| POST | /api/wiki/links/:linkID/confirm | 确认 Link（Record.Status=active） |
| POST | /api/wiki/links/:linkID/reject | 拒绝 Link（Record.Status=archived，记录 reason） |
| POST | /api/wiki/build | 触发增量或全量重建，返回 202 + job_id；并发构建返回 409 |
| GET | /api/wiki/build/:jobID | 查询构建进度 |
| GET | /api/wiki/stats | 论文数、Point 数、Link 数、待审核数 |

### 11.3 返回示例

```json
{
  "paper": {
    "paper_id": "paper-7f3c...",
    "owner_id": "u-123",
    "project_id": "proj-attention",
    "title": "Attention Is All You Need",
    "arxiv_id": "1706.03762",
    "doi": "10.48550/arxiv.1706.03762",
    "tldr": "Transformer 用纯注意力替代 RNN/CNN，在 WMT14 EN-DE 达到 27.3 BLEU。",
    "tldr_inferred": true,
    "sections": [{"title": "3.2.1 Scaled Dot-Product Attention", "start_page": 3, "end_page": 3, "chunks": 2, "characters": 1800}],
    "point_ids": ["point-a1", "point-b2"],
    "link_ids": ["link-x1"],
    "revision": 7
  }
}
```

### 11.4 错误语义

- 跨 owner 或 hidden 论文：404（不返回空对象，避免枚举）；
- 构建中：409 wiki_build_in_progress；
- 非法 DOI/arXiv：400 invalid_paper_ref；
- 引文定位失败：422 quote_not_located（PATCH Point 时）。

---

## 12. 前端设计

### 12.1 论文页

新增 `frontend/src/features/wiki/PaperWikiPanel.tsx`：

- 顶部：标题、作者、年份、venue、arXiv/DOI、project、reproduction badge、revision；
- TLDR 区块（标记 inferred）；
- Point 分组：结论、方法、结果、任务、失败与修复、待验证；
- 每条 Point 展示 evidence（页码、章节、quote）、trust、status；点击打开来源（document page / artifact / task）；
- 右侧或底部：相关论文列表（typed links、confidence、pending 标记、revision）；
- 操作：新增 Point、pin、supersede、确认或拒绝 Link、重建。

### 12.2 交叉图

- 以当前论文为中心，显示 1-hop；
- 边颜色和形状按 Link type；
- pending_review 用虚线，active 用实线，rejected 不显示；
- 点击边打开 evidence drawer；
- 不做全库力导向图，避免性能和可读性问题；单页最多渲染 top 20 Link，其余走“查看全部”。

### 12.3 与现有页面集成

- 计划详情“使用的记忆”增加 Paper Wiki 区块：涉及论文、Point 数、链接数；
- MemoryPanel 增加“论文”tab 或跳转；
- Chat citation 显示 paper 和 point，点击打开 PaperPage。

---

## 13. Agent 与 Prompt 集成

### 13.1 接口

```go
type WikiSource interface {
    RetrievePaper(ctx context.Context, q PaperQuery) (PaperContext, error)
    RetrieveForTask(ctx context.Context, q TaskQuery) (WikiContext, error)
    RefreshPaper(ctx context.Context, plan *models.PlanGraph) error
}
```

- `scheduler.RoutedTaskExecutor` 新增可选 `Wiki WikiSource` 字段（与现有 `Memory memory.Store` 并列，`scheduler/executor.go:64-80`）；
- `ChatAgent` 在 memory context 之后追加 wiki context；
- `LibrarianAgent` 在 paper_parse 完成后写 PaperPage/Point（注意：`librarian.go:197-207` 已有写 document memory 的逻辑，Wiki 在 memory 写入后刷新）；
- `api/memory_sink.go` 与 `scheduler.onTerminal`、`CancelPlanWithActor` 在终态刷新 Wiki；
- **不再是 `PlanFinalizer`**（v1.0 写错）。

### 13.2 Prompt section

`prompts.TaskPromptInput` 增加 `WikiContext string`；`agent/task_prompt.go:78-84` 需要把 `sharedContext["wiki_context"]` 传入；`prompts.BuildTaskPrompt` 在 `retrieved_memory` 之后、`task_inputs` 之前插入 `wiki_context`。

**实现约束（v1.1）**：`BuildSectionsWithLimit`（`prompts/budget.go:144`）用 `sort.SliceStable` 按 `Priority` 升序排序，同 priority 保持插入顺序。因此 `retrieved_memory` 与 `wiki_context` 都设为 priority 3，并在 `task_prompt.go:155-157` 先 append retrieved、再 append wiki，即可得到 §10.4 的顺序。`PromptReport.Sections` 增加 `wiki_context`，并记录 `wiki_tokens`。

### 13.3 引用

- 每条注入的 Point 带 point_id、paper_id、page、section；
- `memory.Citation` 增加可选 PaperID/PointID/Page/Section/Quote 字段（向后兼容，`memory/types.go:237-244`）；
- Chat response 返回 citations（含 paper 信息）与 wiki_citations（仅 wiki 来源），二者并列；
- 不允许无来源的 Point 进入注入（pending_review 默认不注入）。

---

## 14. 生命周期、治理与安全

- **owner 隔离**：PaperPage、Point、Link 全带 owner_id；PaperPage 的 OwnerID、Link 的 `Record.Scope.OwnerID` 是硬过滤；跨 owner 查询 404。
- **alias 隔离**：aliases.json 按 owner 分区，路径含 owner hash；禁止全局 alias 表。
- **hidden benchmark**：paper 来源路径在 `BENCHMARK_PRIVATE_ROOT` 下时不入 Wiki；Refs 的 DocumentID/ArtifactRef/Quote 与 PaperPage 的 Title/Abstract/Authors 都要扫描 hidden 标记（当前 `memory.IsHiddenSource` 只看 Source.Ref/URL，需要扩展文本版）。
- **敏感内容**：不存密钥、完整 PDF、base64、隐藏标签；沿用 memory redaction；Link 的 Quote ≤500 字符。
- **注入扫描**：`ScanInjection` 在 Point/Link 写入前调用（当前只被测试调用），命中则 `Trust=TrustUntrusted` 且不参与注入。
- **保留**：Point 默认 365 天；negative 180 天；Link 365 天；PaperPage 长期；用户 pin 不过期。
- **supersede**：同 key 新 Point 旧 Point `status=superseded`，保留历史；Link 通过 `Record.Version`/`Validity.SupersededBy` 保留历史 revision。
- **删除**：用户删除 Point 为软删除（Status=deleted）；PaperPage 归档；重建不复活 deleted。
- **审计**：每次 Point/Link 变更记录 source、actor、reason、revision（复用 `internal/audit`）。
- **多写**：单 backend 单写；文件锁；与 memory 相同的 WAL/snapshot 策略；WikiBuilder 与 memory.FileStore 各自持有目录锁，避免互相阻塞。

---

## 15. 分阶段实施（v1.1）

### P0 基建（W0 之前，1–2 天）

- `models.PaperMetadata` 增加 DOI；`api/context_resolver.go` 改用 `ApplyProviderRecord`；`literature.LibraryStore` 接入 PaperIdentity；
- `memory.Record` 增加 `Refs []SourceRef` 与 `Attributes map[string]any`；`ValidateRecord` 校验 Refs；
- `memory.Citation` 增加 paper/point 字段；
- 单测：DOI 从 provider 到 `ResearchContext.Paper` 的全链路、Refs 的 hidden/secret 校验。

### W0：PaperPage 加 Point 最小切片（2–3 天）

- `internal/wiki` types/store/index，复用 memory 的 FileStore 模式（仅 papers.jsonl + snapshot + owner 分区 alias）；
- PaperPage 从 Document + PaperMetadata + literature.Record 生成；
- Point 从 paper_parse（pending）与 Document metadata 确定性生成；**不包含 ClaimRubric**；
- `GET /api/wiki/papers`、/:id、/:id/points；
- 单测：身份去重（含 DOI）、Point 抽取、owner 隔离、alias 分区。

### W1：任务与实验 Point（1–2 天）

- 复用 `memory.RecordTask`/`RecordPlan`/`RecordTrial`，在记录上打 `wiki_point` 标签；
- `api/memory_sink.go` 的 RecordTask/RecordPlan 之后调用 `wiki.RefreshPaper`；
- 取消路径 `CancelPlanWithActor` 补 RecordPlan + Wiki 刷新；
- Plan 详情显示论文相关 Point；
- 单测：失败到 negative、修复到 procedure、指标到 result。

### W2：显式 Link（3–4 天）

- 复用 `literature.Client.Citations` 生成 cites；实体倒排生成 uses_same_*；
- Link 写入 memory（`Kind=fact`、`Tags=["wiki_link"]`），索引按 owner 分区；
- Link 的对称方向规范化、稳定 ID、revision；
- `GET /api/wiki/papers/:id/links`（分页）；
- 单测：实体候选、Link 去重、方向规范化、owner 隔离。

### W2.5：PDF references（可选，2–3 天）

- 新增 `documents/references.go` 解析参考文献条目；
- 只在 W2 的 OpenAlex 覆盖不足时启用，默认关闭（`WIKI_PDF_REFS=false`）。

### W3：LLM 摘要与待审核 Link（3–4 天）

- TLDR、非结构化段落摘要、Link 类型裁决；
- `documents.LocateQuote` 校验所有 quote；
- pending_review UI、confirm/reject；
- 评测：Point precision、Link precision、quote 定位率。

### W4：语义和图增强（可选）

- embedding 或向量召回、A-MEM 式链接、跨项目提升；
- 默认关闭；先做 50 篇 fixture 的消融：`WIKI_LINK_MODE=explicit|hybrid|llm`，仅当 hybrid 的 Link Precision 显著优于 explicit 才启用。

---

## 16. 测试与验收

### 16.1 单元测试

- PaperIdentity：DOI、arXiv、标题别名、merge/split、owner 分区、冲突。
- PointExtractor：paper_parse（pending）、PlanGraph、Trial、Environment 的映射。
- LinkBuilder：引用、实体、任务、LLM 候选、方向规范化、去重、评分。
- Retrieval：paper/task/cross-paper 查询、预算、1-hop 限制、retrieved 与 wiki 共享预算。
- Security：owner 隔离、hidden 拒绝、secret 拒绝、注入扫描、Refs.Quote 长度。
- Rebuild：删除 wiki 后从 memory 和 plan/document 重建一致；**Link 从 memory 重建后 revision/status 一致**。
- Migration：旧 memory 记录没有 Refs/Attributes 时正常读取（零值兼容）。

### 16.2 Golden 评测（50 篇论文 fixture）

| 类型 | 例子 | 期望 |
|---|---|---|
| 论文页 | Attention PDF 到 PaperPage | 标题、arXiv、DOI、章节、5 个以上 Point |
| 任务点 | 复现失败到 task/negative Point | 关联 plan/task/error |
| 修复经验 | torch 冲突修复到 procedure Point | trigger/action/evidence |
| 指标 | BLEU 27.3 到 result Point | metric/value/dataset |
| 交叉引用 | A 引用 B | cites Link，evidence 可查 |
| 同数据集 | A/B 都用 WMT14 | uses_same_dataset Link |
| 矛盾 | A/B 结论冲突 | contradicts pending_review |
| 去重 | arXiv、DOI、标题三别名 | 同一 PaperPage |
| 拒答 | 没有证据的论文 | 明确无记录，不编造 |

指标：Point Precision/Recall、Link Precision、Citation Correctness、Cross-paper QA Accuracy、Abstention Accuracy、Quote Locate Rate、Token/Latency。

### 16.3 E2E

1. 上传 Attention PDF，PaperPage 生成，章节和 Point 可见；
2. 计划 A 复现，task/result/negative Point 挂到论文页；
3. 计划 B 追问继续，wiki context 注入，回答引用前计划；
4. 两篇同数据集论文，uses_same_dataset Link 出现，UI 可确认；
5. 删除或停用 Wiki，现有 memory 和 chat 仍正常；重建后 Link 与确认状态保留；
6. 长对话 30 轮加计划 A/B，无 wiki 注入导致的错误，Prompt token 有界；
7. 取消一个执行中的计划，negative Point 仍生成（取消路径已接线）。

### 16.4 性能（v1.1 补充假设）

测试环境：8 vCPU / 16 GB RAM / SSD 的 Docker 部署；数据规模 1 万篇论文、10 万 Point。
- PaperPage 查询 p95 小于 50ms（从内存 snapshot 读，无跨进程 IO）；
- 1-hop link 查询小于 20ms（owner 分区索引）；
- Link 构建：单次增量不超过 100ms；全量 1 万篇不超过 30s（后台，单线程）；
- Wiki 注入 token 不超过单次 PromptLimit 的 3%（硬上限），且默认不超过 2%。

---

## 17. 风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| LLM 幻觉 Point 或 Link | 错误知识 | evidence 必填；LLM-only pending_review；quote 必须 LocateQuote 命中 |
| 论文身份合并错误 | 内容串页 | alias 表按 owner 分区 + 人工 merge/split + merged_from 审计 |
| DOI 断链 | 同一论文分裂成多页 | P0 基建先修 DOI；W0 验收要求“上传 vs 文献库导入同一 paper_id” |
| Link 不可重建 | 删目录丢关系 | Link 写入 memory；取消 links.jsonl |
| O(n²) 链接构建 | 卡顿或成本 | 实体倒排 + 引用邻接 + topK；单实体候选上限 500；对称方向规范化 |
| 上下文膨胀 | 回答变慢或错 | Point 为原子单位；wiki 与 retrieved 共享预算；1-hop |
| 缓存回退 | 成本上升 | wiki 动态块后置；core 不变；稳定前缀不受影响 |
| 隐私或 hidden | 泄漏 | owner 隔离；BENCHMARK_PRIVATE_ROOT 过滤 Title/Abstract/Refs |
| 重建不一致 | 页面漂移 | PaperPage 可删除重建；Point/Link 为 memory source of truth；revision 水位 |
| 多写冲突 | 文件损坏 | 单写进程 + 文件锁 + WAL；构建与写入用不同锁 |

回滚开关：

- `WIKI_ENABLED=false`：不读取、不注入、不写；
- `WIKI_MODE=shadow`：构建并记录，不注入；
- `WIKI_LLM_MODE=off|summary|links|all`；
- `WIKI_LINK_MODE=explicit|hybrid|llm`；
- `WIKI_PDF_REFS=false`（W2.5 默认关闭）；
- `WIKI_DIR`：独立目录 A/B；
- `WIKI_BUDGET_TOKENS`：覆盖默认 2% 预算（仍受 3% 硬上限约束）。

---

## 18. 开放问题

1. PaperPage 是否要跨 project 共享？建议默认 project 内；用户 pin 后提升 owner（同一 owner 内跨 project 可见）。
2. Link cites 是否直接用 OpenAlex 或 Semantic Scholar？建议 W2 用 OpenAlex（literature.Client.Citations 已实现），PDF references 作为 W2.5 可选。
3. Point 的页和节证据是否必须？论文 claim 必须；task point 可用 task/artifact 代替。
4. 是否允许 LLM 自动生成 PaperPage tldr？允许，但 tldr 标记 TLDRInferred=true，不参与 claim 证据。
5. 是否把 Wiki 页面放入 core memory？不；core 只放项目级稳定事实。
6. 是否要对旧论文做全量重建？先增量；提供后台 rebuild（POST /api/wiki/build）。
7. Link 的 retention 多久？默认 365 天；用户 confirm 的 Link 视为 pin，不过期。

---

## 附录 A：memory.Record 到 Wiki Point 映射

| memory.Record | Wiki Point |
|---|---|
| Kind=document 或 fact（paper metadata） | Point kind=claim/method/result（LLM 产物 pending） |
| Kind=episode（task/plan） | Point kind=task |
| Kind=procedure | Point kind=procedure |
| Kind=negative | Point kind=negative |
| Kind=reflection | Point kind=claim/open_question（pending_review） |
| Kind=constraint（env） | Point kind=constraint |
| Source.Ref | Refs.task_id/plan_id/document_id |
| Refs[].Page/Section/Quote | 直接来自 Refs |
| Evidence.Class | trust/confidence 参考 |
| Validity | valid_from/valid_to |
| DerivedFrom | derived_from |
| Tags wiki_link | Link 读模型 |

## 附录 B：PaperPage 示例

```json
{
  "paper_id": "paper-7f3c2a19...",
  "owner_id": "u-123",
  "project_id": "proj-attention",
  "title": "Attention Is All You Need",
  "authors": ["Ashish Vaswani", "Noam Shazeer", "..."],
  "year": 2017,
  "venue": "NeurIPS",
  "arxiv_id": "1706.03762",
  "doi": "10.48550/arxiv.1706.03762",
  "tldr": "Transformer 用纯注意力替代 RNN/CNN，在 WMT14 EN-DE 达到 27.3 BLEU。",
  "tldr_inferred": true,
  "sections": [
    {"title": "3.2.1 Scaled Dot-Product Attention", "start_page": 3, "end_page": 3, "chunks": 2, "characters": 1800}
  ],
  "point_ids": ["point-a1", "point-b2", "point-c3"],
  "link_ids": ["link-x1"],
  "reproduction": {"status": "failed", "last_plan_id": "p-abc", "last_error": "torch version conflict"},
  "revision": 7,
  "updated_at": "2026-11-02T10:00:00Z"
}
```

## 附录 C：Link 示例

```json
{
  "id": "link-x1",
  "owner_id": "u-123",
  "project_id": "proj-attention",
  "from_paper": "paper-7f3c2a19...",
  "to_paper": "paper-8a2b1c30...",
  "type": "uses_same_dataset",
  "evidence": [
    {"page": 3, "section": "4.1", "quote": "We evaluate on WMT 2014 English-German..."}
  ],
  "source": "entity_overlap",
  "confidence": 0.86,
  "status": "active",
  "revision": 1,
  "created_at": "2026-11-02T10:00:00Z"
}
```

## 附录 D：v1.0 → v1.1 修订对照

| 评审编号 | v1.0 问题 | v1.1 修订 |
|---|---|---|
| P0-1 | DOI 在 PaperMetadata 缺失、context_resolver 丢弃 DOI | §3.4 增加前置依赖；§8.1 canonical key 用 DOI；P0 基建先修 |
| P0-2 | Link 只存 links.jsonl，删除即丢 | §5.1/§6.1：Link 写入 memory，取消 links.jsonl；G7 可重建性重写 |
| P0-3 | 自定义 Evidence 与 memory.Evidence 冲突 | §4.1 新增 SourceRef；Point/Link 复用 memory.SourceRef |
| P1-4 | Link 无 owner、aliases 全局 | §3.3/§4.2 Link 带 owner/project；§6.1/§8.2 alias 按 owner 分区 |
| P1-5 | ClaimRubric 被误标为确定性 | §3.2/§7.2：claim 默认 inferred/pending；复用 RecordTask/RecordTrial |
| P1-6 | wiki 预算与 prompt section 优先级冲突 | §10.3 定义统一预算公式与共享策略；§13.2 明确 section 位置 |
| P1-7 | O(n²) 与对称 Link 重复 | §9.2 增加实体候选上限与对称方向规范化 |
| P2-8 | links API 无分页 | §11.1 增加 limit/cursor，默认 50/最大 200 |
| P2-9 | Sections 与 documents.SectionInfo 重复 | §4.2 直接复用 documents.SectionInfo |
| 矛盾项 | 文件命名、Link 类型数量、priority 矛盾 | §6.1 统一 papers.jsonl；§2.1 G4 明确 6 类 + 3 类逐步放开；§13.2 统一 priority；目标编号改为 G1–G7 |

---

## 总结

Paper Wiki 的价值不在于把论文摘要存起来，而在于把科研助手的使用过程组织成论文页、Point 和 Link 三层：论文页是用户可读、可审阅的知识入口；Point 是可检索、可引用、可复用的原子记忆；Link 是带证据的跨论文关系，支持相关工作、矛盾检测、综述与复现经验迁移。

它建立在现有 memory 之上：Point 与 Link 都是 memory.Record，PaperPage 是可删除、可重建的投影缓存。先做确定性、可审计的 PaperPage/Point/显式 Link，再逐步引入 LLM 摘要和语义候选，既不破坏现有记忆，也不让 Wiki 变成不可控的 LLM 编造知识库。
