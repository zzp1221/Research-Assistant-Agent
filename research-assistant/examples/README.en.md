# examples

[简体中文](README.md) | English

Runnable examples with explicit acceptance criteria. Each example documents its operating boundary, success conditions, and acceptance method; they validate the product execution chain and are not full paper-training reproductions.

## Architecture diagrams

- [System architecture](../../docs/diagrams/architecture.drawio) ([SVG preview](../../docs/diagrams/architecture.svg))
- [Runtime flow](../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../docs/diagrams/runtime-flow.svg))
- [Agent execution flow](../../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../../docs/diagrams/agent-execution-flow.svg))

Write example JSON/screenshots to local `reports/` (not committed by default).

| Example | Flow | Runtime | Status |
|---|---|---|---|
| [Attention paper reproduction](paper-reproduction/) | API -> Planner -> Scheduler -> Agents -> Docker Sandbox -> Artifacts | CPU smoke; optional GPU passthrough | Runnable |
| [Intent Router AutoResearch](autoresearch/intent_router/) | ResearchSpec -> baseline -> candidate -> Keep/Reject -> independent validation | CPU, Python stdlib | Runnable |
| [Real-repository AutoResearch](autoresearch/real_repositories/) | Upload -> exact Git revision -> repeated search -> Coding Agent -> hidden validation | Remote Docker CPU | Task packages runnable |
| [Benchmark Agent classification](benchmark-agent/classification/) | Dataset audit -> leakage-safe split -> metric/reward contract -> hidden recomputation | Go test, no external deps | Covered by Go test |
| [Retrieval adapter](scientific-autoresearch/retrieval/) | experiment.spec/v1 -> candidate search -> holdout | CPU, Python deps | Runnable |

See [docs/verification.md](../../docs/verification.md) for offline tests and recent real-repository AutoResearch results; the offline tests for `paper-reproduction` and `autoresearch/intent_router` do not access external services by default.

## Common prerequisites

- Backend and sandbox are running per [research-assistant/README.md](../README.md).
- A usable LLM is configured in `backend.env`.
- Examples that need Docker require an accessible Docker daemon; scripts that call the API use `RA_API_URL` / `RA_API_TOKEN` (or `RESEARCH_ASSISTANT_API_TOKEN`).

## Running

```bash
cd research-assistant
make example-paper-reproduction   # Requires a running stack
make example-autoresearch         # Runs the intent_router baseline offline
```

See each example directory's README for parameters and acceptance criteria.
