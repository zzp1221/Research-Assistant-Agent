# sandbox

`sandbox` 是对 Docker API 的轻量封装，面向需要多轮交互的 AI Agent 环境配置场景。它维护一个持续运行的容器会话，而不是每次命令都创建新容器。

## 核心能力

- **TTY 长连接**：通过 Docker SDK 劫持容器 Stdin/Stdout，命令直接写入持续运行的 bash。
- **状态保持**：环境变量、工作目录和文件系统变更跨命令保留。
- **EnvMap 注入**：`Session.UpdateEnvMap` 可在执行前批量同步上下文变量；`export KEY=VALUE` 也会被解析并记录。
- **双路日志**：审计轨道保存原始输出，推理轨道在内存中缓冲并由 `Session.Execute` 返回。
- **生命周期管理**：`Close` 时销毁容器并释放网络、临时卷和进程资源；`ReattachSession` 可在回滚重建容器后重建 TTY。

## 主要组件

| 文件 | 职责 |
|---|---|
| `sandbox.go` | Sandbox 对象、创建/关闭、命令执行入口与 `ReattachSession` |
| `engine.go` | Docker 容器创建、启动、停止、删除与 `RestartFrom` |
| `session.go` | TTY 会话、读写管道、环境变量映射与超时中断 |
| `stream_splitter.go` | 审计流与推理流的多路分发 |
| `truncator.go` | 首尾截断、省略行提示 |
| `config.go` | 镜像、工作目录、日志目录、超时与资源限制 |

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

如需推理摘要，可在上层对 `session.Execute` 返回的 `ExecResult.RawOutput` 调用 `sandbox.NewTruncator(n, m).Truncate(...)`；`Sandbox.Execute` 目前只返回原始输出。

## 测试

```bash
go test ./sandbox/...
```

集成测试需设置 `SANDBOX_INTEGRATION_TEST=1` 并使用 Docker daemon。测试结束后应确认没有残留容器、未释放端口或临时镜像。

## 边界

`sandbox` 不包含 Agent 决策、任务规划或重试策略。它只负责容器生命周期与命令执行，所有智能体逻辑应放在上层模块。
