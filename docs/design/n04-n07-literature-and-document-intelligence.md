# N-04 真实文献与网页检索 + N-07 文档智能：改进设计与实施方案

- **状态**：设计稿（待评审）
- **日期**：2026-10-27
- **范围**：N-04（真实文献与网页检索）改进版、N-07（文档智能）改进版
- **关联**：`docs/improvement-2026-10-26/03-new-feature-proposals.md`、`research-assistant/backend/internal/literature/`、`research-assistant/backend/internal/documents/`、`research-assistant/backend/internal/api/literature.go`、`research-assistant/backend/internal/api/plan_runtime.go`
- **一句话结论**：N-04 不是“从零建检索”，而是补前端发现层、文献库、网页正文、引用图谱和计划节点；N-07 不是“从零做 OCR”，而是把现有 Tesseract 兜底升级为逐页、混合页、版面/表格/图注、质量可解释的文档智能。

---

## 1. 摘要

原提案（`03-new-feature-proposals.md`）把 N-04 写成“接入 arXiv/Semantic Scholar/OpenAlex/Crossref/PubMed/GitHub”，把 N-07 写成“接入 OCR”。按 2026-10-27 的实际代码与运行态复核，这两个假设都已过时：

- **N-04 的后端已经存在**：`internal/literature` 已支持 arXiv / OpenAlex / Crossref / GitHub 搜索、缓存、按 host 限流、DOI/arXiv/PDF URL 导入；`GET /api/literature/search`、`POST /api/literature/import` 已注册；计划意图含 arXiv/DOI 会自动导入 PDF 与元数据。
- **N-07 的基础已经存在**：`documents/pdf.go` 在 `pdftotext` 无文本时，会用 `pdftoppm + tesseract` 对前 N 页做 OCR；后端镜像已安装 `tesseract-ocr`、`eng`、`chi_sim`；BM25、Outline、LocateQuote 也已具备。
- **真正缺的是产品层和结构化层**：N-04 缺前端搜索/文献库/网页阅读/引用图谱；N-07 缺逐页混合 OCR、版面/表格/图注抽取、OCR 质量报告与前端可视化。

因此本文按“保留已有能力、补齐缺口、明确边界”来设计，不再重复造轮子。

---

## 2. 现状盘点

### 2.1 N-04 现状

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| 多源搜索 | arXiv / OpenAlex / Crossref / GitHub；缓存、按 host 限流、结构化错误、DOI/arXiv/标题去重 | `backend/internal/literature/client.go`、`providers.go`、`literature.go` | 无 Semantic Scholar / PubMed / DBLP / Unpaywall；arXiv 429 无退避重试；GitHub 无 token 易触发 403/限流 |
| 文献导入 | arXiv ID/URL、DOI、PDF URL → 下载 PDF、校验 %PDF、大小上限、按 URL 缓存；落盘为上传附件 | `backend/internal/api/literature.go:107-235` | 仅后端 API；无“导入到当前会话/当前计划”的 UI 流程 |
| 计划自动导入 | 意图含 arXiv URL/ID/DOI 时，`POST /api/plan` 自动取元数据 + PDF，写入 ResearchContext | `backend/internal/api/plan_runtime.go:245-263`、`literature.go:295-359` | 只有“显式引用”才触发；不能按关键词“找论文” |
| 前端入口 | **没有**：`frontend/src` 无 `/api/literature/search` 调用、无文献搜索组件 | grep 为 0 | 用户只能手动上传或粘贴 arXiv/DOI，不能浏览/发现 |
| 文献库 | **没有**：搜索结果不落库，导入后只是一个 upload | 上传目录按 owner/hash；无 literature 表 | 无收藏、标签、BibTeX、引用历史、跨会话复用 |
| 网页阅读 | **没有**：`/api/pdf-proxy` 只接受 PDF，不解析 HTML 正文 | `backend/internal/api/routes.go:158-220` | 不能读博客/项目页/文档站；无 readability、robots、正文抽取 |
| 引用图谱 | **没有**：无 references/cited_by 存储 | 无 provider 字段 | 不能做“这篇论文引用了谁/被谁引用” |
| 真实可用性 | Crossref/OpenAlex 实测可返回结果；arXiv 实测 429；GitHub 无 token 失败 | 本次实测：crossref 2 条、openalex 2 条 | 需要 provider 降级、重试、token 配置与 UI 提示 |

