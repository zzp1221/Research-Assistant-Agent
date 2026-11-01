# 修复报告：Windows 桌面客户端与去 Docker 化（第十二轮）

- 日期：2026-10-31
- 分支：`feature/desk`（基于 `master @ a9419b2`）
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`、`docs/design/2026-10-30-desktop-without-docker.md`
- 状态：实现完成，容器内测试通过；Windows 真机安装/AppContainer 探针受当前 Linux 容器环境限制，未执行。

## 1. 本轮解决的问题（文档 8.1–8.4）

| # | 原问题 | 修复 |
|---|---|---|
| 1 | `scheduler/repo_prepare.go` 直接使用 `syscall.SysProcAttr{Setpgid:true}` / `syscall.Kill`，Windows 编译失败 | 新增 `internal/procgroup`：Unix 进程组、Windows Job Object（`KILL_ON_JOB_CLOSE`、内存/进程数限制）；repo_prepare 改用 `Configure/Kill` |
| 2 | `scheduler/reproduction_mode.go` 使用 `syscall.Statfs`，Windows 编译失败 | 新增 `internal/sysinfo`：Linux `/proc/meminfo` + Statfs、Darwin sysctl、Windows `GlobalMemoryStatusEx` + `GetDiskFreeSpaceExW` |
| 3 | `reproduction_mode.go` 缺 Windows 内存分支 | 由 `sysinfo.MemoryTotalBytes()` 统一提供 |
| 4 | `docker-sandbox/internal/engine/capabilities_disk_windows.go` 返回 0 | 使用 `GetDiskFreeSpaceExW` 返回真实可用空间 |
| 5 | `cmd/api`、`cmd/app`、`docker-sandbox` 向上找仓库目录写日志 | 新增 `internal/apppaths`（backend）与 `resolveLogDir`（sandbox）：`LOG_DIR > APP_DATA_DIR/logs > 仓库回退` |
| 6 | 日志 `O_TRUNC` 每次启动清空 | 新增 `internal/logging.RotatingWriter`：20MB × 5 轮转，backend/api/sandbox 全部接入 |
| 7 | `clearProxyEnv()` 无条件清空代理 | 新增 `internal/proxyenv`：默认保持旧行为；`RA_KEEP_SYSTEM_PROXY=1` 保留并读取 Windows 注册表系统代理 |
| 8 | `workspace.UploadRoots`/`uploads.Root` 回退裸 TempDir | 保留开发态 TempDir；注入 `APP_DATA_DIR` 时使用 `data/uploads`；桌面由 supervisor 显式注入 `UPLOAD_ROOT` |
| 9 | `workspace.validatePath` 大小写敏感 | Windows 下 root/resolved 都做 `EqualFold` 比较；新增 `internal/winpath` 长路径 `\\?\` 规范化 |
| 10 | `appconfig.resolveConfigPath` 只向上搜索 | 支持 `RA_CONFIG_PATH` 与 `APP_DATA_DIR/config` |
| 11 | `cmd/app` 强依赖 `SANDBOX_URL` 否则 Fatal | 桌面注入 `APP_DATA_DIR` 时只警告；开发态保持明确提示 |
| 12 | `cmd/api` 只认 `PORT` | 同时支持 `APP_ADDR` |

## 2. 命令契约（文档第 7 节）

- 新增 `backend/internal/sandbox.Command{Argv,Cwd,Env,Shell,Timeout,Stdin,MaxOutputKB}` 与 `ExecCommandV2` / `ExecCommandStreamV2`；旧方法保留。
- `docker-sandbox` 的 `CommandRequest` 增加 `argv/cwd/env/shell/timeout_sec/stdin/max_output_kb`，旧 `cmd` 兼容。
- docker 引擎：`docker exec -w`、`-e`、`sh -lc` 包装、`python -> python3`、输出上限与 `truncated` 标记。
- 6 处 `bash -lc` 已迁移：autoresearch_eval、benchmark_harness（2 处）、coder_exec、coder_runtime、paper_debug_harness。
- `coder_image.buildMountedWorkspacePythonCommand` 改为宿主侧补齐 `__init__.py` + 返回 `sandbox.Command`。
- `legacyShell` 适配：native Provider 遇到 POSIX shell 返回 `unsupported_posix_shell`，不静默成功。

## 3. Provider 与 native 引擎（文档第 6 节）

- `docker-sandbox/internal/engine/provider.go`：`Provider` 接口、`ProviderCapabilities`、`CreateSpec`、`Session`、`Event`、`Result`、`Usage`。
- `docker_provider.go`：包装现有 `NativeDockerEngine`，行为不变。
- `engine/native`：
  - `runtime.go`：`EnvHash = sha256(pythonVer + sorted requirements + platform)`；`uv python install` → `uv venv` → `uv pip install` → `uv pip freeze`；venv 缓存复用。
  - `translate.go`：`/workspace`、`python/python3` 映射；POSIX shell 拒绝。
  - `runner_windows.go`：Windows Job Object + 进程树回收；`runner_other.go` 返回 `unsupported_platform`。
  - `engine.go`：Provider 实现、会话/工作区/用量/TTL 回收。
- `main.go`：`SANDBOX_PROVIDER=auto|native|docker` 选择，health/capabilities 返回 provider 矩阵；native 分支接入 create/delete/exec/network/usage/reaper。
- `EnvironmentManifest` 扩展 `sandbox_provider/isolation/runtime_hash/base_os/network_enforced`；`runtimeProbe` 新增 `sandbox_capability` 缺失项；`runtimeValidate` 从 capabilities 填充 manifest。

## 4. 桌面外壳（文档第 3/4/5 节）

- `desktop/` 独立 Go module（`go.work` 已加入）：
  - `paths`：per-user 安装/数据/日志目录。
  - `settings`：settings.json、环境变量注入表、DPAPI secret store（Windows）/非 Windows unsupported、内存 store 测试。
  - `supervisor`：单实例锁、端口分配、backend→sandbox 启动顺序、健康检查、失败回收、sandbox→backend 退出顺序；Windows Job Object 绑定。
  - `proxy`：SSE `FlushInterval=-1`、Host/Origin 守卫、SPA 回退。
  - `main_wails.go`（`desktop && wails`）与 `main.go`（`desktop && !wails` stub）；NSIS 骨架。
- 桌面纯逻辑单测通过，Windows 交叉编译通过。

## 5. 前端

- `contracts/api.ts`：`SandboxCapabilities`、`EnvironmentManifestView`。
- `services/api/researchAssistantApi.ts`：`getSandboxCapabilities`。
- `components/EnvironmentBadge.tsx`：容器 / 原生(AppContainer) / 原生(兼容) 徽章。
- `features/settings/RuntimePanel.tsx`：provider 能力、网络强制状态、运行模式偏好。
- `SettingsPanel` 新增「运行模式」tab。
- 组件测试覆盖徽章三种状态。

## 6. 测试证据

> 所有功能测试在容器内执行；详细命令与输出见本轮会话记录。

| 层级 | 命令 | 结果 |
|---|---|---|
| 后端全量 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...`（`golang:1.26.1-bookworm`） | 通过 |
| docker-sandbox 全量 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过 |
| desktop | `go test ./...`、`GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| Windows 交叉编译 | backend / docker-sandbox / desktop 三模块 | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint`（`node:22-bookworm-slim`） | 通过（131 tests） |
| compose | 重建 backend 镜像并启动 `research-assistant` 栈 | 见第 8 节 |
| E2E | `test/e2e/http-smoke.mjs` | 见第 8 节 |

