# scheduler

[简体中文](README.md) | English

`scheduler` executes `PlanGraph`: it schedules nodes by dependency, maintains leases, handles retry/cancel, prepares repositories, injects memory, and routes artifacts to downstream agents.

## Architecture diagrams

- [Runtime flow](../../../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../../../docs/diagrams/runtime-flow.svg))
- [Agent execution flow](../../../../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../../../../docs/diagrams/agent-execution-flow.svg))

## Main files

| File | Responsibility |
|---|---|
| `scheduler.go` | Scheduler core, concurrency slots, stall detection, result externalization, plan cleanup, MemorySink interface |
| `scheduler_control.go` | pause / resume / cancel / retry / reassign / rerun-from-node |
| `scheduler_execute.go` | Plan and single-node execution entry, ready-node promotion, terminal memory write |
| `scheduler_dispatch.go` | Ready-node selection, concurrent dispatch, task usage and log forwarding |
| `scheduler_state.go` | Task start/completion transitions, artifact validation annotation |
| `scheduler_lease_approval.go` | Failure handling, lease expiry, approval timeout, budget and blocking |
| `scheduler_events.go` | Event publication and terminal aggregation |
| `executor.go` | Single-node execution, agent invocation, prompt assembly, memory injection, artifact write |
| `graph.go` | DAG in-degrees, ready queue, dependency management |
| `policy.go` | Scheduling policy and budget decisions |
| `runtime_gate.go` | Runtime approval gate (image/dependency/data/GPU/network) and tool_call gate |
| `repo_discovery.go` | GitHub repository discovery, metadata, revision resolution |
| `repo_prepare.go` | Clone, workspace preparation, dependency analysis, runtime preparation |
| `reproduction_mode.go` | Paper-reproduction mode and smoke constraints |

## Execution model

1. Load the plan from the Plan Store and compute each node's in-degree.
2. Zero-in-degree nodes enter the ReadyQueue and run concurrently on workers.
3. A node acquires a lease before execution to prevent duplicates; on completion it writes artifacts and publishes events.
4. Failures follow retry policy, become blocked, or cancel successors.
5. At terminal state, the scheduler aggregates status and events and calls `MemorySink.RecordPlan`.

## Task control and runtime approval

- Plan level: `pause` stops dispatching new nodes while running nodes finish naturally; `resume` continues; `cancel` propagates `context.Cancel` to running nodes.
- Task level: `retry` resets failed/blocked/canceled nodes and their blocked successors; `reassign` changes `assigned_to` and resets the task; `run` (rerun from node) resets the node and downstream.
- `RuntimeActionRequest`: image pulls, dependency installs, data/model downloads, GPU, and network access pause the plan in `confirm_important` mode for the plan owner to `approve/reject`; `full_access` auto-executes within safety boundaries and records audit.
- Runtime actions have a timeout (`APPROVAL_TIMEOUT_SECONDS`); rejection or timeout terminates the plan with `outcome=blocked`; waiting for approval is never terminal.
- `SetToolGate` wires `tool_call` node `AllowedTools` / `ToolGrant` checks into the same approval gate; calls that would be rejected never prompt.

## Memory injection

Before invoking an agent, `RoutedTaskExecutor` reads from `sharedContext`:

- `core_memory`: stable prefix placed near the front of the prompt.
- `retrieved_memory`: task-relevant episode/procedure/negative memory.
- `memory_refs`: recorded in plan and artifact metadata.

Task completion calls `MemorySink.RecordTask`; terminal plans call `RecordPlan`. Memory failures are logged but do not affect task results.

## Result validation

- `PlanFinalizer` produces `PlanOutcome` (completed/partial/smoke/mock/blocked/failed/needs_review) from node states, artifacts, EnvironmentManifest, TrialLedger, and Claim-Evidence.
- Visual verification writes to `plan.verification`, `node.verification`, `artifact.metadata.verification`, and `verification_*` metrics; visual failure is advisory and never fabricates completed.
- Artifacts must be non-empty, typed, and produced by a unique producer; otherwise the task fails rather than faking success.

## Leases and recovery

- Task leases include executor identity and expiry; results from stale leases are isolated.
- File Plan Store uses atomic writes and can recover unfinished plans after restart.
- Cancellation propagates `context.Cancel` to running nodes and stops dispatching new ones.

## Repository tasks

`repo_discovery.go` resolves GitHub repositories from user input or paper metadata and records default branch, commit, and acquisition method. `repo_prepare.go` clones, cleans the workspace, generates dependency manifests, and prepares the runtime within bounds.

## Tests

```bash
go test ./internal/scheduler/...
go test -race -count=1 ./internal/scheduler/...
```

Tests cover DAG scheduling, leases, retry/reassign/rerun, runtime approval, repository discovery/preparation, reproduction mode, and result aggregation. Real Git/Docker cases need network and a daemon.
