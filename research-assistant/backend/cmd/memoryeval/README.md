# Context / memory mechanism evaluation

`memoryeval` runs frozen, paired A/B/C evaluations through the production context
manager and memory retriever. It is a mechanism pilot, not a benchmark of real
research execution or autonomous long-task success.

| Arm | Context | Long-term memory |
|---|---|---|
| A | Full history, compaction off | Off |
| B | Managed text compaction | Off |
| C | Same configuration as B | Frozen memory snapshot |

Each sample receives isolated memory and context stores. Arm order rotates by
task and repetition. Expected answers are used by the grader and are never sent
to the provider. The deterministic mock reads the actual outgoing request; it
does not have access to expected answers.

## Run

From the backend directory, with Go and dependencies available:

```powershell
go run ./cmd/memoryeval -out ../../reports/context-memory-mock -repetitions 2
```

Real-provider mode uses the existing `appconfig.LoadLLMConfig` environment
configuration. No API key or endpoint URL is written to the report. The endpoint
hash identifies configuration changes.

```powershell
go run ./cmd/memoryeval -real -out ../../reports/context-memory-real `
  -repetitions 1 -context-window 6000 -max-output 768 `
  -trigger 0.55 -summary-ratio 0.15
```

Use a **new output directory** for every run. The command saves the frozen
`suite.json`, configuration and an initial `report.json` before making requests,
then updates the report after every sample. All failures remain in the report.
`-limit N` runs only the first N tasks and is recorded in the configuration.
The configured context window is the evaluation limit, not a capacity probe.

Custom suites must match a SHA256 frozen before execution:

```powershell
$memorySuiteHash = (Get-FileHash ./my-suite.json -Algorithm SHA256).Hash.ToLowerInvariant()
go run ./cmd/memoryeval -suite ./my-suite.json -suite-sha256 $memorySuiteHash `
  -out ../../reports/context-memory-custom -repetitions 2
```

The schema is illustrated by
[`pilot-v1.json`](../../internal/memoryeval/testdata/pilot-v1.json). `history`
contains current-task evidence; `memories` contains the pre-existing training
snapshot. All arms start with equivalent isolated snapshots; only C reads them.
`expected` freezes exact `subject`, `value`, `status` and `source_id` answers.
For no-answer cases use empty value/source and `status: "unknown"`.

## Metrics and limits

- Passing requires every requested value, status and source ID to match, with
  no duplicate or extra subjects. Units and experimental identities are part of
  the exact strings. This intentionally strict grader does not assess prose.
- Negative preservation, source correctness and evidence usage are counted
  separately. Evidence usage means returning the expected source ID; it does
  not prove an actual tool action or scientific adoption.
- `no_answer_false_recall` measures an unsupported answer on a no-answer key.
  It does not mean that every retrieved but unused candidate is a false answer.
- Recall@K and nDCG@K use labelled relevant memory IDs and the actual selected
  ranking (default K=8). Budget evidence coverage uses rendered citations.
  They are absent when no relevant memory is labelled. Consult the retrieval
  and final material-selection reports together: the model preflight can omit
  an otherwise rendered optional block.
- Tokens include compaction and generation. Estimates and unavailable usage
  are marked. Cost remains `null` without configured, verified prices; the CLI
  does not assume a provider's price.
- Paired differences average repetitions within each task before reporting the
  task-level mean difference and standard error. A small synthetic pilot cannot
  establish statistical or practical benefit on real long tasks.

The bundled 12 cases cover corrections, negative results, hypotheses, multiple
experiments, units, data versions, bilingual reuse, protocol reuse, isolation,
pending inference and no-answer behavior. The 4,200-character padding triggers
context management; it is not a realistic research transcript. There is no DAG,
tool execution, extraction-coverage measurement, or multi-day soak test here.
For a benefit study, freeze 30–50 representative research tasks and their
acceptance rules, separate experience-producing and evaluation tasks, and run
real execution with equivalent tools and repeated conditions.
