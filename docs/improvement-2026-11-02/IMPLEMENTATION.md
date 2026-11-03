# Paper Wiki 实现对照（feature/memory）

- 日期：2026-11-02（W3/W2.5 更新：2026-11-03）
- 设计依据：[docs/design/2026-11-02-paper-wiki-memory.md](../design/2026-11-02-paper-wiki-memory.md)（v1.1）
- 评审依据：[docs/review/2026-11-02-paper-wiki-memory-review.md](../review/2026-11-02-paper-wiki-memory-review.md)
- 分支：feature/memory（从 master fast-forward 后实施）
- 测试环境：容器内（golang:1.27.3-bookworm 构建/测试、python:3.11-bullseye 运行后端二进制与 E2E、node:22-bookworm-slim 前端构建/测试）

## 1. 实现范围

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | PaperMetadata.DOI、ApplyProviderRecord/context_resolver、memory.SourceRef/Refs/Attributes、Citation 扩展、NormalizeTitle | 完成 |
| W0 | internal/wiki types/identity/store/builder/points、PaperPage 与 Point API | 完成 |
| W1 | Plan/Task/Trial 记录打 wiki_point 标签、memory_sink 刷新、取消路径 RecordPlan、paper_parse 摘要、metrics result Point | 完成 |
| W2 | 实体倒排 + cites provider 接口 + 对称方向规范化 + 稳定 Link ID/revision + links API/confirm/reject | 完成 |
| W2.5 | PDF references 解析（documents/references.go）→ cites 补充（wiki/pdfrefs.go），默认关闭 | 完成 |
| W3 | 自动 LLM TLDR/段落摘要、Link 类型裁决、Quote LocateQuote 校验、pending_review、注入与预算 | 完成 |
| W4 | embedding/图增强 | 未实现（设计默认关闭，非本轮验收） |
| 前端 | PaperWikiPanel + wikiApi + 设置页“论文”tab | 完成 |

## 2. 文件清单

### 新增

- `research-assistant/backend/internal/wiki/types.go`：PointKind/LinkType/Point/Link/PaperPage/ReproductionStatus/Stats/TaskQuery/WikiContext。
- `internal/wiki/identity.go`：CanonicalKey（DOI>arXiv>title+year）、owner 参与哈希的 PaperID、按 owner 分区的 AliasStore（多对一）。
- `internal/wiki/store.go`：papers.jsonl + papers.snapshot.json + 文件锁 + owner 分区。
- `internal/wiki/builder.go`：PaperPage 物化、RefreshPaper、RefreshForPlan、RebuildOwner（从 memory paper fact 重建）。
- `internal/wiki/points.go`：Record 视图解析、CreatePoint、user_note、paper_parse 摘要、point_key supersede、TagPlanRecords/TagTaskRecord。
- `internal/wiki/links.go`：实体候选、cites provider、稳定 key/revision、confirm/reject、RebuildLinksForOwner。
- `internal/wiki/retrieve.go`：BudgetTokens（2%/3%/150-1500）、RetrieveForTask、wiki XML 渲染、Citation 生成。
- `internal/wiki/ingest.go`：RefreshForTask（paper_parse/metrics/task 标签）。
- `internal/wiki/summarize.go`：W3 LLM 接口、严格 JSON（最多 2 次）、TLDR/摘要 Point、LocateQuote 校验、TLDR memory 持久化、Link 裁决。
- `internal/documents/references.go`：W2.5 PDF References 章节定位、条目切分、DOI/arXiv/年份/标题/作者/venue 抽取。
- `internal/wiki/pdfrefs.go`：W2.5 条目与 PaperPage 的 DOI>arXiv>title+year 匹配，生成 `cites` 候选。
- `internal/wiki/settings.go`：WIKI_ENABLED/WIKI_MODE/WIKI_LLM_MODE/WIKI_LINK_MODE/WIKI_BUDGET_TOKENS/WIKI_PDF_REFS。
- `internal/wiki/*_test.go`：身份、store、Point、Link、检索、设置、W3 摘要与 Link 裁决、W2.5 PDF references 单测。
- `internal/lockfile/`：带 PID 存活检测的跨平台文件锁（修复重启后内存库回退问题）。
- `internal/api/wiki.go` / `wiki_runtime.go`：wiki API 与进程级 builder。
- `internal/api/wiki_test.go`：API CRUD/owner/hidden/links/stats/build。
- `frontend/src/services/api/wikiApi.ts`、`frontend/src/features/wiki/PaperWikiPanel.tsx`。
- `test/e2e/wiki-e2e.py`、`test/e2e/wiki-long-chat.py`、`test/e2e/wiki-w3-e2e.py`、`test/e2e/wiki-pdfrefs-e2e.py`。

