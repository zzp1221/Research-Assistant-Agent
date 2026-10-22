# Phase 1 E2E — PDF、Benchmark 与论文/自动研究

本目录新增一期验收证据。所有服务与测试容器都运行在
`research-assistant_research-assistant-network` 内，宿主机只通过
`127.0.0.1:18080`（backend）和 `127.0.0.1:15173`（frontend）访问，不执行本机项目依赖。

## 环境

| 项目 | 值 |
|---|---|
| 模型 | DeepSeek `deepseek-flash`（`OPENAI_BASE_URL=https://api.deepseek.com`） |
| 后端镜像 | `research-assistant-backend`（Debian bookworm + poppler-utils） |
| 前端镜像 | `research-assistant-frontend`（Nginx + 本地 pdfjs worker/cMaps/fonts） |
| 沙箱 | `docker-sandbox` gateway + 内部 executor，native Docker |
| 健康检查 | `backend.ok=true`、`repository.ok=true`、`sandbox.ok=true`、`research_optimizer.ok=true` |

## 结果

| 检查 | 结果 | 证据 |
|---|---|---|
| 真实 DeepSeek `/api/chat` | HTTP 200，返回 `DEEPSEEK_CHAT_OK` | 容器内请求 |
| PDF 上传与文本提取 | 15 页、61,239 字符、`pdftotext` | `phase1-pdf-e2e.json` |
| 划词翻译 | 真实 DeepSeek，返回中文译文与术语 | `phase1-pdf-e2e.json` |
| 划词解释 | 真实 DeepSeek，返回方法解释 | `phase1-pdf-e2e.json` |
| 全文翻译任务 | 2/2 页完成，逐页译文可读 | `phase1-pdf-e2e.json` |
| 本地 pdfjs 资源 | `/pdfjs/pdf.worker.min.js` 200，`/pdfjs/cmaps/*.bcmap` 200 | Nginx 静态资源探测 |
| 论文复现计划 | 10/10 节点完成，Apriori smoke | `phase1-paper-plan.json` |
| 论文 Artifact | `run_metrics`、`comparison_report`、`claim_evidence_graph` | `phase1-paper-plan.json` |
| Benchmark 计划 | 13/13 节点完成，`benchmark.validation/v1` | `phase1-benchmark.json` |
| AutoResearch | 8/8 节点完成，hidden holdout 3/3 通过 | `phase1-autoresearch.json` |
| 后端测试 | `go test ./...` 全通过；`-race` 覆盖 scheduler/api/store/documents/assist | 容器输出 |
| 前端测试 | 21 个测试通过；build/lint 通过 | 容器输出 |
| 浏览器 UI | 首页、PDF 面板、10 节点 DAG、全文翻译面板可在 headless Chrome 正常渲染 | `ui/home.png`、`ui/pdf-translation.png`、`ui/plan-graph.png` |

## 复现命令

```bash
# 1. 启动容器（使用 research-assistant/.env 与 backend.env）
cd research-assistant
docker compose up -d --build

# 2. PDF、划词与全文翻译
docker run --rm --network research-assistant_research-assistant-network \
  -e E2E_BASE=http://backend:8080 -e E2E_TOKEN=$API_AUTH_TOKEN \
  -v "$PWD/../reports/fixtures:/fixtures:ro" -v "$PWD/../reports:/reports" \
  -v "$PWD/test/e2e:/e2e:ro" \
  node:22-bookworm-slim sh -c "cd /tmp && cp /e2e/phase1-pdf-e2e.mjs . && node phase1-pdf-e2e.mjs"
```

## 备注

- 论文 smoke 选择 Apriori（`asaini/Apriori`），不跑 WMT14 或大规模训练；报告中明确标注 smoke 边界。
- Benchmark 使用仓库自带 `reviews.csv`（24 行），显式指定仓库 `https://github.com/asaini/Apriori`，避免联网仓库检索成为失败点。
- 全文翻译按 40 页/任务上限分页执行；DeepSeek 余额耗尽时任务会标为 `failed`/`partial`，不会伪报成功。
- 全部运行报告不含 API Key；`backend.env` / `.env` 均被 gitignore。
- PDF fixture 约 2.2 MB，已 gitignore；可用 `https://arxiv.org/pdf/1706.03762` 重新下载。