## 7. 限制与后续

- 当前执行环境是 Linux 容器，无法运行 Windows AppContainer / lowil 的真实进程；native runner 只做了 Windows 交叉编译与纯逻辑单测。真实 Windows 11 上仍需跑附录 D 的 7 项探针。
- Wails v2 未 vendoring；`main_wails.go` 需要安装 Wails 并按 `desktop/README.md` 构建。
- 代码签名、自动更新服务器、NSIS 真机安装属于 P3，保留脚本/接口，未在本轮验收。
- vm/remote Provider 仅保留接口，未实现。

## 8. 容器回归与清理

- 后端镜像重建成功（`research-assistant-backend:latest`），`docker compose up -d --no-deps backend` 后 `/api/health` 返回 200。
- docker-sandbox 与 docker-sandbox-executor 镜像重建成功，重启后 `/api/v1/health` 返回 `provider=docker, isolation=container, network_enforced=os`。
- 容器内 `http-smoke.mjs`：17/17 全部通过（健康、OpenAPI、计划创建/幂等/列表/详情/暂停/恢复、导出、指标、上传去重、会话删除、超长 intent 拒绝）。
- 容器内 `desktop-smoke.mjs`：6/6 通过（health、sandbox health、provider、isolation、network enforcement、images）。
- 容器内 `sandbox-command.mjs`：8/8 通过（argv 契约、legacy cmd、输出截断与 `truncated` 标记）。
- 容器内 `sandbox-network.mjs`：8/8 通过（默认断网、审批开网、恢复断网、用量、删除）。
- 前端：31 个测试文件 / 131 个测试通过；`npm run build` 通过；`npm run lint` 通过。
- 清理：删除工作区 `.gocache`（约 1.7GB）；`docker builder prune -f` 累计回收约 2.2GB 构建缓存；`docker image prune -f` 无 dangling 镜像；临时测试容器均为 `--rm` 且已退出；不删除 compose 数据卷与运行中的项目镜像。