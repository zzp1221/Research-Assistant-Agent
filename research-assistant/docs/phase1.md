# Phase 1 — PDF 阅读、划词翻译与结果可读性

一期目标：把"能看的 PDF"升级为"能读、能问、能翻、能追踪"的论文工作台，并把节点结果与 Benchmark 报告从原始 JSON 提升为可读界面。

## 范围

1. PDF 本地化与文本提取
   - 本地 pdfjs worker、cMaps、standard fonts，离线可用，不依赖 unpkg。
   - 上传 PDF 时用 poppler `pdftotext -layout` 提取分页文本，落盘为 `document.json`（0600）。
   - `GET /api/uploads/:id/document` 返回页数与文本状态；`GET /api/uploads/:id/document/pages/:page` 返回单页文本。
2. 真实划词翻译/解释
   - `POST /api/uploads/:id/assist`，带页码与相邻页上下文。
   - 独立 LLM 调用，无工具、无代码执行，文档内容视为不可信数据。
   - 结果按 文档哈希 + 页码 + 模式 + prompt 版本 + 模型 缓存。
3. 全文翻译后台任务
   - `POST /api/uploads/:id/translation-jobs` 启动按页任务；`GET .../translation-jobs/:jobId` 轮询进度。
   - 3 并发分页翻译，逐页缓存；支持取消、失败页重试、重启后标记 interrupted。
4. 节点结果持久化与回填
   - 失败节点保留 partial result/code/structured_data。
   - 前端从 `GET /api/plans/:id/events` 回填日志与结果，刷新/切 session 不丢信息。
5. Benchmark 可读报告
   - `benchmark.validation/v1` / run / audit 三类报告解析为指标表、目标状态、checks 与折叠原始 JSON。
   - 自动为 benchmark 节点显示"评测报告"页签。
6. 工程化
   - 新增单测：documents、assist、Document API、scheduler partial result、Benchmark parser。
   - 所有测试与 E2E 在容器内执行。

## 非目标

- 扫描件 OCR（只明确返回 `text_extractable=false`）。
- 全文翻译的术语库、人工校对与导出。
- 多租户鉴权强化（沿用现有静态 token + 用户头）。
- 完整重写硬编码论文清理（保留到二期）。

## 验收

- 后端 `go test ./...`、`go test -race` 关键包在容器内通过。
- 前端 `npm run build`、`npm run lint`、`npm test` 在容器内通过。
- `docker compose up -d --build` 健康检查 `ok=true`。
- 真实 DeepSeek：`/api/chat`、`/api/plan`、`/assist`、全文翻译任务全部返回真实结果。
- PDF E2E：上传 15 页 arXiv 论文 → 提取 → 划词翻译/解释 → 2 页全文翻译完成。
- 论文 E2E：Apriori smoke 复现 10/10 节点完成，产出 run_metrics、comparison_report、claim_evidence_graph。
- Benchmark E2E：上传评论数据集 + 指定仓库，13/13 节点完成，产出 `benchmark.validation/v1` 报告。
- AutoResearch E2E：rank_bm25 8/8 节点完成，hidden holdout 3/3 通过。
