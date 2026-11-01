# 修复报告：VM Provider 执行 + 规划器能力门禁 + 本地指标/崩溃恢复提示（第十五轮）

- 日期：2026-10-31
- 分支：`feature/desk`
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`
- 状态：实现完成，容器内测试通过；Windows 真机 WSL2 执行、AppContainer/DPAPI/托盘交互未验证。

## 1. 本轮完成

### 1.1 VM Provider 真实执行路径（P4）

| # | 能力 | 实现 |
|---|---|---|
| 1 | WSL 路径转换 | `ToWSLPath`（`C:\x` → `/mnt/c/x`）、`FromWSLPath`、UNC 拒绝 |
| 2 | 命令构造 | `BuildWSLArgs` / `BuildWSLArgsWithEnv`（`-d distro --cd cwd -- env ... argv`） |
| 3 | 命令翻译 | `TranslateVMCommand`：`python/python3`→`python3`、`/workspace` 映射到宿主工作区 |
| 4 | Provider | Create 保存 distro/workspace；Exec 通过 `wsl.exe` 执行；Delete/Usage/Reap/InstallPackages |
| 5 | main 接入 | `vm-` 前缀的 create/delete/network/usage/python/command/stream 分支；health/capabilities 真实返回 |
| 6 | 失败语义 | 非 Windows 或未检测到 WSL2 返回 `ErrUnavailable`，不静默降级 |

### 1.2 规划器能力门禁（6.2）

- `plan.Meta.SandboxCapabilities` 记录创建时 provider/isolation/images/network/python/OS 快照。
- `capabilities.Images=false`（native）时，计划内 `image_prepare` 节点自动转换为 `runtime_probe`，输出改为 `runtime_capabilities/missing_requirements`，与文档 6.2 的 native 行为一致。
- 能力探测失败只记录日志，不阻断计划创建。
- 后端单测覆盖 native 转换、docker 保持、探测失败不突变。

### 1.3 本地指标（13）

- `desktop/shell/metrics.go`：Prometheus 文本解析（`go_goroutines`、`process_resident_memory_bytes`、`http_requests_total` 累加）。
- `GET /desktop/metrics`：通过 loopback 后端 `/metrics` 获取并返回 `{goroutines, resident_memory_bytes, request_count, collected_at}`。
- RuntimePanel 新增“本地指标”区块（goroutines / 常驻内存 / HTTP 请求 / 采集时间）。

### 1.4 崩溃恢复提示（3.3）

- Shell Status 增加 `last_restart_at`；重启成功时写入。
- 新增 `DesktopStatusBanner`：轮询 `/desktop/state`（5s），重启或失败时显示提示条，可关闭。
- 组件测试覆盖重启、失败原因、健康首启三种状态。

## 2. 实际与文档不一致处

- 文档 6.5 的 vm Provider 在第十四轮只有检测；本轮补齐 WSL2 执行路径（真机未验证）。
- 文档 6.2 的规划器门禁此前没有写 `Plan.Meta`，也没有 native 的 `image_prepare→runtime_probe`；本轮补齐。
- 文档 13 的本地指标此前只有后端 `/metrics`；本轮新增桌面聚合接口与 UI。
- 文档 3.3 的“后端已重启，进行中的计划已中断”提示此前无前端实现；本轮补齐。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | `go build ./...`、`go test ./...` | 通过（vm/remote/native/engine 全绿） |
| docker-sandbox Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| backend 能力门禁单测 | `go test ./internal/api/` | 通过（native 转换 / docker 保持 / 探测失败） |
| backend 全量 | `go build/vet/test` | 通过 |
| desktop 单测 | `go test ./...`（metrics 解析/FetchMetrics/全部既有） | 通过 |
| desktop Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` + Wails tag | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint` | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network | 全部通过 |

## 4. 限制与后续

- **WSL2 真机未验证**：容器内只能验证路径/参数纯逻辑与 Windows 交叉编译；需要 Windows 11 + WSL2 实测。
- AppContainer 双 profile 实际降权、systray 交互、DPAPI、WebView2、NSIS 安装仍未真机验收。
- 规划器门禁目前只覆盖 `image_prepare→runtime_probe`；更多节点类型（GPU/系统库/Python 版本）仍由执行期 runtime_probe 兜底。
- remote Provider 未与真实 OpenSandbox 联调。
- 代码签名、真实更新服务器、本地指标历史曲线未做。

## 5. 清理

- 删除 `D:\tmp\ra-backend-patch2`；临时容器均为 `--rm`。
