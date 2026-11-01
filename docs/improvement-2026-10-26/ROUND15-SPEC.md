# 规格：VM Provider 执行 + 规划器能力门禁 + 本地指标/崩溃恢复提示（2026-10-31 第十五轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`（6.2 规划器能力门禁、6.5 vm Provider、13 本地指标、3.3 崩溃恢复提示）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑单测验证。
> 基线：`feature/desk`（含第十四轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND15.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | vm Provider 真实执行路径（WSL2） | `engine/vm` 支持 `ToWSLPath`/`FromWSLPath`/`BuildWSLArgs`；Create 保存发行版与工作区；Exec 通过 `wsl.exe -d <distro> --cd <path> -- argv` 执行；Delete/Usage/Reap；纯逻辑单测 + Windows 交叉编译 |
| G2 | 规划器能力门禁 | 创建计划时读取沙箱能力并写入 `Plan.Metadata["sandbox_capabilities"]`；当计划需要镜像/系统库而 provider 不支持时，产生 `sandbox_capability` 缺失项并阻断；后端单测 |
| G3 | 本地指标 | `desktop/shell` 新增 `/desktop/metrics`，聚合后端 `/metrics` 的 goroutine/内存/请求计数；RuntimePanel 展示；单测 |
| G4 | 崩溃恢复提示 | `desktop/shell` 状态增加 `last_restart_at`；前端 `DesktopStatusBanner` 轮询 `/desktop/state`，重启后提示“服务已重启，进行中的计划已中断”；组件测试 |
| G5 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E；无新增失败 |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 `GOOS=windows GOARCH=amd64 go build ./...` 通过（desktop 含 Wails tag）；
3. 前端 `npm test`、`npm run build`、`npm run lint` 通过；
4. compose 栈重建后 http-smoke、desktop-smoke、sandbox-command、sandbox-network 通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十五轮）

| 文档描述 | 实际 |
|---|---|
| 6.5 vm Provider 执行 | 第十四轮只有检测 + 能力矩阵 + `ErrUnavailable`，没有 WSL2 执行路径 |
| 6.2 规划器能力门禁 | health/capabilities 有矩阵；`Plan.Metadata` 未写入 `sandbox_capabilities`，planner 未按 provider 能力阻断 |
| 13 本地指标 | 只有后端 `/metrics`；桌面无本地指标接口/展示 |
| 3.3 崩溃恢复提示 | shell 有重启计数，但前端无提示 |
| 15 P4 | vm/remote 仅骨架 |

## 3. 范围

### 3.1 本轮实现
- **vm 执行**：`engine/vm/translate.go`（Windows→WSL 路径、argv 构造、发行版选择）、`provider.go` Create/Exec/Delete/Usage/Reap；非 Windows 返回 `ErrUnavailable`。
- **规划器门禁**：backend plan creation 读取 `sandbox_capabilities` 写入 Plan.Metadata；根据 `Images`/`NetworkControl`/`PythonVersions` 与计划节点需求生成 `sandbox_capability` 缺失项；单测。
- **本地指标**：`desktop/shell` `/desktop/metrics`（后端 `/metrics` 解析为 JSON：goroutines、heap、请求数）；RuntimePanel 新增“本地指标”区块。
- **崩溃恢复提示**：shell Status `LastRestartAt`；`DesktopStatusBanner` 轮询状态并在重启/失败时展示。
- **前端**：RuntimePanel 指标区块、DesktopStatusBanner、相应测试。

### 3.2 不在本轮
- Windows 真机 WSL2 执行验收（容器无 WSL2）。
- 代码签名、真实更新服务器、NSIS 真机安装。
- remote Provider 与真实 OpenSandbox 联调。

## 4. 关键设计

### 4.1 WSL 路径与命令
- `ToWSLPath("C:\\Users\\me\\ws") = "/mnt/c/Users/me/ws"`；`FromWSLPath` 反向。
- `BuildWSLArgs(distro, cwd, argv)` → `["-d", distro, "--cd", cwd, "--", argv...]`；未指定发行版时省略 `-d`。
- `Exec` 用 `exec.CommandContext(ctx, "wsl.exe", args...)`；非 Windows 直接返回 `ErrUnavailable`。
- 工作区默认 `DataDir/workspaces/<id>`；Create 时 `os.MkdirAll`。

### 4.2 规划器门禁
- 在 `CreatePlan` 成功后（或之前）调用 `Sandbox.Capabilities`，写入 `plan.Meta.SandboxCapabilities`。
- native 等 `capabilities.Images=false` 的 provider：计划中的 `image_prepare` 节点自动转换为 `runtime_probe`（准备 venv/探测运行时），与文档 6.2 一致。
- 若后续 runtime_probe 发现显式镜像/GPU/磁盘不足，由既有 `sandbox_capability` 等缺失项阻断执行。
- 能力读取失败不阻断计划，只记录 warning（避免后端不可用导致无法创建计划）。

### 4.3 本地指标
- `/desktop/metrics` 转发 backend `/api/metrics`（或 `/metrics`）并解析 Prometheus 文本中的 `go_goroutines`、`process_resident_memory_bytes`、`http_requests_total` 等，返回 `{goroutines, resident_memory_bytes, request_count, collected_at}`。
- 失败时返回 502 JSON，不阻塞 UI。

### 4.4 崩溃恢复提示
- shell Status 增加 `LastRestartAt`（RFC3339）。
- 前端 `DesktopStatusBanner` 每 5s 轮询 `/desktop/state`；当 `restarts>0` 或 `state=failed` 时显示黄色提示条；可关闭。

## 5. 测试策略（容器内）
1. docker-sandbox：`go test ./...`（vm translate/provider）、Windows 交叉编译。
2. backend：`go test ./...`（planner capability gate）。
3. desktop：`go test ./...`（metrics 解析、status）、Windows 交叉编译。
4. 前端：`npm test/build/lint`。
5. E2E：compose 四套。

## 6. 完成定义
- [x] G1 vm 执行
- [x] G2 规划器门禁
- [x] G3 本地指标
- [x] G4 崩溃恢复提示
- [x] G5 回归
- [x] FIX-REPORT-ROUND15

## 7. 风险
| 风险 | 对策 |
|---|---|
| WSL2 在容器不可用 | 纯逻辑测试 + Windows 交叉编译；运行时明确 unsupported |
| 规划器改动影响既有计划 | 只在 capability 明确不支持时阻断；默认能力读取失败不阻断 |
| metrics 格式变化 | 解析失败返回空指标 + 502 可选 |
| 前端轮询影响测试 | 组件测试注入状态，避免真实定时器 |
