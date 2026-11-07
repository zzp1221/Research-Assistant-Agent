# watchdog

[简体中文](README.md) | English

`watchdog` is the execution-layer safety guard. It detects ineffective retries, interactive blocking, and timed-out commands, and returns actionable remediation to the Planner.

## Core capabilities

### Semantic circuit breaker

It computes a semantic hash of "command + normalized error output" and records it in a sliding window. When the same hash appears repeatedly within `MaxRetryWindow`, the breaker trips, refuses further execution, and returns a `CIRCUIT_BROKEN` error that forces replanning.

### Timeout and hang prevention

- Every command is bounded by `context.WithTimeout`; on timeout or upper-layer cancellation, `sandbox.Session` sends `Ctrl+C` (`\x03`) to the TTY to release a terminal stuck at an interactive prompt.
- Returns structured hints such as "the command may be waiting for input; add `-y` or non-interactive flags".

### Error analysis

Converts low-level exit codes, timeouts, signals, and stderr into `WatchdogError` values with hints so the agent can correct the command next round.

## Main components

| Component | Responsibility |
|---|---|
| `CircuitBreaker` | Semantic hash, sliding window, trip decision |
| `Checker` | Crash-signal detection; optionally calls the agent for diagnostics |
| `WatchdogError` / `IsCircuitBroken` | Error classification and predicates |
| `Watchdog` | Wraps the sandbox executor with `Execute` and `RollbackToSafeState` |
| `Simulator` | Docker-free test double |

## Example

```go
package main

import (
    "context"
    "fmt"
    "time"

    "research-assistant-agent/docker-core/watchdog"
)

func main() {
    ctx := context.Background()
    cfg := watchdog.Config{
        MaxRetryWindow: 5,
        CommandTimeout: 30 * time.Second,
    }
    wd := watchdog.NewWatchdog(cfg, box, manager, agent)

    for i := 0; i < 6; i++ {
        _, err := wd.Execute(ctx, "pip install unexisting-package")
        if err != nil && watchdog.IsCircuitBroken(err) {
            fmt.Println("circuit broken: replan required")
            break
        }
    }
}
```

In `NewWatchdog(cfg, box, checkpointManager, agent)`, `agent` may be `nil` (deterministic auditing only, no LLM calls).

## Tests

```bash
go test ./watchdog/...
```

Tests cover repeated-error circuit breaking, timeout interruption, and error classification. Real Docker/LLM cases are skipped by default; enable with `REAL_INTEGRATION_TEST=1` or `REAL_LLM_TEST=1`.

## Boundaries

The circuit breaker is based on semantic hashes of commands and errors; it does not understand business semantics. It only blocks obviously repeated failure paths and cannot replace global Planner replanning.