### 2.2 N-07 现状

| 能力 | 现状 | 证据/代码 | 缺口 |
|---|---|---|---|
| PDF 文本层 | `pdftotext -layout -enc UTF-8`，带 `PDF_PARSE_MAX_MB`（默认 128MB）与 `PDF_PARSE_TIMEOUT_SECONDS`（默认 90s） | `documents/pdf.go` | 双栏论文 reading order 可能错；无 block/bbox |
| 扫描件 OCR | 仅当整篇 `Characters == 0` 时，用 `pdftoppm + tesseract` 对前 `PDF_OCR_MAX_PAGES` 页（默认 5）OCR；300 DPI；每页 60s | `documents/pdf.go:132-355` | 混合页（部分文本页 + 部分扫描页）不会 OCR；只前 5 页；页数被截断；无质量报告 |
| OCR 语言 | `PDF_OCR_LANG` 默认 `eng`；镜像装了 `chi_sim`，但 compose 没传 `PDF_OCR_LANG` | `pdf.go:327-332`、`docker-compose.yml:152-153` | 中文扫描件默认 OCR 质量差 |
| 章节/检索 | `Outline` 识别标题；`BM25`（IDF/k1/b/长度归一化）；`LocateQuote` 支持页码/偏移 | `documents/outline.go / bm25.go / quote.go` | 表格/图注不是 chunk；OCR 文本没有 block 结构；无表格/图检索 |
| 元数据 | `ExtractMetadata` + provider 校正；Attention/Louvain fixture 已修正 | `documents/metadata.go` | 扫描件/双栏的标题/作者仍可能不准；无 layout 证据 |
| 表格/图/公式 | **没有**：`Document` 只有 `Pages[].Text` | `documents/document.go` | 不能把表格变成 CSV/JSON，不能把图注绑定到图，不能做公式 OCR |
| 质量报告 | 只有 `Status/Error`；无 per-page OCR 置信度/失败页 | `documentSummary` 只返回 status/page_count/characters/extractor | 用户不知道 OCR 是否可靠、哪些页失败 |
| 前端展示 | PDF 阅读器 + 文本；无 OCR 徽标、表格/图预览 | `frontend/src/features/pdf-viewer` | 不能看到“这页是 OCR 的/置信度多少/这里有个表” |

**本次实测**

- 上传一份“扫描版”测试 PDF（图片转 PDF，35,353 bytes）：
  - 返回 `status=extracted`、`extractor=tesseract-ocr`、`page_count=1`、`characters=121`；
  - 打开 `document.json`，正文为 “SCANNED PAPER TITLE / This isa scanned document …”，文本正确但有一个 OCR 空格错误（`isa`）。
- 这说明 **OCR 兜底真实可用**，但也暴露：没有置信度、没有纠错提示、没有“哪些页是 OCR”的结构化信息。

---

## 3. 问题出在哪

### 3.1 N-04：不是不能读，是“能读不能搜、能导不能管、能 PDF 不能网页”

#### 3.1.1 能读不能搜：用户只能“给文献”，不能“找文献”

- 现状：用户必须上传 PDF，或在 prompt 里写 arXiv/DOI/PDF URL；Librarian 的 `general_research` 没有调用 `literature.Search`。
- 后果：
  - “帮我找 5 篇关于 graph neural network 的论文并总结”会走 LLM 知识，不会真实检索；
  - 用户无法看到候选、年份、引用数、是否开放获取；
  - 计划里没有“文献检索”节点，证据链从第一步就不可审计。
- 根因：
  - 前端没有 Literature 面板/服务调用；
  - planner 没有 `literature_search` task type；
  - `librarian.ExecuteTask` 只在有 `document_id` 时读文档，没有 provider search 分支（`librarian.go:98-170`）。

#### 3.1.2 能导入不能管理：搜索结果是一次性的

- 现状：`/api/literature/search` 返回 JSON；导入后变成 upload；没有 literature record、没有 library、没有 BibTeX、没有引用历史。
- 后果：
  - 同一篇论文重复搜索/重复导入（虽然 upload 有 SHA 去重，但元数据/收藏/标签丢失）；
  - 用户无法在多个计划间复用文献；
  - 无法从“这篇论文”跳到“它引用的论文/被谁引用”。
