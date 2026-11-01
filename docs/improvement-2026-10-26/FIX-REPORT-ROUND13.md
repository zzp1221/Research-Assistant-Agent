# 修复报告：桌面外壳产品化 + 发布工程基础 + Windows Runner（第十三轮）

- 日期：2026-10-31
- 分支：`feature/desk`（基于 `master @ a9419b2` 的第十二轮工作区）
- 依据：`docs/design/2026-10-30-windows-desktop-client.md`
- 状态：实现完成，容器内测试通过；Windows 真机安装/AppContainer 降权/DPAPI 仍受当前 Linux 容器环境限制，未执行。

## 1. 本轮解决的问题

### 1.1 桌面外壳真正可启动（P0 收尾）

| # | 原问题 | 修复 |
|---|---|---|
| 1 | `desktop/main_wails.go` 只调用 `wails.Run` 打开空窗口，不启动任何服务 | 新增 `desktop/shell`：加载 settings → 分配端口 → 生成 32B token → 启动 supervisor（backend→sandbox 健康检查）→ 启动 loopback 代理 → 暴露状态 |
| 2 | 后端启动失败白屏 | `/desktop/error` 错误页（日志路径、导出诊断、重试）；`/desktop/state` 状态 JSON；失败时 API 返回 502 |
| 3 | 无崩溃恢复 | `shell` 健康监控（默认 5s）+ 指数退避重启（最多 3 次）；超过上限进入 failed |
| 4 | 无桌面桥接 | `desktop/bridge` + Wails `Bind`：NeedsOnboarding / CompleteOnboarding / TestConnection / Notify / ExportDiagnostics / OpenLogs / Retry；前端通过 `window.go.main.API` 回退解析 |
| 5 | 无首次启动向导 | `desktop/onboarding`（校验、最小 completion 连接测试、完成落盘）+ `features/onboarding/Onboarding.tsx` |
| 6 | 无诊断包 | `desktop/diagnostics`：manifest / 脱敏 settings / health / system / failed-plans / 日志尾部 5MB |
| 7 | 无更新能力 | `desktop/updater`：latest.json、semver、SHA-256 + Ed25519 验签、300MB 上限、原子暂存；Wails 启动后台检查并 emit `ra:update-available` |
| 8 | NSIS 只有骨架 | per-user 安装、静默安装、卸载保留数据（交互确认）、`researchassistant://` 协议注册、uv.exe/webview 布局 |

### 1.2 Windows Runner（P2 核心）

| # | 能力 | 实现 |
|---|---|---|
| 1 | 隔离档位选择 | `isolation.go`：auto→探针→appcontainer/lowil；GPU 任务强制 lowil；显式设置优先 |
| 2 | 附录 D 兼容性探针 | `probe.go` 7 项定义 + `probe_windows.go` 执行/聚合；结果进入 capabilities 与 UI |
| 3 | Job Object 限额 | `limits_windows.go`：KILL_ON_JOB_CLOSE、ProcessMemoryLimit、ActiveProcessLimit=64、CPU rate hard cap、UI 限制（剪贴板/句柄） |
| 4 | AppContainer | `appcontainer_windows.go`：`CreateAppContainerProfile` / SID 派生 / 删除 / 工作区 ACL（icacls 仅授当前用户 + AppContainer SID）/ `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES` 启动 |
| 5 | lowil | `lowil_windows.go`：复制当前主令牌 + `TokenIntegrityLevel=Low`，以受限令牌启动 |
| 6 | 资源设置链路 | settings → `SANDBOX_MEMORY_MB/CPU_CORES/MAX_PROCESSES` → native Config 默认 Limits；backend RuntimeHardLimits 同步扩展 |
| 7 | 环境保真度 | native session 保存 envHash，`/usage` 返回 `runtime_hash`，backend 写入 `EnvironmentManifest.runtime_hash` |

### 1.3 前端

