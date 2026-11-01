# 修复报告：自动更新闭环 + Remote Provider 集成 + 导出审计 + Windows 回归脚本（第十七轮）

- 日期：2026-10-31
- 分支：`feature/desk`
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`
- 状态：实现完成，容器内测试通过；Windows 真机签名/安装/WSL2/托盘未验证。

## 1. 本轮完成

### 1.1 自动更新闭环（11）

- `updater.Manager`：Check（manifest/版本比较）、Download（sha256 + Ed25519 验签、原子暂存）、UpdateStatus。
- bridge 新增 `CheckForUpdate()` / `DownloadUpdate(version)`；未配置时返回明确错误。
- main_wails 从 `RA_UPDATE_ENDPOINT` + `RA_UPDATE_PUBLIC_KEY` 构建 Manager，下载目录 `DataDir/updates`。
- RuntimePanel 新增“更新”区块（检查更新、下载并校验、版本/路径/错误展示）。
- 单测覆盖 Manager Check/Download、未配置错误、公钥解析。

### 1.2 Remote Provider 集成测试（6.5）

- 新增 `docker-sandbox/main_remote_test.go`：httptest mock OpenSandbox（create/commands/delete）+ `NewSandboxService(SANDBOX_PROVIDER=remote)`，通过真实 Gin 路由执行 create → exec → delete，并检查 health 的 provider=remote。
- 覆盖 remote provider 与 main 选择、OpenSandbox REST 映射、删除路径。

### 1.3 导出审计（9）

- `plan_exported` 审计事件新增 `sandbox_provider` / `isolation`（json/md/zip 三种格式）。
- 帮助定位导出包对应的运行环境；不改变响应体。

### 1.4 Windows 真机回归脚本（14.2）

- `desktop/test/windows-runner-probe.ps1`：附录 D 7 项（multiprocessing/torch/pip/matplotlib/写桌面/网络/子进程回收），输出 JSON。
- `desktop/test/windows-launch.ps1`：启动 shell、等待 backend/sandbox、退出后检查残留进程。
- `desktop/test/README.md` 说明真机执行方式与判定标准。

## 2. 实际与文档不一致处

- 文档 11 的更新此前只有 Checker/Installer，没有 Manager/bridge/UI 闭环；本轮补齐。
- 文档 6.5 的 remote 只有适配器，没有集成测试；本轮新增 mock OpenSandbox 测试。
- 文档 9 的导出审计此前只记录 format/bytes/schema，没有 provider/isolation；本轮补齐。
- 文档 14.2 的真机回归此前无脚本；本轮新增。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| desktop 单测 | `go test ./...`（updater Manager、bridge） | 通过 |
| desktop Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` + Wails tag | 通过 |
| backend 全量 | `go build/vet/test` | 通过 |
| docker-sandbox 全量 | `go build/test` + Windows 交叉编译 | 通过 |
| remote 集成测试 | `go test -run TestRemoteProviderServiceLifecycle -v .` | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint` | 35 文件 / 141 tests 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- Windows 真机签名（EV/Azure Trusted Signing）、NSIS 安装/卸载、真实更新服务器未执行。
- 更新闭环只做“检查 + 下载 + 验签 + 暂存”，安装触发仍由安装器/用户完成。
- 真实 WSL2/OpenSandbox 服务未联调；remote 测试使用 mock。
- AppContainer 实际降权、systray、DPAPI、WebView2 未真机验证。

## 5. 清理

- 删除 `D:\tmp\ra-backend-patch6`；临时容器均为 `--rm`。
