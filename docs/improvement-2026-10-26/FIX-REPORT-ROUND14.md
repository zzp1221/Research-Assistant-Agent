# 修复报告：AppContainer 双 profile 网络 + vm/remote Provider + 崩溃转储 + 托盘（第十四轮）

- 日期：2026-10-31
- 分支：`feature/desk`
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`
- 状态：实现完成，容器内测试通过；Windows 真机 AppContainer/托盘/DPAPI 与真实 WSL2/OpenSandbox 服务未验证。

## 1. 本轮完成

### 1.1 AppContainer 双 profile 网络（P2）

| # | 能力 | 实现 |
|---|---|---|
| 1 | 离线 profile | `ResearchAssistant.Sandbox`，无 capability |
| 2 | 在线 profile | `ResearchAssistant.Sandbox.Online`，授予 `internetClient`（`S-1-15-2-1`） |
| 3 | profile 创建/派生/删除 | `CreateAppContainerProfile` / `DeriveAppContainerSidFromAppContainerName` / `DeleteAppContainerProfile` |
| 4 | 网络切换 | `Provider.SetNetwork` 仅 appcontainer 可用；下一次 `Exec` 选择 online/offline profile，等价于进程重启 |
| 5 | 工作区 ACL | 同时授权离线与在线 profile SID，切换 profile 后仍可访问工作区 |
| 6 | 能力矩阵 | `NetworkControl=true`（appcontainer），`NetworkEnforced=os` |

### 1.2 vm / remote Provider（P4）

| # | 能力 | 实现 |
|---|---|---|
| 1 | WSL2 检测 | `engine/vm.Detect` 执行 `wsl.exe -l -v`，支持 UTF-16LE 输出解码与发行版解析 |
| 2 | vm 能力矩阵 | `provider=vm, isolation=vm, images=true, network_enforced=os, gpu=true` |
| 3 | vm 明确失败 | `ErrUnavailable`，不静默降级；health 返回 `ok=false` + 原因 |
| 4 | remote 适配器 | `engine/remote` 包装 `OpenSandboxEngine`：Create/Exec/Delete/Usage |
| 5 | main 选择 | `SANDBOX_PROVIDER=vm|remote`；remote 需要 `OPEN_SANDBOX_URL`；health/capabilities 返回真实 provider |
| 6 | 明确错误 | 未知 provider、缺失 URL、VM 占位均通过 `providerError` 暴露 |

### 1.3 崩溃转储（P3）

- `diagnostics.Options.CrashDir`；扫描 `data/crashes` 下最近 3 个 `.dmp`（按 mtime）。
- 诊断包新增 `crashes.json`（name/size/modified）与 `crashes/<name>`（每个最多 1MB）。
- 目录不存在时输出空数组，不报错。

### 1.4 Windows 托盘（P0）

- `desktop/tray` 新增 `Native` 接口与 `New()` 平台构造。
- Windows 使用 `github.com/getlantern/systray`：tooltip 显示 provider/运行状态，菜单「显示主窗口」「退出」，通知先入队待 onReady。
- 非 Windows 使用内存实现，保证跨平台测试。

## 2. 实际与文档不一致处

- 文档 6.3.4 的双 profile 网络此前完全未实现；本轮补齐代码（真机未验证）。
- 文档 6.5 的 vm Provider 只有接口占位，实际没有包；本轮新增检测与能力矩阵。
- 文档 6.5 的 remote Provider 实际已有 `OpenSandboxEngine` 但没有 Provider 适配器；本轮补齐。
- 文档 13 的诊断包实际不含崩溃转储；本轮补齐。
- 文档 3.1 的托盘只有契约；本轮新增 systray 驱动。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | `go build ./...`、`go test ./...` | 通过（vm/remote/native/engine 全绿） |
| docker-sandbox Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| vm 健康检查 | `SANDBOX_PROVIDER=vm` 运行真实二进制，`/api/v1/health` | `ok=false`、provider=vm、error 明确、capabilities 正确 |
| remote 健康检查 | `SANDBOX_PROVIDER=remote OPEN_SANDBOX_URL=...` | `ok=true`、provider=remote、capabilities 正确 |
| desktop 单测 | `go test ./...` | 通过（含 diagnostics crash、tray memory） |
| desktop Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...`、Wails tag | 通过 |
| backend 全量 | `go build/vet/test` | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint` | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network | 全部通过 |

## 4. 限制与后续

- Windows 真机：AppContainer 双 profile 实际降权、systray 托盘交互、DPAPI、WebView2、NSIS 安装未执行。
- vm Provider 仅检测与能力矩阵，真实 WSL2 执行未实现（P4）。
- remote Provider 未与真实 OpenSandbox 服务联调；网络切换由远端管理。
- 代码签名、真实更新服务器、本地指标展示、崩溃转储上报仍未做。
- 托盘通知使用 tooltip 兜底，未接入 Windows Toast。

## 5. 清理

- 临时容器均为 `--rm`；不删除 compose 数据卷与项目镜像。
