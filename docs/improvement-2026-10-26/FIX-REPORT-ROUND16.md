# 修复报告：导出/复现包 Provider 分支 + 规划器细粒度门禁 + 原生对话框 + 签名脚本（第十六轮）

- 日期：2026-10-31
- 分支：`feature/desk`
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`
- 状态：实现完成，容器内测试通过；Windows 真机签名/安装/WSL2 未验证。

## 1. 本轮完成

### 1.1 导出/复现包 Provider 分支（9）

- `bundleEnvironment` 增加 `sandbox_provider/isolation/runtime_hash/base_os/network_enforced`。
- 复现包新增 `environment/manifest.json`，与 `plan.EnvironmentManifest` 一致。
- native provider：不要求 `image_digest`，要求 `runtime_hash`；docker/容器 provider：仍要求 `image` + `image_digest`。
- README 的 “How to reproduce” 在 native 下改为 venv/requirements.lock 步骤，不再指导 `docker build`。
- 单测覆盖 native 与 docker 两种分支。

### 1.2 规划器细粒度能力门禁（6.2）

- 计划工件包含 `reproduction_runtime_spec` 时，按 GPU/Python 需求生成 `gpu_unavailable` / `python_version_unavailable` 缺失项，并将计划标记 blocked。
- `runtimeProbe` 同步使用 `caps.GPU || caps.GPURuntimeAvailable` 判断 GPU，支持 native 宿主 GPU；Python 版本按 major.minor 匹配。
- 单测覆盖 GPU/Python 缺失、版本匹配、无需求不误伤。

### 1.3 原生对话框（10）

- `desktop/bridge` 新增 `ChooseDirectory(title)` 与 `ShowInFolder(path)`，可注入测试实现。
- Wails 入口接线 `runtime.OpenDirectoryDialog` 与 `explorer.exe /select,<path>`。
- 前端 `DesktopBridge` 增加 `chooseDirectory/showInFolder`；RuntimePanel 新增“仓库目录”区块（选择目录/在文件夹中显示）。

### 1.4 签名与更新脚手架（11）

- `desktop/build/sign.ps1`：对 exe/安装包执行 `signtool` SHA-256 时间戳签名。
- `desktop/build/update-manifest.example.json`：latest.json 示例。
- `updater.PublicKeyFromEnv()` 读取 `RA_UPDATE_PUBLIC_KEY`（base64 Ed25519），main 启动时校验；单测覆盖。

## 2. 实际与文档不一致处

- 文档 9 的导出包此前只写 image/image_digest，native 的 runtime_hash 没有进入复现包；本轮补齐。
- 文档 6.2 的 GPU/Python 门禁此前只在执行期 runtimeProbe 的部分字段；本轮补齐版本匹配与计划工件门禁。
- 文档 10 的原生对话框此前完全没有 bridge/前端实现；本轮补齐。
- 文档 11 的签名此前只有 NSIS 骨架；本轮新增脚本/示例/公钥注入。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| backend 单测 | `go test ./internal/api/ ./internal/agent/` | 通过（导出分支/GPU/Python 门禁） |
| backend 全量 | `go build/vet/test` | 通过 |
| desktop 单测 | `go test ./...`（bridge 对话框/updater 公钥） | 通过 |
| desktop Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` + Wails tag | 通过 |
| docker-sandbox 全量 | `go build/test` + Windows 交叉编译 | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint` | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network | 全部通过 |

## 4. 限制与后续

- Windows 真机签名（EV/Azure Trusted Signing）、NSIS 安装/卸载、真实更新服务器未执行。
- WSL2/OpenSandbox 真实联调仍未执行。
- 规划器门禁依赖计划工件 `reproduction_runtime_spec`；普通新计划在创建时没有该工件，仍由执行期 runtime_probe 兜底。
- 原生目录选择/在文件夹中显示未在 Windows 真机验证。

## 5. 清理

- 临时容器均为 `--rm`；不删除 compose 数据卷与项目镜像。