- 根因：数据模型只有 `uploadMetadata / documents`，没有 `LiteratureRecord`；搜索结果没有持久化。

#### 3.1.3 能 PDF 不能网页：只代理 PDF

- 现状：`/api/pdf-proxy` 只接受 PDF URL，校验 content-type、大小、SSRF；没有 HTML 正文抽取。
- 后果：
  - 用户给项目主页/博客/文档链接，系统不能读正文；
  - 文献调研只能靠 PDF，不能用网页资料；
  - 引用只能是 PDF 页码，不能是 URL + 段落。
- 根因：缺少 `web_read` 工具/API；没有 readability、robots、正文缓存。

#### 3.1.4 Provider 可靠性与覆盖不足

- 现状：默认 4 个来源；arXiv 实测 429，代码只返回 `upstream_error`，不解析 `Retry-After`、不做指数退避；GitHub 无 token 时易触发 403/限流。
- 后果：
  - 只选 arXiv 时，一次 429 就整个搜索失败；
  - 没有 Semantic Scholar/PubMed/DBLP，医学/CS 覆盖率不足；
  - 没有 Unpaywall/OA 解析，很多 DOI 导入会 `pdf_unavailable`。
- 根因：`literature.Client.fetch` 对 429 无特殊处理；provider 集合固定。

#### 3.1.5 搜索质量与可解释性

- 现状：结果按 source 顺序 + 年份排序；没有 relevance 分数、citation 排序、venue 过滤；warnings 有返回但前端无展示。
- 后果：用户不知道“为什么这条排第一”，也无法按引用数/年份/venue 排序。
- 根因：`Result` 有 `citations` 但排序未使用；无 per-result rank/explain。

### 3.2 N-07：不是没有 OCR，是 OCR 不够“文档智能”

#### 3.2.1 混合页不 OCR：最常见的长论文场景失效

- 现状：`ExtractPDF` 只有整篇 `Characters == 0` 才走 `extractScannedPDF`（`pdf.go:96-98`）。
- 后果：一本论文前几页是扫描图、后面是文本层，`pdftotext` 会返回非零字符，扫描页保持空白，且不会触发 OCR。
- 根因：判断粒度是“整篇”，不是“逐页”。

#### 3.2.2 只 OCR 前 5 页：长扫描件被截断

- 现状：`ocrFallbackPages` 只循环 `page <= maxPages`（默认 5）；`PageCount = len(pages)` 因此变成 5，哪怕原 PDF 有 50 页。
- 后果：
  - 第 6 页以后不可检索、不可引用；
  - 用户看到 `page_count=5`，误以为 PDF 只有 5 页；
  - 计划里的页码引用可能错位。
- 根因：`PDF_OCR_MAX_PAGES` 被当成“文档页数上限”，而不是“OCR 预算”；没有 `total_pages` 与 `partial` 状态。

#### 3.2.3 默认英文：中文扫描件质量差

- 现状：`ocrTesseractArgs` 读 `PDF_OCR_LANG`，默认 `eng`；docker-compose 没有设置 `PDF_OCR_LANG`，尽管镜像安装了 `chi_sim`。
- 后果：中文扫描论文 OCR 出现大量乱码/漏字；用户不知道为什么。
- 根因：默认值与镜像能力不一致；没有语言检测/自动 fallback。

#### 3.2.4 没有版面/阅读顺序：双栏论文 OCR 会串行

- 现状：Tesseract 直接输出纯文本；没有 `hOCR/TSV` 的 block/bbox；没有栏检测。
- 后果：
  - 双栏论文的左右栏文本交错，句子断裂；
  - 标题、作者、章节层级丢失，`Outline` 失效；
  - BM25 检索把页眉/页脚/参考文献当正文。
- 根因：只保存 `Pages[].Text`，没有 `Blocks[]` 与 layout 信息。

#### 3.2.5 没有表格/图注/公式：科研文档的核心结构丢失

- 现状：`Document` 只有文本页；表格变成一串词，图注变成普通段落，公式变成乱码。
- 后果：
  - 不能把实验表格抽成 CSV/JSON 给 Benchmark/AutoResearch；
  - 不能把图注绑定到图，Claim Evidence 无法引用“Table 2 / Figure 3”；
  - 复现论文时，模型看不到关键表格数据。
