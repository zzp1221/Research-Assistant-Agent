# docker-sandbox

[简体中文](README.md) | English

A standalone Go sandbox service that gives the backend a unified execution layer for containers, processes, VMs, and remote sandboxes. It listens on `:8082` by default (override with `SANDBOX_LISTEN_ADDR`).

## Architectural position

- [System architecture](../../docs/diagrams/architecture.svg) ([draw.io](../../docs/diagrams/architecture.drawio))
- [Agent execution flow](../../docs/diagrams/agent-execution-flow.svg) ([draw.io](../../docs/diagrams/agent-execution-flow.drawio))

The sandbox is the only boundary between the backend and real execution: backend agents call only the Sandbox Client; the gateway authenticates and proxies; the executor holds host privileges and creates/runs sandboxes.

Compose splits the same image into two roles:

- `docker-sandbox` (`SANDBOX_MODE=gateway`): control plane, Bearer auth and HTTP proxying only; no Docker socket and no host `/tmp`.
- `docker-sandbox-executor` (`SANDBOX_MODE=engine`, default): execution plane, holds the Docker socket and `/tmp`; publishes no host port.

## Provider matrix

The sandbox supports four execution backends behind a unified `engine.Provider` interface, selected by `SANDBOX_PROVIDER`:

| Provider | Isolation | Environment source | Network | GPU | Notes |
|---|---|---|---|---|---|
| `docker` | Container | OCI image + digest | OS-level; offline bridge by default | NVIDIA runtime passthrough | Default; servers and existing Docker |
| `native` | Process (AppContainer / lowil / Job Object) | uv-managed CPython + per-plan venv | AppContainer dual profiles | Host GPU | Zero-Docker Windows desktop path |
| `vm` | Virtual machine (WSL2, etc.) | Docker Engine / OCI inside VM | VM network | GPU inside VM (config-dependent) | No Docker Desktop but full OCI needed |
| `remote` | Remote service | Provided by the service | Provided by the service | Provided by the service | GPU / large memory / long jobs |

Selection:

- `SANDBOX_PROVIDER=auto` (default): use Docker when available; otherwise switch to native when available.
- Explicit `native` / `vm` / `remote`: if the provider is unavailable, health returns `ok=false` with `provider_error`; no silent fallback.
- Only when `ENABLE_OPENSANDBOX_FALLBACK=true` and Native Docker is unavailable, the legacy OpenSandbox fallback is used if `SANDBOX_ALLOW_UNLABELED_FALLBACK=true`; that fallback cannot apply tenant/task labels and is limited to trusted local environments.

### Native provider

- Uses `uv` to manage relocatable CPython and creates a venv per plan; records `runtime_hash` (Python version + lock hash) in `EnvironmentManifest`.
- On Windows supports `appcontainer` (preferred, stronger isolation) and `lowil` (better compatibility); Job Objects limit processes/memory/CPU and reap the process tree.
- Exposes `/api/v1/runtime/status`, `/api/v1/runtime/install`, and `/api/v1/runtime/cleanup` for the desktop RuntimePanel.
- System-level dependencies (apt, libgl, ffmpeg, exact CUDA alignment) are not covered; missing capabilities return `blocked` + `missing_requirements=[sandbox_capability]`.

### VM provider

- Prefers WSL2 on Windows; detects `wsl.exe` and available distributions and translates host paths `D:\...` ↔ `/mnt/d/...`.
- Provides create/exec/delete/usage; commands run inside Linux, preserving the POSIX contract.
- First-time WSL enablement may require administrator rights and a reboot; the UI states this explicitly.

### Remote provider

- Adapts the OpenSandbox HTTP API; suitable for GPU/large-memory jobs that a local machine cannot host.
- Requires network access and an account; credentials and quotas are managed by the deployment owner.

## API

