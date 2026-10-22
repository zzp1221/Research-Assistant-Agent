# docker-core

`docker-core` 是一个独立的 Go 模块，用于验证和演示 Docker / gVisor 环境下的持久化沙箱、DAG 调度、状态快照与安全护栏。它不依赖 `research-assistant` 主应用，可单独构建和测试，也可以作为沙箱执行层的参考实现。

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

模块边界：

- `sandbox` 只封装 Docker 与 gVisor，不包含 Agent 决策逻辑。
- `checkpoint` 建立在 `sandbox` 之上，只负责 Commit / Rollback / 快照注册。
- `executor` 负责把环境配置目标拆成 DAG，并调度节点执行。
- `watchdog` 负责拦截死循环、交互式阻塞和重复失败路径。

## 核心设计

### 持久化交互式沙箱

通过 Go Docker SDK 劫持容器伪终端，让 bash 进程在容器内持续运行。跨指令的环境变量、工作目录和文件系统状态得以保留，适合渐进式环境配置。

### 双路日志

- 审计轨道：保留完整 ANSI 原始输出，供 UI 回放和人工核查。
- 推理轨道：经过 `Log-Truncator` 首尾截断，只把启动信息和错误尾部交给模型。

### DAG 调度与节点竞速

调度器按入度把就绪节点放入队列，由多个 Goroutine 并行执行。单个节点可以提供多种实现路径（例如 pip、conda、源码编译），第一个成功路径触发 `context.Cancel`，其余分支被终止。

### 快照与回滚

每个核心节点通过 Checker 验证后执行 `Docker Commit`，生成 imageID。节点全部失败或环境损坏时，销毁当前容器并从上一个成功快照恢复，同时向前端上下文注入负反馈，避免模型重复同一错误路径。

### 资源网关

`Gatekeeper` 使用加权信号量限制 I/O 与内存密集型任务，并用容量为 1 的 GPU 信号量保证同一时刻只有一个高显存任务运行。

## 快速开始

```bash
cd docker-core
go mod tidy
go test ./...
```

运行主程序：

```bash
go run .
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

`LLM_API_KEY` 环境变量会覆盖配置文件中的 `llm.api_key`。

## 测试

```bash
go test ./...
go test ./sandbox/...      # 沙箱生命周期与资源泄漏
go test ./executor/...     # DAG 调度与竞速
go test ./watchdog/...     # 断路器与超时
go test ./checkpoint/...   # 快照与回滚
```

真实 Docker 集成测试位于 `tests/`，需要可访问的 Docker daemon。测试前请确认 Docker 正常运行，并检查容器、网络端口和临时镜像是否被正确清理。

## 子模块说明

- [config](config/README.md)
- [sandbox](sandbox/README.md)
- [checkpoint](checkpoint/README.md)
- [executor](executor/README.md)
- [watchdog](watchdog/README.md)

## 安全边界

该模块面向受控研究环境。即使启用了 gVisor、capability 限制和只读挂载，Docker socket 仍然具有较高宿主机权限。不要把不受信任的租户直接接入该执行层；生产部署需要额外的网络隔离、镜像签名、资源配额和审计。
