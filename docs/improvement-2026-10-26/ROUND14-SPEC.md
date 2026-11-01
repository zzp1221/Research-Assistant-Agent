# 规格：AppContainer 双 profile 网络 + vm/remote Provider + 崩溃转储 + 托盘（2026-10-31 第十四轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md` 状态表（第十三轮后 P2/P3 部分完成、P4 未开始）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行（Go 使用 `golang:1.26.1-bookworm`，前端 `node:22-bookworm-slim`，E2E 使用运行中的 `research-assistant` compose 栈）。Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑单测验证。
> 基线：`feature/desk`（含第十三轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND14.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | AppContainer 双 profile 网络 | 离线 profile 无 capability、在线 profile 含 internetClient；`SetNetwork` 切换后下一次执行使用对应 profile；`Capabilities.NetworkControl=true`（仅 appcontainer）；纯逻辑单测 + Windows 交叉编译 |
| G2 | vm/remote Provider | `engine/vm` 检测 WSL2 并返回能力矩阵，不可用时明确失败；`engine/remote` 包装现有 OpenSandbox 引擎；main 支持 `SANDBOX_PROVIDER=vm|remote`；单测 + 交叉编译 |
| G3 | 崩溃转储收集 | `desktop/diagnostics` 扫描 `data/crashes`，诊断包包含最近 3 个 dump 的元数据/内容（大小上限）；单测 |
| G4 | Windows 托盘驱动 | `desktop/tray` 新增 Win32 `Shell_NotifyIconW` 驱动（菜单显示/退出、tooltip、通知），无 Windows 时内存实现；`GOOS=windows` 编译通过 |
| G5 | 回归 | 三模块容器 build/vet/test；前端 test/build/lint；compose 四套 E2E 通过；无新增失败 |

硬性完成条件：
1. `desktop`、`docker-sandbox`、`backend` 三模块容器内 build/vet/test 通过；
2. 三模块 `GOOS=windows GOARCH=amd64 go build ./...` 通过（desktop 另验证 Wails tag）；
3. 前端 `npm test`、`npm run build`、`npm run lint` 通过；
4. compose 栈重建后 http-smoke、desktop-smoke、sandbox-command、sandbox-network 通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十四轮）

| 文档描述 | 实际 |
|---|---|
| 6.3.4 AppContainer 双 profile 网络 | 第十三轮只创建离线 profile；`SetNetwork` 返回 unsupported；`NetworkControl=false` |
| 6.5 / P4 vm Provider | 无 `engine/vm` 包；main 不识别 `SANDBOX_PROVIDER=vm` |
| 6.5 / P4 remote Provider | 已有 `OpenSandboxEngine`，但没有 Provider 适配器；main 不识别 `remote` |
| 13 崩溃转储 | `desktop/diagnostics` 只收集日志/设置/health/system/失败计划，无 crash 目录 |
| 3.1 托盘 | 只有 `desktop/tray` 契约 + 内存实现，无 Win32 驱动 |
| 15 P2 | AppContainer/lowil/Job 限额已实现；双 profile 网络未实现 |
| 15 P4 | 未开始 |

## 3. 范围

### 3.1 本轮实现
- **native 双 profile**：`ResearchAssistant.Sandbox`（offline）与 `ResearchAssistant.Sandbox.Online`（internetClient）；`session.networkOn`；`runSpec.NetworkOn`；每次 exec 选择 profile；`SetNetwork` 仅 appcontainer 可用。
- **vm Provider**：`engine/vm`：`Detect()` 执行 `wsl.exe -l -v` 解析发行版；`Provider` 实现 Capabilities（vm/VM 隔离/images=true/network_enforced=os）；Create/Exec 返回 `ErrVMUnavailable` 并给出安装建议；非 Windows 返回 unsupported。
- **remote Provider**：`engine/remote`：包装 `OpenSandboxEngine` 的 Health/Create/Exec/Delete/Usage；Capabilities(provider=remote, isolation=remote, images=true, network_enforced=os)。
- **main 选择**：`SANDBOX_PROVIDER=vm|remote` 构造对应 Provider；health 返回真实 provider 与能力；不可用时明确报错，不静默降级。
- **崩溃转储**：`diagnostics.Options.CrashDir`；扫描最近 3 个 `*.dmp`，`crashes.json` 元数据 + 每个 dump 前 1MB；单测。
- **托盘**：`tray_windows.go` 使用 Win32（`RegisterClassW`/`CreateWindowExW`/`Shell_NotifyIconW`/`TrackPopupMenu`）实现 `Tray`；`tray_other.go` 使用内存实现；`GOOS=windows` 编译。
- **前端**：RuntimePanel 展示 vm/remote provider 能力（复用现有能力字段）；无新增页面。

