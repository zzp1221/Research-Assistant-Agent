# 记忆系统压测缺陷修复方案（Remediation Plan）

> **版本**：v1.0（2026-11-03）
> **基线**：master + feature/memory，commit d6b3ad5（本地 master 与 feature/memory 同一提交）
> **依据**：[记忆系统压力测试报告](../../reports/memory-stress-2026-11-03-stress-2026-11-03-r1/REPORT.md)、[测试方案](2026-11-03-memory-stress-test-plan.md)
> **范围**：报告中的 17 条主用例失败 + 2 条真实 LLM 补充失败，共 19 条缺陷、13 类根因
> **性质**：修复实施文档，不是测试报告；本文的“修复方案”均在编码前给出，落地后须用第 8 节回归验证

---

## 0. 结论先行

- 19 条缺陷中 **S1 共 12 条**，集中在四类：作用域隔离、身份/去重、中文检索、证据注入；其中“未传 project_id 泄漏”“ContentHash 跨论文去重”“中文论文查询回退到无关论文”“220 篇后最早 20 篇无 Link”属于**必须优先修复**的正确性问题。
- 2 条缺陷是**真实 LLM 下才暴露的回答质量/证据问题**：长论文正文没有进入 chat prompt、跨论文回答没有 wiki 引用。修复它们需要补“文档上下文注入 + 引用返回”，不是改模型。
- 其余失败（LC-09/LC-13/LC-15、PF-01/PF-06、CP-13）是**稳定性与性能问题**：写失败未回滚、10 万条导入超时、Link 构建 O(n²)、检索一次请求跑两遍。
- 建议按 A→B→C→D 四批修复，预计 5 个工作日；每批都必须跑对应回归用例并更新报告。

---

## 1. 修复目标与验收门槛

### 1.1 发布门槛（与测试方案 §7.3 对齐）

| 门槛 | 要求 |
|---|---|
| P0 用例 | 78/78 通过 |
| S0/S1 缺陷 | 0 条未关闭 |
| Needle Recall（长任务） | ≥0.95 |
| 长论文 Quote Locate | ≥0.95（本轮 100/100） |
| Link Rebuild Jaccard | ≥0.99（本轮 1.0） |
| 120 篇全量构建 | ≤30s（本轮 132s） |
| 1 万条检索 p95 | <50ms（本轮 51.24ms） |
| 10 万条导入 | ≤10min（本轮 8.5 万条/480s，未达标） |
| 真实 LLM 长论文问答 | 3/3 可回答且带 document/page 引用 |

### 1.2 修复原则

1. **先正确性后性能**：隔离、去重、身份、中文匹配不做“参数调优”，直接改语义。
2. **失败要显式**：存储层任何降级（锁冲突、快照损坏、只读目录、磁盘满）不得静默回退内存库；要么 fail-closed，要么显式只读降级并在 /api/health 暴露。
3. **写路径先持久化再改内存**：避免“接口报错但记录已进内存”的分裂状态。
4. **所有内部全量扫描 paginate**：200 条上限只能约束 API 分页，不能约束构建/匹配/重建。
5. **证据优先**：回答必须有 document/page/point 级引用；没有证据时明确拒答。
6. **可回滚**：新增开关默认保持当前行为仅限过渡期；数据迁移必须可逆或可重建。

---

## 2. 缺陷与根因总表

