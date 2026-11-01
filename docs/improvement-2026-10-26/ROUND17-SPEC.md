# 规格：自动更新闭环 + Remote Provider 集成 + 导出审计 + Windows 回归脚本（2026-10-31 第十七轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`（11 自动更新、6.5 remote、9 审计、14.2 安全回归）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑单测验证。
> 基线：`feature/desk`（含第十六轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND17.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 自动更新闭环 | updater.Manager 提供 Check/Download/Verify；bridge 暴露 CheckForUpdate/DownloadUpdate；RuntimePanel 更新区块；main 从 RA_UPDATE_ENDPOINT/RA_UPDATE_PUBLIC_KEY 构建；单测 |
| G2 | Remote Provider 集成 E2E | 新增 sandbox-remote.mjs：Node mock OpenSandbox + 独立 sandbox 进程（SANDBOX_PROVIDER=remote），覆盖 create/exec/delete；容器内通过 |
| G3 | 导出审计 | plan export 记录 provider/isolation 审计事件；单测/接口验证 |
| G4 | Windows 安全回归脚本 | desktop/test/windows-runner-probe.ps1 覆盖附录 D 7 项；desktop/test/README.md 说明真机执行 |
| G5 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle + remote E2E |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test、npm run build、npm run lint 通过；
4. compose 栈四套 E2E + repro-bundle + remote E2E 通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十七轮）

| 文档描述 | 实际 |
|---|---|
| 11 自动更新 | updater 只有 Checker/Installer；无 Manager、无 bridge/UI 闭环 |
| 6.5 remote Provider | 已有 remote Provider 与 main 选择，但没有独立 E2E 覆盖 create/exec/delete |
| 9 审计 | 导出包按 provider 分支已做；导出审计事件未记录 provider/isolation |
| 14.2 安全回归 | 无 Windows 真机脚本 |
| 15 P3 | 更新闭环/签名真机未完成 |

## 3. 范围

### 3.1 本轮实现
- updater.Manager：Check（Checker）、Download（Installer）、Status 结构；支持 endpoint/public key 为空时明确错误。
- bridge：CheckForUpdate()、DownloadUpdate(version)；错误可读；下载目录 DataDir/updates。
- frontend：RuntimePanel 新增“更新”区块（检查/下载按钮、状态/错误、版本信息）。
- main_wails：从 RA_UPDATE_ENDPOINT、RA_UPDATE_PUBLIC_KEY 构建 Manager，注入 bridge。
- remote E2E：Node mock OpenSandbox（/v1/sandboxes、/commands、DELETE）+ 启动 sandbox 进程（remote provider），覆盖 create/exec/delete。
- 导出审计：recordAuditEvent("plan_exported", ...) 带 provider/isolation/format。
- Windows 回归脚本：PowerShell 脚本 + README。

### 3.2 不在本轮
- 真实代码签名证书、真实更新服务器、NSIS 真机安装。
- 真实 WSL2/OpenSandbox 联调。
- 完整更新安装器（仅下载/校验/暂存）。

## 4. 关键设计

### 4.1 Update Manager
Manager 持有 Checker、Installer、DownloadDir。Check 返回 UpdateStatus{Available,Version,NotesURL,Path}；Download 下载并验签后返回本地路径。公钥或 endpoint 缺失返回明确错误，不 panic；下载目录 DataDir/updates；失败删除 part 文件。

### 4.2 Remote E2E
mock 服务实现 OpenSandbox REST：POST /v1/sandboxes 返回 {id}；POST /v1/sandboxes/:id/proxy/49152/commands 返回 {stdout,exit_code}；DELETE。启动 sandbox 二进制（SANDBOX_PROVIDER=remote、OPEN_SANDBOX_URL、SANDBOX_API_TOKEN、SANDBOX_LISTEN_ADDR），通过 sandbox REST 调用 create/exec/delete，断言 provider=remote。

### 4.3 导出审计
在 exportPlan / zip 导出成功路径记录 plan_exported 事件，payload 含 format、sandbox_provider、isolation。单测可检查审计记录或接口响应。

### 4.4 Windows 回归
desktop/test/windows-runner-probe.ps1：运行附录 D 7 项（multiprocessing/torch/pip/matplotlib/写桌面/网络/Job 清理），输出 JSON 报告。仅脚本与文档，容器内不执行。

## 5. 测试策略（容器内）
1. desktop：go test ./...（updater manager/bridge）、Windows 交叉编译。
2. backend：go test ./...（导出审计）、全量。
3. docker-sandbox：go test ./...、Windows 交叉编译。
4. 前端：npm test/build/lint。
5. E2E：四套 + repro-bundle + remote。

## 6. 完成定义
- [x] G1 自动更新闭环
- [x] G2 Remote E2E
- [x] G3 导出审计
- [x] G4 Windows 回归脚本
- [x] G5 回归
- [x] FIX-REPORT-ROUND17

## 7. 风险
| 风险 | 对策 |
|---|---|
| 更新下载无真实服务器 | Manager 用 httptest 单测；bridge 无配置时明确错误 |
| remote E2E 端口/进程管理 | 使用随机端口 + 子进程 kill；失败输出日志 |
| 审计事件影响既有测试 | 仅在导出成功路径追加，不改响应体 |
| Windows 脚本无法执行 | 只做脚本与文档，交叉编译不受影响 |