- 根因：缺少 table/figure extraction 与 artifact 模型。

#### 3.2.6 没有质量报告：用户无法判断 OCR 是否可信

- 现状：只有 `status/error`；没有 per-page OCR 置信度、失败页、跳过页、语言。
- 后果：
  - 用户不知道 121 字符里有多少是错的；
  - 计划基于低质量 OCR 生成结论，风险不可见；
  - 排障只能看日志。
- 根因：`Document` 数据模型没有 `OCRStats/Quality` 字段。

---

## 4. 为什么这么做

### 4.1 N-04：先补“发现层”，不要重写“阅读层”

- 后端已有 provider 客户端、缓存、限流、去重、导入、自动导入；重写会重复劳动并引入行为差异。
- 真正缺口是：
  1. **用户看不见**：没有搜索 UI/文献库；
  2. **计划用不上**：没有 `literature_search` 节点；
  3. **网页读不了**：只有 PDF；
  4. **引用链断**：没有 citation graph。
- 因此 N-04 改进版遵循：**复用 `internal/literature` → 加 UI/库/节点/网页/图谱**，而不是再写一套 provider。

### 4.2 N-07：OCR 是兜底，不是“理解”

- OCR 只负责“把像素变成带位置和置信度的文本”；理解、摘要、引用判断仍由后续 Agent 做。
- 因此：
  - 逐页处理：避免混合页漏 OCR；
  - 保留 block/bbox/confidence：为版面、表格、引用定位提供证据；
  - 表格/图注作为一等 artifact：Benchmark/Claim 才能消费；
  - 质量报告显式化：低质量 OCR 必须让用户看见；
  - 不追求“完美还原 PDF”，只追求“可检索、可引用、可复现”。

### 4.3 共同原则

- **Provenance first**：任何文本都要能回到 page/block/table/figure/URL。
- **Graceful degradation**：provider 失败给 warning；OCR 失败给 partial + missing requirement；不静默伪造。
- **Cache & idempotency**：搜索按 URL 缓存；OCR 按 PDF SHA + 配置缓存；导入按 SHA 去重。
- **Security by default**：网页/PDF 抓取做 SSRF、大小、超时、content-type、robots 检查。
- **Human-visible**：warning、置信度、失败页、来源、许可证都要展示。

---

## 5. 怎么做

### 5.1 N-04 目标架构

```text
前端 LiteraturePanel
  ├─ 搜索框 / 来源筛选 / 年份 / 排序
  ├─ 结果卡片（标题/作者/年份/venue/引用数/OA/来源）
  ├─ 一键导入（仅元数据 / 下载 PDF）
  └─ 文献库（收藏/标签/BibTeX/加入计划）
        │
        ▼
GET /api/literature/search  ──►  Provider Registry
POST /api/literature/import       ├─ arXiv
GET  /api/literature/records      ├─ OpenAlex
GET  /api/literature/records/:id  ├─ Crossref
POST /api/literature/records/:id/attach ── GitHub
GET  /api/literature/records/:id/bibtex  ├─ Semantic Scholar（新增）
GET  /api/literature/citations/:doi      ├─ PubMed（新增）
        │                          └─ Unpaywall（新增）
        ▼
   LiteratureRecord（SQLite/JSON index）
        │
        ▼
   Upload + Document（现有上传/解析链）
        │
        ▼
   Plan node literature_search / ResearchContext
```

#### 5.1.1 数据模型

```go
type LiteratureRecord struct {
    ID          string    // 稳定 ID（source:id）
    Source      string    // arxiv/openalex/crossref/github/s2/pubmed
    ExternalID  string    // arXiv ID / DOI / OpenAlex ID
    Title       string
    Authors     []string
    Year        int
    Venue       string
    Abstract    string
    DOI         string
    ArxivID     string
    URL         string
    PDFURL      string
    Citations   int
    OpenAccess  bool
    License     string
    Tags        []string
    UploadID    string    // 导入后关联的上传
    CreatedAt   time.Time
}
```

#### 5.1.2 Provider 抽象

```go
type Provider interface {
    Name() string
    Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error)
    Metadata(ctx context.Context, ref string) (*Result, error)
    Import(ctx context.Context, ref string) (*ImportResult, error)
}
```

