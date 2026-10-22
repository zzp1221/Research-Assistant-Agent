# watchdog

`watchdog` 是执行层的安全护栏，负责识别无效重试、交互式阻塞和超时命令，并向 Planner 返回可操作的修复建议。

## 核心能力

### 语义断路器

对“命令 + 标准化错误输出”计算语义哈希，记录在滑动窗口中。当同一哈希在 `max_retry_window` 内重复出现时触发熔断，拒绝继续执行并返回 `CircuitBroken` 错误，强制上层重新规划。

### 超时与防挂起

- 所有命令受 `context.WithTimeout` 约束。
- 超时后向 TTY 发送 `Ctrl+C`，释放被交互式提示锁死的终端。
- 返回结构化提示，例如“命令可能在等待输入，请添加 `-y` 或非交互参数”。

### 异常分析

将底层退出码、超时、信号和 stderr 转换为带 Guidance 的错误对象，方便 Agent 在下一轮修正命令。

## 主要组件

| 组件 | 职责 |
|---|---|
| `CircuitBreaker` | 语义哈希、滑动窗口与熔断判定 |
| `TimeoutController` | 命令超时、TTY 中断与状态包装 |
| `ExceptionAnalyzer` | 错误分类、结构化提示与恢复建议 |
| `Watchdog` | 包装沙箱执行器，提供 `ExecuteSafe` |

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
    wd := watchdog.New(cfg, box)

    for i := 0; i < 6; i++ {
        _, err := wd.ExecuteSafe(ctx, "pip install unexisting-package")
        if err != nil && watchdog.IsCircuitBroken(err) {
            fmt.Println("circuit broken: replan required")
            break
        }
    }
}
```

## 测试

```bash
go test ./watchdog/...
```

测试覆盖重复错误熔断、超时中断和错误分类。真实 Docker 用例需要可用 daemon。

## 边界

断路器基于命令与错误的语义哈希，不理解业务语义。它只负责阻止明显重复的失败路径，不能替代 Planner 的全局重规划。
