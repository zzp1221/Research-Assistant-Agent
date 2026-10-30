# docker-sandbox

独立的 Go 沙箱服务，为后端提供容器生命周期管理与代码/命令执行能力。默认监听 `:8082`（可用 `SANDBOX_LISTEN_ADDR` 覆盖）。

## 架构位置

- [系统架构图](../../docs/diagrams/architecture.svg)（[draw.io](../../docs/diagrams/architecture.drawio)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.svg)（[draw.io](../../docs/diagrams/agent-execution-flow.drawio)）

沙箱是后端与真实 Docker 之间唯一的执行边界：Backend 的 Agent 只调用 Sandbox Client；gateway 负责鉴权与转发；executor 才持有 Docker socket 并创建/执行容器。

Compose 将同一个镜像拆成两个角色：

- `docker-sandbox`（`SANDBOX_MODE=gateway`）：控制面，只做 Bearer 鉴权和 HTTP 转发，不挂载 Docker socket，也不挂载宿主 `/tmp`。
- `docker-sandbox-executor`（`SANDBOX_MODE=engine`，默认）：执行面，持有 Docker socket 和 `/tmp`，不发布宿主机端口。

## 执行引擎

| 引擎 | 说明 |
|---|---|
| Native Docker | 默认引擎，通过 Docker CLI 创建容器、挂载工作区并执行命令 |
| OpenSandbox | 可选 fallback，通过 `OPEN_SANDBOX_URL` 接入外部沙箱服务 |

Native Docker 优先；只有 `ENABLE_OPENSANDBOX_FALLBACK=true`、Native Docker 不可用，并且显式设置 `SANDBOX_ALLOW_UNLABELED_FALLBACK=true` 时才使用 OpenSandbox（该 fallback 无法施加租户/任务标签，仅限可信本地环境）。

## API

