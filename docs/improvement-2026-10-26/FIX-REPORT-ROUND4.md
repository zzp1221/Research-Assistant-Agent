# FIX-REPORT-ROUND4：N-04 真实文献与网页检索 + N-07 文档智能

- 日期：2026-10-27
- 设计依据：`docs/design/n04-n07-literature-and-document-intelligence.md`
- 基线：`master @ 0f605c2`（含第三轮未提交成果，第三轮容器测试已通过）
- 测试环境：golang:1.26.1-bookworm / ra-doctest（Go + poppler + tesseract）/ node:22-bookworm-slim / 运行中的 compose 栈
- 结论：设计文档 5.4 的 N-04 6 条、N-07 8 条全部在容器内可复现；后端/前端/集成/E2E/回归全部通过。

## 1. 实际复核与文档差异

设计文档对现状的判断与代码一致：`internal/literature` 已有 arXiv/OpenAlex/Crossref/GitHub 搜索与 DOI/arXiv/PDF 导入；`documents/pdf.go` 仅在整篇 0 字符时 OCR 前 N 页；前端无文献调用；`/api/pdf-proxy` 只代理 PDF；`Document` 只有 Pages[].Text。

本轮按“保留已有能力、补齐产品层/结构化层”实施，实际差异记录在 `spec.md` 第 9 节。关键决策：
- 公式 OCR 非目标（设计文档 8.1 也未列入验收），仅预留 `Block.Type=formula`。
- 容器未安装 OpenCV，表格采用确定性行/空白列启发式 + Tesseract TSV，失败显式 warning。
- 引用图谱使用 OpenAlex（无需 key、稳定）；Semantic Scholar 作为搜索 provider。
- 文献库采用 owner 隔离 JSON 文件（与 plan/modelprofile 存储一致），原子写。

## 2. 实现内容

### N-04 文献与网页
- **Provider**：新增 Semantic Scholar、PubMed、DBLP、Unpaywall；`Result` 增加 `rank/score/rank_reason/open_access/license`；`SearchOptions.Sort` 支持 relevance/citations/year。
- **可靠性**：`fetch` 对 429/503 解析 Retry-After（秒/HTTP-date）并指数退避，最多 `LITERATURE_MAX_RETRIES`（默认 2）；单 provider 失败降级 warning。
- **文献库** `internal/literature/library.go`：Record、owner 隔离、原子写、DOI/arXiv/标题去重合并、标签、BibTeX、加入会话；导入/元数据导入自动 Upsert；重复 DOI 导入复用已有 upload/record，不重复下载。
- **引用图谱** `internal/literature/citations.go`：OpenAlex references/cited_by，部分失败 warning。
- **网页正文** `internal/webread`：SSRF（公网解析 + 逐跳重定向 + DialContext 防 DNS rebinding）、robots.txt、content-type/2MB/15s、readability 评分、段落/链接、URL 缓存。
- **API**：`/api/literature/records`（GET）、`/records/:id`（GET/DELETE）、`/records/:id/attach`、`/records/:id/bibtex`、`/api/literature/citations/:doi`、`POST /api/web/fetch`。
- **计划节点**：注册 `literature_search`（librarian_agent，real execution）；General 发现类意图生成 `literature_search → general_research → general_synthesis`；Librarian 真实检索、top K 导入 PDF、metadata-only 写 `pdf_unavailable` missing requirement，输出 `literature_results` artifact。
- **前端**：`LiteraturePanel`（检索/来源/年份/排序/结果卡/导入 PDF/仅元数据/文献库/标签/BibTeX/引用/网页），左侧工作区新增“文献”标签。

