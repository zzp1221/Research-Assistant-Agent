# 修复报告：运行时设置持久化 + 运行时状态 + 诊断/日志入口（第二十轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内测试通过；Windows 真机未验证。

## 1. 本轮完成

### 1.1 运行时设置持久化（5.1）

- bridge 新增 RuntimeSettingsView、GetRuntimeSettings、UpdateProvider。
- UpdateProvider 校验 auto/native/docker/vm/remote，写 settings.json；shell 运行中时重启子进程使 SANDBOX_PROVIDER 生效。
- RuntimePanel 启动时通过 bridge 读取 provider，选择时通过 bridge 持久化（浏览器模式回退 localStorage）。
- 单测覆盖读取默认值、保存 native、非法 provider。

### 1.2 运行时状态（6.3.1）

- native Provider RuntimeStatus/ScanRuntimeStatus：Python 版本目录、venv 数量、磁盘占用。
- sandbox 新增 GET /api/v1/runtime/status（native 返回 supported=true；其他 provider supported=false）。
- shell 新增 FetchRuntimeStatus + GET /desktop/runtime。
- RuntimePanel 展示 Python/Venv/磁盘占用。
- 单测覆盖 native 扫描与 shell 拉取。

### 1.3 诊断/日志入口（10/13）

- RuntimePanel 新增“导出诊断包”“打开日志目录”按钮，调用 bridge ExportDiagnostics/OpenLogs。
- 展示诊断包路径与错误。

## 2. 实际与文档不一致处

- 文档 5.1 的 provider 设置此前只存 localStorage，不写 settings.json；本轮修复。
- 文档 6.3.1 的 venv 缓存此前无状态接口；本轮新增 RuntimeStatus。
- 文档 10/13 的诊断包入口此前只在错误页/代码里，本轮补常规 UI。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| desktop 单测 | go test ./...（bridge settings、shell runtime） | 通过 |
| desktop Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... + Wails tag | 通过 |
| docker-sandbox 全量 | go build/test + Windows 交叉编译 | 通过 |
| backend 全量 | go build/vet/test | 通过 |
| 前端 | npm test、npm run build、npm run lint | 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- Windows 真机 WER dump、安装/签名、更新服务器、AppContainer/systray/DPAPI/WebView2 未验证。
- 运行时状态只展示已安装缓存，不提供下载进度。
- RuntimePanel 的 provider 变更会重启子进程；运行中计划会被中断（与设计一致）。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。
