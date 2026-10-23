# Phase 2 provider retest — api.ai-pixel.online

Phase 2 完成了数据驱动 catalog、前端组件测试和 Playwright E2E，并用指定的
DeepSeek 网关重新跑了一轮真实模型验收。测试全部在容器内执行，API key 只存在于
gitignored 的 `research-assistant/backend.env`，未写入仓库。

## Configuration

| Item | Value |
|---|---|
| Base URL | `https://api.ai-pixel.online/v1` |
| Model | `deepseek-v4.1-flash-1017` |
| Backend | `research-assistant-backend-1` (rebuilt from current source) |
| Frontend | `research-assistant-frontend-1` (rebuilt from current source) |
| Provider probe | `POST /api/chat` → `PROVIDER_OK` (3.7s) |

## Results

| Check | Result | Evidence |
|---|---|---|
| Backend full suite | `go test ./...` pass | container output |
| Backend race | `go test -race` on scheduler/api/store/documents/assist/catalog pass | container output |
| Backend vet / gofmt | `go vet ./...` clean, `gofmt -l` empty | container output |
| Frontend parser tests | 21 pass | `npm run test:unit` |
| Frontend component tests | 5 files / 11 tests pass | `npm run test:components` |
| Frontend lint/build | ESLint clean; Vite production build OK | container output |
| Playwright E2E | 3/3 pass (app, PDF, benchmark report) | rebuilt frontend |
| Provider chat | `PROVIDER_OK` | container request |
| PDF E2E | 8 steps: 15 pages / 61,239 chars, translate, explain, 2/2 translation pages | `phase2-pdf-e2e.json` |
| Paper E2E | Apriori 10/10, 21 artifacts | `phase2-paper-plan.json` |
| Claim/evidence | 25,181 chars; comparison report 5,478 chars | paper artifacts |
| Benchmark fresh retest | adapter generation blocked by gateway HTTP 504 twice | `phase2-benchmark-gateway-504.json` |
| Benchmark prior evidence | 13/13 nodes, 46 artifacts, `benchmark.validation/v1` | `phase1-benchmark-pixel.json` |
| AutoResearch fresh retest | spec/evaluator upload fixed; dependency install blocked by PyPI mirror stall | `phase2-autoresearch-dependency-blocked.json` |
| AutoResearch prior evidence | 8/8 nodes, 20 artifacts, hidden holdout 3/3 | `phase1-autoresearch-pixel.json` |

## Benchmark fix found during the retest

The fresh benchmark run exposed a real integrity-check bug: running the adapter
created Python bytecode under `__pycache__`, and the repository fingerprint counted
those generated files as repository edits, failing with:

`repository files changed outside .research-assistant during benchmark execution`.

Fix:

- benchmark harness now runs with `PYTHONDONTWRITEBYTECODE=1`;
- repository fingerprint ignores VCS/cache directories, `__pycache__`, `*.pyc`,
  `*.pyo`, and the repository metadata marker;
- regression test `TestBenchmarkRepositoryFingerprintIgnoresGeneratedMetadata`
  proves generated metadata does not change the fingerprint while a real source
  edit still does;
- adapter repository context was bounded to 32 KB after observing gateway 504s on
  the larger prompt.

The remaining benchmark retest failure is external: the gateway returned HTTP 504
during adapter code generation. The phase-1 benchmark run with the same gateway and
model completed 13/13, and the changed fingerprint logic is covered by unit tests.

## AutoResearch retest note

The original E2E script did not upload `autoresearch.json` or the two evaluator
files, so the spec loader correctly failed. The script now uploads all three in
order; the plan froze the spec and prepared the runtime. The fresh run was stopped
at dependency installation because the configured PyPI mirror stalled while
downloading `numpy==2.1.3`. The phase-1 AutoResearch run with the same model
completed 8/8 with hidden holdout 3/3.

## Commands

```bash
# Backend
docker run --rm -v "$PWD/research-assistant:/workspace/research-assistant:ro" \
  -v research-assistant-go-mod:/go/pkg/mod \
  -w /workspace/research-assistant/backend golang:1.26.1-bookworm \
  sh -c 'go test ./... && go vet ./...'

# Frontend
docker run --rm -v "$PWD/research-assistant/frontend:/app" -w /app \
  node:22-bookworm-slim sh -c 'npm run test && npm run lint && npm run build'

# Playwright (compose network)
docker exec -e E2E_BASE_URL=http://research-assistant-frontend-1 \
  -e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright ra-e2e-runner \
  npx playwright test --reporter=list
```

## Operational note

Repeated plan E2E runs grew the persisted volume `/app/data/plans.json` to about
1 GB. This is persisted test data in the Docker volume, not a source artifact;
the API and scheduler continued to operate normally.
