# agent

[简体中文](README.md) | English

The `agent` package implements input contracts, prompt assembly, model calls, tool execution, and result parsing for each specialized agent. Agents never execute user code directly; they send commands through the `internal/sandbox` client, and tool calls go through `internal/tools`' `ToolInvoker`.

## Architecture diagrams

- [Agent execution flow](../../../../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../../../../docs/diagrams/agent-execution-flow.svg))
- [System architecture](../../../../docs/diagrams/architecture.drawio) ([SVG preview](../../../../docs/diagrams/architecture.svg))

## Agent overview

| Agent | Main files | Responsibility |
|---|---|---|
| Chat | `chat.go` | General Q&A, lightweight task entry, multi-turn messages, citations |
| Librarian | `librarian.go`, `claim_evidence.go`, `literature_search.go` | Paper parsing, retrieval, method/claim extraction, claim rubric, literature search/import |
| Benchmark | `benchmark_agent.go`, `benchmark_contract_agent.go`, `benchmark_dataset.go`, `benchmark_metric_contract.go`, `benchmark_harness.go`, `benchmark_adapter.go` | Dataset audit, splits, leakage checks, Metric/Reward contracts, hidden metric recomputation |
| Coder | `coder.go`, `coder_eino.go`, `coder_runtime.go`, `coder_runtime_flow.go`, `coder_exec.go`, `coder_python.go`, `coder_deps.go`, `coder_image.go` | Code generation, plotting, runtime prepare/install/validate, dependency resolution, bounded repair |
| Research Coding | `research_coding_agent.go`, `reproduction_spec.go` | Paper-repo debugging, benchmark adapters, RuntimeSpec, code-patch AutoResearch |
| Data | `data.go` | Metric aggregation, paper-claim comparison, evidence-graph construction, reports/charts, visual-verification orchestration |
| AutoResearch | `autoresearch.go`, `autoresearch_run.go`, `autoresearch_validation.go`, `autoresearch_spec.go`, `autoresearch_workspace.go`, `autoresearch_apply.go`, `autoresearch_helpers.go`, `autoresearch_eval.go` | ResearchSpec freeze, candidate execution, Keep/Reject, rollback, holdout validation |
| Experiment Research | `experiment_research.go`, `experiment_retrieval_adapter.go`, `experiment_portable_adapter.go` | `experiment.spec/v1` configuration search and domain adapters |
| Ablation ToT | `ablation_tot.go` | Bounded Tree-of-Thoughts ablation design |
| Paper Debug | `paper_debug_harness.go` | Paper-repo debugging state machine and bounded patch validation |
| Visual Verifier | `visual_verifier.go` | Image structure gate and multimodal verdict (pass/fail/inconclusive) |
| Answer | `answer.go` | Final user-facing answer synthesis |

## Design conventions

1. Each agent handles only its own domain; cross-domain work is routed by the Scheduler.
2. Every execution agent submits commands through the sandbox client; none calls `os/exec` on user code directly.
3. Structured results become artifacts; long text and logs flow through events.
4. Paper claims, benchmark metrics, and AutoResearch decisions must use frozen contracts in `models`; models never self-report final metrics.
5. Prompts live in `internal/prompts` for auditing and versioning; `BuildTaskPrompt` assembles core_memory → plan_context → task_instruction → retrieved_memory → task_inputs → volatile with section-level `PromptBudget` and rune-safe truncation.
6. Repair loops use `internal/agent/conversation.Session`: keep system + first user, append only the previous assistant output and feedback; Planner / PaperDebug / BenchmarkAdapter / AutoResearch / Coder self-correction share this convention.
7. Runtime models are resolved per call through `dynamic_model.go` / `modelresolver`; they are not frozen at startup.
8. Tool calls must pass `ToolInvoker` schema/grant/approval checks; every call records a `ToolCallRecord`.

## Memory injection

`RoutedTaskExecutor` resolves `sharedContext` before invoking an agent:

- `core_memory`: stable project/user facts placed near the front of the prompt.
- `retrieved_memory`: task-relevant episode/procedure/negative memory placed before task inputs.
- `memory_refs`: memory IDs used this run, recorded in plan details and artifact metadata.

Agents never read or write the memory store directly; the Scheduler's `MemorySink` and the `memory` package own it.

## Tests

```bash
go test ./internal/agent/...
go test -race -count=1 ./internal/agent/conversation/...
```

Most unit tests use `httptest` or a mock sandbox and need no real LLM. Tests marked as real integration require environment variables and access the network or Docker.
