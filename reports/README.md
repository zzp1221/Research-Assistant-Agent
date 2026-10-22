# Container E2E Reports

All product tests were run from containers on the Compose network. The host
ports `8080`/`5173` were already occupied by an unrelated project, so this run
published the backend on `127.0.0.1:18080` and the frontend on
`127.0.0.1:15173`. Service-to-service traffic still used the internal ports
(`backend:8080`, `docker-sandbox:8082`, `research-optimizer:8090`).

## Results

| Check | Result |
|---|---|
| DeepSeek `/chat/completions` probe | HTTP 200, model `deepseek-flash` |
| Backend `/api/chat` with real DeepSeek | `DEEPSEEK_CHAT_OK` |
| Backend `/api/plan` with real DeepSeek | HTTP 200, `Code_Execution`, 6 nodes |
| Legacy `/api/execute` | HTTP 410 tombstone |
| Plan-scoped task execution | HTTP 200 SSE `result`; generated code; client-supplied `/etc` paths ignored |
| Sandbox create / Python execute / delete | 200 / `SANDBOX_OK` / 200 |
| Sandbox control-container name attack | rejected (500, `invalid sandbox container id`) |
| Compose health from backend network | all `backend`, `repository`, `sandbox`, `research_optimizer` = ok |
| Frontend Nginx + API proxy | HTTP 200 for `/` and `/api/health` |
| Apriori paper reproduction | 10/10 nodes completed; 15 frequent itemsets, 5 association rules |
| rank_bm25 AutoResearch | 8/8 nodes completed; baseline 0.5556 → best 1.0; hidden holdout mean 1.0, 3/3 runs |
| Go backend/docker-core/docker-sandbox tests | pass (including `-race` for scheduler/api/store) |
| Frontend build / lint / tests | pass (Node 22 container) |
| Python optimizer tests | 8/8 pass (Python 3.12 container) |
| AutoResearch example evaluator tests | 1/1 pass |
| Paper example tests | 1/1 pass |
| Benchmark + AutoResearch Go regression tests | 28/28 pass |

## Artifacts

- `apriori-smoke.json` — complete Apriori paper plan, claim rubric, run metrics,
  comparison report, and claim-evidence graph.
- `autoresearch-rank-bm25.json` — complete rank_bm25 AutoResearch plan graph and
  trial ledgers.

## Notes

The initial Attention Is All You Need smoke attempt was canceled after it began
downloading the CUDA `torch` wheel (888 MB) plus multiple NVIDIA libraries.
That is not a lightweight reproduction, and the dependency retry left a second
pip process alive. The lightweight Apriori path was added instead: it uses the
repository's `INTEGRATED-DATASET.csv` and a bounded smoke runner, with no model
training and no large downloads.
