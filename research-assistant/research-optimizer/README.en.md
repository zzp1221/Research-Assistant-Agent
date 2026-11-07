# research-optimizer

[简体中文](README.md) | English

Python research optimizer that provides data features, candidate ranking, and cross-task experience to the Go Harness. It does not execute experiments or decide scientific acceptance.

## Position in the system

- [System architecture](../../docs/diagrams/architecture.drawio) ([SVG preview](../../docs/diagrams/architecture.svg))

Research Optimizer is the candidate-ranking advisor to the Go Harness: the Go side freezes ResearchSpec, executes candidates, performs Keep/Reject, and validates on a holdout; the Python side only ranks candidates from normalized samples and historical experience and never touches the experiment workspace.

## Responsibilities

- Extract bounded features from frozen data (`POST /v1/profile`, schema `experiment.features/v1`).
- Select the next candidate from current-route statistics and historical experience (`POST /v1/select`).
- Record candidate outcomes and validation state (`POST /v1/experience/outcome`, `POST /v1/experience/validation`).
- Persist experience in SQLite and use only validated campaigns as cross-task priors.

## Search strategy

Current policy version: `hierarchical-contextual-ucb-uct/v2`

1. Go runs every legal model combination once with default configuration (`bounded-exhaustive/v1`).
2. Route UCB allocates budget across model parameter trees.
3. Top-K Beam plus an exploration channel forms the active frontier.
4. UCT-style scores choose parent paths within a tree.
5. Virtual visits prevent concurrent agents from selecting the same route.
6. Real validation reward updates scheduling statistics; candidate scores and root default scores remain unchanged.

This policy has no simulated rollout or value network and is not full MCTS; continuous Bayesian optimization and Hyperband require separate protocols, and discrete enumeration is never presented as them.

## API

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/profile` | Build dataset features |
| `POST` | `/v1/select` | Select the next candidate |
| `POST` | `/v1/experience/outcome` | Record candidate outcome |
| `POST` | `/v1/experience/validation` | Record validation state |
| `GET` | `/v1/stats` | Experience-store statistics |
| `GET` | `/health` | Health check |

`/health` needs no auth; with `RESEARCH_OPTIMIZER_API_TOKEN` set, all other endpoints require a Bearer token. Without a token, the service treats the deployment as unauthenticated local development.

## Run

```bash
RESEARCH_OPTIMIZER_API_TOKEN=local-optimizer-token \
RESEARCH_OPTIMIZER_DB_PATH=/tmp/research-assistant-experiences.sqlite3 \
python3 -m research_optimizer.service
```

Or `make run-optimizer` (writes SQLite to `/tmp/research-assistant-experiences.sqlite3` by default). The listen port is controlled by `RESEARCH_OPTIMIZER_PORT` (default 8090).

## Tests

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -p 'test_*.py'
```

All 8 container tests pass; `/health`, `/v1/stats` auth, and the `/v1/profile` contract probe pass. See [docs/verification.md](../../docs/verification.md) for the latest results.

## Files

| File | Responsibility |
|---|---|
| `research_optimizer/policy.py` | UCB/UCT/Beam candidate selection |
| `research_optimizer/profiling.py` | Data feature extraction and fingerprints |
| `research_optimizer/store.py` | SQLite experience store |
| `research_optimizer/service.py` | HTTP service and routes |
| `tests/` | Policy, service, and store tests |

## Boundaries

The optimizer only emits candidate-priority advice. The Go Harness retains candidate legality, budget, evaluator execution, Keep/Reject, rollback, and holdout acceptance. The service mounts no experiment workspace and receives only normalized samples validated by Go.
