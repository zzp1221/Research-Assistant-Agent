# config

[简体中文](README.md) | English

`config` provides TOML configuration structures, default templates, and environment-variable overrides for `docker-core`.

## Configuration structure

```go
type Config struct {
    LLM      LLMConfig
    Resource ResourceConfig
    Watchdog WatchdogConfig
}
```

| Field | Type | Description |
|---|---|---|
| `llm.model` | string | Model name |
| `llm.base_url` | string | OpenAI-compatible API base URL |
| `llm.api_key` | string | API key; overridden by `LLM_API_KEY` |
| `resource.max_concurrent_io` | int64 | Concurrency cap for I/O and memory-intensive tasks |
| `resource.max_gpu_tasks` | int64 | GPU task concurrency cap, usually 1 |
| `watchdog.max_retry_window` | int | Maximum retry window for identical semantic errors |
| `watchdog.command_timeout_seconds` | int64 | Per-command timeout |

## Usage

```go
import "research-assistant-agent/docker-core/config"

cfg, err := config.LoadConfig("config/config.toml")
if err != nil {
    log.Fatal(err)
}
```

Copy `config-template.toml` to `config.toml` and edit it. `config.toml` is gitignored; never commit real keys.

## Load priority

1. Read the TOML file.
2. If `LLM_API_KEY` is set, it overrides `llm.api_key`.
3. Other fields come from the file; missing fields keep Go zero values, and callers must validate required values. When the `watchdog` section is absent, the upper layer must supply defaults.
