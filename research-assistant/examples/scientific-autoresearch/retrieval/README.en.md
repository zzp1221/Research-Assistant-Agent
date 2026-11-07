# Retrieval adapter example

[简体中文](README.md) | English

Demonstrates the generic `experiment.spec/v1` with a retrieval task. The generic harness does not understand BM25 or graph retrieval; it reads only the method branches, finite parameter domains, evaluation command, metric, target, and budget declared by the adapter.

## Inputs

- `corpus.jsonl`: 13 mixed industrial knowledge records with `id`, `text`, and optional `links`.
- `queries.jsonl`: 5 search cases and 3 holdout cases, each with `query` and `relevant_doc_ids`.
- Fixed metric: `NDCG@1`.
- Candidate methods: BM25, TF-IDF, BM25/TF-IDF RRF, and explicit-relation-edge graph-enhanced retrieval.
- Budget: at most 6 candidates, 180 seconds; target score `0.60`; holdout repeated twice.

The `graph_hybrid` method uses only explicit relation edges in the data; it is not Microsoft GraphRAG and does not call an LLM to build a graph.

## Run in the product

Upload `corpus.jsonl` and `queries.jsonl` together in the Web UI, then enter:

```text
Run automated research on this industrial data, comparing RAG retrieval strategies and hyperparameters.
Fix NDCG@1, at most 6 experiments, 3 minutes wall-clock, target score 0.60, and 2 independent validation runs.
```

The Planner generates an eight-node main chain:

```text
adapt research data -> freeze experiment contract -> design bounded candidate tree
-> prepare runtime -> install dependencies -> candidate search -> holdout acceptance -> evidence report
```

The candidate tree first compares default configurations, then performs univariate parameter ablations around informative branches. The Go Harness decides Keep/Reject from evaluator output; candidates cannot modify data, runner, metric, or target.

## Result files

- `result.json`: machine summary of a real run.
- `optimizer_experience_result.json`: Research Optimizer experience write and revalidation record.

Results prove only the chain behavior on this small dataset, not production optimality.

## Boundaries

- Improvement on the search set is not global optimality.
- The holdout was not used for candidate selection, so it is more trustworthy than search scores; but 3 holdout cases are still small.
- Real industrial use should provide a labeled evaluation set covering business distribution, failure cost, and time slices.
- Without query/relevant-document/answer labels, the system refuses to claim one strategy is better.