Except for `GET /api/v1/health`, every route requires `Authorization: Bearer <SANDBOX_API_TOKEN>`; the service refuses to start without a token (fail-closed).

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/health` | Current provider, capability matrix, and fallback health |
| `GET` | `/api/v1/capabilities` | Host capabilities: Docker, GPU, CPU, memory, disk, image allowlist, security profile, Python versions, probes |
| `POST` | `/api/v1/images/ensure` | Validate and ensure a runtime image; returns digest/size (Docker/VM) |
| `POST` | `/api/v1/sandboxes` | Create a sandbox; returns `sandbox_id` (`dk-` / `os-` / `native-` / `vm-` / `remote-` prefix) |
| `GET` | `/api/v1/sandboxes?plan_id=...` | List sandboxes by plan_id (`plan_id` required; reads plan labels) |
| `POST` | `/api/v1/sandboxes/cleanup` | Clean up all sandboxes for a plan_id |
| `DELETE` | `/api/v1/sandboxes/:id` | Delete a sandbox |
| `POST` | `/api/v1/sandboxes/:id/network` | Enable/disable egress (`mode` = `bridge` or `none`; Docker) |
| `GET` | `/api/v1/sandboxes/:id/usage` | Network bytes / disk / process usage and limits |
| `POST` | `/api/v1/sandboxes/:id/python` | Execute Python |
| `POST` | `/api/v1/sandboxes/:id/python/stream` | Stream Python execution (NDJSON) |
| `POST` | `/api/v1/sandboxes/:id/commands` | Execute a command (argv contract) |
| `POST` | `/api/v1/sandboxes/:id/commands/stream` | Stream command execution (NDJSON) |
| `GET` | `/api/v1/runtime/status` | Native runtime cache status (Python versions, venv count, disk usage, install stage) |
| `POST` | `/api/v1/runtime/install` | Install/prepare a Python runtime |
| `POST` | `/api/v1/runtime/cleanup` | Clean the runtime/venv cache |

Create request:

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

Default `network_mode=none`: the Docker provider attaches the container to the internal `research-assistant-offline` network (`--internal`, no gateway, ICC disabled), so it cannot resolve or reach the public internet. For dependency installation or networked execution, the backend calls `POST /sandboxes/:id/network {"mode":"bridge"}`, then switches back to `none` immediately after the command. Exceeding `max_image_bytes`, `max_download_bytes`, `max_disk_gb`, or `max_runtime_sec` returns 507 with usage details.

`task_id` is required and must match `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`; `tenant_id` defaults to `local`, and `plan_id` is optional; all three must match the same pattern. Creation writes `research-assistant.sandbox=true`, `research-assistant.tenant-id`, `research-assistant.task-id`, and `research-assistant.plan-id` labels; execution/deletion runs `docker inspect` and rejects containers not bound to the tenant/task. Native Docker IDs must match `^[a-f0-9]{12,64}$`; other providers validate their IDs too. `GET /sandboxes?plan_id=` and `POST /sandboxes/cleanup` share the same plan labels for per-plan reclamation.

Execute Python:

```json
{ "code": "print('hello')" }
```

Execute a command (argv contract):

```json
{ "cmd": ["python", "-c", "print('hello')"] }
```

> Commands use the unified `Command{Argv, Cwd, Env, Shell, TimeoutSec, Stdin, MaxOutputKB}` contract; native/vm providers translate logical paths and interpreter names to platform-specific implementations.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `SANDBOX_API_TOKEN` | Required | Refuses to start if missing; non-health requests require Bearer |
| `SANDBOX_MODE` | `engine` | `gateway` or `engine` |
| `SANDBOX_LISTEN_ADDR` | `:8082` | Listen address |
| `SANDBOX_UPSTREAM_URL` | `http://docker-sandbox-executor:8082` | Gateway upstream executor |
| `SANDBOX_PROVIDER` | `auto` | `auto` / `docker` / `native` / `vm` / `remote` |
| `SANDBOX_DOCKER_GPUS` | empty | GPU passthrough, e.g. `all` |
| `SANDBOX_CPU_LIMIT` | `2` | CPU quota |
| `SANDBOX_MEMORY_LIMIT` | `4g` | Memory limit |
| `SANDBOX_PIDS_LIMIT` | `256` | PID limit |
| `SANDBOX_NETWORK_MODE` | `none` | Default network mode; `none` uses the internal offline network |
| `SANDBOX_ALLOW_NETWORK` | `true` | Whether explicitly opening the bridge (after approval) is allowed |
| `SANDBOX_OFFLINE_NETWORK` | `research-assistant-offline` | Offline internal network name |
| `SANDBOX_SECURITY_PROFILE` | `standard` | `strict` = non-root + read-only rootfs + tmpfs |
| `SANDBOX_RUNTIME` | empty | Container runtime (e.g. `runsc`, must be installed on the host) |
| `SANDBOX_ALLOWED_RUNTIMES` | `runc,runsc,nvidia` | Allowed runtimes |
| `SANDBOX_WORKSPACE_ROOTS` | `/tmp` | Allowed workspace roots for mounts (`filepath.SplitList`) |
| `SANDBOX_IMAGE_ALLOWLIST` | empty | Allowed image prefixes, comma-separated; empty means unrestricted |
| `SANDBOX_CONTAINER_USER` | empty | Container user |
| `SANDBOX_READ_ONLY_ROOT` | `false` | Read-only rootfs (`strict` forces it on) |
| `SANDBOX_REGISTRY_MIRROR` | empty | Registry mirror for image pulls |
| `SANDBOX_PIP_CACHE_DIR` | empty | Shared pip cache directory |
| `SANDBOX_TTL_MINUTES` | `120` | Maximum sandbox lifetime; reaper cleans up after expiry |
| `SANDBOX_REAPER_INTERVAL_SECONDS` | `300` | Reaper interval (minimum 10 s) |
| `SANDBOX_WINDOWS_RUNNER` | `auto` | Native Windows runner: `auto` / `appcontainer` / `lowil` / `raw` |
| `SANDBOX_DEFAULT_PYTHON` | empty | Default Python version for the native provider |
| `SANDBOX_MEMORY_MB` / `SANDBOX_CPU_CORES` / `SANDBOX_MAX_PROCESSES` | 0 | Native resource limits (0 = defaults) |
| `OPEN_SANDBOX_URL` | empty | OpenSandbox service URL |
| `ENABLE_OPENSANDBOX_FALLBACK` | `false` | Enable the fallback |
| `SANDBOX_ALLOW_UNLABELED_FALLBACK` | `false` | Allow unlabeled OpenSandbox fallback (trusted local only) |

