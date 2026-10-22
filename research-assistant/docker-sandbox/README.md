# docker-sandbox

独立的 Go 沙箱服务，为后端提供容器生命周期管理与代码/命令执行能力。默认监听 `:8082`。

## 执行引擎

| 引擎 | 说明 |
|---|---|
| Native Docker | 默认引擎，通过 Docker SDK 创建容器、挂载工作区并执行命令 |
| OpenSandbox | 可选 fallback，通过 `OPEN_SANDBOX_URL` 接入外部沙箱服务 |

Native Docker 优先；只有 `ENABLE_OPENSANDBOX_FALLBACK=true` 且 Native Docker 不可用时才使用 OpenSandbox。

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
  "mount_path": "/tmp/workspace"
}
```

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
| `SANDBOX_API_TOKEN` | 空 | 非空时要求 `Authorization: Bearer <token>` |
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

- 容器挂载 Docker socket，等价于较高宿主机权限，只应在本机或受控网络使用。
- 默认限制 CPU、内存、PID、capability、镜像与挂载路径，但不等同于强多租户隔离。
- 生产环境建议使用 gVisor、只读根文件系统、镜像签名和独立网络。
