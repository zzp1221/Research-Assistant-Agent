# docker-core

[简体中文](README.md) | English

`docker-core` is a standalone Go module for validating and demonstrating persistent sandboxes, DAG scheduling, state snapshots, and safety guardrails under Docker / gVisor. It does not depend on the `research-assistant` main application; it builds and tests independently and serves as a reference implementation for the sandbox execution layer.

## Relationship to the main project

- [System architecture](../docs/diagrams/architecture.drawio) ([SVG preview](../docs/diagrams/architecture.svg))
- [Agent execution flow](../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../docs/diagrams/agent-execution-flow.svg))

`docker-core` is a research/reference implementation of the sandbox and scheduling kernel; the product runtime uses `research-assistant/docker-sandbox` gateway/executor with docker/native/vm/remote providers. Both share the goals of isolated execution, snapshot/rollback, and resource guardrails, but the modules are independent and build/test separately.

## Module layout

```text
docker-core/
├── config/       TOML configuration and defaults
├── sandbox/      Docker container lifecycle, persistent TTY, dual-channel logs
├── checkpoint/   Docker Commit snapshots, rollback, negative feedback
├── executor/     DAG topological scheduling, node racing, resource gateway
├── watchdog/     Semantic circuit breaker, timeouts, error analysis
└── tests/        Real Docker integration tests
```

`main.go` is currently a placeholder entry; the module is used as a Go library by `tests/` and upper-layer integration code, not as a directly runnable service.

Module boundaries:

- `sandbox` wraps Docker and gVisor only; it contains no agent decision logic.
- `checkpoint` builds on `sandbox` and owns Commit / Rollback / snapshot registration and negative-feedback text.
- `executor` decomposes environment-configuration goals into a DAG and schedules node execution.
- `watchdog` intercepts infinite loops, interactive blocking, and repeated failure paths.

## Core design

### Persistent interactive sandbox

The Go Docker SDK hijacks the container pseudo-terminal so a bash process keeps running inside the container. Environment variables, working directory, and filesystem state persist across commands, which suits incremental environment configuration.

`Sandbox.Execute` returns the raw command output; `ExecResult` from `session.Execute` also carries `InferenceSummary`, reserved for upper layers to produce head/tail-truncated summaries with `Truncator` (currently `Sandbox.Execute` does not fill it automatically).

### Dual-channel logs

- Audit channel: complete ANSI output is written line by line to `AuditLogDir/audit_*.log` for replay and human inspection.
- Inference channel: `StreamSplitter` buffers output in memory and `Session.Execute` returns it by delimiter; `Truncator` can keep only startup information and the error tail for the model.

### DAG scheduling and node racing

The scheduler computes in-degrees, places ready nodes in a queue, and executes them with multiple goroutines. A single node may offer several implementation paths (e.g., pip, conda, source build); the first success triggers `context.Cancel` and terminates the rest. Each node times out after 15 minutes by default (`Engine.NodeTimeout`).

### Snapshots and rollback

After each core node succeeds, `Engine` calls `CheckpointManager.Commit` to run `Docker Commit`, producing an imageID registered in `SnapshotRegistry`. On failure, `checkpoint.Manager` can restart the container from a snapshot and rebuild the TTY session, while `FeedbackInjector` generates negative-feedback text to prevent the model from repeating the same wrong path.

### Resource gateway

`Gatekeeper` uses weighted semaphores to limit I/O and memory-intensive tasks, and a capacity-1 GPU semaphore to ensure only one high-VRAM task runs at a time.

## Quick start

```bash
cd docker-core
go mod tidy
go test ./...
```

Configuration template: `config/config-template.toml`:

```toml
[llm]
model = "deepseek-chat"
base_url = "https://api.deepseek.com"
api_key = "your-api-key"

[resource]
max_concurrent_io = 10
max_gpu_tasks = 1
```

Load configuration:

```go
cfg, err := config.LoadConfig("config/config.toml")
if err != nil {
    log.Fatal(err)
}
```

The `LLM_API_KEY` environment variable overrides `llm.api_key`. `config.toml` is gitignored; never commit real keys.

## Usage examples

Create a sandbox and keep state across commands:

```go
box, err := sandbox.New(ctx, sandbox.DefaultConfig())
if err != nil {
    panic(err)
}
defer box.Close()

if _, err := box.Execute(ctx, "export MY_VAR=hello_agent"); err != nil {
    panic(err)
}
out, err := box.Execute(ctx, "echo $MY_VAR")
fmt.Println(out) // hello_agent
```

Snapshot and rollback:

```go
manager := checkpoint.NewManager(box)

imageID, err := manager.Commit(ctx, "env-node-A")
if err != nil {
    panic(err)
}
if err := manager.Rollback(ctx, imageID); err != nil {
    panic(err)
}
feedback := manager.RecordFailureAndGetFeedback("env-node-A", "pip install cuda-python", "no matching distribution")
fmt.Println(feedback)
```

DAG scheduling + resource gateway:

```go
gatekeeper := executor.NewGatekeeper(executor.ResourceConfig{
    MaxConcurrentIO: 10,
    MaxGPUTasks:     1,
})
engine := executor.NewEngine(gatekeeper, box, manager, agent)

graph := executor.NewDAG()
base := graph.AddNode("install_base", []string{"apt-get install -y build-essential"})
opencv := graph.AddNode("install_opencv", []string{
    "pip install opencv-python",
    "conda install -c conda-forge opencv",
})
graph.AddEdge(base, opencv)

if err := engine.ExecuteGraph(ctx, graph); err != nil {
    log.Fatal(err)
}
```

In `executor.NewEngine(gatekeeper, box, manager, agent)`, `box` must satisfy `executor.Sandbox`, `manager` must satisfy `executor.CheckpointManager`, and `agent` must satisfy `executor.Agent` (it may be `nil`; empty-command nodes then generate no strategy).

## Tests

```bash
go test ./...
go test ./sandbox/...      # Sandbox lifecycle and resource leaks
go test ./executor/...     # DAG scheduling and racing
go test ./watchdog/...     # Circuit breaker and timeouts
go test ./checkpoint/...   # Snapshots and rollback
```

Real Docker integration tests live in `sandbox/test/` and `tests/` and require `SANDBOX_INTEGRATION_TEST=1` or `REAL_INTEGRATION_TEST=1` (the latter also needs a valid LLM key and `config/config.toml`). Confirm Docker is running and that containers, network ports, and temporary images are cleaned up.

Build and unit tests can be rerun in `golang:1.27.3-bookworm`; see [docs/verification.md](../docs/verification.md) for the latest results.

## Sub-modules

- [config](config/README.md)
- [sandbox](sandbox/README.md)
- [checkpoint](checkpoint/README.md)
- [executor](executor/README.md)
- [watchdog](watchdog/README.md)

## Security boundaries

This module targets controlled research environments. Even with gVisor, capability limits, and read-only mounts, the Docker socket carries high host privileges. Do not connect untrusted tenants directly to this execution layer; production deployments need additional network isolation, image signing, resource quotas, and audit.
