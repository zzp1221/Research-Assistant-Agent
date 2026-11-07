# Real-repository AutoResearch task packages

[简体中文](README.md) | English

This directory contains six bounded AutoResearch task packages for real open-source repositories. Each records an upstream commit, a public search evaluator, and (for most packages) a model-hidden holdout, validating the "real repository + real execution + hidden acceptance" chain.

## Packages

| Target | Repository | Package |
|---|---|---|
| rank-bm25 | https://github.com/dorianbrown/rank_bm25 | `rank_bm25/` |
| Tenacity | https://github.com/jd/tenacity | `tenacity/` |
| LightRAG | https://github.com/HKUDS/LightRAG | `lightrag/` |
| Microsoft GraphRAG | https://github.com/microsoft/graphrag | `graphrag/`, `graphrag_v2/` |
| Mem0 | https://github.com/mem0ai/mem0 | `mem0/` |

Each package contains:

```text
<target>/
├── autoresearch.json       # editable/protected scope, commands, metrics, budget
├── evaluator.py            # public search contract
└── holdout_evaluator.py    # model-hidden final contract (mem0 currently has evaluator only)
```

`mem0`'s `autoresearch.json` currently has no `holdout_command`, so it cannot be used for hidden-holdout acceptance; use a package with `holdout_evaluator.py` when hidden validation is required.

## Frozen fields

The current specification also freezes:

- `repository_revision`: a 40-character upstream commit.
- `search_runs`: evaluator repetitions for the baseline and each candidate.
- `search_aggregation`: `mean`, `median`, or direction-aware `worst`.
- `target_score`: deterministic stop after a kept candidate reaches the target.
- `validation_runs`: number of independent validation processes after search.

## Run

```bash
python3 examples/autoresearch/run_campaign.py \
  --repository https://github.com/HKUDS/LightRAG.git \
  --package examples/autoresearch/real_repositories/lightrag \
  --output /tmp/lightrag_run.json \
  --base-url http://localhost:8080 \
  --max-trials 8 \
  --wall-minutes 5 \
  --validation-runs 3
```

The runner requires `autoresearch.json`, `evaluator.py`, and `holdout_evaluator.py` in the package; it uploads all three, creates an eight-node AutoResearch plan, waits for terminal state, and saves the full `plan_graph`. The console summary includes requested revision, actual revision, acquisition method, target stop, search statistics, and hidden-validation statistics.

## Acceptance criteria

- A completed plan is not automatically a pass: the final report must satisfy pre-declared hidden thresholds and integrity checks.
- Public search scores only rank candidates; they never replace the holdout.
- Results must record the actual commit; if the revision cannot be pinned, the report must say so explicitly.
- Failure records must be retained too; do not show only successful samples.

## Note

These are module-level engineering experiments, not upstream projects' full benchmarks. Scores are fixed-case ratios from package evaluators and do not represent general model or method performance.