### 修改

- `internal/models/context.go`：PaperMetadata 增加 `DOI`。
- `internal/documents/metadata.go`：ApplyProviderMetadata/ApplyProviderRecord 保留 DOI。
- `internal/api/context_resolver.go`：改用 ApplyProviderRecord 并写入 Paper.DOI。
- `internal/api/literature.go`：自动导入 paper 带 DOI。
- `internal/literature/literature.go`：导出 NormalizeTitle。
- `internal/memory/types.go`：SourceRef、Record.Refs/Attributes、Citation 扩展、Patch.Refs/Attributes。
- `internal/memory/redact.go`：Refs quote ≤500、hidden/injection 校验、IsHiddenText。
- `internal/memory/store.go`：Patch 支持 Refs/Attributes、使用 lockfile。
- `internal/memory/extract.go`：plan paper fact 增加 DOI。
- `internal/api/memory_sink.go`：RecordPlan/RecordTask 后刷新 wiki。
- `internal/api/wiki_runtime.go` / `internal/api/chat_runtime.go` / `internal/api/plan_runtime.go`：wiki 注入与 SetWiki。
- `internal/scheduler/executor.go`：WikiSource 接口、wiki context 注入、任务后 RefreshForTask。
- `internal/scheduler/scheduler_control.go`：取消路径补 RecordPlan。
- `internal/prompts/task_prompt.go` / `budget.go`：WikiContext section（priority 3，retrieved 之后、task_inputs 之前）与 WikiTokens。
- `internal/agent/task_prompt.go`：sharedContext wiki_context 透传。
- `cmd/api/main.go`：SIGTERM 优雅关闭，释放 memory/wiki 锁并落盘 snapshot。
- `frontend/src/contracts/api.ts`、`frontend/src/features/settings/SettingsPanel.tsx`：wiki 类型与“论文”tab。

## 3. 设计条目对照（评审 9 项）

| 评审编号 | 设计问题 | 实现 |
|---|---|---|
| P0-1 DOI 断链 | PaperMetadata 无 DOI、context_resolver 丢弃 | PaperMetadata.DOI；ApplyProviderRecord 保留；context_resolver 写入；literature 自动导入带 DOI；单测 `TestApplyProviderRecordKeepsDOI` |
| P0-2 Link 不可重建 | Link 只存 links.jsonl | Link = memory.Record（Kind=fact、Tags=wiki_link）；删除 data/wiki 后 Link 与 confirm/reject 状态保留（E2E 验证） |
| P0-3 Evidence 类型冲突 | 自定义 Evidence | 新增 memory.SourceRef，Record.Refs/Attributes；ValidateRecord 校验 quote 长度/hidden/injection |
| P1-4 Link/alias owner | Link 无 owner、alias 全局 | Link 读模型带 OwnerID/ProjectID；aliases.json 按 owner hash 分区；store 双校验 |
| P1-5 ClaimRubric 误标确定性 | claim 应为 pending | paper_parse 摘要 Point 默认 TrustInferred/StatusPending；user_note 才 user_explicit/active |
| P1-6 预算与 section 冲突 | wiki 预算与 prompt priority | WikiContext priority 3，retrieved 后插入；预算 2%/3%/150-1500；与 retrieved 共享预算 |
| P1-7 O(n²)/对称重复 | 实体候选无上限、对称重复 | 单实体候选上限 500、单论文最多 200；对称类型 from<to 规范化 |
| P2-8 links API 无分页 | 无 limit/cursor | `GET /api/wiki/papers/:id/links` 与 `/api/wiki/links` 支持 limit/cursor/next_cursor |
| P2-9 Sections 重复 | 自定义 Section | PaperPage.Sections 直接复用 documents.SectionInfo |

