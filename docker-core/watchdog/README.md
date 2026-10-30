# watchdog

`watchdog` 是执行层的安全护栏，负责识别无效重试、交互式阻塞和超时命令，并向 Planner 返回可操作的修复建议。

## 核心能力

### 语义断路器

对“命令 + 标准化错误输出”计算语义哈希，记录在滑动窗口中。当同一哈希在 `MaxRetryWindow` 内重复出现时触发熔断，拒绝继续执行并返回 `CIRCUIT_BROKEN` 错误，强制上层重新规划。

### 超时与防挂起

- 所有命令受 `context.WithTimeout` 约束；`sandbox.Session` 在超时或上层取消时向 TTY 发送 `Ctrl+C`（`\x03`），释放被交互式提示锁死的终端。
- 返回结构化提示，例如“命令可能在等待输入，请添加 `-y` 或非交互参数”。

### 异常分析

将底层退出码、超时、信号和 stderr 转换为带 Hint 的 `WatchdogError`，方便 Agent 在下一轮修正命令。

## 主要组件

| 组件 | 职责 |
|---|---|
| `CircuitBreaker` | 语义哈希、滑动窗口与熔断判定 |
| `Checker` | 崩溃信号检测；可选调用 Agent 生成诊断建议 |
| `WatchdogError` / `IsCircuitBroken` 等 | 错误分类与判定辅助 |
| `Watchdog` | 包装沙箱执行器，提供 `Execute` 与 `RollbackToSafeState` |
| `Simulator` | 无 Docker 的测试替身 |

## 示例

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

`NewWatchdog(cfg, box, checkpointManager, agent)` 中 `agent` 可为 `nil`（此时只做确定性审计，不调用 LLM）。

## 测试

```bash
go test ./watchdog/...
```

测试覆盖重复错误熔断、超时中断和错误分类。真实 Docker/LLM 用例默认跳过，可设置 `REAL_INTEGRATION_TEST=1` 或 `REAL_LLM_TEST=1`。

## 边界

断路器基于命令与错误的语义哈希，不理解业务语义。它只负责阻止明显重复的失败路径，不能替代 Planner 的全局重规划。