| 缺陷 | 级别 | 用例 | 根因位置（d6b3ad5） | 修复批次 |
|---|---|---|---|---|
| DEF-001 | S2 | CP-02 同标题不同年误合并 | internal/wiki/identity.go:203（年份差 ≤1 即合并） | A |
| DEF-002 | S1 | CP-03 同标题同年不同作者误合并 | internal/wiki/identity.go:197-207（无作者校验） | A |
| DEF-003 | S1 | CP-12 220 篇后最早 20 篇无 Link | internal/wiki/store.go:325（limit 截断 200）+ links.go:478 | C |
| DEF-004 | S1 | CP-13 120 篇构建 132s | internal/wiki/links.go:331（每次候选扫描 5000 条 Link） | C |
| DEF-005 | S1 | CP-25 中文查询回退注入无关论文 | internal/wiki/retrieve.go:101（strings.Fields）、124-135（无条件回退最近 2 篇） | A |
| DEF-006 | S3 | LC-09 磁盘满返回 400 且残留记录 | internal/memory/store.go:202（先改内存后 appendWAL） | B |
| DEF-007 | S2 | LC-13 10 万条导入超时 | internal/api/memories.go:210（逐条 Put + 每条 fsync） | B |
| DEF-008 | S2 | LC-15 重建后 LinkIDs 不一致 | internal/wiki/links.go 重建顺序/依赖，需复跑确认 | C |
| DEF-009 | S1 | LP-07 跨论文同文本 Point 去重 | internal/memory/types.go:197（ContentHash 不含 paper_id/tags） | A |
| DEF-010 | S1 | LT-01 20 条 core 针只召回 19 条 | internal/memory/retrieve.go:292（perKindMax=3 对所有 kind）、renderWithBudget 512 token | A |
| DEF-011 | S1 | LT-14 环境 shadow 对 chat 无效 | internal/api/chat_runtime.go:104-108（只读 per-owner Settings.Mode） | A |
| DEF-012 | S1 | LT-16 未传 project_id 泄漏 | internal/memory/retrieve.go:36（空 project 不过滤） | A |
| DEF-013 | S2 | PF-01 1 万条检索 p95 51ms | internal/api/memories.go 先 Search 再 Retrieve（两次 searchLocked） | C |
| DEF-014 | S2 | PF-06 120 篇构建 300s 超时 | 同 DEF-004 + 全局 build 锁（internal/api/wiki.go:392） | C |
| DEF-015 | S2 | RB-03（复跑已通过） | 首次运行环境抖动，非代码缺陷 | 关闭观察 |
| DEF-016 | S1 | SC-02 跨 project 泄漏 | 同 DEF-012（matchQuery） | A |
| DEF-017 | S2 | SC-11 嵌套 attributes 绕过 hidden 校验 | internal/memory/redact.go:90 附近只检查一层字符串 | D |
| DEF-018 | S1 | REAL-LP-23 长论文问答无正文 | chat 只注入 wiki 元信息；WIKI_LLM_MODE=off 时无 TLDR/摘要点 | A/D |
| DEF-019 | S2 | REAL-CP-17 跨论文无 wiki_citations | internal/wiki/retrieve.go:141（只为 Point 生成 citation，paper 级无引用） | A/D |

补充观察（不单独立缺陷单）：

- H-02（任务链路不读 per-owner settings）本轮未复现：LT-13 与 RB-09 均通过；保持现有单测，不阻塞发布。
- H-08（TLDR 只读前 24000 字符）本轮未覆盖：LP-03 只验证了文档长度；需 WIKI_LLM_MODE=summary 专项。
- H-09（重建 Link 漂移）本轮未复现：CP-06 Jaccard=1.0、CP-07 决策 10/10 保留、CP-14 无重复 active；LC-15 单独失败，先按 DEF-008 复跑确认。
- RB-03 首次失败为“chat 404”，独立复跑通过；判定为环境/端口抖动，不立缺陷。

---

## 3. 批次 A：正确性与隔离（1.5 天，S1 优先）

### A1. 作用域过滤必须严格（DEF-012 / DEF-016，R-07 / H-04）

**现象**：项目级记忆在查询未带 project_id 时出现在检索结果与 chat prompt 中；SC-02、LT-16 均复现。

**根因**：internal/memory/retrieve.go:36 matchQuery 使用“双方都非空才比较”的宽松语义：

~~~go
if rec.Scope.ProjectID != "" && q.ProjectID != "" && rec.Scope.ProjectID != q.ProjectID {
    return false
}
~~~

ProjectID / SessionID / PlanID / AgentID 四个维度都是这个模式，因此 q 不传时等于“不过滤”。

**修复方案**：

1. 把作用域过滤改为“记录有作用域则查询必须匹配”：

~~~go
func scopeMatches(recScope, q string) bool {
    if recScope == "" { return true }   // 记录是全父级可见
    return q != "" && recScope == q
}
// matchQuery 内：
if !scopeMatches(rec.Scope.ProjectID, q.ProjectID) { return false }
if !scopeMatches(rec.Scope.SessionID, q.SessionID) { return false }
if !scopeMatches(rec.Scope.PlanID, q.PlanID) { return false }
if !scopeMatches(rec.Scope.AgentID, q.AgentID) { return false }
~~~

