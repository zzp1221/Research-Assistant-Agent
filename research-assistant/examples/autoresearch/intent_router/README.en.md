# Intent Router AutoResearch example

[简体中文](README.md) | English

Validates the AutoResearch harness itself without GPU, network, or third-party Python packages.

## File roles

| File | Role | Editable by AutoResearch |
|---|---|---|
| `candidate.py` | Six-class intent-router candidate | Yes |
| `evaluator.py` | Metric calculation and output contract | No; SHA-256 frozen before running |
| `benchmark.json` | 26 fixed evaluation samples | No; SHA-256 frozen before running |
| `autoresearch.json` | File scope, commands, metrics, budget | No; added to the protected set after loading |

## Run the baseline directly

From `research-assistant/`:

```bash
python3 examples/autoresearch/intent_router/evaluator.py
# or
make example-autoresearch
```

The last line must be JSON and contain at least:

```json
{"metrics":{"accuracy":0.0,"macro_f1":0.0,"p95_latency_ms":0.0},"status":"ok"}
```

The harness reads `metrics.macro_f1` from the last valid JSON line; it never guesses metrics from natural-language logs.

## Run through the product API

```text
Run AutoResearch on https://github.com/OWNER/REPOSITORY using
examples/autoresearch/intent_router/autoresearch.json,
at most 3 experiments, 5 minutes wall-clock.
```

Success criteria:

1. The planner generates the fixed eight-node AutoResearch DAG.
2. The baseline runs before any candidate.
3. A candidate is kept only if the metric improves by at least `0.001`; otherwise it is rejected and the previous best files are restored.
4. `research_trial_ledger` records each hypothesis, patch hash, command result, metric, and decision.
5. With `validation_runs=3`, three independent guard/evaluator processes run; each must match the ledger's best metric before a successful `research_validation_report` is produced.
6. Search and validation artifacts summarize command counts, successes/failures, cumulative command time, and wall-clock time.

## Scope

This is not a full BERT or Qwen training experiment; it is a low-cost harness acceptance sample. The same `autoresearch.spec/v1` can point editable files and evaluation commands at a real training script and run in a GPU sandbox.