## 4. 与设计文档的差异（以实际代码为准）

1. **W3 已接线**：`Builder.LLM` 由 `api/wiki_runtime.go` 的 `wikiModelLLM` 适配 `modelresolver.Resolver.Complete`；`WIKI_LLM_MODE=summary|all` 自动生成 TLDR/摘要 Point，`links|all` 做 Link 类型裁决。无 provider 或调用失败时静默降级，不阻塞论文创建。
2. **W4 未实现**：设计明确 W4 可选且默认关闭；本轮不做 embedding/图增强。
3. **PDF references（W2.5）已实现但默认关闭**：`WIKI_PDF_REFS=true` 时解析 PDF References 并与已知 PaperPage 匹配生成 `cites`；默认 `false`，外部引用图（OpenAlex）仍是首选。
4. **前端入口**：PaperWikiPanel 放在设置页“论文”tab（而非独立一级路由），复用现有设置壳，减少导航改动。
5. **PaperPage 额外字段**：CanonicalKey、DocumentIDs、Entities、Archived；均为重建/链接所需，不影响设计契约。

## 5. 安全与可重建性

- owner 隔离：PaperPage/Point/Link 均带 owner；store.Get 校验 owner；API 跨 owner 404。
- hidden benchmark：PaperPage Title/Abstract/Authors 与 Refs DocumentID/ArtifactRef/Quote 全部扫描；命中不入库、列表不返回。
- 注入扫描：Refs.Quote 命中 ScanInjection 时 Trust 降为 untrusted，不参与注入。
- 可重建：`RebuildOwner` 从 memory 的 paper/metadata fact 与 document/sections fact 重建 PaperPage；Point 来自 memory；Link 来自 memory；用户 confirm/reject 状态保存在 Record.Status。
- W3 可重建：自动 TLDR 持久化为 `wiki_tldr` memory 记录（StatusPending、TrustInferred）；删除 `data/wiki` 后 `EnsurePaper` 先读该记录，**不重新调用 LLM**。
- W3 quote：LLM 产出的每个 Point 必须带 quote，`documents.LocateQuoteLocation` 定位失败则清空 quote 并保持 `pending_review`；无 document 时只生成 TLDR，不生成事实 Point。
- 并发：单进程文件锁 + WAL + snapshot；锁带 PID 存活检测，崩溃后不会永久阻塞。

## 6. API 一览

- `GET/POST /api/wiki/papers`、`GET/PATCH/DELETE /api/wiki/papers/:paperID`
- `GET/POST /api/wiki/papers/:paperID/points`
- `PATCH/DELETE /api/wiki/points/:pointID`
- `GET /api/wiki/papers/:paperID/links`、`GET /api/wiki/links`
- `POST /api/wiki/links/:linkID/confirm`、`POST /api/wiki/links/:linkID/reject`
- `POST /api/wiki/build`、`GET /api/wiki/build/:jobID`
- `GET /api/wiki/stats`

## 7. 回滚开关

`WIKI_ENABLED=false`（完全不注册路由、不写、不注入）、`WIKI_MODE=shadow`（构建不注入）、`WIKI_LLM_MODE=off|summary|links|all`、`WIKI_LINK_MODE=explicit|hybrid|llm`、`WIKI_PDF_REFS=false`、`WIKI_DIR`、`WIKI_BUDGET_TOKENS`。
