# executor

`executor` 是环境配置任务的 DAG 调度与并发控制核心。它把目标拆成有向无环图，按拓扑顺序调度节点，并支持单节点多实现竞速。

## 核心能力

### 拓扑驱动调度

调度器计算每个节点的入度，将所有入度为 0 的节点放入 `ReadyQueue`，由多个 Worker 并行消费。节点完成后递减后继节点入度，直到图执行完毕或出现不可恢复错误。

### 节点内竞速

一个节点可以提供多条实现命令。执行器并发启动这些路径，第一个成功返回的实现触发 `context.Cancel`，其余分支被终止，成功结果被固定。

### Gatekeeper 资源网关

- GPU 信号量容量为 1，防止多个 CUDA 任务同时占用显存。
- 加权信号量限制 I/O 和内存密集型任务的全局并发。
- 资源配额与 Linux CFS 协同，避免瞬时高并发压垮宿主机。

## 主要组件

| 组件 | 职责 |
|---|---|
| `Graph` / `Node` | DAG 数据结构、依赖关系与环检测 |
| `Scheduler` | 入度计算、拓扑排序、ReadyQueue 生命周期 |
| `Racer` | 单节点多协程启动、成功判定与取消 |
| `Gatekeeper` | 基于 `golang.org/x/sync/semaphore` 的资源配额 |
| `Engine` | 组合调度器、执行器与沙箱对象 |

## 示例

```go
package main

import (
    "context"
    "fmt"

    "research-assistant-agent/docker-core/executor"
)

func main() {
    ctx := context.Background()

    gatekeeper := executor.NewGatekeeper(executor.ResourceConfig{
        MaxConcurrentIO: 10,
        MaxGPUTasks:     1,
    })

    engine := executor.NewEngine(gatekeeper, box)

    graph := executor.NewDAG()
    base := graph.AddNode("install_base", []string{"apt-get install -y build-essential"})
    opencv := graph.AddNode("install_opencv", []string{
        "pip install opencv-python",
        "conda install -c conda-forge opencv",
    })
    graph.AddEdge(base, opencv)

    if err := engine.ExecuteGraph(ctx, graph); err != nil {
        fmt.Printf("execute failed: %v\n", err)
    }
}
```

## 测试

```bash
go test ./executor/...
```

测试覆盖 DAG 构建、拓扑调度、节点竞速和资源配额。需要 Docker 的用例会在 daemon 不可用时跳过或失败，具体以测试输出为准。

## 边界

`executor` 只负责调度与资源控制，不判断命令的业务正确性。节点是否成功由返回码、Checker 或上层验证器决定。
