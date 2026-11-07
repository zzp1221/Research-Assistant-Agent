# AutoResearch examples

[简体中文](README.md) | English

This directory demonstrates budget-constrained automated research: with frozen evaluators, data, and file scope, an agent generates candidates, executes them for real, applies Keep/Reject by metric, and finally validates with independent processes or a hidden holdout.

## Examples

| Directory | Description | Runtime |
|---|---|---|
| [intent_router](intent_router/) | Code-patch search for a six-class intent router; validates the harness itself | CPU, Python stdlib |
| [real_repositories](real_repositories/) | Real-repository task packages for rank-bm25, Tenacity, LightRAG, GraphRAG, and Mem0 | Remote Docker CPU |

## Common protocol

Every task package contains at least:

- `autoresearch.json`: editable files, protected files, commands, metrics, budget, and validation runs.
- `evaluator.py`: public search evaluator; SHA-256 frozen before running.
- `holdout_evaluator.py`: model-hidden final evaluator (optional; `run_campaign.py` currently requires it).

Search flow:

```text
Freeze ResearchSpec
  -> run baseline
  -> generate candidate (code patch or parameter config)
  -> real evaluator
  -> Keep / Reject / rollback
  -> reach target or exhaust budget
  -> independent validation / hidden holdout
```

## Runner

`run_campaign.py` uploads a task package through the product API and creates an eight-node AutoResearch plan:

```bash
python3 examples/autoresearch/run_campaign.py \
  --repository https://github.com/OWNER/REPOSITORY.git \
  --package examples/autoresearch/real_repositories/lightrag \
  --base-url http://localhost:8080 \
  --max-trials 8 \
  --wall-minutes 5 \
  --validation-runs 3 \
  --output /tmp/lightrag_run.json
```

The script waits for a terminal state and prints a plan summary including revision, search statistics, and validation statistics. `--output` is required.

## Boundaries

- These examples validate the execution chain and harness behavior, not upstream projects' full benchmarks.
- Search scores represent only the best observed result within the given candidate space and budget.
- Public-evaluator replay is not hidden validation; only a model-hidden holdout may be marked `hidden_holdout`.