2. 为“管理/导出/整合”等需要看全量的内部路径增加显式开关：

~~~go
type Query struct {
    // ...
    AllScopes bool // 仅 export/consolidate/admin 使用，普通检索禁止置 true
}
~~~

并在 matchQuery 中仅当 AllScopes=true 时跳过上述过滤；`/api/memories/export`、`consolidate`、`Owners()` 内部查询显式置 true。

3. `/api/chat` 增加项目上下文约束：当请求带 project_id 时，检索只允许该项目与全局记录；未带 project_id 时，只允许全局记录（与测试方案 H-04 的判定一致）。如果产品希望“未选项目时也能看到最近项目记忆”，必须新增显式参数并在 UI 上可见，不能默认泄漏。

**兼容性影响**：修复后“未指定项目”的会话将看不到任何项目级记忆；这是预期行为。需要在 release note 中说明，并提供“选择项目后可见”的 UI 引导。

**验证**：LT-16、SC-02 通过；新增单测 matchQuery_scope_strict；MB/SC 全量回归。

### A2. 去重键必须区分论文身份（DEF-009，R-13 / H-07）

**现象**：LP-07 在两篇论文上写相同文本的 user_note，第二篇 points=0；第一篇记录被复用。

**根因**：ContentHash 只含 kind + scope + title + content（types.go:197），而 wiki Point 用 Tags 里的 paper:<id> 区分论文；Put 的 byHash 命中后直接返回旧记录。

**修复方案**（不改已持久化的 content_hash，避免全量迁移）：

1. 新增去重键函数，把身份维度加入 dedupe key：

~~~go
// DedupeKey 是存储层去重键；content_hash 仍保持原语义，供外部引用。
func (r Record) DedupeKey() string {
    identity := ""
    if r.Attributes != nil {
        if v, ok := r.Attributes["paper_id"].(string); ok { identity = v }
    }
    if identity == "" {
        for _, tag := range r.Tags {
            if strings.HasPrefix(tag, "paper:") { identity = tag; break }
        }
    }
    return r.Scope.OwnerID + "|" + r.ContentHash + "|" + identity
}
~~~

2. FileStore 的 byHash 改为 byDedupe（map[string]string），Put / Update / Delete / loadSnapshot / replayWAL 全部使用 DedupeKey()。旧数据加载时现场计算，无需迁移。
3. 对普通记忆（无 paper 标签）DedupeKey 等于原 key，行为不变；MB-12 的 dedupe 语义保持。
4. 迁移检查脚本：扫描 memory.snapshot.json，统计“同 ContentHash 不同 DedupeKey”的记录，确认没有历史误合并需要拆分。

**验证**：LP-07 第二篇 points≥1；MB-12 UseCount=2 且单条记录；新增单测 TestDedupeKeyIncludesPaper；全量 MB/LP。

### A3. 论文身份合并规则（DEF-001 / DEF-002，R-29 / ADV-04）

**现象**：CP-02（同标题 2023/2024）被合并；CP-03（同标题同年不同作者）被合并。

**根因**：identity.go:203 只在 title alias 相同且 `abs(year diff) <= 1` 时合并，没有作者/会议约束；year 缺省（0）时更宽松。

**修复方案**：

1. 合并条件收紧为以下之一：
   - DOI 相同（规范化后）或 arXiv 相同（去版本号）；
   - 标题规范化相同 **且 年份精确相等**，且作者集合有交集（至少 1 个规范化姓名相同），或双方作者都为空；
   - 标题规范化相同 **且 年份都为 0**，且作者交集非空。
2. 删除“年份差 ≤1”合并分支；ADV-02 的 arXiv 版本差异仍由 paperid.StripVersion 处理。
3. 新增 `AliasStore.ResolveWithReason`，返回命中 alias 与判定原因，写入审计日志，便于线上排查误合并。
4. 已有误合并数据修复：提供 `tools/wiki_split_merged` 离线脚本，按 papers.jsonl + literature 记录重建 alias；同一 title 下不同 DOI/作者拆分为多个 PaperPage，Point/Link 按 Attributes.paper_id 归属。迁移前必须备份 data/wiki 与 memory。

