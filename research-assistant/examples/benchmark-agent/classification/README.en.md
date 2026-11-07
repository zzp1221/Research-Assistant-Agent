# Benchmark Agent classification example

[简体中文](README.md) | English

A small-sample validation of the standalone Benchmark Agent; it does not prove any model's quality.

Input is 24 classification records with `review` and `label`. The system performs:

```text
dataset audit
  -> stratified_hash split
  -> materialize train / validation / test_features
  -> leakage check
  -> macro_f1 metric contract
  -> candidate_priority_only reward contract
  -> backend metric recomputation on hidden labels
```

`test_features.jsonl` contains no `label`. Hidden labels and the hidden evaluator live outside the repository workspace; the Repository Adapter may output only `id + prediction`, and the Go backend recomputes final `macro_f1` and `accuracy`.

## Run

```bash
cd research-assistant/backend
go test ./internal/agent -run TestBenchmarkAgentBuildsLeakageSafeContractAndRecomputesHiddenMetrics -v
```

The test checks:

- all three splits are non-empty;
- normalized inputs never cross splits;
- the public test file contains no labels;
- no hidden-label file exists in the workspace;
- prediction IDs correspond one-to-one with hidden labels;
- final metrics come from backend recomputation, not adapter self-reporting.

See `expected_summary.json` for the expected contract summary.

## Files

| File | Description |
|---|---|
| `reviews.csv` | 24 labeled classification samples |
| `expected_summary.json` | Expected contract summary |
| `README.md` | This document |