### N-07 文档智能
- **模型** `document.text/v2`：Block/Table/Figure/PageQuality/OCRStats、TotalPages、StatusPartial；旧 v1 可加载。
- **逐页 OCR**：`pdfinfo` 真实页数；`pdftotext -layout` + `-bbox-layout`；needs_ocr 阈值；`PDF_OCR_MAX_PAGES` 作为预算；Tesseract TSV 解析 block/line/word bbox 与置信度；默认 `PDF_OCR_LANG=eng+chi_sim`；`partial` 明示跳过/失败页。
- **版面**：双栏检测与 reading order（左栏→右栏）、页眉/页脚/标题、OCR blocks。
- **结构**：表格行对齐抽取 + caption + CSV + 置信度；图注识别 + `pdfimages` 导出 + Figure；BM25 text/table/figure chunks；`LocateQuoteLocation` 返回 page/byte/block 偏移。
- **质量**：per-page source/chars/OCR 置信度/失败/跳过 + OCRStats；API `/document/quality`、`/pages/:page/layout`、`/tables/:tableId`（JSON/CSV）、`/figures/:figureId`（PNG/JSON）、`/document/locate`。
- **缓存**：`sha256(pdf)+ocr_config` → document.json + tables/figures 资源。
- **前端**：PDF 摘要 badge（文本层/OCR/混合/部分）、OCR 页数/平均置信度、低置信度页警告、表格/图注面板与 CSV/原图链接。

## 3. 设计文档 5.4 验收对照

### N-04
| # | 验收 | 证据 |
|---|---|---|
| 1 | “graph neural network” ≥2 provider 结果 | E2E `literature.mjs`：Crossref/OpenAlex 返回 8 条 |
| 2 | 导入 → upload/document/文献库记录 | E2E：metadata-only import 生成 record；单元测试覆盖 PDF 落盘/解析路径 |
| 3 | 同一 DOI 二次导入复用 upload/record | API 增加 FindByDOI 短路；`library_lookup_test` + 手工 E2E 覆盖 |
| 4 | “找 5 篇 GNN 综述”生成 literature_search 节点，≥3 PDF/2 metadata-only | E2E `literature-plan.mjs` 节点断言；Librarian 单元测试断言导入 + missing requirement |
| 5 | provider 失败 warning、其他结果可用 | `TestProviderFailureIsWarningWhenAnotherSourceWorks`；429 退避单测 |
| 6 | /api/web/fetch 公开页返回标题+正文；私网 403 | E2E `literature.mjs`：example.com 200，127.0.0.1 403 ssrf_blocked |

### N-07
| # | 验收 | 证据 |
|---|---|---|
| 1 | 20 页扫描 PDF：page_count=20、OCR 20 页；预算 5 时 partial/15 页跳过 | `TestExtractScannedPDFWithFullBudget`、`TestExtractScannedPDFBudgetProducesPartial`；E2E 20 页全量 |
| 2 | 混合 PDF：2 页 OCR、8 页 text，全部可检索 | `TestExtractMixedPDFPerPageOCR`；E2E mixed-10pages |
| 3 | 中文扫描页 eng+chi_sim 可用 | `TestExtractChineseScanWithChiSim`；E2E 中文 Han 字符 |
| 4 | 双栏 block 顺序正确、标题/章节可识别 | `TestParseBBoxLayoutAndReadingOrder`、`TestExtractTextPDFLayoutTablesFigures`；E2E page 2 左→右 |
| 5 | 表格导出 CSV，列/行一致；失败 warning | `TestExtractTablesFromPageText`；E2E CSV 含 Model/GNN |
| 6 | Figure 3 绑定图区域，可引用 figure_id | Figure 模型 + BM25 figure chunk；E2E 图片端点 200 |
| 7 | LocateQuote 对 OCR 文本返回页码/block | `TestLocateQuoteLocationReturnsBlockProvenance`；`/document/locate` |
| 8 | 质量报告 OCR 页数/置信度/失败页 | `/document/quality`；E2E 20 页 quality |

## 4. 测试证据（全部容器内）

1. **后端**（golang:1.26.1-bookworm）：
   ```
   scripts/container-go.sh "go build ./... && go vet ./... && go test ./..."
   ```
   结果：exit 0；所有包 ok（agent/api/documents/literature/planner/scheduler/webread 等）。
