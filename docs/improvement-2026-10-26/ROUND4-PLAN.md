# 实施计划：N-04 真实文献与网页检索 + N-07 文档智能（2026-10-27 第四轮）

- 基线：`master @ 0f605c2`（设计稿提交）+ 第三轮未提交成果（运行期模型配置与视觉校验，容器测试已通过）
- 工作分支：`feature/literature-document-intelligence-2026-10-27`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / compose 栈）
- 完成定义：`spec.md` 第 8 节；每阶段结束跑容器内构建/单测，最后统一 E2E。
- 状态：全部阶段完成（2026-10-27）；容器证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND4.md`。

## 阶段 P0 — 基线与规格
1. 复核实际代码与设计文档差异，写 `spec.md` / `plan.md` / `tasks.md`（本文件）。
2. 容器内基线：`go build ./...`、`go vet ./...`、`go test ./...`、前端 `npm test/build/lint`。
3. 归档第三轮结果（提交或保留工作区），确保本轮 diff 可审计。

## 阶段 P1 — N-07 文档模型与逐页解析
1. `documents/document.go`：Block/Table/Figure/PageQuality/OCRStats；Document 扩展；StatusPartial；Save 资源落盘；Load v1 兼容。
2. `documents/layout.go`：`pdftotext -bbox-layout` XML 解析；block/line/word；栏检测与 reading order；页眉/页脚/标题。
3. `documents/pdf.go`：pdfinfo 页数；逐页 needs_ocr；OCR 预算；`tesseract tsv` 置信度；默认 eng+chi_sim；缓存；partial 状态。
4. `documents/tables.go`：文本行对齐表格、caption、CSV、置信度。
5. `documents/figures.go`：图注识别、pdfimages 导出、Figure 绑定。
6. `documents/store.go/bm25.go`：Chunk Kind/TableID/FigureID；table/figure chunks；LocateQuote block 偏移。
7. 单测：模型序列化/旧版兼容、bbox 解析、栏序、页眉页脚、needs_ocr/预算/partial、TSV 解析、表格、图注、CSV、缓存。

## 阶段 P2 — N-07 API 与前端展示
1. `api/documents.go`：摘要扩展、quality/layout/tables/figures 端点、CSV 下载、图片响应。
2. `api/documents_test.go`：端点契约、owner 隔离、404、CSV content-type。
3. 前端 `contracts/api.ts`、`documentApi.ts`：类型与请求。
4. `features/pdf-viewer`：质量 badge、低置信度提示、表格/图注面板。
5. 组件测试。

## 阶段 P3 — N-04 Provider、排序、文献库、网页、引用
1. `literature/literature.go`：Result 扩展、SearchOptions.Sort、排序/rank 实现。
2. `literature/providers.go`：S2/PubMed/DBLP 搜索；429/503 Retry-After + 退避；字段映射。
3. `literature/import.go`：Unpaywall OA 解析；metadata 兜底。
4. `literature/library.go`：Record/Store/Upsert/List/Get/Delete/Tags/Attach/BibTeX。
5. `literature/citations.go`：OpenAlex references/cited_by。
6. `webread/`：SSRF、robots、readability、缓存、链接。
7. `api/literature.go`：records/citations 路由、import 落库、search sort。
8. `api/web.go`：POST /api/web/fetch。
9. 单测 + httptest 集成。

## 阶段 P4 — 计划节点 literature_search
1. `planner/taskregistry.go`：注册 literature_search。
2. `planner/planner.go`：General 发现类意图模板。
3. `planner/agent_planner.go`：允许/提示。
4. `agent/librarian.go`：executeLiteratureSearch；导入 PDF 落盘（复用 uploads 逻辑）；structured data + missing requirements。
5. 单测：触发条件、registry 校验、librarian 执行（httptest provider + 临时目录）。

## 阶段 P5 — 前端 LiteraturePanel
1. `contracts/api.ts` + `services/api/literatureApi.ts` / `webApi.ts`。
2. `features/literature/LiteraturePanel.tsx`：搜索/筛选/排序/结果卡/导入/文献库/标签/BibTeX/引用。
3. 入口：ChatPanel 顶部“文献”标签或工作区切换。
4. 组件测试 + build + lint。

## 阶段 P6 — 容器 E2E、回归、清理
1. 重建 backend/frontend 镜像；compose 起栈。
2. E2E 脚本：
   - `test/e2e/literature.mjs`：真实 search（Crossref/OpenAlex）、records、BibTeX、web fetch；
   - `test/e2e/document-intelligence.mjs`：扫描/混合 PDF、quality/layout/tables/figures；
   - `test/e2e/literature-plan.mjs`：planner literature_search 节点。
3. 全量回归：后端 go test、前端 test/build/lint、http-smoke。
4. 清理临时容器/镜像/卷/文件；更新 README/OpenAPI/FIX-REPORT-ROUND4/spec/plan/tasks。

## 风险与对策
- poppler bbox XML 版本差异：解析失败不阻断，回退纯文本 + 无 blocks。
- Tesseract TSV 置信度：不同版本字段一致；解析容错，异常时 confidence=0。
- OCR 慢：预算 + 缓存 + 每页超时；E2E 用小页数样本。
- 真实 provider 限流：E2E 允许 warning；确定性测试用 httptest。
- 文献库并发：单进程 mutex + 原子写；owner 目录隔离。
- 前端体积：LiteraturePanel lazy 加载。
- 旧数据兼容：Document v1 字段零值；不改变 plan schema 既有字段。
