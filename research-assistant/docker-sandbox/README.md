# docker-sandbox

独立的 Go 沙箱服务，为后端提供容器生命周期管理与代码/命令执行能力。默认监听 `:8082`。

## 架构位置

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

沙箱是后端与真实 Docker 之间唯一的执行边界：Backend 的 Agent 只调用 Sandbox Client；gateway 负责鉴权与转发；executor 才持有 Docker socket 并创建/执行容器。

Compose 将同一个镜像拆成两个角色：

- `docker-sandbox`（`SANDBOX_MODE=gateway`）：控制面，只做 Bearer 鉴权和 HTTP 转发，不挂载 Docker socket，也不挂载宿主 `/tmp`。
- `docker-sandbox-executor`（`SANDBOX_MODE=engine`）：执行面，持有 Docker socket 和 `/tmp`，不发布宿主机端口。

## 执行引擎

| 引擎 | 说明 |
|---|---|
| Native Docker | 默认引擎，通过 Docker CLI 创建容器、挂载工作区并执行命令 |
| OpenSandbox | 可选 fallback，通过 `OPEN_SANDBOX_URL` 接入外部沙箱服务 |

Native Docker 优先；只有 `ENABLE_OPENSANDBOX_FALLBACK=true`、Native Docker 不可用，并且显式设置 `SANDBOX_ALLOW_UNLABELED_FALLBACK=true` 时才使用 OpenSandbox（该 fallback 无法施加租户/任务标签）。

## API

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/v1/health` | Docker 与 fallback 健康状态 |
| `POST` | `/api/v1/sandboxes` | 创建沙箱，返回 `sandbox_id` |
| `DELETE` | `/api/v1/sandboxes/:id` | 删除沙箱 |
| `POST` | `/api/v1/sandboxes/:id/python` | 执行 Python 代码 |
| `POST` | `/api/v1/sandboxes/:id/python/stream` | 流式执行 Python |
| `POST` | `/api/v1/sandboxes/:id/commands` | 执行命令 |
| `POST` | `/api/v1/sandboxes/:id/commands/stream` | 流式执行命令 |

创建请求：

```json
{
  "image": "python:3.9-bullseye",
  "mount_path": "/tmp/workspace",
  "tenant_id": "tenant-a",
  "task_id": "task-123",
  "plan_id": "plan-456"
}
```

`task_id` 必填，且必须匹配 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`。创建后会写入 `research-assistant.sandbox=true`、`research-assistant.tenant-id`、`research-assistant.task-id` 和 `research-assistant.plan-id` 标签；执行/删除前会 `docker inspect` 校验标签，未绑定租户/任务的容器会被拒绝。Native Docker ID 必须匹配 `^[a-f0-9]{12,64}$`，OpenSandbox ID 也会做格式校验。

执行 Python：

```json
{
  "code": "print('hello')"
}
```

执行命令：

```json
{
  "cmd": ["python", "-c", "print('hello')"]
}
```

## 配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `SANDBOX_API_TOKEN` | 必填 | 缺失时拒绝启动；非 health 请求要求 `Authorization: Bearer <token>` |
| `SANDBOX_MODE` | `engine` | `gateway` 或 `engine` |
| `SANDBOX_UPSTREAM_URL` | `http://docker-sandbox-executor:8082` | gateway 的上游 executor 地址 |
| `SANDBOX_DOCKER_GPUS` | 空 | GPU 透传，例如 `all` |
| `SANDBOX_CPU_LIMIT` | `2` | CPU 配额 |
| `SANDBOX_MEMORY_LIMIT` | `4g` | 内存上限 |
| `SANDBOX_PIDS_LIMIT` | `256` | PID 上限 |
| `SANDBOX_NETWORK_MODE` | `bridge` | 容器网络模式 |
| `SANDBOX_WORKSPACE_ROOTS` | `/tmp` | 允许挂载的工作区根目录 |
| `SANDBOX_IMAGE_ALLOWLIST` | 空 | 允许的镜像前缀，逗号分隔 |
| `SANDBOX_CONTAINER_USER` | 空 | 容器内运行用户 |
| `SANDBOX_READ_ONLY_ROOT` | `false` | 根文件系统只读 |
| `OPEN_SANDBOX_URL` | 空 | OpenSandbox 服务地址 |
| `ENABLE_OPENSANDBOX_FALLBACK` | `false` | 是否启用 fallback |

## 构建与运行

```bash
go mod tidy
go build -o bin/sandbox .
./bin/sandbox
```

或使用项目脚本：

```bash
../scripts/unix/start-sandbox.sh
```

## 安全边界

- 只有内部 executor 挂载 Docker socket；gateway 不持有 socket，也不挂载宿主工作区。executor 仍等价于较高宿主机权限，只应通过内部网络访问。
- 默认限制 CPU、内存、PID、capability、镜像与挂载路径，并强制租户/任务标签，但不等同于强多租户隔离。
- 生产环境建议使用 gVisor、只读根文件系统、镜像签名、独立网络和 rootless/socket-proxy 方案。
