# Reports

本目录用于**本地/容器内**生成验收报告与 E2E 产物，默认不进入版本库。

`.gitignore` 的约定：

- 仅保留本 `README.md`；
- `reports/**` 下的 JSON、Markdown、PNG、PDF fixture 与评估报告均为本地生成物；
- 需要长期保留的结论请先脱敏，再整理到 `docs/` 或模块 README，而不是直接提交原始运行日志。

## 生成方式

E2E 脚本位于 `research-assistant/test/e2e/`，容器内默认把结果写到 `/reports`。示例：

```bash
docker run --rm --network research-assistant_research-assistant-network \
  -v "$PWD/research-assistant/test/e2e:/scripts:ro" \
  -v "$PWD/reports:/reports" \
  -e E2E_BASE=http://backend:8080 -e E2E_TOKEN=*** \
  node:22-bookworm-slim node /scripts/phase2-paper-e2e.mjs
```

## 当前本地报告

- `reports/eval-2026-10-23/`：评分卡、Apriori/AutoResearch/PDF/Benchmark 证据与安全探针结果。
- `reports/eval-2026-10-25/`：Louvain 论文四场景回归与缺陷根因分析。
- `reports/eval-2026-10-28/`：2026-10-28 大规模测试（`REPORT.md`、`summary.json`、round7 脚本日志、绘图/论文/Apriori/rank_bm25/Playwright 证据与清理记录）。

以上目录均被 `.gitignore` 忽略，不随仓库提交。
