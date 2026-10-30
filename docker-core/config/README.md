# config

`config` 提供 `docker-core` 的 TOML 配置结构、默认模板和环境变量覆盖。

## 配置结构

```go
type Config struct {
    LLM      LLMConfig
    Resource ResourceConfig
    Watchdog WatchdogConfig
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `llm.model` | string | 模型名称 |
| `llm.base_url` | string | OpenAI-compatible API 地址 |
| `llm.api_key` | string | API 密钥，可被 `LLM_API_KEY` 覆盖 |
| `resource.max_concurrent_io` | int64 | I/O 与内存密集型任务并发上限 |
| `resource.max_gpu_tasks` | int64 | GPU 任务并发上限，通常为 1 |
| `watchdog.max_retry_window` | int | 相同语义错误的最大重试窗口 |
| `watchdog.command_timeout_seconds` | int64 | 单条命令超时时间 |

## 使用

```go
import "research-assistant-agent/docker-core/config"

cfg, err := config.LoadConfig("config/config.toml")
if err != nil {
    log.Fatal(err)
}
```

复制 `config-template.toml` 为 `config.toml` 后修改。`config.toml` 已加入 `.gitignore`，不要提交真实密钥。

## 加载优先级

1. 读取 TOML 文件。
2. 如果设置了 `LLM_API_KEY` 环境变量，覆盖 `llm.api_key`。
3. 其余字段使用文件中的值；缺失字段保持 Go 零值，调用方应自行校验必填项；`watchdog` 段缺失时需上层提供默认值。
