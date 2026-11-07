# executor

[简体中文](README.md) | English

`executor` is the DAG scheduling and concurrency core for environment-configuration tasks. It decomposes goals into a directed acyclic graph, schedules nodes in topological order, and supports racing multiple implementations within one node.

## Core capabilities

### Topology-driven scheduling

The scheduler computes each node's in-degree, places all zero-in-degree nodes in `ReadyQueue`, and consumes them with multiple workers. When a node completes, it decrements its successors' in-degrees until the graph finishes or an unrecoverable error occurs. Each node times out after 15 minutes by default (`Engine.NodeTimeout`).

### Intra-node racing

A node may provide multiple implementation commands. The executor starts them concurrently; the first success triggers `context.Cancel`, terminates the rest, and fixes the successful result. When a node has no command, it asks `Agent.GenerateStrategies` for candidate strategies.

### Gatekeeper resource gateway

- A capacity-1 GPU semaphore prevents multiple CUDA tasks from occupying VRAM simultaneously.
- Weighted semaphores limit global concurrency for I/O- and memory-intensive tasks.

## Main components

| Component | Responsibility |
|---|---|
| `DAG` / `Node` | Graph structure, dependencies, in-degrees |
| `Engine` | Topological scheduling loop, node timeouts, checkpoint callbacks |
| `Gatekeeper` | I/O/GPU quotas based on `golang.org/x/sync/semaphore` |
| `ExecutorAgent` | Generates execution strategies and plans through an OpenAI-compatible LLM |

## Example

```go
package main

import (
    "context"
    "log"

    "research-assistant-agent/docker-core/executor"
)

func main() {
    ctx := context.Background()

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
}
```

In `NewEngine(gatekeeper, box, checkpointManager, agent)`, `box` must implement `Execute(ctx, cmd) (string, error)` and `Close() error`; `agent` may be `nil`.

## Tests

```bash
go test ./executor/...
```

Tests cover DAG construction, topological scheduling, node racing, and resource quotas. Cases requiring real Docker or an LLM are skipped by default and run with `REAL_INTEGRATION_TEST=1`.

## Boundaries

`executor` handles scheduling and resource control only; it does not judge business correctness. Success is determined by exit codes, the `Watchdog` checker, or upper-layer validators.
