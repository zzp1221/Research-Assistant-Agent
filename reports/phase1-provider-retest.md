# Phase 1 provider retest — api.ai-pixel.online

Re-ran the full phase 1 acceptance suite after switching the model gateway
from `api.deepseek.com` to `https://api.ai-pixel.online/v1`.

## Configuration

| Item | Value |
|---|---|
| Base URL | `https://api.ai-pixel.online/v1` |
| Model | `deepseek-v4.1-flash-1017` (response reports `deepseek-v4-1-flash`) |
| Key location | `research-assistant/backend.env` (gitignored, not in this repo) |

## Results

| Check | Result | Evidence |
|---|---|---|
| Provider probe `/v1/chat/completions` | HTTP 200, replies `PIXEL_OK` | gateway test |
| Backend `/api/chat` | HTTP 200, replies `PROVIDER_OK` | container request |
| Health | `ok=true` for backend / repository / sandbox / optimizer | `/api/health` |
| PDF extract | 15 pages, 61,239 chars | `phase1-pdf-e2e-pixel.json` |
| Assist translate | model `deepseek-v4.1-flash-1017`, 1 term | `phase1-pdf-e2e-pixel.json` |
| Assist explain | real explanation with page context | `phase1-pdf-e2e-pixel.json` |
| Full translation | 2/2 pages completed | `phase1-pdf-e2e-pixel.json` |
| Paper reproduction | 10/10 nodes, 21 artifacts | `phase1-paper-plan-pixel.json` |
| Claim evidence | 11 claims / 34 criteria, 6 verified, coverage 100% | plan artifact |
| Benchmark | 13/13 nodes, `benchmark.validation/v1` | `phase1-benchmark-pixel.json` |
| AutoResearch | 8/8 nodes, hidden holdout 3/3 | `phase1-autoresearch-pixel.json` |
| Backend tests | `go test ./...` + `-race` pass | container output |
| Frontend tests | 21 tests, build + lint pass | container output |
| Browser UI | 10/10 nodes and 10 edges rendered, no console errors | `ui/pixel-provider-plan.png` |

## Notes

- The benchmark repo was switched from `asaini/Apriori` to
  `muatik/naive-bayes-classifier` because the Apriori repository has no
  text-classification entry point for the `reviews.csv` benchmark contract;
  the adapter correctly refused it instead of faking a result.
- This container network cannot reach `github.com` directly. Repository clone
  was routed through the host HTTP proxy (`http://host.docker.internal:33210`)
  configured inside the running backend container. This is a runtime workaround,
  not a committed change.
- `POST /api/plan`, all agent calls, assist and translation used the new gateway
  and returned real model output (no mocks).