**验证**：CP-02、CP-03 通过；ADV-01~07 专项；新增单测 TestResolveYearAndAuthorGuard。

### A4. 中文查询与“这篇论文”回退（DEF-005，R-22 / R-23 / H-05）

**现象**：CP-25 中文查询“这篇论文的方法是什么”命中 0 个候选，回退取最近 2 篇，注入了无关英文论文；LP-24 同样退化。

**根因**：internal/wiki/retrieve.go:101 用 strings.Fields 对查询分词，中文整句没有空格，永远匹配不到英文/中文标题；124-135 在 0 命中时无条件回退最近 2 篇。

**修复方案**：

1. 复用 memory.Tokenize（CJK bigram + 拉丁词）做查询分词，统一大小写/空白：

~~~go
queryTokens := memory.Tokenize(text)
for _, token := range queryTokens {
    if len([]rune(token)) < 2 { continue }
    if strings.Contains(haystack, strings.ToLower(token)) { score += 2 }
}
~~~

2. 回退条件收紧：仅当当前 project（或 session）下**恰好 1 篇 active 论文**时，才把这篇作为“这篇论文”的回退；否则不注入 wiki block。绝不允许回退到“最近 N 篇”。
3. 新增可选绑定：chat 请求可带 paper_id / document_id（或 session 上记住当前论文），匹配失败时优先使用显式绑定。
4. 注入前记录 match_reason（explicit / title_match / token_match / single_paper_fallback / none），写入 PromptReport 与审计，便于回归。

**验证**：CP-25 不再出现 Unrelated English Paper，且单论文项目仍能注入；LP-24；英文查询 CP-17/18 不回归；新增单测 TestMatchPapersCJK。

### A5. 环境 shadow 必须在三个入口一致（DEF-011，R-05 / H-03）

**现象**：MEMORY_MODE=shadow 时 chat 仍注入记忆；LT-14 复现。

**根因**：chat_runtime.go:104-108 只读 per-owner Settings.Mode；环境变量只在 MemoryMode() 中，chat 未使用。plan/task 路径也各有自己的判断。

**修复方案**：

1. 新增统一函数：

~~~go
func EffectiveMode(settings Settings) string {
    env := strings.ToLower(strings.TrimSpace(os.Getenv("MEMORY_MODE")))
    if env == "off" { return "off" }
    if env == "shadow" { return "shadow" }
    if !settings.Enabled { return "off" }
    return settings.Mode
}
~~~

2. chat / plan / task 三个入口统一调用；返回 shadow 时照常检索并记录 PromptReport，但**不注入** core/retrieved 文本（用于观察召回）。
3. WEB UI 与审计显示 effective mode（env + per-owner 的合成结果），避免“设置关了但还在注入”的错觉。

**验证**：LT-14、RB-02；新增单测 EffectiveMode 矩阵。

### A6. 真实 LLM 长论文与跨论文证据（DEF-018 / DEF-019）

**现象**：REAL-LP-23 三个问题模型都回答“只有标题/ID，没有正文或参考文献列表”；REAL-CP-17 回答没有 wiki_citations。

**根因**：

- chat 的 wiki 上下文只渲染 PaperPage + Point + Link（retrieve.go:141），不包含文档正文；WIKI_LLM_MODE=off 时连 TLDR/摘要点都没有，模型只能看到标题。
- RenderContext 只为 Point 生成 citation（164-170），paper 级块没有 citation，因此没有 Point 的论文引用为空。

**修复方案（分两步）**：

**第一步：paper 级引用（当天可完成）**

1. RenderContext 为每篇渲染的论文追加一条 Citation：paper_id、title、year、doi/arxiv、source=wiki。
2. 为每条 Link 追加 citation（from/to/type/evidence quote），保证关系回答有来源。
3. chat /api/chat 的 wiki_citations 直接返回这些 citation；测试断言“有论文块就有 citation”。

**第二步：文档上下文注入（1-2 天）**

1. 在 documents 侧新增确定性摘要（不需要 LLM）：标题层级 outline + 每节首段 + 表格/图注引用，落到 `document_sections` 记忆或 Point，tags 含 document:<id>、paper:<id>。
2. chat 命中某篇论文时，追加 `[document context]` 块：
   - 从 documents.RankDocument 取 top 2-3 chunk（bounded 800~1500 token）；
   - 每个 chunk 带 document_id、page、section、quote；
   - 与 retrieved/wiki 共享预算，wiki 先扣、document 后扣，超预算截断并记录 PromptReport。
