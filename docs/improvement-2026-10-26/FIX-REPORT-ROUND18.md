# 修复报告：VM Provider 可测试化 + 更新安装流程 + NSIS 文件关联（第十八轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机安装/签名/WSL2 未验证。

## 1. 本轮完成

### 1.1 VM Provider 可测试化（6.5）

- vm.Config 增加 WSLPath 与 AllowUnsupportedPlatform；默认仍为 wsl.exe，非 Windows 默认拒绝。
- Provider.Exec 使用配置的 WSL 可执行文件，便于用伪 wsl 脚本在 Linux 容器做端到端验证。
- 新增 provider_exec_test.go：临时伪 wsl 脚本 + 临时工作区，验证 Create → Exec（argv/cwd/发行版/python3 映射）→ Usage → Delete。
- Windows 交叉编译通过。

### 1.2 更新安装流程（11）

- updater.Manager.Install(path)：校验文件存在、非目录；默认 Windows 启动安装器 /S，测试注入 fake Launcher。
- bridge 新增 InstallUpdate(path)。
- RuntimePanel 更新区块新增“安装并重启”按钮（下载校验成功后可用）。
- 单测覆盖 fake launcher、空路径、缺失文件。

### 1.3 NSIS 文件关联（4.1）

- 新增可选 Section AssociatePDF：写 HKCU Software\Classes\.pdf\OpenWithProgids 与 ResearchAssistant.pdf，默认不选中。
- 卸载删除对应注册表项；researchassistant:// 协议保持。

## 2. 实际与文档不一致处

- 文档 6.5 的 vm 执行路径此前无法在容器测试（硬编码 wsl.exe）；本轮通过 WSLPath 注入 + 伪 wsl 脚本补齐。
- 文档 11 的更新只有检查/下载，没有安装；本轮补齐 Manager.Install/bridge/UI。
- 文档 4.1 的 NSIS 只有协议关联；本轮新增可选 PDF 关联。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| docker-sandbox 全量 | go build/test | 通过（含 vm fake-wsl 集成） |
| docker-sandbox Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... | 通过 |
| desktop 全量 | go test ./... | 通过（updater Install/bridge） |
| desktop Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... + Wails tag | 通过 |
| backend 全量 | go build/vet/test | 通过 |
| 前端 | npm test、npm run build、npm run lint | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- 真实 Windows 签名、NSIS 安装/卸载、更新服务器、安装器实际执行未验证。
- 真实 WSL2 执行未验证；伪 wsl 仅覆盖命令构造/路径/进程调用。
- PDF 关联为可选 section，默认不选中；真机关联行为未验证。
- AppContainer 实际降权、systray、DPAPI、WebView2 未真机验证。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。
