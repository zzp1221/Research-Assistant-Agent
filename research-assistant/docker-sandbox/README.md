# docker-sandbox

简体中文 | [English](README.en.md)

独立的 Go 沙箱服务，为后端提供容器、进程、VM 与远程沙箱的统一执行能力。默认监听 `:8082`（可用 `SANDBOX_LISTEN_ADDR` 覆盖）。

## 架构位置

- [系统架构图](../../docs/diagrams/architecture.svg)（[draw.io](../../docs/diagrams/architecture.drawio)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.svg)（[draw.io](../../docs/diagrams/agent-execution-flow.drawio)）

沙箱是后端与真实执行环境之间唯一的边界：Backend 的 Agent 只调用 Sandbox Client；gateway 负责鉴权与转发；executor 才持有宿主权限并创建/执行沙箱。

Compose 将同一个镜像拆成两个角色：

- `docker-sandbox`（`SANDBOX_MODE=gateway`）：控制面，只做 Bearer 鉴权和 HTTP 转发，不挂载 Docker socket，也不挂载宿主 `/tmp`。
- `docker-sandbox-executor`（`SANDBOX_MODE=engine`，默认）：执行面，持有 Docker socket 和 `/tmp`，不发布宿主机端口。

## Provider 矩阵

沙箱通过统一 `engine.Provider` 接口支持四种执行后端，由 `SANDBOX_PROVIDER` 选择：

| Provider | 隔离 | 环境来源 | 网络 | GPU | 说明 |
|---|---|---|---|---|---|
| `docker` | 容器 | OCI 镜像 + digest | OS 级；默认离线 bridge | NVIDIA runtime 透传 | 默认；服务端与已有 Docker 环境 |
| `native` | 进程（AppContainer / lowil / Job Object） | uv 管理的 CPython + 每计划 venv | AppContainer 双 profile | 宿主 GPU | Windows 桌面零 Docker 路径 |
| `vm` | 虚拟机（WSL2 等） | VM 内 Docker Engine / OCI | VM 网络 | VM 内 GPU（视配置） | 无 Docker Desktop 但需完整 OCI |
| `remote` | 远程服务 | 由服务方提供 | 由服务方提供 | 由服务方提供 | GPU/大内存/长任务 |

选择逻辑：

- `SANDBOX_PROVIDER=auto`（默认）：Docker 可用时用 Docker；Docker 不可用且 native 可用时切 native。
- 显式指定 `native` / `vm` / `remote` 时，如果 Provider 不可用，健康检查返回 `ok=false` 并带 `provider_error`，不会静默回退。
- `ENABLE_OPENSANDBOX_FALLBACK=true` 且 Native Docker 不可用时，才在显式 `SANDBOX_ALLOW_UNLABELED_FALLBACK=true` 下使用旧 OpenSandbox fallback；该 fallback 无法施加租户/任务标签，仅限可信本地环境。

### Native Provider

- 使用 `uv` 管理可重定位 CPython，按计划创建 venv，记录 `runtime_hash`（Python 版本 + lock hash）到 `EnvironmentManifest`。
- Windows 支持 `appcontainer`（默认优先，隔离更强）与 `lowil`（兼容性更好）；通过 Job Object 限制进程数/内存/CPU 并回收进程树。
- 提供 `/api/v1/runtime/status`、`/api/v1/runtime/install`、`/api/v1/runtime/cleanup` 供桌面 RuntimePanel 使用。
- 系统级依赖（apt、libgl、ffmpeg、CUDA 精确对齐）不覆盖；能力不足返回 `blocked` + `missing_requirements=[sandbox_capability]`。

### VM Provider

- Windows 优先 WSL2；检测 `wsl.exe` 与可用发行版，做宿主路径 `D:\...` ↔ `/mnt/d/...` 翻译。
- 提供 create/exec/delete/usage；命令在 Linux 内执行，保留 POSIX 契约。
- 首次启用 WSL 可能需要管理员权限与重启，UI 会明确提示。

### Remote Provider

- 适配 OpenSandbox HTTP API；适合本地无法承载的 GPU/大内存任务。
- 需要网络与账号；凭据与配额由部署方管理。

## API

除 `GET /api/v1/health` 外，所有路由都要求 `Authorization: Bearer <SANDBOX_API_TOKEN>`；未配置 Token 时服务拒绝启动（fail-closed）。