3. 预算新增 `DOCUMENT_CONTEXT_BUDGET_TOKENS`（默认 1200，0=关闭），并按 prompt 限额 clamp（例如 ≤5%）。
4. 无证据时 prompt 明确“未检索到相关段落”，模型应拒答；测试用 Abstention Accuracy 判定。

**验收**：REAL-LP-23 三问能答出 4.2 节标题、Table 7 数值/明确缺失、参考文献 120 条，且 citations 含 document_id/page；REAL-CP-17 wiki_citations≥1；LP-05/LP-26 页码引用不回归。

---

## 4. 批次 B：稳定性与写入语义（1 天）

### B1. 存储初始化 fail-closed（DEF-015 关闭；新增 DEF-006/007 之外的 LC-06/LC-07/LC-10）

**现象**：快照损坏、文件锁冲突、只读目录时，进程只打一行日志就回退 NewMemoryStore，随后对外提供空/分裂视图（LC-07 复现：第二个进程 stats=0，日志有 falling back to in-memory store）。

**根因**：internal/api/memory_runtime.go:90-94；wiki_runtime 同样问题。

**修复方案**：

1. 默认 fail-closed：NewFileStore 失败时 store=nil、enabled=false，/api/memories/* 返回 503 + error code `memory_store_unavailable`，/api/chat 不带记忆继续；/api/health 增加 `"memory":{"ok":false,"degraded":true,"reason":...}`。
2. 提供显式降级开关 `MEMORY_ALLOW_EPHEMERAL=true`（仅测试/开发），启动时打印 WARN 并在 health 标记 `ephemeral=true`；生产默认 false。
3. 锁冲突时返回持有者 PID（lockfile 已写 PID），并在启动日志给出“另一个进程正在使用该目录”的明确错误。
4. wiki store 同样 fail-closed；/api/wiki/* 503，chat 不注入 wiki。

**验证**：LC-07（第二进程不再提供分裂视图）、LC-10（启动明确失败或显式降级）、LC-06（快照损坏的处理见 B2）。

### B2. 快照损坏恢复（LC-06）

**修复方案**：

1. loadSnapshot 解码失败时：把坏文件重命名为 `memory.snapshot.json.corrupt-<ts>`，尝试仅用 WAL 重建；若 WAL 可重放，启动成功并记录 `recovered_from_wal=true` + 丢失记录数（应为 0）。
2. 若 WAL 也为空/损坏，fail-closed（不创建空库），并在 health 暴露 reason，等待人工处理。
3. compact 写快照使用 fsync + 原子 rename（已具备），增加 CRC/大小校验头，降低“半文件”概率。

**验证**：LC-06 断言 total=5 或明确失败，不允许“静默 total=0”。

### B3. 写路径先 WAL 后内存（DEF-006 / LC-09）

**根因**：Put 先 `s.records[rec.ID]=rec; s.indexRecord(rec)`，再 appendWAL；磁盘满时接口返回 400，但记录已留在内存（stats 从 5 变 6）。

**修复方案**：

1. Put/Update/Delete 改为“两阶段”：
   - 先在锁外完成校验/哈希/版本计算；
   - 锁内写 WAL（append+fsync）成功后再改 records/byDedupe/idx；
   - 或在 appendWAL 失败时执行回滚（删除新增记录、恢复旧记录、恢复 index）。
2. 存储 I/O 错误统一映射 5xx（`ErrStorageUnavailable`），不要把磁盘错误当 400 参数错误。
3. 增加写失败注入单测：WAL 不可写时 stats 不变、返回 5xx。

**验证**：LC-09 通过（4xx/5xx 且 total 保持 5）；MB-26 并发写不回归。

### B4. 批量导入与 WAL group commit（DEF-007 / LC-13）

**根因**：import 逐条 Put，每条 fsync；每 2000 条触发全量 snapshot；实测 8.5 万条/480s（约 170 条/秒）。

**修复方案**：

1. Store 新增 `PutBatch(ctx, records []Record) (imported, skipped int, err error)`：
   - 单次锁内完成去重/版本；
   - WAL 批量 append（一次 Write + 一次 fsync，按 500-1000 条或 1MB 切批）；
   - 批量更新 index/byDedupe；
   - 失败整批回滚或返回已成功数量。
2. import handler 按 500 条切批调用 PutBatch，响应返回 imported/skipped/errors。
3. compaction 改为后台/按字节阈值触发（例如 WAL >64MB 或 >5000 条异步 compact），避免导入过程反复全量写快照。
4. 目标：10 万条 ≤10min（≥167 条/秒已接近，批量后应 >2000 条/秒）。

**验证**：LC-13 10 万条 ≤600s；导入后搜索正常；内存占用 <1.5GB。

### B5. LC-15 重建一致性（DEF-008，先复跑）

**处理顺序**：先按报告复跑 3 次确认是否稳定复现；若复现，按 C2 的“Link 索引一次构建 + 稳定排序”修复；若不复现，关闭为环境抖动并保留 nightly 用例。

---

## 5. 批次 C：性能与规模（1.5 天）

### C1. 内部全量扫描去 200 上限（DEF-003 / DEF-014 / H-06）

**根因**：PaperStore.List 把 limit 截到 200（store.go:325）；matchPapers（retrieve.go:82）、RebuildLinksForOwner（links.go:478）、BuildLinksForPaper 都用 200，导致第 201 篇起不参与链接与注入。

**修复方案**：

1. Store 新增 `ListAll(ctx, ownerID) ([]PaperPage, error)`：直接遍历 owner 分区（快照已在内存），不设上限；或分页循环直到取完。
2. 内部构建/匹配/重建一律用 ListAll；API `GET /api/wiki/papers` 仍保留 limit≤200 的分页语义。
3. matchPapers 只有“渲染结果”受 limit 约束，候选池不再截断。
4. 增加规模单测：220 篇时最早 20 篇的 link_ids 非空。

**验证**：CP-12、CP-26、CP-27 通过；构建时间不因全量扫描明显变差（配合 C2）。

### C2. Link 构建索引化（DEF-004 / DEF-014 / CP-13 / PF-06）

**根因**：linksForKey 每次候选都 List 5000 条 Link 再线性过滤；RebuildLinksForOwner 对每篇论文重复；120 篇构建 132s，220 篇直接 300s 超时。

**修复方案**：

1. 一次构建内加载 Link 索引：

~~~go
type linkIndex struct {
    byKey map[string][]existingLink
}
func (b *Builder) loadLinkIndex(ctx, ownerID) (*linkIndex, error) {
    records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{TagWikiLink},
        Statuses: []memory.Status{Active, Pending, Superseded, Archived}, Limit: 0}) // 0=不限
    // 按 Attributes["link_key"] 分组
}
~~~

2. writeLink 接收 index，不再各自 List；BuildLinksForPaper/RebuildLinksForOwner 共用一个 index，构建结束统一刷新 LinkIDs。
3. 实体倒排索引（entity -> papers）在构建开始时一次生成，替代每篇论文的 O(P) 扫描。
4. build 锁改为 per-owner（map[ownerID]running），不同用户可并行；job 增加 TTL/持久化，避免进程重启后 404 与内存泄漏。
5. 目标：120 篇 ≤30s；220 篇 ≤90s；PF-06 不再 300s 超时。

**验证**：CP-13、PF-06、CP-12、CP-26/27；新增基准测试 BenchmarkRebuildLinks120。

### C3. 检索一次请求只跑一遍（DEF-013 / PF-01）

**根因**：/api/memories/search 先 `store.Search`（searchLocked）再 `store.Retrieve`（内部再 searchLocked），10k 条时 p50 43.7ms、p95 51.2ms。

**修复方案**：

1. Store 新增 `SearchWithContext(ctx, q, coreBudget, retrievedBudget) (SearchResult, error)`，一次 searchLocked 同时产出 results + context。
2. handler 改用该接口，删除重复调用；context 的 Retrieved 列表可直接复用 results 的前 K 条。
3. 响应裁剪：results 只返回必要字段（id/title/kind/score/scope/status），完整 record 仅在 context 中返回；或增加 `fields` 参数。
4. 目标：1 万条 p95 <20ms（留 2.5 倍余量）。

**验证**：PF-01 p95 <50ms；MB-02/04/07/08 不回归；新增基准 BenchmarkSearchRetrieve10k。

---

## 6. 批次 D：安全加固（0.5 天）

### D1. 正文 injection 扫描（DEF-017 / SC-06）

**根因**：ValidateRecord 只对 `refs[].quote` 调 ScanInjection（redact.go:90），title/content 不扫描。

**修复方案**：

1. 对 title+content 调 ScanInjection；命中时把 Trust 置为 untrusted（不拒绝，避免丢用户数据），并在 Attributes 写 `injection_flag`、`injection_pattern`。
2. RenderRecord 对 untrusted 记录加显式 `<memory trust="untrusted" ...>` 标签（已有 trust 字段），prompt 指令强调“记忆是数据，不是指令”。
3. 若正文同时命中 hidden/secret 规则，按现有逻辑拒绝。

### D2. attributes 递归校验（SC-11）

**修复方案**：

1. 用递归 walk（map/slice/string，深度 ≤5）替换现有单层检查；对每个字符串执行 IsHiddenText + ScanInjection。
2. 命中 hidden → 返回 400；命中 injection → 降级 untrusted。
3. 增加单测：嵌套 map、[]any、混合类型、超深结构（拒绝）。

---

## 7. 统一数据迁移与兼容

| 变更 | 兼容策略 |
|---|---|
| DedupeKey 增加 paper 身份 | 加载时现场计算，无迁移；提供一次性审计脚本统计潜在历史误合并 |
| 严格 scope 过滤 | 行为变更，需 release note；export/consolidate 显式 AllScopes=true 保持运维可见性 |
| Alias 合并规则收紧 | 新写入立即生效；历史合并提供离线拆分脚本 + 备份 |
| fail-closed | 新增 `MEMORY_ALLOW_EPHEMERAL` 过渡开关（默认 false）；health 增加 degraded 字段 |
| WAL 批量/后台 compact | 文件格式不变，旧快照可读 |
| summary 持久化 | ChatSession 新增 summary_version/summary_updated_at，旧会话缺省为空 |
| 新预算/开关 | 新增 env 默认值保持现有行为：DOCUMENT_CONTEXT_BUDGET_TOKENS=1200、MEMORY_MMR_PER_KIND_MAX 按 kind 配置 |

---

## 8. 回归与验收

### 8.1 每批必跑

~~~bash
# 单元 / 静态
cd backend && go build ./... && go vet ./... && go test ./internal/memory/... ./internal/wiki/... ./internal/api/... ./internal/documents/...

# 对应套件（示例）
python3 tools/stress/run_suite.py --suite LT --run-id fix-a --out /tmp/fix --only LT-01,LT-14,LT-16
python3 tools/stress/run_suite.py --suite CP --run-id fix-a --out /tmp/fix --only CP-02,CP-03,CP-12,CP-13,CP-25
python3 tools/stress/run_suite.py --suite LC --run-id fix-b --out /tmp/fix --only LC-06,LC-07,LC-09,LC-10,LC-13,LC-15
python3 tools/stress/run_suite.py --suite PF --run-id fix-c --out /tmp/fix --only PF-01,PF-06

# 真实 LLM 证据
REAL_PORT=18500 python3 tools/stress/real_llm_check.py stress-fix-real-llm
~~~

### 8.2 发布前全量

~~~bash
python3 tools/stress/run_suite.py --suite all --run-id stress-release-r1 --out /tmp/stress-release
python3 tools/stress/make_report.py --run-dir /tmp/stress-release --run-id stress-release-r1 \
  --out reports/memory-stress-release --real-dir /tmp/stress-real
~~~

### 8.3 逐条验收映射

| 缺陷 | 验收用例 | 通过标准 |
|---|---|---|
| DEF-012/016 | LT-16, SC-02 | 0 泄漏（prompt 不含其他项目内容） |
| DEF-009 | LP-07, MB-12 | 第二篇论文 points≥1；同论文仍去重 |
| DEF-001/002 | CP-02, CP-03, ADV-01~07 | 不误合并；正例仍合并 |
| DEF-005 | CP-25, LP-24 | 不注入无关论文；单论文项目可注入 |
| DEF-011 | LT-14, RB-02 | shadow 下 chat prompt 无 memory 块 |
| DEF-010 | LT-01 | 20/20 召回且 citations 命中 |
| DEF-018/019 | REAL-LP-23, REAL-CP-17 | 3/3 回答带 document/page 引用；wiki_citations≥1 |
| LC-06/07/10 | LC-06, LC-07, LC-10 | 不静默降级；health 显式 degraded |
| DEF-006 | LC-09 | 返回 5xx 且 total 不变（5） |
| DEF-007 | LC-13 | 100k ≤600s |
| DEF-003/004/014 | CP-12, CP-13, PF-06 | 最早 20 篇有 Link；120 篇 ≤30s |
| DEF-013 | PF-01 | 10k p95 <50ms |
| DEF-017 | SC-06, SC-11 | 正文/嵌套注入降级；hidden 拒绝 |

---

## 9. 风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| 严格 scope 过滤导致“看不到最近项目记忆” | 用户体感变化 | UI 显式选择项目；release note；过渡期可加 `MEMORY_SCOPE_STRICT=false`（默认 true） |
| ContentHash 去重键变化产生重复记录 | 数据膨胀 | 加载时统一计算 key；上线前审计脚本统计 |
| fail-closed 提高 503 | 可用性 | 启动重试 + health 告警；仅对 memory/wiki 路由降级，chat 继续 |
| 批量导入 WAL group commit 丢尾部 | 数据一致性 | 批量 fsync 后再返回；崩溃恢复按批重放；单测 kill -9 |
| 文档上下文注入超预算 | 成本/上下文挤占 | 独立预算 + clamp + PromptReport；默认 1200 token |
| Alias 拆分脚本误伤 | 论文重复 | 先 dry-run 输出 diff，人工确认后 apply；备份 data/wiki |

回滚：每批改动独立 commit；env 开关保留一个 release；数据迁移脚本提供 `--rollback`（恢复备份 + 重建 alias）。

---

## 10. 排期建议（5 个工作日）

| 天 | 内容 | 产出 |
|---|---|---|
| D1 | A1/A2/A5 + 单测 | 隔离、去重、shadow 修复；LT-16/SC-02/LP-07/LT-14 回归 |
| D2 | A3/A4 + A6 第一步 | 身份合并、中文匹配、paper 级 citation |
| D3 | B1/B2/B3/B4 | fail-closed、快照恢复、写语义、批量导入 |
| D4 | C1/C2/C3 | 去 200 上限、Link 索引、build 锁、检索去重 |
| D5 | A6 第二步 + D1/D2 + 全量回归 | 文档上下文注入、安全加固、release 报告 |

---

## 11. 附录：失败用例复现命令与证据

| 用例 | 复现命令（容器内） | 证据 |
|---|---|---|
| LT-01/14/16 | run_suite.py --suite LT --only LT-01,LT-14,LT-16 | evidence/stress-2026-11-03-r1/LT-*/verdict.json |
| CP-02/03/12/13/25 | run_suite.py --suite CP --only CP-02,CP-03,CP-12,CP-13,CP-25 | evidence/.../CP-*/verdict.json |
| LP-07 | run_suite.py --suite LP --only LP-07 | evidence/.../LP-07/verdict.json |
| LC-06/07/09/10/13/15 | run_suite.py --suite LC --only LC-06,LC-07,LC-09,LC-10,LC-13,LC-15 | evidence/.../LC-*/verdict.json |
| PF-01/06 | run_suite.py --suite PF --only PF-01,PF-06 | evidence/.../PF-*/verdict.json |
| REAL-LP-23/CP-17 | real_llm_check.py stress-fix-real-llm | evidence/stress-fix-real-llm/REAL-*/verdict.json |

> 注：报告中 CP 套件部分用例来自 probe-cp 运行（全局 build 锁导致同运行 409），修复后应以同一次全量 run 重新采集；LC-15 需 3 次复跑确认是否为稳定缺陷。

---

## 12. 修订记录

| 版本 | 日期 | 修订人 | 变更 |
|---|---|---|---|
| v1.0 | 2026-11-03 | 测试负责人 | 首版：19 条缺陷、13 类根因、A~D 四批修复与验收 |