- `contracts/desktop.ts` + `services/desktopBridge.ts`（Wails 绑定回退）。
- `features/onboarding/Onboarding.tsx`：欢迎/隐私、凭据与测试连接、运行模式、完成。
- `components/NetworkIsolationBanner.tsx`：native + lowil 常驻黄色提示。
- `app/hooks/usePlanCompletionNotification.ts`：计划 completed/failed 系统通知。
- `RuntimePanel`：展示 AppContainer 探针逐项结果与降级原因。

## 2. 实际与文档不一致处

- 文档 3.1–3.5 认为 shell 骨架已具备，实际 `main_wails.go` 完全不启动 supervisor/proxy；本轮补齐。
- 文档 5.4/10 标注首次启动向导、系统通知、错误页未完成；实际均无实现，本轮新增。
- 文档 11 标注“仅 NSIS 骨架”；实际没有 updater 包，本轮新增。
- 文档 6.3 标注 native runner 只有 Job Object 骨架；实际无 lowil/AppContainer/限额/探针，本轮补齐代码。
- 文档 9 标注 native runtime_hash 未填充；本轮通过 `/usage.runtime_hash` 回填 manifest。
- `desktop/main_wails.go` 的 Wails v2 `options.App` 没有 `JS` 字段，改用 `Bind` + 前端 `window.go.main.API` 解析。

## 3. 测试证据

> 所有功能测试在容器内执行；Go 使用 `golang:1.26.1-bookworm`，前端使用 `node:22-bookworm-slim`，E2E 使用运行中的 `research-assistant` compose 栈。

| 层级 | 命令 | 结果 |
|---|---|---|
| desktop 单测 | `go test ./...`（bridge/diagnostics/onboarding/proxy/settings/shell/supervisor/tray/updater） | 通过 |
| desktop Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| Wails 入口交叉编译 | `GOOS=windows GOARCH=amd64 go build -tags "desktop wails" ./...` | 通过 |
| backend 全量 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过（含 internal/agent、tests） |
| backend Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| docker-sandbox 全量 | `go build ./...`、`go test ./...` | 通过 |
| docker-sandbox Windows 交叉编译 | `GOOS=windows GOARCH=amd64 go build ./...`、`go vet ./...` | 通过 |
| 前端 | `npm test`、`npm run build`、`npm run lint` | 34 个测试文件 / 138 个测试通过；build/lint 通过 |
| E2E http-smoke | `node e2e/http-smoke.mjs`（compose 网络） | 17/17 通过 |
| E2E desktop-smoke | `node e2e/desktop-smoke.mjs` | 6/6 通过 |
| E2E sandbox-command | `node e2e/sandbox-command.mjs` | 8/8 通过 |
| E2E sandbox-network | `node e2e/sandbox-network.mjs` | 8/8 通过 |

## 4. 限制与后续

- **Windows 真机未执行**：AppContainer 真实降权、lowil 令牌、DPAPI 加解密、Wails WebView2 窗口、NSIS 安装/卸载需要 Windows 11 实机验收；本轮完成代码 + Windows 交叉编译 + 纯逻辑单测。
- **托盘**：已定义 `desktop/tray` 契约与内存实现；真实 Win32 托盘图标/菜单未接线。
- **代码签名与更新服务器**：updater 的验签逻辑与 NSIS 静默安装已实现；EV 证书、Azure Trusted Signing、真实 latest.json 服务器未配置。
- **AppContainer 双 profile 网络切换**：当前只创建离线 profile（无 `internetClient`），`SetNetwork` 在 native 模式返回 `unsupported`；`Capabilities.NetworkControl=false` 诚实反映现状。
- **vm/remote Provider**：仍为 P4，仅保留接口占位。
- 崩溃转储与本地指标展示未实现（诊断包已包含日志/能力/失败计划）。

## 5. 清理

- 删除 `D:\tmp\ra-backend-patch`（临时 Dockerfile 与后端二进制）。
- 临时容器均为 `--rm`；不删除 compose 数据卷与运行中的项目镜像。
