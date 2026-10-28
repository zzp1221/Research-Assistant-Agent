# 任务清单：N-04 真实文献与网页检索 + N-07 文档智能（2026-10-27 第四轮）

## P0 基线与规格
- [x] 复核设计文档与实际代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 go build/vet/test + 前端 test/build/lint

## P1 N-07 文档模型与逐页解析
- [x] document.go 扩展（Block/Table/Figure/PageQuality/OCRStats/StatusPartial）
- [x] layout.go（bbox XML、栏检测、reading order、页眉页脚、标题）
- [x] pdf.go（pdfinfo、逐页 needs_ocr、OCR 预算、TSV 置信度、缓存、partial）
- [x] tables.go（行对齐表格、caption、CSV、置信度）
- [x] figures.go（图注、pdfimages、Figure）
- [x] store.go/bm25.go（Chunk Kind、table/figure chunks、LocateQuote block）
- [x] 单测：模型/旧版兼容/bbox/栏序/预算/TSV/表格/图注/缓存
- [x] 真实 PDF 集成测试（20 页扫描/5 页预算/混合/双栏/表格/图注/中文/缓存）

## P2 N-07 API 与前端
- [x] documents API：quality/layout/tables/figures/CSV/locate
- [x] API 集成测试
- [x] 前端类型 + documentApi
- [x] PDF 质量 badge/低置信度/表格图注面板
- [x] 组件测试

## P3 N-04 Provider/排序/文献库/网页/引用
- [x] Result/SearchOptions 排序与 rank
- [x] S2/PubMed/DBLP + 429 退避
- [x] Unpaywall + metadata 兜底
- [x] literature library（Record/Store/Upsert/BibTeX/Attach/FindByDOI）
- [x] citations（OpenAlex）
- [x] webread（SSRF/robots/readability/缓存）
- [x] API 路由 + 单测/集成

## P4 计划节点 literature_search
- [x] taskregistry 注册
- [x] 模板 planner 触发
- [x] agent planner 允许
- [x] librarian executeLiteratureSearch + 落盘 + missing requirements
- [x] 单测

## P5 前端 LiteraturePanel
- [x] contracts + API client
- [x] LiteraturePanel（搜索/筛选/排序/导入/文献库/BibTeX/引用/网页）
- [x] 入口接线（左侧“文献”标签）
- [x] 组件测试 + build + lint

## P6 容器 E2E、回归、清理
- [x] 重建镜像 + compose 起栈
- [x] document-intelligence E2E（20 页扫描/混合/表格/图注/中文）
- [x] literature E2E（真实 Crossref/OpenAlex、records、BibTeX、citations、web fetch、SSRF）
- [x] literature-plan E2E
- [x] http-smoke 回归
- [x] 清理临时测试镜像/文件
- [x] README/OpenAPI/FIX-REPORT-ROUND4/spec/plan/tasks 更新