- 统一 `Result` 字段；每个 provider 自己处理认证、分页、429/Retry-After、字段映射。
- 失败策略：单 provider 失败 → warnings；全部失败 → 结构化错误；有结果就返回。
- 排序：relevance（provider 原始分）→ citation → year；UI 可切换。

#### 5.1.3 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/literature/search?q=&sources=&year_from=&limit=&sort=` | 现有，增加 `sort`（relevance/citations/year） |
| POST | `/api/literature/import` | 现有：`{ref, attach}` |
| GET | `/api/literature/records?q=&tag=&cursor=` | 文献库列表（owner 隔离） |
| GET | `/api/literature/records/:id` | 详情 |
| DELETE | `/api/literature/records/:id` | 删除收藏（不删上传文件，除非无引用） |
| POST | `/api/literature/records/:id/attach` | 把已导入文献加入会话/计划 |
| GET | `/api/literature/records/:id/bibtex` | BibTeX 导出 |
| GET | `/api/literature/citations/:doi` | 引用/被引列表（OpenAlex/S2） |
| POST | `/api/web/fetch` | 网页正文抽取（见 5.1.5） |

#### 5.1.4 计划节点 literature_search

- 触发：用户说“找/搜索/调研/综述/相关论文”，且没有上传 PDF。
- DAG：`literature_search (librarian_agent) → paper_parse（对 top K 下载 PDF）→ general_research → verify_result → general_synthesis`
- 输入：`query`、`sources`、`limit`、`min_year`、`open_access_only`。
- 输出：`literature_results`（JSON，含 title/doi/abstract/url/pdf_url）、`selected_papers`。
- 逻辑：
  1. 调 `literature.Search`，取 top K（默认 5）；
  2. 按 relevance/citation 排序，过滤无标题/重复 DOI；
  3. 对前 N 篇尝试 `Import`；失败则保留 metadata-only，并写 `MissingRequirement{kind:"pdf_unavailable"}`；
  4. 把选中论文写入 `ResearchContext.Attachments`，后续节点走现有 `paper_parse`。
- 前端：结果显示“已导入 3/5 篇，2 篇仅元数据”，可点“重试下载”。

#### 5.1.5 网页正文 web_read

- API：`POST /api/web/fetch {url, max_bytes?}` → `{title, url, text, excerpt, links, fetched_at}`。
- 实现：
  - SSRF：仅 http/https，解析公网 IP，禁止私网/环回/链路本地；重定向逐跳校验；同 `validateRemotePDFURL`。
  - content-type 仅 `text/html` / `text/plain` / `application/xhtml+xml`；大小上限（如 2MB）；超时 15s。
  - 正文抽取：readability 算法（Go 可用 `go-readability` 或自写 DOM 评分），保留标题、段落、列表、代码块。
  - robots.txt：按 UA 检查；不抓取禁止路径（可配置严格模式）。
  - 缓存：URL + 内容 hash；TTL 1h；不缓存登录态内容。
  - 引用：返回段落级 `quote` 与 URL，前端可点回原文。
- 非目标：不执行 JS、不登录、不绕付费墙；复杂 SPA 返回“需要浏览器渲染”的明确错误。

#### 5.1.6 前端 LiteraturePanel

- 入口：左侧工作区新增“文献”标签，或 PDF 面板顶部“文献库”。
- 搜索区：关键词、来源多选（arXiv/OpenAlex/Crossref/GitHub/S2/PubMed）、年份、仅 OA、排序。
- 结果卡片：标题、作者、年份、venue、引用数、来源 badge、OA badge、摘要展开、`导入 PDF` / `仅元数据`。
- 文献库：收藏、标签、搜索、批量导出 BibTeX、加入当前计划。
- 错误：provider warnings 显示“arXiv 限流，已用 OpenAlex 结果”；全部失败给“手动上传/稍后重试”。

### 5.2 N-07 目标架构