### 3.2 不在本轮
- Windows 真机安装、AppContainer 实际降权验证。
- 代码签名、真实更新服务器。
- vm Provider 的真实 WSL2 执行（仅检测 + 明确 unsupported）。
- 前端新增大页面。

## 4. 关键设计

### 4.1 双 profile
```text
offline: ResearchAssistant.Sandbox       capabilities = []
online : ResearchAssistant.Sandbox.Online capabilities = [S-1-15-2-1 internetClient]
```
- `SetNetwork(on)` 只更新 session 标记；下一次 `Exec` 使用对应 profile 启动新进程，天然满足“重启进程”。
- `Capabilities.NetworkControl = isolation == appcontainer`；`NetworkEnforced = os`。
- 非 appcontainer 的 native 模式 `SetNetwork` 返回 `unsupported`。

### 4.2 vm/remote
- `vm.Detect(ctx)` 解析 `wsl.exe -l -v`：首行标题，后续 `* name state version`；返回发行版列表与默认发行版。
- `vm.Provider.Capabilities`：`{Provider:"vm", Isolation:"vm", Images:true, NetworkControl:true, NetworkEnforced:"os", GPU:false}`。
- `remote.Provider` 包装 `engine.OpenSandboxEngine`；`Capabilities` 固定 `remote/remote/os`。
- main：`SANDBOX_PROVIDER=vm` 且检测失败 → `/api/v1/health` 返回 `ok=false, error`，不静默切换。

### 4.3 崩溃转储
- `CrashDir = DataDir/crashes`；按 mtime 倒序取 3 个；每个文件最多 1MB；`crashes.json` 记录 name/size/modified。
- 目录不存在时输出空数组，不报错。

### 4.4 托盘
- Win32 消息窗口 + `Shell_NotifyIconW`；左键双击显示主窗口；菜单「显示主窗口」「退出」。
- 无 Windows 平台返回内存实现，保证 `go test ./...` 跨平台。

## 5. 测试策略（容器内）
1. docker-sandbox：`go test ./...`（vm 检测解析、remote 能力、双 profile 选择）、`GOOS=windows go build ./...`。
2. desktop：`go test ./...`（diagnostics crash、tray memory）、`GOOS=windows go build ./...` + Wails tag。
3. backend：`go build/vet/test`。
4. 前端：`npm test/build/lint`。
5. E2E：compose 四套脚本。

## 6. 完成定义
- [x] G1 双 profile
- [x] G2 vm/remote
- [x] G3 崩溃转储
- [x] G4 托盘驱动
- [x] G5 全部回归通过
- [x] FIX-REPORT-ROUND14

## 7. 风险
| 风险 | 对策 |
|---|---|
| Win32 托盘 API 结构复杂 | 复用 x/sys/windows；先保证交叉编译与消息循环最小实现 |
| WSL 命令在容器不存在 | Detect 仅在 Windows 编译；解析函数纯逻辑单测 |
| remote Provider 与 OpenSandbox 签名差异 | 先读接口再适配；编译期校验 |
| 双 profile capability SID | 使用 `windows.StringToSid("S-1-15-2-1")`；失败回退离线 profile |
