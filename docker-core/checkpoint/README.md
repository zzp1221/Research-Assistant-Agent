# checkpoint

`checkpoint` 为环境配置过程提供物理快照与认知反馈两层容错：

1. **物理快照**：在 DAG 节点成功后执行 `Docker Commit`，保存完整文件系统与环境变量。
2. **回滚**：节点全部实现失败或环境损坏时，销毁受损容器并从上一个成功 imageID 恢复。
3. **负反馈注入**：把失败命令和回滚原因写入下一轮 LLM 上下文，阻止重复尝试同一路径。

## 主要组件

| 组件 | 职责 |
|---|---|
| `Manager` | 协调 Commit、Rollback 与容器生命周期 |
| `SnapshotRegistry` | 记录 DAG 节点 ID 与 imageID 的映射，支持链式回溯 |
| `FeedbackInjector` | 生成结构化负反馈，插入模型上下文 |

## 示例

```go
package main

import (
    "context"
    "fmt"

    "research-assistant-agent/docker-core/checkpoint"
)

func main() {
    ctx := context.Background()

    manager := checkpoint.NewManager(dockerClient, currentContainerID)

    snapshot, err := manager.Commit(ctx, "env-node-A")
    if err != nil {
        panic(err)
    }
    fmt.Printf("checkpoint: %s\n", snapshot.ImageID)

    if _, err := manager.Rollback(ctx, snapshot.ImageID); err != nil {
        panic(err)
    }

    feedback := manager.GenerateNegativeFeedback("pip install cuda-python")
    fmt.Println(feedback)
}
```

## 测试

```bash
go test ./checkpoint/...
```

`test/` 下的测试覆盖快照注册、回滚和反馈注入。涉及真实 Docker 的用例需要 daemon 可用。

## 设计约束

- 快照使用 Docker Commit，兼容性优先，但镜像层会占用磁盘空间；生产环境需要定期清理。
- 回滚会销毁当前容器，调用方应确保没有仍在运行的子进程。
- 负反馈只提供上下文提示，不替代上层的断路器与预算控制。
