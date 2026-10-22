# sandbox

`sandbox` 是对 Docker API 的轻量封装，面向需要多轮交互的 AI Agent 环境配置场景。它维护一个持续运行的容器会话，而不是每次命令都创建新容器。

## 核心能力

- **TTY 长连接**：通过 Docker SDK 劫持容器 Stdin/Stdout，命令直接写入持续运行的 bash。
- **状态保持**：环境变量、工作目录和文件系统变更跨命令保留。
- **EnvMap 注入**：每次执行前自动注入上下文变量，避免 Shell 切换导致路径丢失。
- **双路日志**：审计轨道保存原始输出，推理轨道输出截断后的摘要。
- **生命周期管理**：`Close` 时销毁容器并释放网络、临时卷和进程资源。

## 主要组件

| 文件 | 职责 |
|---|---|
| `sandbox.go` | Sandbox 对象、创建/关闭与命令执行入口 |
| `engine.go` | Docker 容器创建、启动、停止与删除 |
| `session.go` | TTY 会话、读写管道与环境变量映射 |
| `stream_splitter.go` | 审计流与推理流的多路分发 |
| `truncator.go` | 首尾截断、错误尾部提取 |
| `config.go` | 镜像、工作目录、日志目录等配置 |

## 示例

```go
package main

import (
    "context"
    "fmt"

    "research-assistant-agent/docker-core/sandbox"
)

func main() {
    ctx := context.Background()
    cfg := sandbox.Config{
        Image:       "ubuntu:22.04",
        WorkingDir:  "/workspace",
        AuditLogDir: "/var/logs/audit",
    }

    box, err := sandbox.New(ctx, cfg)
    if err != nil {
        panic(err)
    }
    defer box.Close()

    first, _ := box.Execute("export MY_VAR=hello_agent")
    fmt.Println(first.InferenceSummary)

    second, _ := box.Execute("echo $MY_VAR")
    fmt.Println(second.InferenceSummary) // hello_agent
}
```

## 测试

```bash
go test ./sandbox/...
```

集成测试需要 Docker daemon。测试结束后应确认没有残留容器、未释放端口或临时镜像。

## 边界

`sandbox` 不包含 Agent 决策、任务规划或重试策略。它只负责容器生命周期与命令执行，所有智能体逻辑应放在上层模块。