## Tests

```bash
go test ./...
```

Container E2E: `test/e2e/sandbox-network.mjs` verifies offline → open → online → close; `test/e2e/round7-sandbox-deep.mjs` covers commands, streaming, list-by-plan, and delete; `test/e2e/desktop-smoke.mjs` covers the provider capability matrix; `test/e2e/sandbox-command.mjs` covers argv/legacy/truncation. Recent results are in [docs/verification.md](../../docs/verification.md).

## Build and run

```bash
go mod tidy
go build -o bin/sandbox .
SANDBOX_API_TOKEN=dev-token ./bin/sandbox
```

Or use the project script:

```bash
../scripts/unix/start-sandbox.sh
```

## Security boundaries

- Only the internal executor mounts the Docker socket; the gateway holds no socket and no host workspace. The executor still has high host privileges and should only be reachable on the internal network.
- CPU, memory, PID, capabilities, images, and mount paths are limited by default, and tenant/task labels are enforced, but this is not strong multi-tenant isolation.
- The default network is an internal offline bridge (no gateway, no public DNS); only explicitly approved install/network nodes temporarily attach to the default bridge and disconnect immediately afterwards.
- `SANDBOX_SECURITY_PROFILE=strict` enables non-root, read-only rootfs, and tmpfs; production should still use gVisor/Kata, image signing, rootless, and socket-proxy.
- Native AppContainer/lowil isolation is weaker than containers; use it only for single-user desktops and controlled code, never on multi-tenant servers.
- The Docker socket is mounted only in the internal executor; never publish the executor port or hand untrusted code to a non-isolated runtime.
