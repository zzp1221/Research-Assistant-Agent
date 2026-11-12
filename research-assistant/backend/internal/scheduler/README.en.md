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

## Result transitions and waits

- Every node result goes through one entry point, `applyTaskResult`: wait (approval / question), completed, `blocked`, `failed`, `canceled`, or an explicit protocol error. Automatic dispatch and the manual single-node endpoint share it, so there is no second "anything but completed is a failure" rule.
- `awaiting_approval` / `awaiting_input` must persist the matching pending approval or question record in the same state transition; a bare wait state returns a protocol error and writes nothing. A wait never writes `Error` / `FinishedAt` / `Failure`, never charges a failed retry, marks the recovery checkpoint `Waiting=true, PendingRetry=false`, and releases the execution lease.
- Execution exceptions outrank waits: a canceled, timed-out or explicitly failed result stays terminal even when it carries a Question or RuntimeAction; the attachment is kept only as diagnostics.
- A `tool_call` node that needs approval returns `awaiting_approval` instead of failing. Approving it resumes with the same call_id and arguments, and an already completed tool side effect is not repeated.
- One execution fence (`ExecutionID / LeaseOwner / ExecutionEpoch`) also covers questions and approvals: a stale execution cannot write a wait, a question, an approval, or a dependent's status.
- Dispatch re-validates legality at lease time: `markTaskStarted` takes a `dispatchGuard` that checks enabled state, hard dependencies, artifacts and pending approvals; the manual entry point reuses the same `dispatchLegality`.

## Failure propagation and block reasons

- Hard dependencies are defined once: `hardDependencyIDs` = a node's `Dependencies` plus artifact producers (`data` edges). Readiness, initial status, failure propagation and recovery all read that one definition.
- An upstream `failed` / `blocked` / `canceled` blocks its hard dependents inside the same `UpdatePlan` with `upstream_failed` / `upstream_blocked` / `upstream_canceled`; `awaiting_*` never propagates, so dependents stay pending. Independent branches keep running.
- `TaskNode.block_info` stores `code/message/causes[]/occurred_at`; `causes[]` keeps every root cause (`root_task_id / via_task_id / revision / execution_epoch / status`). Events are built from the same object, and `task_blocked / upstream_task_id` remain for older clients.
- Recovery recomputes effective causes: retrying one root only drops that root's older-epoch cause; the node stays blocked while another cause remains, and returns to `pending` only when every cause is gone (readiness is then re-evaluated). Nodes blocked before structured reasons existed are not released automatically; they need an explicit rerun.

## Terminal decision

- `reconcilePlanState` is the single terminal entry point: the outer snapshot only decides when to try, and every decision is taken on the committed graph, so a stale "A=blocked, B=pending" read cannot write an empty `Outcome`.
- Order: keep an explicit stop → promote runnable nodes → wait when runnable work cannot be dispatched yet (global slots saturated) → legitimate waits / deferred retries / pause → summarize a fully terminal graph → only then treat it as a dependency deadlock.
- The evaluated result is committed together with `Status / Outcome / OutcomeReason / OutcomeDetails / FinishedAt`; the terminal event, cleanup, budget release, memory and notification run exactly once, on the transition that committed it.
- `PlanFinalizer` now runs for every ended graph (not only ones without failures) in a fixed order: stop semantics → core failure `failed` / core block `blocked` → insufficient evidence `needs_review` → mock/smoke → acceptance contract → optional losses only `partial` → `completed`.
- Budget only stops new work: a fully terminal graph is summarized by its acceptance result, and `CancelPlan("run budget exceeded")` is used only while runnable work remains.

## Partial outcome contract

- New optional fields (legacy plans stay strictly required): `TaskNode.optional`, `TaskNode.optional_dependencies`, `PlanGraph.acceptance`, `PlanGraph.outcome_details`.
- `optional` decides whether a failure breaks the core goal; `optional_dependencies` only decides whether the consumer must wait for the producer to succeed. Hard `dependencies` still require completed/skipped plus required artifacts.
- Graphs carry an `optional` edge type; planner and API validation both check the two relation kinds together: a pair cannot be both, a cycle cannot cross kinds, and an optional producer's artifact cannot be a required input.
- `acceptance` is frozen before review: at least one core goal, every goal needs verifiable evidence (artifact keys or `min_verified_claims`), and a plan whose tasks are all optional cannot declare a contract.
- `outcome_details` records accepted core goals, optional losses, missing artifacts and limitations; the UI, notifications, export and memory all read that one result.

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

The scheduler-semantics regression set lives in `result_transition_test.go`, `terminal_reconcile_test.go`, `blocked_reason_test.go`, `partial_outcome_test.go`, `tool_approval_wait_test.go`, `terminal_wait_test.go` and `dispatch_and_round_test.go`; interleavings are fixed with channels/barriers rather than sleep-based luck. On Windows, four cases in `repo_prepare_test.go` need `core.symlinks=true` and `core.autocrlf=false` and are unrelated to the scheduler logic.