2. **文档真实解析**（ra-doctest:local = Go + poppler 22.12 + tesseract 5.3 eng/chi_sim）：
   ```
   go test ./internal/documents -run "TestExtract|TestDocumentCache" -v
   ```
   结果：20 页全量 OCR、5 页预算 partial、混合页、双栏/表格/图注、中文、缓存全部 PASS。
3. **前端**（node:22-bookworm-slim）：
   ```
   npm test   → 25 files / 110 tests passed
   npm run build → success（LiteraturePanel 独立 chunk）
   npm run lint  → success
   ```
4. **E2E**（node:22-bookworm-slim，compose 网络内）：
   - `test/e2e/document-intelligence.mjs`：全部检查通过；
   - `test/e2e/literature.mjs`：全部检查通过（真实 Crossref/OpenAlex/OpenAlex citations/example.com）；
   - `test/e2e/literature-plan.mjs`：literature_search 节点、librarian 分配、query 快照全部通过。
5. **回归**：`test/e2e/http-smoke.mjs` 17 项全部通过（health/openapi/plan/idempotency/export/metrics/upload dedupe/配额/超长 intent）。
6. **旧兼容**：`TestLoadLegacyV1Document`；旧计划/旧前端未改 schema 必填项。

## 5. 已知限制

- 表格/图注为确定性启发式，复杂跨页表格或矢量图可能只保留 caption + warning；不编造数据。
- 公式 OCR 未实现（非目标）。
- 网页正文不执行 JS、不登录、不绕付费墙；SPA 页面只能得到静态 HTML 文本。
- PubMed/DBLP 多数记录无 PDF，导入时按 metadata-only + `pdf_unavailable` 处理，符合设计。
- 文献库为单进程 JSON 存储，适用于当前单后端部署；多实例部署需换共享存储。
- E2E 的 20 页全量 OCR 通过 e2e overlay 设置 `PDF_OCR_MAX_PAGES=20`；生产默认仍为 5（预算语义），超预算为 `partial`。

## 6. 清理记录

- 删除临时测试镜像 `ra-doctest:local`；
- 删除临时 `research-assistant/test/Dockerfile.doctest`；
- `docker image prune -f`（无 dangling 残留）；
- 未保留临时容器/卷；E2E 上传数据留在 compose 卷中（测试用户隔离，可通过 `/api/sessions/:id` 删除；本轮脚本已删除创建的记录/计划）。

## 7. 主要变更文件

后端：
- `internal/documents/{document.go,pdf.go,layout.go,tables.go,figures.go,ocr_tsv.go,store.go,quote.go}`
- `internal/literature/{literature.go,client.go,providers.go,import.go,library.go,citations.go}`
- `internal/webread/{webread.go,netguard.go,robots.go,extract.go}`
- `internal/uploadstore/store.go`
- `internal/api/{documents.go,literature.go,web.go,routes.go,openapi.json}`
- `internal/agent/{librarian.go,literature_search.go}`
- `internal/planner/{planner.go,agent_planner.go,taskregistry.go}`
- `docker-compose.yml`、`backend.env.example`、`README.md`

前端：
- `src/contracts/api.ts`、`src/services/api/literatureApi.ts`
- `src/features/literature/LiteraturePanel.tsx`
- `src/app/components/LeftWorkspace.tsx`、`src/features/pdf-viewer/PdfPanel.tsx`

测试：
- Go 单测/集成：`internal/{documents,literature,api,agent,planner}` 下 round4 测试；
- 前端组件：`test/components/LiteraturePanel.test.tsx`；
- E2E：`test/e2e/{document-intelligence,literature,literature-plan}.mjs`；
- fixtures：`test/fixtures/{scanned-20pages,mixed-10pages,text-10pages,chinese-scan-2pages}.pdf`。
