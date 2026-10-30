# executor

`executor` 是环境配置任务的 DAG 调度与并发控制核心。它把目标拆成有向无环图，按拓扑顺序调度节点，并支持单节点多实现竞速。

## 核心能力

### 拓扑驱动调度

调度器计算每个节点的入度，将所有入度为 0 的节点放入 `ReadyQueue`，由多个 Worker 并行消费。节点完成后递减后继节点入度，直到图执行完毕或出现不可恢复错误。每个节点默认超时 15 分钟（`Engine.NodeTimeout`）。

### 节点内竞速

一个节点可以提供多条实现命令。执行器并发启动这些路径，第一个成功返回的实现触发 `context.Cancel`，其余分支被终止，成功结果被固定。节点命令为空时会向 `Agent.GenerateStrategies` 请求候选策略。

### Gatekeeper 资源网关

- GPU 信号量容量为 1，防止多个 CUDA 任务同时占用显存。
- 加权信号量限制 I/O 和内存密集型任务的全局并发。

## 主要组件

| 组件 | 职责 |
|---|---|
| `DAG` / `Node` | 图数据结构、依赖关系与入度 |
| `Engine` | 拓扑调度循环、节点超时、Checkpoint 回调 |
| `Gatekeeper` | 基于 `golang.org/x/sync/semaphore` 的 I/O/GPU 配额 |
| `ExecutorAgent` | 通过 OpenAI-compatible LLM 生成执行策略与计划 |

## 示例

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

`NewEngine(gatekeeper, box, checkpointManager, agent)` 中 `box` 需实现 `Execute(ctx, cmd) (string, error)` 与 `Close() error`；`agent` 可为 `nil`。

## 测试

```bash
go test ./executor/...
```

测试覆盖 DAG 构建、拓扑调度、节点竞速和资源配额。需要真实 Docker 或 LLM 的用例默认跳过，启用 `REAL_INTEGRATION_TEST=1` 后可运行。

## 边界

`executor` 只负责调度与资源控制，不判断命令的业务正确性。节点是否成功由返回码、`Watchdog` 的 Checker 或上层验证器决定。