除 `GET /api/v1/health` 外，所有路由都要求 `Authorization: Bearer <SANDBOX_API_TOKEN>`；未配置 Token 时服务拒绝启动（fail-closed）。

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/v1/health` | Docker 与 fallback 健康状态 |
| `GET` | `/api/v1/capabilities` | 宿主能力：Docker、GPU、CPU、内存、磁盘、镜像 allowlist、安全 profile |
| `POST` | `/api/v1/images/ensure` | 校验并确保运行时镜像存在，返回 digest/size |
| `POST` | `/api/v1/sandboxes` | 创建沙箱，返回 `sandbox_id`（`dk-` / `os-` 前缀） |
| `GET` | `/api/v1/sandboxes?plan_id=...` | 按 plan_id 列出沙箱（`plan_id` 必填，读取 plan 标签） |
| `POST` | `/api/v1/sandboxes/cleanup` | 按 plan_id 清理该计划的沙箱 |
| `DELETE` | `/api/v1/sandboxes/:id` | 删除沙箱 |
| `POST` | `/api/v1/sandboxes/:id/network` | 打开/关闭沙箱出口（`mode` 为 `bridge` 或 `none`；仅 Native Docker） |
| `GET` | `/api/v1/sandboxes/:id/usage` | 查看网络字节/磁盘用量与限制 |
| `POST` | `/api/v1/sandboxes/:id/python` | 执行 Python 代码 |
| `POST` | `/api/v1/sandboxes/:id/python/stream` | 流式执行 Python（NDJSON） |
| `POST` | `/api/v1/sandboxes/:id/commands` | 执行命令 |
| `POST` | `/api/v1/sandboxes/:id/commands/stream` | 流式执行命令（NDJSON） |

创建请求：

```json
{
  "image": "python:3.11-bullseye",
  "mount_path": "/tmp/workspace",
  "tenant_id": "tenant-a",
  "task_id": "task-123",
  "plan_id": "plan-456",
  "network_mode": "none",
  "runtime": "",
  "limits": {
    "max_image_bytes": 8589934592,
    "max_download_bytes": 8589934592,
    "max_disk_gb": 50,
    "max_runtime_sec": 3600
  }
}
```

默认 `network_mode=none`：容器加入内部网络 `research-assistant-offline`（`--internal`、无网关、禁用 ICC），无法解析或访问公网。依赖安装/联网执行时，后端先调用 `POST /sandboxes/:id/network {"mode":"bridge"}` 打开默认 bridge，命令结束后立即切回 `none`。镜像超过 `max_image_bytes`、网络接收超过 `max_download_bytes`、磁盘超过 `max_disk_gb` 或运行超过 `max_runtime_sec` 时返回 507 并附用量。

`task_id` 必填，且必须匹配 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`；`tenant_id` 默认 `local`，`plan_id` 可选，三者都必须匹配同一模式。创建后会写入 `research-assistant.sandbox=true`、`research-assistant.tenant-id`、`research-assistant.task-id` 和 `research-assistant.plan-id` 标签；执行/删除前会 `docker inspect` 校验标签，未绑定租户/任务的容器会被拒绝。Native Docker ID 必须匹配 `^[a-f0-9]{12,64}$`，OpenSandbox ID 也会做格式校验。`GET /sandboxes?plan_id=` 与 `POST /sandboxes/cleanup` 使用同一组 plan 标签，便于按计划回收运行时容器。

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
| `SANDBOX_LISTEN_ADDR` | `:8082` | 监听地址 |
| `SANDBOX_UPSTREAM_URL` | `http://docker-sandbox-executor:8082` | gateway 的上游 executor 地址 |
| `SANDBOX_DOCKER_GPUS` | 空 | GPU 透传，例如 `all` |
| `SANDBOX_CPU_LIMIT` | `2` | CPU 配额 |
| `SANDBOX_MEMORY_LIMIT` | `4g` | 内存上限 |
| `SANDBOX_PIDS_LIMIT` | `256` | PID 上限 |
| `SANDBOX_NETWORK_MODE` | `none` | 默认网络模式；`none` 使用内部离线网络 |
| `SANDBOX_ALLOW_NETWORK` | `true` | 是否允许显式打开 bridge（审批后） |
| `SANDBOX_OFFLINE_NETWORK` | `research-assistant-offline` | 离线内部网络名 |
| `SANDBOX_SECURITY_PROFILE` | `standard` | `strict` = 非 root + 只读 rootfs + tmpfs |
| `SANDBOX_RUNTIME` | 空 | 指定容器 runtime（如 `runsc`，需宿主已安装） |
| `SANDBOX_ALLOWED_RUNTIMES` | `runc,runsc,nvidia` | runtime 允许列表 |
| `SANDBOX_WORKSPACE_ROOTS` | `/tmp` | 允许挂载的工作区根目录（按 `filepath.SplitList` 解析） |
| `SANDBOX_IMAGE_ALLOWLIST` | 空 | 允许的镜像前缀，逗号分隔；为空表示不限制 |
| `SANDBOX_CONTAINER_USER` | 空 | 容器内运行用户 |
| `SANDBOX_READ_ONLY_ROOT` | `false` | 根文件系统只读（`strict` 会强制开启） |
| `SANDBOX_REGISTRY_MIRROR` | 空 | 拉取镜像时使用的 registry mirror |
| `SANDBOX_PIP_CACHE_DIR` | 空 | 跨沙箱共享的 pip cache 目录 |
| `SANDBOX_TTL_MINUTES` | `120` | 沙箱最长存活时间，超时由 reaper 回收 |
| `SANDBOX_REAPER_INTERVAL_SECONDS` | `300` | reaper 扫描间隔（最小 10 秒） |
| `OPEN_SANDBOX_URL` | 空 | OpenSandbox 服务地址 |
| `ENABLE_OPENSANDBOX_FALLBACK` | `false` | 是否启用 fallback |
| `SANDBOX_ALLOW_UNLABELED_FALLBACK` | `false` | 是否允许无标签的 OpenSandbox fallback（仅可信本地） |

## 测试

```bash
go test ./...
```

容器内 E2E：`test/e2e/sandbox-network.mjs` 验证离线网络 → 打开 → 联网 → 关闭；`test/e2e/round7-sandbox-deep.mjs` 覆盖命令、流式命令、按计划列表与删除。近期结果见 [docs/verification.md](../../docs/verification.md)。

## 构建与运行

```bash
go mod tidy
go build -o bin/sandbox .
SANDBOX_API_TOKEN=dev-token ./bin/sandbox
```

或使用项目脚本：

```bash
../scripts/unix/start-sandbox.sh
```

## 安全边界

- 只有内部 executor 挂载 Docker socket；gateway 不持有 socket，也不挂载宿主工作区。executor 仍等价于较高宿主机权限，只应通过内部网络访问。
- 默认限制 CPU、内存、PID、capability、镜像与挂载路径，并强制租户/任务标签，但不等同于强多租户隔离。
- 默认网络是内部离线 bridge（无网关、无公网 DNS）；只有显式审批后的安装/联网节点会临时接入默认 bridge，执行完立即断开。`test/e2e/sandbox-network.mjs` 在容器内验证“离线 → 打开 → 联网 → 关闭 → 离线”。
- `SANDBOX_SECURITY_PROFILE=strict` 启用非 root、只读 rootfs 与 tmpfs；生产环境仍建议使用 gVisor/Kata、镜像签名、rootless 与 socket-proxy。
- Docker socket 只挂载在内部 executor；它等价于宿主机高权限。不要发布 executor 端口，也不要把不可信代码直接交给非隔离 runtime。