```text
PDF → 逐页处理
  ├─ pdftotext -bbox-layout（有文本层）
  │    → blocks（标题/正文/表格候选）+ bbox + page
  ├─ 无文本/低文本页
  │    → pdftoppm 300dpi → tesseract hOCR/TSV
  │    → OCR blocks + confidence
  ├─ 版面分析
  │    → 栏检测 / reading order / 标题层级
  ├─ 表格/图注
  │    → table detection → rows/cols → CSV/JSON
  │    → figure region → PNG + caption
  └─ Document（pages/blocks/tables/figures/quality）
        │
        ├─ Outline（章节树）
        ├─ BM25（text/table/figure chunks）
        ├─ LocateQuote（page/block offset）
        └─ PDF viewer overlays / API
```

#### 5.2.1 数据模型扩展

```go
type Block struct {
    ID         string
    Type       string  // title/paragraph/header/footer/table/figure/formula
    BBox       [4]float64
    Text       string
    Confidence float64 // OCR 置信度；文本层为 1
    Page       int
}

type Table struct {
    ID      string
    Page    int
    Caption string
    BBox    [4]float64
    Rows    [][]string
    CSVRef  string // blob/文件引用
}

type Figure struct {
    ID      string
    Page    int
    Caption string
    BBox    [4]float64
    ImageRef string
}

type PageQuality struct {
    Page          int
    Source        string  // text/ocr/mixed
    Chars         int
    OCRConfidence float64
    Failed        bool
    Error         string
}

type Document struct {
    // 现有字段...
    Status      Status // extracted/partial/empty/encrypted/unsupported
    TotalPages  int    // 真实页数；OCR 只处理部分时也保留
    Pages       []Page
    Blocks      []Block
    Tables      []Table
    Figures     []Figure
    Quality     []PageQuality
    OCRStats    OCRStats
}
```

#### 5.2.2 逐页 OCR 策略

- 对每页先跑 `pdftotext -f page -l page -bbox-layout` 或整篇 bbox 输出，统计字符数。
- 若某页字符数低于阈值（如 < 20）或只有页眉页脚，标记为 `needs_ocr`。
- 只对 `needs_ocr` 页做 `pdftoppm + tesseract`；文本页保留文本层。
- `PDF_OCR_MAX_PAGES` 改为“本次最多 OCR 页数”（预算），不是“只读前 N 页”；剩余页标记 `skipped`，状态 `partial`，UI 明示。
- 默认 `PDF_OCR_LANG=eng+chi_sim`（compose 设置）；可按文档语言检测覆盖。
- 缓存：`sha256(pdf) + ocr_config` → document.json；重复上传直接复用。

#### 5.2.3 版面与阅读顺序

- 文本 PDF：`pdftotext -bbox-layout` 给 word/block bbox；按 x/y 聚类成栏、按栏内 y 排序，生成 reading order。
- 扫描 PDF：Tesseract `hOCR/TSV` 给 block/line/word bbox；同样做栏检测。
- 标题识别：字号/加粗/centering + 章节关键词；保留 `Outline` 的标题树。
- 页眉/页脚：位于页面上下 5% 且重复出现的文本，标记 `header/footer`，BM25 降权。
- 不追求复杂 ML 版面模型；先用确定性几何 + 关键词，后续可替换。

#### 5.2.4 表格抽取

- 文本 PDF：
  - 用 `pdftotext -layout` 的表格块或 `pdftohtml -xml` 的 `<text>` 坐标；
  - 规则：连续行、列对齐、分隔线/空白列；识别 caption（“Table N: …”）；
  - 输出 `Rows [][]string` + CSV blob；置信度（列数一致性/单元格填充率）。
- 扫描 PDF：
  - 先做表格区域检测（OpenCV 线检测或 Tesseract 的 table block），再 OCR 单元格；
  - 失败则保留表格区域图 + “未能结构化”的 warning，不硬造数据。
- 表格 chunk 进入 BM25：标题 + 表头 + 行文本；检索时可命中“Table 2 accuracy”。

#### 5.2.5 图注/图区域

- 检测 “Figure N: …” / “Fig. N …” caption；绑定到最近图区域（bbox IoU/距离）。
- 图区域导出 PNG（crop），图注作为 `Figure.Caption`；前端 PDF 页可高亮/查看大图。
- Claim Evidence 可引用 `figure_id` + caption + page，而不是只引用正文。

#### 5.2.6 质量报告

