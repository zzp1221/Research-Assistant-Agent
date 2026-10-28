# 规格：N-04 真实文献与网页检索 + N-07 文档智能（2026-10-27 第四轮）

> 依据：`docs/design/n04-n07-literature-and-document-intelligence.md`（设计稿，2026-10-27）
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 9 节，以实际为准。
> 约束：一切与功能相关的测试只在容器内执行（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 compose 栈）。
> 前几轮（Top-10、R-01~R-17、运行期模型配置与视觉校验）的成果为基线，本轮不回归、不重复。
> **状态：已实现并通过容器内验证（2026-10-27）。证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND4.md`、`tasks.md` 与 `test/e2e/*.mjs`。**

## 1. 目标

把设计文档中的 N-04（真实文献与网页检索）与 N-07（文档智能）从设计稿落成可用实现，满足设计文档 5.4 的验收标准：

### N-04
1. 保留并增强现有 4 个 provider（arXiv/OpenAlex/Crossref/GitHub），新增 Semantic Scholar / PubMed / DBLP / Unpaywall；
2. 429/503 支持 Retry-After + 指数退避；provider 失败降级为 warning；
3. 搜索支持 relevance/citations/year 排序与每条结果的 rank/explain；
4. 新增文献库（owner 隔离、收藏、标签、BibTeX、加入会话/计划）；
5. 新增网页正文抽取 `POST /api/web/fetch`（SSRF/robots/content-type/大小/超时/缓存/段落引用）；
6. 新增引用/被引图谱 `GET /api/literature/citations/:doi`；
7. planner 新增 `literature_search` 节点，Librarian 真实检索并导入 top K，metadata-only 写 missing requirement；
8. 前端新增 LiteraturePanel（搜索/筛选/排序/导入/文献库/BibTeX/引用）。

### N-07
1. 逐页混合 OCR：文本页保留文本层，仅对 needs_ocr 页 OCR；`PDF_OCR_MAX_PAGES` 变为 OCR 预算；`TotalPages` 保留真实页数；预算不足状态 `partial` 且明示跳过页；
2. 默认 `PDF_OCR_LANG=eng+chi_sim`（compose + 代码默认）；
3. bbox/blocks/reading order：文本层 `pdftotext -bbox-layout`，OCR `tesseract tsv`，两栏按栏排序；页眉/页脚标记；
4. 表格抽取：文本/OCR 行对齐启发式 → Rows + CSV；失败保留 warning 不编造；
5. 图注/图区域：`Figure N` / `图 N` 绑定图区域并导出 PNG；
6. 质量报告：per-page source/chars/OCR 置信度/失败/跳过 + 文档级 OCRStats；API `/document/quality`；
7. API：`/document/pages/:page/layout`、`/document/tables/:tableId`、`/document/figures/:figureId`；
8. BM25 支持 text/table/figure 类型 chunk；LocateQuote 返回 block 偏移；
9. 前端文档摘要 badge（文本层/OCR/混合/部分）、低置信度提示、表格/图注查看。

## 2. 现状复核（以实际代码为准）

| 设计文档描述 | 实际（2026-10-27 复核） |
|---|---|
| `internal/literature` 已有 arXiv/OpenAlex/Crossref/GitHub 搜索、缓存、限流、DOI/arXiv/PDF 导入 | 属实：`client.go/providers.go/import.go/literature.go` 存在；无 S2/PubMed/DBLP/Unpaywall；`fetch` 对 429 无重试 |
| `documents/pdf.go` 仅在整篇 0 字符时 OCR 前 N 页 | 属实：`ExtractPDF` 第 96-98 行；`ocrFallbackPages` 只循环前 maxPages |
| 前端无 `/api/literature/search` 调用 | 属实：grep 为 0；无文献组件 |
| `/api/pdf-proxy` 只代理 PDF | 属实：`routes.go` 163-234 行 |
| 无 citation graph / records / web fetch | 属实 |
| `Document` 只有 Pages[].Text | 属实：`document.go` 仅 Page{Number,Text,Chars} |
| 质量报告只有 status/error | 属实：`documentSummary` 仅 status/page_count/characters/extractor |
| compose 未传 `PDF_OCR_LANG` | 属实：docker-compose.yml 152-153 只有页数/超时 |
| 镜像已装 tesseract eng/chi_sim | 属实：容器实测 tesseract 5.3.0，语言 chi_sim/eng/osd；poppler 22.12.0 含 pdftotext/pdftoppm/pdfinfo/pdfimages/pdftohtml |
| 文档称 PDF 阅读器已有 | 属实：frontend/src/features/pdf-viewer |
| 文档称 `PDF_OCR_MAX_PAGES` 被当文档页数上限 | 属实：`PageCount = len(pages)`，会截断 |

## 3. 范围

### 3.1 后端 N-04

| 模块 | 内容 |
|---|---|
| `internal/literature/providers.go` | 新增 Semantic Scholar / PubMed / DBLP 搜索与字段映射；429/503 Retry-After + 指数退避（可配置重试次数/基础延迟） |
| `internal/literature/literature.go` | `Result` 增加 Rank/Score/RankReason/OpenAccess/License；`SearchOptions.Sort`；排序实现 relevance/citations/year；provider 集合扩展 |
| `internal/literature/import.go` | DOI 导入缺 PDF 时用 Unpaywall 解析 OA PDF；S2/PubMed metadata 兜底 |
| `internal/literature/library.go`（新） | LiteratureRecord 数据模型、owner 隔离文件存储（原子写/版本）、Upsert 去重（DOI/arXiv/title）、List/Get/Delete/SetTags/Attach、BibTeX 导出 |
| `internal/literature/citations.go`（新） | OpenAlex 引用/被引查询，返回标准化 references/cited_by |
| `internal/webread/`（新） | SSRF 校验、robots、content-type/大小/超时、HTML readability 正文抽取、段落/链接、URL 缓存 |
| `internal/api/literature.go` | 新增 records/citations 路由；import 落库；search 支持 sort |
| `internal/api/web.go`（新） | `POST /api/web/fetch` |
| `internal/agent/librarian.go` | `literature_search` 任务执行：真实检索 → top K 导入 PDF → metadata-only + missing requirement → structured JSON |
| `internal/planner/taskregistry.go` | 注册 `literature_search`（librarian_agent，real execution） |
| `internal/planner/planner.go` | General 意图含“找/搜索/调研/综述/相关论文”且无上传时生成 literature_search → general_research → general_synthesis |
| `internal/planner/agent_planner.go` | 允许/提示 literature_search 节点 |

### 3.2 后端 N-07

| 模块 | 内容 |
|---|---|
| `internal/documents/document.go` | Block/Table/Figure/PageQuality/OCRStats；Document 扩展 TotalPages/Blocks/Tables/Figures/Quality/OCRStats；StatusPartial；Save 写 CSV/图资源；Load 兼容旧 v1 |
| `internal/documents/pdf.go` | 逐页文本+bbox 解析；needs_ocr 判定；OCR 预算；tesseract tsv 置信度；reading order/栏检测；页眉页脚；表格/图注抽取；OCR 缓存；默认 eng+chi_sim |
| `internal/documents/layout.go`（新） | bbox XML 解析、栏聚类、reading order、页眉页脚、标题启发式 |
| `internal/documents/tables.go`（新） | 文本行对齐表格识别、caption、CSV 序列化、置信度 |
| `internal/documents/figures.go`（新） | 图注识别、pdfimages 导出、Figure 绑定 |
| `internal/documents/store.go / bm25.go` | Chunk 增加 Kind/BlockID/TableID/FigureID；table/figure chunks；LocateQuote block 偏移 |
| `internal/api/documents.go` | document 摘要扩展；layout/tables/figures/quality 端点 |
| `docker-compose.yml` | `PDF_OCR_LANG=eng+chi_sim` 默认、`DOCUMENT_CACHE_DIR` |

### 3.3 前端

- `contracts/api.ts`：LiteratureRecord/SearchResult/Library/Citation/WebFetch/DocumentQuality/Block/Table/Figure 类型；
- `services/api/literatureApi.ts`（新）、`webApi.ts`（新）、`documentApi.ts` 扩展；
- `features/literature/LiteraturePanel.tsx`（新）：搜索、来源多选、年份、仅 OA、排序、结果卡片、导入、文献库、标签、BibTeX、引用；
- `features/pdf-viewer/`：文档质量 badge、低置信度提示、表格/图注面板；
- 入口：左侧工作区标签（校样/文献）或 ChatPanel 顶部“文献库”。

### 3.4 测试与交付

- Go 单测：provider 解析/429 退避/排序/去重/records 隔离/BibTeX/引用解析/webread readability+SSRF/robots；documents 逐页 OCR/预算/partial/bbox/栏序/表格/图注/质量/缓存/旧版兼容；planner 触发；librarian literature_search。
- API 集成（httptest）：search/records/import/bibtex/citations/web-fetch/quality/layout/tables/figures。
- 前端：组件测试 + build + lint。
- 容器 E2E：真实 compose 栈验证 N-04 搜索（Crossref/OpenAlex 实测）、records、web fetch、N-07 扫描/混合 PDF、质量报告、planner literature_search。
- 更新 README/OpenAPI；`docs/improvement-2026-10-26/FIX-REPORT-ROUND4.md` 记录证据与偏差。
- 清理本轮临时容器/镜像/卷/文件。

## 4. 关键设计

### 4.1 N-04 Provider 与排序

```go
const (
    SourceSemanticScholar = "semanticscholar"
    SourcePubMed          = "pubmed"
    SourceDBLP            = "dblp"
    SourceUnpaywall       = "unpaywall"
)
type Result struct {
    // 现有字段...
    Rank       int     `json:"rank,omitempty"`
    Score      float64 `json:"score,omitempty"`
    RankReason string  `json:"rank_reason,omitempty"`
    OpenAccess bool    `json:"open_access,omitempty"`
    License    string  `json:"license,omitempty"`
}
type SearchOptions struct {
    Sources []string; Limit int; YearFrom int
    Sort string // relevance|citations|year
}
```

- 各 provider 内保留原始返回顺序，结果附 `provider_rank`；全局 relevance = provider 顺序 + 原始 rank；citations = citations desc, year desc；year = year desc。
- `fetch` 对 429/503：读取 `Retry-After`（秒或 HTTP-date），最多重试 `LITERATURE_MAX_RETRIES`（默认 2），延迟 = min(Retry-After, 2s) 或 base*2^n（base 200ms，测试可注入 0）。仍失败返回 `CodeRateLimited`。

### 4.2 文献库

```go
type Record struct {
    ID string; Source string; ExternalID string
    Title string; Authors []string; Year int; Venue string
    Abstract string; DOI string; ArxivID string
    URL string; PDFURL string; Citations int
    OpenAccess bool; License string
    Tags []string; UploadID string
    AttachedSessions []string
    CreatedAt, UpdatedAt time.Time
}
```

- 存储：`LITERATURE_LIBRARY_DIR`（默认 `<PLAN_STORE_PATH 目录>/literature-library`），每 owner 一个 `records.json`，原子写，带 version。
- ID：sha256(source|externalID|doi|normalizedTitle) 前 24 hex。
- Upsert 去重键：DOI 小写 > arXiv ID > source:externalID > 规范化标题；重复时合并非空字段、保留最早 CreatedAt、更新 UploadID/Tags。
- API：`GET /api/literature/records?q=&tag=&limit=&cursor=`、`GET/DELETE /api/literature/records/:id`、`POST /api/literature/records/:id/attach`、`GET /api/literature/records/:id/bibtex`。
- 导入成功即 Upsert（即使 attach=false，仅元数据也入库）。

### 4.3 网页正文 web_read

- `POST /api/web/fetch {url, max_bytes?}` → `{title,url,text,excerpt,links,fetched_at,content_type,word_count,cached}`。
- SSRF：仅 http/https、禁止 userinfo、解析公网 IP、逐跳重定向校验；测试可用 `ALLOW_PRIVATE_WEB_FETCH=true`（默认关闭）。
- content-type 仅 text/html/text/plain/xhtml；默认上限 2MB（`WEB_FETCH_MAX_MB`）；超时 15s（`WEB_FETCH_TIMEOUT_SECONDS`）。
- readability：DOM 评分（文本长度、逗号、标签权重、链接密度惩罚），保留标题/段落/列表/代码块；失败回退 body 文本。
- robots：默认遵守（`WEB_FETCH_RESPECT_ROBOTS=true`），按 UA 匹配 Disallow；测试可关。
- 缓存：URL+内容 hash，TTL 1h（`WEB_FETCH_CACHE_TTL_SECONDS`），不缓存非 200。

### 4.4 引用图谱

`GET /api/literature/citations/:doi?limit=25` →
`{doi, openalex_id, cited_by_count, references:[Record摘要], cited_by:[Record摘要], warnings}`。
实现：OpenAlex work by DOI → referenced_works（OpenAlex IDs）批量取元数据；cited_by 用 `filter=cites:W...`。单 provider 失败返回 warnings + 空列表，不 500。

### 4.5 literature_search 计划节点

- 触发（模板 planner）：General 意图、无上传附件、无 paper_metadata，且命中“找/搜索/调研/综述/相关论文/文献/search papers/find papers/literature review”等。
- 节点：`literature_search (librarian_agent)` → `general_research` → `general_synthesis`。
- 执行：
  1. 从 task.Inputs（query/sources/limit/min_year/open_access_only）或 description 解析参数；
  2. `SearchWithWarnings`，top K 默认 5；
  3. 逐篇 `Import`，成功则落盘 upload + `documents.ExtractPDF`，记录 upload_id/document_id；失败保留 metadata-only 并写 `MissingRequirement{kind:"pdf_unavailable", name:title, ...}`；
  4. `task.StructuredData` = JSON `{query, warnings, results, selected_papers, imported_count, metadata_only_count}`；
  5. `task.Result` = 可读摘要；`task.Metadata["literature_results"]` 供下游；
  6. 全部 provider 失败 → task failed + missing requirement `literature_search_failed`。

### 4.6 N-07 文档模型

```go
type Block struct { ID, Type string; BBox [4]float64; Text string; Confidence float64; Page int }
type Table struct { ID string; Page int; Caption string; BBox [4]float64; Rows [][]string; CSVRef string; Confidence float64 }
type Figure struct { ID string; Page int; Caption string; BBox [4]float64; ImageRef string }
type PageQuality struct { Page int; Source string; Chars int; OCRConfidence float64; Failed, Skipped bool; Error string }
type OCRStats struct { Engine, Language string; OCRPages int; AverageConfidence float64; FailedPages, SkippedPages, TotalPages int; Partial bool }
type Document struct {
    Version string; UploadID string; Status Status; Extractor string
    PageCount int; TotalPages int; Characters int
    Pages []Page; Blocks []Block; Tables []Table; Figures []Figure
    Quality []PageQuality; OCRStats OCRStats
    Error string; ExtractedAt time.Time
}
type Page struct { Number int; Text string; Chars int; Source string }
```

- 兼容：旧 `document.text/v1` 可 Load；新版本写 `document.text/v2`；缺省字段按零值。
- `StatusPartial`：有文本但存在 skipped/failed 页。
- `HasText()` 接受 extracted/partial。

### 4.7 逐页 OCR 与缓存

- 页数：`pdfinfo` → TotalPages；失败用文本页数兜底。
- 文本层：`pdftotext -layout -enc UTF-8`；bbox：`pdftotext -bbox-layout -enc UTF-8`（失败不阻断）。
- needs_ocr：page chars < `PDF_OCR_MIN_TEXT_CHARS`（默认 20）或（去空白后 < 10）。
- 预算：`PDF_OCR_MAX_PAGES`（默认 5）是“本次最多 OCR 页数”；按页序处理 needs_ocr，超出预算标 skipped。
- OCR：`pdftoppm -f p -l p -r 300 -png -singlefile` → `tesseract image stdout -l eng+chi_sim tsv`；解析 TSV 得 words/lines/blocks/conf；平均置信度 = 有效词 conf 均值 /100。
- 页文本：文本层优先；needs_ocr 页用 OCR 文本；低文本页若 OCR 失败保留原文本并标 failed。
- 缓存：`DOCUMENT_CACHE_DIR`（默认 `<UPLOAD_ROOT>/../document-cache`，即 upload 根同级）；key = sha256(pdf bytes) + config hash；命中直接复用并更新 UploadID/ExtractedAt；`DOCUMENT_CACHE_DISABLED=true` 可关。

### 4.8 版面/阅读顺序

- bbox XML `<page width height><block><line><word>`；block 文本、bbox。
- 栏检测：页面块数 ≥ 6、左右半区各 ≥ 3、块宽 < 0.65*页宽 → 双栏；左栏（x1 <= mid）按 y、x 排序，右栏按 y、x；否则全页 y、x。
- 页眉/页脚：y1 < 0.05*height 或 y0 > 0.95*height；或跨页重复且位于上下 10% → 标记 header/footer。
- 标题：每页第一个非页眉块，若字号代理（块高）> 页面块均高*1.25 或匹配章节关键词 → title。
- 生成 Page.Text 时按 reading order 拼接 block 文本（块间换行）；保留原文本作为回退（若 block 为空）。

### 4.9 表格/图注

- 表格：按行分割，`2+` 空格分列；连续 ≥2 行且列数一致（±1）→ 表；caption 取上方最近 `(?i)^(table|表)\s*([0-9]+|[IVX]+)`；置信度 = 列一致性*0.6 + 填充率*0.4；CSV 写入 `tables/<id>.csv`；不足 2 行或单列 → 不产出。
- 图注：`(?i)^(figure|fig\.?|图)\s*([0-9]+|[IVX]+)`；Figure 记录 caption/page；`pdfimages -list` 有图则导出 PNG 到 `figures/fig-p<page>-<n>.png`，按页内顺序绑定；无图仅 caption + warning。
- BM25：table chunk = "Table N: caption + header + rows"；figure chunk = "Figure N: caption"。

### 4.10 质量报告 API

- `GET /api/uploads/:id/document` 增加 `total_pages, quality, ocr_stats, tables, figures` 摘要。
- `GET /api/uploads/:id/document/quality` → `{upload_id, status, total_pages, ocr_stats, pages:[PageQuality]}`。
- `GET /api/uploads/:id/document/pages/:page/layout` → `{page, blocks:[Block]}`。
- `GET /api/uploads/:id/document/tables/:tableId` → Table（JSON）；`?format=csv` → text/csv。
- `GET /api/uploads/:id/document/figures/:figureId` → PNG（Content-Type image/png）或 JSON（`?format=json`）。

## 5. API 契约（新增/变更）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/literature/search | 增加 `sort=relevance\|citations\|year`；返回 rank/score/rank_reason/open_access/license |
| GET | /api/literature/records | 文献库列表（owner 隔离，q/tag/limit/cursor） |
| GET | /api/literature/records/:id | 详情 |
| DELETE | /api/literature/records/:id | 删除收藏（不删上传） |
| POST | /api/literature/records/:id/attach | 加入会话/计划 |
| GET | /api/literature/records/:id/bibtex | BibTeX 导出（text/plain attachment） |
| GET | /api/literature/citations/:doi | 引用/被引 |
| POST | /api/web/fetch | 网页正文抽取 |
| GET | /api/uploads/:id/document/quality | 质量报告 |
| GET | /api/uploads/:id/document/pages/:page/layout | blocks+bbox |
| GET | /api/uploads/:id/document/tables/:tableId | 表格 JSON/CSV |
| GET | /api/uploads/:id/document/figures/:figureId | 图 PNG/JSON |

错误码：`invalid_url`、`ssrf_blocked`、`robots_denied`、`unsupported_content_type`、`content_too_large`、`fetch_timeout`、`literature_record_not_found`、`literature_library_unavailable`、`table_not_found`、`figure_not_found`。

## 6. 前端交互

- LiteraturePanel：
  - 搜索框、来源多选（arXiv/OpenAlex/Crossref/GitHub/S2/PubMed/DBLP）、年份、仅 OA、排序（相关度/引用/年份）；
  - 结果卡片：标题/作者/年份/venue/引用数/来源 badge/OA badge/摘要展开/导入 PDF/仅元数据；
  - warnings 展示（如“arXiv 限流，已用 OpenAlex 结果”）；
  - 文献库：列表、搜索、标签、BibTeX 下载、删除、加入会话；
  - 引用/被引：输入 DOI 查看列表。
- 文档智能：
  - 摘要 badge：文本层/OCR/混合/部分；
  - 质量条：OCR 页数/平均置信度/失败页/跳过页；
  - 低置信度（<0.6）页黄色提示；
  - 表格列表 + CSV 下载；图注列表 + 查看原图。

## 7. 测试策略（全部容器内）

1. 后端：`scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."`；新增包 `-race`。
2. 前端：node:22-bookworm-slim 容器内 `npm ci && npm test && npm run build && npm run lint`。
3. API 集成：httptest 覆盖 search sort/429/records 隔离/BibTeX/citations/web fetch SSRF/robots/quality/layout/tables/figures。
4. 文档：生成合成 PDF（python reportlab 或 Go 生成）覆盖 20 页扫描、2 扫描+8 文本混合、中文、双栏、表格、图注。
5. E2E：重建 backend/frontend 镜像 + compose 起栈；真实 Crossref/OpenAlex 搜索（允许网络）；文献库闭环；web fetch 公开页；文档质量端点。
6. 安全：SSRF、路径穿越、owner 隔离、robots、大小上限。
7. 兼容：旧 document.json（v1）读取正常；旧 plan 不受影响。

## 8. 完成条件

- 第 7 节全部测试通过（容器内证据）；
- 设计文档 5.4 的 N-04 6 条、N-07 8 条全部可复现；
- 无新增回归（现有后端/前端测试全绿）；
- 无密钥/隐私泄漏；
- 清理本轮产生的临时容器/镜像/卷/文件。

## 9. 与设计文档的差异（以实际为准）

| 文档描述 | 实际/本轮决策 |
|---|---|
| 文档列出 Semantic Scholar/PubMed/DBLP/Unpaywall | 全部实现；DBLP 无 PDF，仅元数据；Unpaywall 仅 DOI OA 解析 |
| 文档称“公式 OCR” | 非目标（第 8.1 节亦未列入验收）；本轮不实现公式识别，仅 Block.Type 预留 formula |
| 文档称 OpenCV 表格检测 | 容器未安装 OpenCV；采用确定性文本行/空白列启发式 + Tesseract TSV，失败显式 warning |
| 文档称“citation graph 用 OpenAlex/S2” | 本轮用 OpenAlex（无 key、稳定）；S2 仅作为搜索 provider |
| 文档建议 SQLite/JSON index | 采用 owner 隔离 JSON 文件（与现有 plan/modelprofile 存储一致），原子写 |
| 文档称前端 PDF 页叠加 block 高亮“可选” | 实现 layout API 与基础表格/图注/质量 UI；PDF 像素级 overlay 不实现（非验收） |
| 文档称 20 页扫描 PDF OCR 全覆盖 | 默认预算 5 页；E2E 用 `PDF_OCR_MAX_PAGES=20` 验证全覆盖，另用 5 验证 partial |
| 文档称 `PDF_OCR_LANG` 由 compose 设置 | 代码默认改为 `eng+chi_sim`，compose 显式传默认值 |

## 10. 非目标

- 不做完整 PDF 版式还原/像素级一致；
- 不保证表格 100% 结构化；
- 不绕过付费墙、不抓登录态、不执行 JS；
- 不做公式识别；
- 不做知网等非开放数据源。
