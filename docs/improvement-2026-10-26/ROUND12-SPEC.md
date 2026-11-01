# 规格：Windows 桌面客户端与去 Docker 化（2026-10-31 第十二轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`、`docs/design/2026-10-30-desktop-without-docker.md`。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在本文件第 2 节。
> 约束：功能测试只在容器内执行（Go 使用 `golang:1.26.1-bookworm`，前端 `node:22-bookworm-slim`，E2E 使用运行中的 `research-assistant` compose 栈）。
> 基线：`master @ a9419b2`，分支 `feature/desk`。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND12.md`。

## 1. 目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 桌面外壳与监督进程 | `desktop/` 可编译；Supervisor 单实例锁/端口/健康检查/退出顺序/Job Object 有单测 |
| G2 | 无 Docker 的 native Python 路径 | native Provider 有 uv 运行时管理、venv 环境哈希缓存、命令翻译、lowil/Job Object runner；纯逻辑单测 + Windows 交叉编译通过 |
| G3 | 有 Docker 自动升级 | docker Provider 行为不变；既有后端/沙箱测试与 compose E2E 全绿 |
| G4 | 关闭窗口不中断、退出无残留 | Supervisor 退出顺序（sandbox→backend→锁）与 Job `KILL_ON_JOB_CLOSE` 实现并有测试 |
| G5 | 能力不足显式失败 | `Capabilities` 协商、`EnvironmentManifest` 扩展、`sandbox_capability` 缺失项接入 |
| G6 | 更新/诊断/卸载骨架 | 诊断接口、更新检查器、NSIS 脚本存在（真机安装留待 Windows 环境） |

硬性完成条件：
1. backend、docker-sandbox、desktop 三模块 `GOOS=windows GOARCH=amd64 go build ./...` 通过；
2. 三模块容器内 `go test ./...` 通过；
3. compose 栈重建后 `http-smoke` 与 `desktop-smoke` 通过；
4. 文档 8.1–8.4 的既有问题全部修复；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（实际代码，2026-10-31）

| 文档描述 | 实际 |
|---|---|
| `repo_prepare.go` 使用 Setpgid/Kill | 属实，Windows 编译失败 |
| `reproduction_mode.go` 使用 Statfs | 属实，Windows 编译失败 |
| `/proc/meminfo` 缺 Windows 分支 | 属实 |
| `capabilities_disk_windows.go` 返回 0 | 属实 |
| 三处 `resolveProjectRoot` | 属实 |
| `workspace/uploads` 回退 TempDir | 属实 |
| `validatePath` 大小写敏感 | 属实 |
| `appconfig` 无 `RA_CONFIG_PATH` | 属实 |
| 日志 O_TRUNC | 三处均属实 |
| `clearProxyEnv` 无保留开关 | 两处均属实 |
| 6 处 `bash -lc` | 实际 6 处（autoresearch/benchmark x2/coder_exec/coder_runtime/paper_debug） |
| 无 `desktop/`、无 native 引擎、无 Provider 契约 | 属实 |
| `EnvironmentManifest` 无 provider/isolation 字段 | 属实 |

补充约束：`cmd/app` 已支持 `APP_ADDR`，`cmd/api` 只认 `PORT`；sandbox 是 gateway/executor 双进程；backend Dockerfile 只构建 `cmd/api`；根 `spec/plan/tasks` 被 .gitignore 忽略。

## 3. 范围

### 3.1 本轮实现
- **跨平台基础**：`internal/procgroup`（Unix 进程组 + Windows Job Object）、`internal/sysinfo`（Linux/Darwin/Windows 内存与磁盘）、`internal/apppaths`（LOG_DIR/APP_DATA_DIR/RA_CONFIG_PATH）、`internal/logging`（20MB×5 轮转）、`internal/proxyenv`（RA_KEEP_SYSTEM_PROXY + Windows 系统代理）、`internal/winpath`（长路径）、workspace 大小写不敏感、uploads/workspace 数据目录回退。
- **命令契约**：backend `sandbox.Command` + `ExecCommandV2/StreamV2`；docker-sandbox `CommandRequest` 扩展 argv/cwd/env/shell/timeout/stdin/max_output_kb；docker 引擎 cwd/env/shell/python 映射与截断；6 处 bash -lc 与 coder_image find 脚本迁移。
- **Provider/native**：`engine.Provider` 契约、docker 适配器、`engine/native`（uv/venv/envHash、translate、Windows Job Object runner、非 Windows stub）、main `SANDBOX_PROVIDER` 选择与 health capabilities。
- **桌面外壳**：`desktop/` 独立 module 的 paths/settings（DPAPI）/supervisor/proxy/main stub+Wails 入口/NSIS 骨架。
- **前端**：`SandboxCapabilities`/`EnvironmentManifestView` 类型、`getSandboxCapabilities`、RuntimePanel、EnvironmentBadge、SettingsPanel 运行模式 tab。

### 3.2 不在本轮
- macOS/Linux 客户端、MS Store、GPU 直通、Windows 真机 AppContainer 探针（Linux 容器无法执行）。
- 代码签名、自动更新服务器、NSIS 真机安装验收。
- vm/remote Provider 实现（仅接口占位）。

## 4. 关键设计
- **procgroup**：Unix `Setpgid`+`Kill(-pid)`；Windows Job Object（KILL_ON_JOB_CLOSE、ProcessMemoryLimit、ActiveProcessLimit）+ `TerminateJobObject`。
- **apppaths**：`LogDir() = LOG_DIR > APP_DATA_DIR/logs > 仓库回退`；`DataDir() = APP_DATA_DIR > 仓库 data`；`ConfigPath() = RA_CONFIG_PATH`。
- **日志轮转**：写前检查大小，`path.1..path.N` 轮转；单文件 20MB、保留 5 份；mutex 并发安全。
- **Command**：`Argv/Cwd/Env/Shell/Timeout/Stdin/MaxOutputKB`；`/workspace` 与 `python`/`python3` 由 Provider 翻译；legacy shell 在 native 返回 `unsupported_posix_shell`。
- **native runtime**：`envHash = sha256(pythonVer + sorted requirements + platform)`；uv python install → uv venv → uv pip install → uv pip freeze；venv 缓存复用。
- **Supervisor**：单实例锁 → settings → 分配端口 → backend 健康（30s）→ sandbox 健康（20s）→ 窗口；退出 sandbox→backend→锁。
- **环境注入**：APP_ADDR、SANDBOX_URL、SANDBOX_API_TOKEN、API_AUTH_TOKEN、REQUIRE_API_AUTH、PLAN_STORE_PATH、UPLOAD_ROOT、BLOB_STORE_PATH、LITERATURE_LIBRARY_DIR、SANDBOX_WORKSPACE_ROOTS、LOG_DIR、OPENAI_*、SANDBOX_PROVIDER、SANDBOX_DEFAULT_IMAGE、NoDefaultCurrentDirectoryInExePath。

## 5. 测试策略（容器内）
1. backend：`go build ./... && go vet ./... && go test -count=1 ./...`；新增包 `-race`。
2. docker-sandbox：`go build ./... && go vet ./... && go test -count=1 ./...`。
3. desktop：`go test ./...`；三模块 `GOOS=windows GOARCH=amd64 go build ./...`。
4. 前端：`npm test`、`npm run build`、`npm run lint`。
5. E2E：`http-smoke.mjs`、`desktop-smoke.mjs`、`sandbox-command.mjs`、`sandbox-network.mjs`。

## 6. 完成定义
- [x] 8.1 编译级阻塞在 Windows 下清零
- [x] 8.2/8.3/8.4 路径/日志/代理问题修复
- [x] 命令契约改造且 docker 回归通过
- [x] Provider + docker 适配器 + native 骨架
- [x] desktop supervisor/proxy/settings 单测与 Windows 交叉编译
- [x] 前端 runtime 面板与徽章
- [x] compose 栈健康，四套 E2E 通过
- [x] 清理临时产物/镜像
- [x] FIX-REPORT-ROUND12

## 7. 风险
| 风险 | 对策 |
|---|---|
| Windows API 交叉编译细节 | x/sys/windows + build tag stub，反复 GOOS=windows 验证 |
| native 无法在 Linux 执行 | 纯逻辑单测 + 交叉编译，真机验收记录环境限制 |
| 命令契约回归 | 保留 legacy 方法，docker E2E 覆盖 |
| Wails 依赖 | desktop 独立 module，Wails 入口 build tag，单测不依赖 |
