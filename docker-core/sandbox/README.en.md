# sandbox

[简体中文](README.md) | English

`sandbox` is a lightweight wrapper over the Docker API for multi-turn AI-agent environment configuration. It maintains one long-running container session instead of creating a new container for every command.

## Core capabilities

- **Persistent TTY** — hijack container Stdin/Stdout through the Docker SDK and write commands directly into a running bash.
- **State retention** — environment variables, working directory, and filesystem changes persist across commands.
- **EnvMap injection** — `Session.UpdateEnvMap` synchronizes context variables before execution; `export KEY=VALUE` is parsed and recorded too.
- **Dual-channel logs** — the audit channel keeps raw output; the inference channel buffers in memory and returns through `Session.Execute`.
- **Lifecycle management** — `Close` destroys the container and releases network, temporary volumes, and process resources; `ReattachSession` rebuilds the TTY after a rollback/restart.

## Main components

| File | Responsibility |
|---|---|
| `sandbox.go` | Sandbox object, create/close, command entry, `ReattachSession` |
| `engine.go` | Docker container create/start/stop/delete and `RestartFrom` |
| `session.go` | TTY session, read/write pipes, env map, timeout interruption |
| `stream_splitter.go` | Multiplexing audit and inference streams |
| `truncator.go` | Head/tail truncation and omitted-line hints |
| `config.go` | Image, working directory, log directory, timeouts, resource limits |

## Example

```go
package main

import (
    "context"
    "fmt"

    "research-assistant-agent/docker-core/sandbox"
)

func main() {
    ctx := context.Background()
    cfg := sandbox.DefaultConfig()
    cfg.Image = "ubuntu:22.04"
    cfg.WorkingDir = "/workspace"
    cfg.AuditLogDir = "/var/logs/audit"

    box, err := sandbox.New(ctx, cfg)
    if err != nil {
        panic(err)
    }
    defer box.Close()

    if _, err := box.Execute(ctx, "export MY_VAR=hello_agent"); err != nil {
        panic(err)
    }
    out, err := box.Execute(ctx, "echo $MY_VAR")
    if err != nil {
        panic(err)
    }
    fmt.Println(out) // hello_agent
}
```

For inference summaries, call `sandbox.NewTruncator(n, m).Truncate(...)` on `ExecResult.RawOutput` returned by `session.Execute`; `Sandbox.Execute` currently returns raw output only.

## Tests

```bash
go test ./sandbox/...
```

Integration tests require `SANDBOX_INTEGRATION_TEST=1` and a Docker daemon. After tests, confirm no containers, ports, or temporary images remain.

## Boundaries

`sandbox` contains no agent decisions, task planning, or retry policy. It owns container lifecycle and command execution only; all agent logic belongs to upper layers.
