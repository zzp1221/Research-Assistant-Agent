# checkpoint

[简体中文](README.md) | English

`checkpoint` provides two layers of fault tolerance for environment configuration: physical snapshots and cognitive feedback.

1. **Physical snapshots** — after a DAG node succeeds, run `Docker Commit` to save the full filesystem and environment variables.
2. **Rollback** — when all implementations of a node fail or the environment is corrupted, destroy the damaged container and restore from the last successful imageID.
3. **Negative feedback** — write the failed command and rollback reason into the next LLM context to prevent repeating the same path.

## Main components

| Component | Responsibility |
|---|---|
| `Manager` | Coordinates Commit, Rollback, container lifecycle, and feedback generation |
| `SnapshotRegistry` | Maps DAG node IDs to imageIDs with chain backtracking |
| `FeedbackInjector` | Records failures and generates structured negative feedback for the model context |

## Example

```go
package main

import (
    "context"
    "fmt"

    "research-assistant-agent/docker-core/checkpoint"
)

func main() {
    ctx := context.Background()

    manager := checkpoint.NewManager(box)

    imageID, err := manager.Commit(ctx, "env-node-A")
    if err != nil {
        panic(err)
    }
    fmt.Printf("checkpoint: %s\n", imageID)

    if err := manager.Rollback(ctx, imageID); err != nil {
        panic(err)
    }

    feedback := manager.RecordFailureAndGetFeedback(
        "env-node-A", "pip install cuda-python", "no matching distribution",
    )
    fmt.Println(feedback)
}
```

Besides single records, `GetFullFeedbackContext` aggregates all failures in the current round and can be inserted at the top of the LLM system prompt.

## Tests

```bash
go test ./checkpoint/...
```

Tests under `test/` cover snapshot registration, rollback, and feedback injection; `checkpoint.NewManager(nil)` supports feedback and registry tests that do not need a real container.

## Design constraints

- Snapshots use Docker Commit for compatibility, but image layers consume disk; production needs periodic cleanup.
- Rollback destroys the current container; callers must ensure no child processes are still running.
- Negative feedback is contextual only; it does not replace the upper-layer circuit breaker or budget control.
