# docker-core

`docker-core` 是一个独立的 Go 模块，用于验证和演示 Docker / gVisor 环境下的持久化沙箱、DAG 调度、状态快照与安全护栏。它不依赖 `research-assistant` 主应用，可单独构建和测试，也可以作为沙箱执行层的参考实现。

## 与主项目的关系

- [系统架构图](../docs/diagrams/architecture.drawio)（[SVG 预览](../docs/diagrams/architecture.svg)）
- [Agent 执行流程图](../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../docs/diagrams/agent-execution-flow.svg)）

`docker-core` 是沙箱/调度内核的研究与参考实现；产品运行链使用 `research-assistant/docker-sandbox` 的 gateway/executor 组合。两者共享“隔离执行 + 快照/回滚 + 资源护栏”的设计目标，但模块独立、可分别构建和测试。

## 模块定位

```text
docker-core/
├── config/       TOML 配置加载与默认值
├── sandbox/      Docker 容器生命周期、TTY 长连接与双路日志
├── checkpoint/   Docker Commit 快照、回滚与负反馈注入
├── executor/     DAG 拓扑调度、节点内竞速与资源网关
├── watchdog/     语义断路器、超时控制与异常分析
└── tests/        真实 Docker 集成测试
```

`main.go` 目前只是占位入口；该模块以 Go 库的形式被 `tests/` 与上层集成代码使用，不是可直接运行的服务。

模块边界：

- `sandbox` 只封装 Docker 与 gVisor，不包含 Agent 决策逻辑。
- `checkpoint` 建立在 `sandbox` 之上，只负责 Commit / Rollback / 快照注册与负反馈文本。
- `executor` 负责把环境配置目标拆成 DAG，并调度节点执行。
- `watchdog` 负责拦截死循环、交互式阻塞和重复失败路径。

## 核心设计

### 持久化交互式沙箱

通过 Go Docker SDK 劫持容器伪终端，让 bash 进程在容器内持续运行。跨指令的环境变量、工作目录和文件系统状态得以保留，适合渐进式环境配置。

`Sandbox.Execute` 返回命令的原始输出字符串；`session.Execute` 内部的 `ExecResult` 还带有 `InferenceSummary` 字段，预留给上层用 `Truncator` 生成首尾截断摘要（当前 `Sandbox.Execute` 尚未自动填充该字段）。

### 双路日志

- 审计轨道：完整 ANSI 原始输出按行落盘到 `AuditLogDir/audit_*.log`，供回放和人工核查。
- 推理轨道：`StreamSplitter` 在内存中缓冲输出，由 `Session.Execute` 按 delimiter 返回；可用 `Truncator` 做首尾截断，只把启动信息和错误尾部交给模型。

### DAG 调度与节点竞速

调度器按入度把就绪节点放入队列，由多个 Goroutine 并行执行。单个节点可以提供多种实现路径（例如 pip、conda、源码编译），第一个成功路径触发 `context.Cancel`，其余分支被终止。每个节点默认超时 15 分钟（`Engine.NodeTimeout`）。

### 快照与回滚

每个核心节点成功后，`Engine` 调用 `CheckpointManager.Commit` 执行 `Docker Commit`，生成 imageID 并登记到 `SnapshotRegistry`。节点失败时，`checkpoint.Manager` 可基于快照重启容器并重建 TTY 会话，同时用 `FeedbackInjector` 生成负反馈文本，避免模型重复同一错误路径。

### 资源网关

`Gatekeeper` 使用加权信号量限制 I/O 与内存密集型任务，并用容量为 1 的 GPU 信号量保证同一时刻只有一个高显存任务运行。

## 快速开始

```bash
cd docker-core
go mod tidy
go test ./...
```

配置模板位于 `config/config-template.toml`：

```toml
[llm]
model = "deepseek-chat"
base_url = "https://api.deepseek.com"
api_key = "your-api-key"

[resource]
max_concurrent_io = 10
max_gpu_tasks = 1
```

加载配置：

```go
cfg, err := config.LoadConfig("config/config.toml")
if err != nil {
    log.Fatal(err)
}
```

`LLM_API_KEY` 环境变量会覆盖配置文件中的 `llm.api_key`。`config.toml` 已被 `.gitignore` 忽略，不要提交真实密钥。

## 使用示例

创建沙箱并跨命令保持状态：

```go
box, err := sandbox.New(ctx, sandbox.DefaultConfig())
if err != nil {
    panic(err)
}
defer box.Close()

if _, err := box.Execute(ctx, "export MY_VAR=hello_agent"); err != nil {
    panic(err)
}
out, err := box.Execute(ctx, "echo $MY_VAR")
fmt.Println(out) // hello_agent
```

快照与回滚：

```go
manager := checkpoint.NewManager(box)

imageID, err := manager.Commit(ctx, "env-node-A")
if err != nil {
    panic(err)
}
if err := manager.Rollback(ctx, imageID); err != nil {
    panic(err)
}
feedback := manager.RecordFailureAndGetFeedback("env-node-A", "pip install cuda-python", "no matching distribution")
fmt.Println(feedback)
```

DAG 调度 + 资源网关：

```go
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
```

`engine := executor.NewEngine(gatekeeper, box, manager, agent)` 的 `box` 需满足 `executor.Sandbox`，`manager` 需满足 `executor.CheckpointManager`，`agent` 需满足 `executor.Agent`（可为 `nil`，此时空命令节点不会生成策略）。

## 测试

```bash
go test ./...
go test ./sandbox/...      # 沙箱生命周期与资源泄漏
go test ./executor/...     # DAG 调度与竞速
go test ./watchdog/...     # 断路器与超时
go test ./checkpoint/...   # 快照与回滚
```

真实 Docker 集成测试位于 `sandbox/test/` 与 `tests/`，需要设置 `SANDBOX_INTEGRATION_TEST=1` 或 `REAL_INTEGRATION_TEST=1`（后者还需要有效 LLM Key 与 `config/config.toml`）。测试前请确认 Docker 正常运行，并检查容器、网络端口和临时镜像是否被正确清理。

构建与单元测试可在 `golang:1.26.1-bookworm` 容器内复跑；最近一次结果见 [docs/verification.md](../docs/verification.md)。

## 子模块说明

- [config](config/README.md)
- [sandbox](sandbox/README.md)
- [checkpoint](checkpoint/README.md)
- [executor](executor/README.md)
- [watchdog](watchdog/README.md)

## 安全边界

该模块面向受控研究环境。即使启用了 gVisor、capability 限制和只读挂载，Docker socket 仍然具有较高宿主机权限。不要把不受信任的租户直接接入该执行层；生产部署需要额外的网络隔离、镜像签名、资源配额和审计。
