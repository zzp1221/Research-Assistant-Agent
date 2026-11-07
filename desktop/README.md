# ResearchAssistant Desktop

简体中文 | [English](README.en.md)

ResearchAssistant 的 Windows 桌面外壳。外壳是一个轻量的 MyGo 窗口：它把 Go 后端与沙箱作为子进程启动，通过固定 loopback origin 提供 React 前端，并负责整个进程树的生命周期。

## 包结构

| 包 | 职责 |
|---|---|
| `paths` | 每用户安装/数据/日志目录 |
| `settings` | `settings.json`、环境变量注入、DPAPI 密钥存储 |
| `supervisor` | 单实例锁、端口分配、健康检查、Job Object 回收 |
| `proxy` | loopback 反向代理，SSE 透传、SPA 回退、Host/Origin 守卫 |
| `shell` | 编排 settings → 端口 → token → supervisor → proxy；暴露 `/desktop/state`、`/desktop/diagnostics`、`/desktop/retry` 与错误页 |
| `diagnostics` | 脱敏支持包（日志尾部、设置、健康、系统信息、失败计划） |
| `updater` | `latest.json` 检查、semver 比较、SHA-256 + Ed25519 校验、分阶段下载 |
| `onboarding` | 首次启动状态、凭据校验与最小完成测试 |
| `bridge` | 桌面 API；通过 `mygo.Bind` 暴露，Wails 回退仍暴露 `window.go.main.API` |
| `mygo` | MyGo 适配层：托盘、对话框、通知、Shell 集成 |
| `tray` | 托盘/通知契约（MyGo 驱动、systray 回退、测试用 memory 驱动） |
| `crashdump` | WER LocalDumps 崩溃转储注册 |
| `onboarding` / `paths` | 首次启动与目录解析 |

## 构建

```bash
# Linux/CI：纯逻辑包与测试
go test ./...

# Windows 交叉编译默认 MyGo 入口
GOOS=windows GOARCH=amd64 go build ./...

# Wails 回退入口
GOOS=windows GOARCH=amd64 go build -tags "desktop wails" ./...

# Headless stub：打印解析后的路径，不打开窗口
GOOS=windows GOARCH=amd64 go build -tags headless ./...
```

发布构建使用 `desktop/build/build.ps1`：构建前端、backend.exe、sandbox.exe，运行 `mygo build`，再用现有 NSIS 脚本打包。`desktop/mygo.json` 固定应用名、版本、资源与 bindings。NSIS 向导允许选择安装目录与可选 PDF 关联。

前端构建产物必须先复制到 `desktop/webui/dist`；`build.ps1` 会自动完成。仓库中的占位文件只用于保持 embed 指令可编译。

安装包应在普通用户目录（如 Desktop/Downloads）运行。NSIS 需要在安装包旁创建临时文件；某些受 ACL 限制的工作区会报 `Error writing temporary file. Make sure your temp folder is valid.` 详见 [build/README.md](build/README.md)。

`mygo generate` 会把类型化客户端写到 `research-assistant/frontend/src/services/mygo.ts`。修改 `bridge.API` 的导出方法或声明事件后需要重新运行。

## 运行目录

```text
%LOCALAPPDATA%\Programs\ResearchAssistant\   ResearchAssistant.exe backend.exe sandbox.exe uv.exe
%LOCALAPPDATA%\ResearchAssistant\            settings.json secrets.dat lock data\ runtimes\ workspaces\ logs\
```

## 生命周期

1. `main_mygo.go` 解析 `paths`、加载 `settings`、创建 `shell.New(...)`。
2. `shell.Prepare()` 先绑定 loopback 监听并返回 loading page，窗口可以立即打开。
3. `shell.Start(ctx)` 分配端口、生成随机 token、启动 `backend.exe` 与 `sandbox.exe`、健康检查，然后切到运行页。
4. 退出时 `shell.Stop` 按顺序停止子进程并回收 Job Object；关闭到托盘由 `CloseToTray` 控制。
5. 崩溃/健康检查失败按 `MaxRestarts` 与退避重启，状态通过 `/desktop/state` 与托盘更新。

## 说明

- `main_mygo.go` 是默认入口（build tag `!wails && !headless`）：打开 MyGo 窗口、绑定 `bridge.API`、加载 shell 的 loopback proxy，因此同源前端、SSE、上传与下载完全不变。
- `main_wails.go` 保留为 Wails 回退（`-tags "desktop wails"`）。
- `main.go` 是 headless stub（`-tags headless`）。
- MyGo 固定为 v0.7.4，只通过 `desktop/mygo` 适配层使用，升级只改一处。
- shell 不向 WebView 暴露 API token；proxy 注入 `Authorization` 并拒绝非 loopback Host。
- AppContainer/lowil 执行位于 `research-assistant/docker-sandbox/internal/engine/native`；本模块只通过 settings 选择 runner。
- 更新包使用 SHA-256 + Ed25519；`RA_UPDATE_ENDPOINT` 与 `RA_UPDATE_PUBLIC_KEY` 启用检查与安装。
- 诊断包默认脱敏 API key/token/secret，仅包含日志尾部、设置、健康与失败计划摘要。

## 测试

```bash
go test ./...
go vet ./...
```

Windows 真机项见 [test/README.md](test/README.md)：AppContainer 兼容性探针、启动/退出残留检查、DPAPI、托盘、NSIS 静默安装与签名行为。