| Method | Endpoint | 说明 |
|---|---|---|
| `GET` | `/api/v1/health` | 当前 Provider、能力矩阵与 fallback 健康状态 |
| `GET` | `/api/v1/capabilities` | 宿主能力：Docker、GPU、CPU、内存、磁盘、镜像 allowlist、安全 profile、Python 版本、探针 |
| `POST` | `/api/v1/images/ensure` | 校验并确保运行时镜像存在，返回 digest/size（Docker/VM） |
| `POST` | `/api/v1/sandboxes` | 创建沙箱，返回 `sandbox_id`（`dk-` / `os-` / `native-` / `vm-` / `remote-` 前缀） |
| `GET` | `/api/v1/sandboxes?plan_id=...` | 按 plan_id 列出沙箱（`plan_id` 必填，读取 plan 标签） |
| `POST` | `/api/v1/sandboxes/cleanup` | 按 plan_id 清理该计划的沙箱 |
| `DELETE` | `/api/v1/sandboxes/:id` | 删除沙箱 |
| `POST` | `/api/v1/sandboxes/:id/network` | 打开/关闭沙箱出口（`mode` 为 `bridge` 或 `none`；Docker） |
| `GET` | `/api/v1/sandboxes/:id/usage` | 网络字节/磁盘/进程用量与限制 |
| `POST` | `/api/v1/sandboxes/:id/python` | 执行 Python 代码 |
| `POST` | `/api/v1/sandboxes/:id/python/stream` | 流式执行 Python（NDJSON） |
| `POST` | `/api/v1/sandboxes/:id/commands` | 执行命令（argv 契约） |
| `POST` | `/api/v1/sandboxes/:id/commands/stream` | 流式执行命令（NDJSON） |
| `GET` | `/api/v1/runtime/status` | native 运行时缓存状态（Python 版本、venv 数、磁盘用量、安装阶段） |
| `POST` | `/api/v1/runtime/install` | 安装/准备指定 Python 运行时 |
| `POST` | `/api/v1/runtime/cleanup` | 清理运行时/venv 缓存 |

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
  "python_version": "3.11",
  "limits": {
    "max_image_bytes": 8589934592,
    "max_download_bytes": 8589934592,
    "max_disk_gb": 50,
    "max_runtime_sec": 3600
  }
}
```

默认 `network_mode=none`：Docker Provider 将容器加入内部网络 `research-assistant-offline`（`--internal`、无网关、禁用 ICC），无法解析或访问公网。依赖安装/联网执行时，后端先调用 `POST /sandboxes/:id/network {"mode":"bridge"}` 打开默认 bridge，命令结束后立即切回 `none`。镜像超过 `max_image_bytes`、网络接收超过 `max_download_bytes`、磁盘超过 `max_disk_gb` 或运行超过 `max_runtime_sec` 时返回 507 并附用量。

`task_id` 必填，且必须匹配 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`；`tenant_id` 默认 `local`，`plan_id` 可选，三者都必须匹配同一模式。创建后会写入 `research-assistant.sandbox=true`、`research-assistant.tenant-id`、`research-assistant.task-id` 和 `research-assistant.plan-id` 标签；执行/删除前会 `docker inspect` 校验标签，未绑定租户/任务的容器会被拒绝。Native Docker ID 必须匹配 `^[a-f0-9]{12,64}$`，其他 Provider 的 ID 也会做格式校验。`GET /sandboxes?plan_id=` 与 `POST /sandboxes/cleanup` 使用同一组 plan 标签，便于按计划回收运行时容器。

执行 Python：

```json
{ "code": "print('hello')" }
```

执行命令（argv 契约）：

```json
{ "cmd": ["python", "-c", "print('hello')"] }
```

> 命令统一使用 `Command{Argv, Cwd, Env, Shell, TimeoutSec, Stdin, MaxOutputKB}` 契约；native/vm Provider 负责把逻辑路径与解释器名翻译到平台实现。

## 配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `SANDBOX_API_TOKEN` | 必填 | 缺失时拒绝启动；非 health 请求要求 Bearer |
| `SANDBOX_MODE` | `engine` | `gateway` 或 `engine` |
| `SANDBOX_LISTEN_ADDR` | `:8082` | 监听地址 |
| `SANDBOX_UPSTREAM_URL` | `http://docker-sandbox-executor:8082` | gateway 的上游 executor 地址 |
| `SANDBOX_PROVIDER` | `auto` | `auto` / `docker` / `native` / `vm` / `remote` |
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
| `SANDBOX_READ_ONLY_ROOT` | `false` | 根文件系统只读（`strict` 强制开启） |
| `SANDBOX_REGISTRY_MIRROR` | 空 | 拉取镜像时使用的 registry mirror |
| `SANDBOX_PIP_CACHE_DIR` | 空 | 跨沙箱共享的 pip cache 目录 |
| `SANDBOX_TTL_MINUTES` | `120` | 沙箱最长存活时间，超时由 reaper 回收 |
| `SANDBOX_REAPER_INTERVAL_SECONDS` | `300` | reaper 扫描间隔（最小 10 秒） |
| `SANDBOX_WINDOWS_RUNNER` | `auto` | native Windows runner：`auto` / `appcontainer` / `lowil` / `raw` |
| `SANDBOX_DEFAULT_PYTHON` | 空 | native Provider 默认 Python 版本 |
| `SANDBOX_MEMORY_MB` / `SANDBOX_CPU_CORES` / `SANDBOX_MAX_PROCESSES` | 0 | native 资源限制（0 表示使用默认） |
| `OPEN_SANDBOX_URL` | 空 | OpenSandbox 服务地址 |
| `ENABLE_OPENSANDBOX_FALLBACK` | `false` | 是否启用 fallback |
| `SANDBOX_ALLOW_UNLABELED_FALLBACK` | `false` | 是否允许无标签的 OpenSandbox fallback（仅可信本地） |

## 测试

```bash
go test ./...
```

容器内 E2E：`test/e2e/sandbox-network.mjs` 验证离线网络 → 打开 → 联网 → 关闭；`test/e2e/round7-sandbox-deep.mjs` 覆盖命令、流式命令、按计划列表与删除；`test/e2e/desktop-smoke.mjs` 覆盖 Provider 能力矩阵；`test/e2e/sandbox-command.mjs` 覆盖 argv/legacy/截断。近期结果见 [docs/verification.md](../../docs/verification.md)。

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
- 默认网络是内部离线 bridge（无网关、无公网 DNS）；只有显式审批后的安装/联网节点会临时接入默认 bridge，执行完立即断开。
- `SANDBOX_SECURITY_PROFILE=strict` 启用非 root、只读 rootfs 与 tmpfs；生产环境仍建议使用 gVisor/Kata、镜像签名、rootless 与 socket-proxy。
- native Provider 的 AppContainer/lowil 隔离低于容器；仅用于单用户桌面与受控代码，不要在多租户服务器启用。
- Docker socket 只挂载在内部 executor；不要发布 executor 端口，也不要把不可信代码直接交给非隔离 runtime。
