# 规格：VM Provider 可测试化 + 更新安装流程 + NSIS 文件关联（2026-10-31 第十八轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`（6.5 vm Provider、11 自动更新/安装包、4.1 安装布局）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑/伪 wsl 单测验证。
> 基线：`feature/desk`（含第十七轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND18.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | VM Provider 可测试化 | Config 增加 WSLPath/AllowUnsupportedPlatform；Linux 容器用伪 wsl 脚本跑通 Create/Exec/Delete/Usage；单测 |
| G2 | 更新安装流程 | updater.Manager.Install（注入 Launcher）验签后启动安装器；bridge InstallUpdate；RuntimePanel “安装并重启”；单测 |
| G3 | NSIS 文件关联 | 可选 .pdf 关联 + researchassistant:// 协议保持；脚本完善 |
| G4 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十八轮）

| 文档描述 | 实际 |
|---|---|
| 6.5 vm Provider 执行 | 已有 WSL 路径/argv 翻译与 wsl.exe 执行，但 Exec 硬编码 wsl.exe，Linux 容器无法测试 |
| 11 自动更新 | Manager 只支持 Check/Download，没有 Install/启动安装器 |
| 11 安装包 | NSIS 只有协议关联，没有 .pdf 关联 |
| 15 P3/P4 | 安装/更新闭环仍未完成 |

## 3. 范围

### 3.1 本轮实现
- vm.Config 增加 WSLPath（默认 wsl.exe）、AllowUnsupportedPlatform（仅测试/伪 wsl）；Provider.Exec/Create 使用配置。
- 新增 vm provider 集成测试：临时伪 wsl 脚本、临时 workspace，验证 Create/Exec/Delete/Usage 与路径/argv。
- updater.Manager 增加 Install(path) 与 Launcher 注入（默认 Windows 启动安装器 /S）；bridge InstallUpdate；RuntimePanel 安装按钮。
- NSIS 增加可选 PDF 关联 section（默认不选中，避免劫持）。
- 文档/报告更新。

### 3.2 不在本轮
- 真实 Windows 签名证书、真实更新服务器、NSIS 真机安装。
- 真实 WSL2/OpenSandbox 服务。
- 完整静默升级回滚。

## 4. 关键设计

### 4.1 VM Provider
Config 增加 WSLPath、AllowUnsupportedPlatform。Create 在非 Windows 且未允许时返回 ErrUnavailable；Exec 使用 wslPath。伪 wsl 测试通过 shell 脚本输出 argv，验证 BuildWSLArgs/TranslateVMCommand 的端到端效果。

### 4.2 更新安装
Manager.Install(path) 校验 path 存在、非空；Launcher 默认 Windows 执行 installer /S，非 Windows 返回 unsupported；bridge InstallUpdate 调用 Manager.Install；前端下载成功后显示“安装并重启”按钮。

### 4.3 NSIS
新增可选 Section “AssociatePDF”，写 HKCU Software\Classes\.pdf 和 ResearchAssistant.pdf；默认不选中，用户可勾选。协议关联保持。

## 5. 测试策略（容器内）
1. docker-sandbox：go test ./...（vm 伪 wsl 集成）。
2. desktop：go test ./...（updater Install/bridge）。
3. backend：go build/vet/test。
4. 前端：npm test/build/lint。
5. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 VM 可测试化
- [x] G2 更新安装
- [x] G3 NSIS 文件关联
- [x] G4 回归
- [x] FIX-REPORT-ROUND18

## 7. 风险
| 风险 | 对策 |
|---|---|
| 伪 wsl 脚本平台差异 | 测试仅 Linux 容器执行，脚本 chmod +x |
| 安装器启动破坏测试 | Launcher 注入 fake；默认仅 Windows 执行 |
| PDF 关联被默认勾选 | 默认不选中，显式说明 |