- 每页 `PageQuality`：source（text/ocr/mixed）、chars、OCR 置信度、失败原因。
- 文档级 `OCRStats`：OCR 页数、平均置信度、失败页数、跳过页数、语言。
- API：`GET /api/uploads/:id/document/quality`；前端文档摘要显示“3 页 OCR，平均置信度 0.82；2 页失败”。
- 低置信度（如 < 0.6）在 UI 标黄，Librarian prompt 标注“该页 OCR 可能不准确”。

#### 5.2.7 API/前端

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/uploads/:id/document` | 增加 quality、tables、figures 摘要 |
| GET | `/api/uploads/:id/document/pages/:page/layout` | blocks + bbox + type |
| GET | `/api/uploads/:id/document/tables/:tableId` | 表格 JSON/CSV |
| GET | `/api/uploads/:id/document/figures/:figureId` | 图片/图注 |
| GET | `/api/uploads/:id/document/quality` | 质量报告 |

前端：
- PDF 页叠加 block 高亮（可选），表格/图注侧栏；
- 文档摘要 badge：“文本层 / OCR / 混合 / 部分”；
- 表格预览 + 下载 CSV；图注 + 原图；
- 低置信度页提示；
- 引用时从 `LocateQuote` 返回 page/block。

### 5.3 实施阶段

| 阶段 | 周期 | 内容 | 交付 |
|---|---|---|---|
| Phase 0：N-07 正确性 | 1 周 | 逐页混合 OCR；PDF_OCR_MAX_PAGES 作为预算；total_pages/partial；默认 eng+chi_sim；质量报告；OCR 缓存 | 扫描件 20 页完整解析；混合 PDF 空页补齐；中文 OCR 可用 |
| Phase 1：N-04 发现层 | 2 周 | 前端 LiteraturePanel；文献库；literature_search 节点；S2/PubMed provider；BibTeX | 关键词搜索→导入→加入计划闭环 |
| Phase 2：N-07 结构 | 2–3 周 | bbox/blocks/reading order；表格抽取；图注/图区域；BM25 类型 chunk | 双栏论文顺序正确；Table 2 可导出 CSV；Figure 3 可引用 |
| Phase 3：N-04 网页与图谱 | 2–3 周 | /api/web/fetch；citation graph；多文档 RAG；UI 引用 | 读网页正文；看引用/被引；跨文献综述 |

### 5.4 验收标准（容器内）

**N-04**

1. 关键词 “graph neural network” 返回 ≥2 个 provider 的结果（Crossref/OpenAlex 实测可）；
2. 点击导入 → upload 落盘、document 解析、文献库有记录；
3. 同一 DOI 二次导入 → 复用同一 upload/record，不重复下载；
4. prompt “帮我找 5 篇 GNN 综述并总结” → 生成 literature_search 节点，至少 3 篇带 PDF，2 篇 metadata-only 有 missing requirement；
5. provider 失败（如 arXiv 429）→ 警告展示，其他 provider 结果仍可用；
6. `POST /api/web/fetch` 对公开博客返回标题+正文；私网 URL 返回 403。

**N-07**

1. 20 页扫描 PDF：page_count=20，OCR 覆盖 20 页；若设置 PDF_OCR_MAX_PAGES=5，状态 partial，明确“15 页跳过”；
2. 混合 PDF（前 2 页扫描、后 8 页文本）：2 页 OCR、8 页 text，全部可检索；
3. 中文扫描页：PDF_OCR_LANG=eng+chi_sim，字符数/可读性显著优于 eng；
4. 双栏论文：block 顺序正确（左栏→右栏），标题/章节可识别；
5. 含表格页：导出 CSV，列数/行数与页面一致；无法结构化时给 warning，不编造；
6. 图注 “Figure 3” 绑定到图区域，Claim 可引用 figure_id；
7. LocateQuote 对 OCR 文本返回页码/block 偏移；
8. 质量报告显示 OCR 页数/置信度/失败页。

---

## 6. 与旧提案的差异

| 项 | 旧提案（03-new-feature-proposals.md） | 本文改进版 |
|---|---|---|
| N-04 范围 | “接入 arXiv/S2/OpenAlex/Crossref/PubMed/GitHub；URL/DOI 导入；引用图谱；论文库” | **后端 4 个 provider 已存在**；重点改为前端 LiteraturePanel、文献库、literature_search 节点、S2/PubMed/DBLP 补充、web_read、citation graph |
| N-04 工作量 | M（2–3 周） | 拆成 Phase 1（2 周）+ Phase 3（2–3 周）；避免重复实现已有客户端 |
| N-07 范围 | “OCR + 版面/表格/图注/公式 + 章节树 + 质量报告” | **OCR 兜底已存在**；重点改为逐页/混合页、全页预算、语言、bbox/reading order、表格/图、质量报告、前端叠加 |
| N-07 工作量 | L（3–4 周） | Phase 0（1 周）+ Phase 2（2–3 周）；基础 OCR 不重写 |
| 风险 | 未区分“已有/待建” | 明确已有代码、缺口、验收；避免把已有能力算成新功能 |

> 建议评审通过后，同步更新 `03-new-feature-proposals.md` 的 N-04/N-07 两节，避免后续排期按“从零”估算。

---

## 7. 关键文件与现有实现索引

| 层 | 文件 | 说明 |
|---|---|---|
| 文献客户端 | `backend/internal/literature/client.go` | Search/Import/缓存/限流 |
| 文献 provider | `backend/internal/literature/providers.go` | arXiv/OpenAlex/Crossref/GitHub 解析 |
| 文献 API | `backend/internal/api/literature.go` | search/import/auto-import |
| 计划自动导入 | `backend/internal/api/plan_runtime.go:245-263` | arXiv/DOI 自动导入 |
| 文档模型 | `backend/internal/documents/document.go` | Pages/Status |
| PDF 解析/OCR | `backend/internal/documents/pdf.go` | pdftotext + tesseract fallback |
| BM25/检索 | `backend/internal/documents/bm25.go` | IDF/k1/b |
| 章节树 | `backend/internal/documents/outline.go` | SectionInfo |
| 引用定位 | `backend/internal/documents/quote.go` | LocateQuote |
| 元数据 | `backend/internal/documents/metadata.go` | title/author + provider 校正 |
| 文档 API | `backend/internal/api/documents.go` | pages/search/section/assist |
| 前端 PDF | `frontend/src/features/pdf-viewer/` | 阅读器/翻译 |
| 前端 API | `frontend/src/services/api/documentApi.ts` | 文档分页 |
| 后端镜像 | `research-assistant/backend/Dockerfile:33` | tesseract eng/chi_sim |
| Compose | `research-assistant/docker-compose.yml:152-153` | OCR 页数/超时（缺语言） |

---

## 8. 非目标与风险

### 8.1 非目标

- 不做完整 PDF 版式还原（不追求与原文像素级一致）；
- 不保证表格 100% 结构化；失败必须显式 warning；
- 不做知网/付费墙绕过；只做开放获取与用户上传；
- 不做登录态网页抓取/执行 JS；
- 不做 Semantic Scholar 之外的“全互联网学术搜索”承诺；
- 不把 OCR 文本直接当事实；仍需引用校验。

### 8.2 风险与缓解

| 风险 | 缓解 |
|---|---|
| OCR 慢/占 CPU | 逐页预算、缓存、后台任务、限制并发；长文档返回 partial |
| Tesseract 中文准确率 | chi_sim + 语言检测；低置信度标记；允许用户校对 |
| 双栏 reading order 复杂 | 先几何 + 标题规则，失败保留原顺序并标记 |
| 表格抽取误判 | 置信度 + 人工确认；失败保留图 + warning |
| 网页抓取法律/robots | robots 检查、仅公开页、缓存、用户可删除 |
| Provider 限流/封禁 | 缓存、退避、token、警告、降级 |
| 文献库膨胀 | owner 配额、按需保留 PDF、metadata-only 可选 |
| 版权 | 只下载 OA/用户有权内容；记录 license；不对外分发 |

---

## 9. 结论

- **N-04 的正确投入顺序**：先把已有 `internal/literature` 接到前端和计划节点，再补 S2/PubMed、web_read、citation graph；不要重写 provider。
- **N-07 的正确投入顺序**：先把现有 OCR 从“前 5 页兜底”修成“逐页/混合/全页预算/质量报告”，再做版面/表格/图注；不要从零造 OCR。
- 两者共享同一个 provenance 模型（page/block/table/figure/URL），这是后续 Claim Evidence、Benchmark、AutoResearch 可信度的基础。
