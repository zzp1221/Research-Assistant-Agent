# Phase 2 — 数据驱动论文目录与前端回归

二期目标：把论文/仓库的硬编码特判从 Go 代码移到可审计的数据目录，并补上组件级与浏览器级回归测试。

## 范围

1. 数据驱动 catalog
   - 新增 `backend/internal/catalog`，embed `repositories.json`、`recipes.json` 和 `templates/*.py`。
   - `repo_discovery` 通过 `catalog.MatchRepositories` 读取 curated 仓库；回退结果仍标记为 `curated_fallback`。
   - `repo_prepare` 通过 smoke recipe 选择模板；Apriori、Attention 消融和通用 Transformer forward 都是数据条目，不再有 Go 分支。
   - 删除 `extractPaperTitle` / `extractPaperMethodName` 硬编码，以及 GitHub 回退查询里硬编码的 `title + " transformer"`。
   - 前端推荐指令不再硬编码 "Attention Is All You Need"。
2. 组件测试
   - Vitest + jsdom + Testing Library。
   - 覆盖 PDF 本地 worker/全文翻译面板、Benchmark 报告、ExecutionSidebar、ChatComposer。
3. 浏览器 E2E
   - Playwright + Chromium；覆盖首页、PDF 上传/打开/全文翻译、Benchmark 报告页。
   - E2E 通过 route mock 提供确定性数据，不依赖真实 LLM。
4. 回归修复
   - Benchmark adapter 执行时设置 `PYTHONDONTWRITEBYTECODE=1`。
   - repository fingerprint 忽略 `__pycache__`、`*.pyc`、VCS/缓存目录和 repository marker，避免把解释器产物误判为仓库被修改。
   - 增加 fingerprint 回归测试。

## 运行命令

```bash
# 后端
docker run --rm -v "$PWD:/workspace/research-assistant:ro" \
  -v research-assistant-go-mod:/go/pkg/mod \
  -w /workspace/research-assistant/backend golang:1.26.1-bookworm go test ./...

# 前端
docker run --rm -v "$PWD/frontend:/app" -w /app node:22-bookworm-slim npm run test
docker run --rm -v "$PWD/frontend:/app" -w /app node:22-bookworm-slim npm run lint
docker run --rm -v "$PWD/frontend:/app" -w /app node:22-bookworm-slim npm run build

# Playwright（compose 网络内）
docker run --rm --network research-assistant_research-assistant-network \
  -e E2E_BASE_URL=http://research-assistant-frontend-1 \
  -v "$PWD/frontend:/app" -w /app ra-e2e-runner npm run test:e2e
```

## 验收证据

- 后端：`go test ./...`、`go test -race` 关键包、`go vet ./...` 通过。
- 前端：21 个 Node 解析测试 + 11 个组件测试通过；lint/build 通过。
- Playwright：3/3 通过。
- 真实 provider：`https://api.ai-pixel.online/v1` + `deepseek-v4.1-flash-1017`，`/api/chat` 返回 `PROVIDER_OK`。
- PDF E2E：15 页提取、划词翻译/解释、2/2 页全文翻译通过。
- 论文 E2E：Apriori smoke 10/10，21 个 artifact，run_metrics/comparison_report/claim_evidence_graph 产出。
- Benchmark：phase 1 同 provider 13/13、46 artifacts、`benchmark.validation/v1`；
  二期 fresh retest 在 adapter 生成处遇到网关 HTTP 504，见
  `reports/phase2-benchmark-gateway-504.json`。本次 retest 暴露并修复了
  `__pycache__` 被 fingerprint 误判的问题，新增回归测试。
- AutoResearch：phase 1 同 provider 8/8、20 artifacts、hidden holdout 3/3；
  二期 fresh retest 修复了 spec/evaluator 上传，之后在 PyPI 镜像下载依赖时停滞，
  见 `reports/phase2-autoresearch-dependency-blocked.json`。
- 报告：`reports/phase2-provider-retest.md`、`reports/phase2-paper-plan.json`、
  `reports/phase2-pdf-e2e.json`。
