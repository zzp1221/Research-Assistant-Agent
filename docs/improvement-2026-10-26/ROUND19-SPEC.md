# 规格：崩溃转储注册 + 打包流水线 + 全量 E2E 回归（2026-10-31 第十九轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（13 崩溃转储、11 打包、14 测试策略）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑单测验证。
> 基线：feature/desk（含第十八轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND19.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 崩溃转储注册 | desktop/crashdump：Targets/RegistryPath 纯逻辑 + Windows Enable 写 HKCU WER LocalDumps（ResearchAssistant/backend/sandbox）；非 Windows no-op；单测 |
| G2 | 打包流水线 | desktop/build/package.ps1：校验/暂存 exe、uv、webview、makensis；README 说明；脚本存在且参数合理 |
| G3 | 全量 E2E 回归 | 运行现有非 LLM/API 级 E2E 套件（http-smoke、routing-and-prompts、literature、literature-plan、document-intelligence、model-profiles、verification、plan-library、repro-bundle、cost-budget、budget-recovery、plan-revisions、review-hitl、tool-ecosystem、sandbox-command、sandbox-network、desktop-smoke）；失败则修复 |
| G4 | 回归 | 三模块 build/vet/test；前端 test/build/lint；不新增失败 |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 套件结果记录（环境限制项单独标注）；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十九轮）

| 文档描述 | 实际 |
|---|---|
| 13 崩溃转储 | diagnostics 已收集 data/crashes 下 dump，但没有任何代码注册 WER LocalDumps，实际不会产生 dump |
| 11 打包 | 有 installer.nsi 与 sign.ps1，没有 staging/makensis 脚本 |
| 14 测试策略 | 此前只跑 http-smoke/desktop-smoke/sandbox-command/sandbox-network/repro-bundle；其余既有 E2E 未全量复跑 |
| 15 P3 | 崩溃转储注册缺失 |

## 3. 范围

### 3.1 本轮实现
- desktop/crashdump：Targets、RegistryPath、Windows Enable/Disable、非 Windows no-op；main_wails 启动时 Enable。
- desktop/build/package.ps1 + build/README.md。
- 全量 E2E 回归并修复发现的问题（仅限真实回归，不强行改无关环境）。
- 文档/报告。

### 3.2 不在本轮
- 真实 Windows 安装/签名/WER dump 验证。
- 真实 LLM 场景与需要外部网络的 E2E（标注环境限制）。
- 遥测上报。

## 4. 关键设计

### 4.1 崩溃转储
- WER 注册表路径：HKCU\Software\Microsoft\Windows\Windows Error Reporting\LocalDumps\<exe>。
- 值：DumpFolder=<DataDir>\crashes（REG_EXPAND_SZ）、DumpType=2（完整 dump）、DumpCount=5。
- 目标 exe：ResearchAssistant.exe、backend.exe、sandbox.exe。
- Windows 用 golang.org/x/sys/windows/registry；非 Windows 返回 nil（no-op）。

### 4.2 打包
- package.ps1 参数：Version、Configuration、WailsBin、Output。
- 校验 ResearchAssistant.exe/backend.exe/sandbox.exe/uv.exe/webview/dist 存在；复制到 staging；调用 makensis /DVERSION。
- 失败明确退出码。

### 4.3 E2E
- 使用运行中的 compose 栈与 test/e2e 脚本；记录每个脚本的通过/失败/环境限制。
- 失败若为真实回归则修复；若依赖外部 LLM/网络则标注环境限制。

## 5. 测试策略（容器内）
1. desktop：go test ./...（crashdump 纯逻辑）、Windows 交叉编译。
2. docker-sandbox/backend：全量 build/vet/test。
3. 前端：npm test/build/lint。
4. E2E：上述套件。

## 6. 完成定义
- [x] G1 崩溃转储注册
- [x] G2 打包流水线
- [x] G3 全量 E2E 回归
- [x] G4 回归
- [x] FIX-REPORT-ROUND19

## 7. 风险
| 风险 | 对策 |
|---|---|
| WER registry API 在 Linux 不可用 | 纯逻辑 Targets/RegistryPath 单测 + Windows 交叉编译 |
| 打包脚本无法在容器执行 | 只做脚本/文档，不阻塞 Go/前端测试 |
| E2E 依赖 LLM/网络 | 标注环境限制，不当作代码回归 |
