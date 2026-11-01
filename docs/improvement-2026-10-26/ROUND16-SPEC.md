# 规格：导出/复现包 Provider 分支 + 规划器细粒度门禁 + 原生对话框 + 签名脚本（2026-10-31 第十六轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`（9 环境保真度/导出、6.2 能力门禁、10 原生对话框、11 签名）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑单测验证。
> 基线：`feature/desk`（含第十五轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND16.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 导出/复现包按 provider 分支 | `EnvironmentManifest` 输出 provider/isolation/runtime_hash/base_os/network_enforced；native 导出不含 OCI image、含 runtime_hash；docker 导出含 image/digest；后端单测 |
| G2 | 规划器细粒度能力门禁 | GPU 需求且 provider 不支持 → `gpu_unavailable`；请求 Python 版本不在 `PythonVersions` → `python_version_unavailable`；单测 |
| G3 | 原生对话框 | bridge 增加 `ChooseDirectory` / `ShowInFolder`；前端 `desktopBridge` 暴露；组件/服务测试 |
| G4 | 签名与更新脚手架 | `desktop/build/sign.ps1`、`desktop/build/update-manifest.example.json`、updater 公钥从 `RA_UPDATE_PUBLIC_KEY` 注入；文档更新 |
| G5 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 `GOOS=windows GOARCH=amd64 go build ./...` 通过（desktop 含 Wails tag）；
3. 前端 `npm test`、`npm run build`、`npm run lint` 通过；
4. compose 栈重建后四套 E2E 通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第十六轮）

| 文档描述 | 实际 |
|---|---|
| 9.1 EnvironmentManifest 扩展 | 模型字段已有；导出/复现包是否按 provider 分支需复核 |
| 6.2 能力门禁 | 第十五轮已有 capability 快照与 image_prepare→runtime_probe；GPU/Python 版本前置门禁未接入 |
| 10 原生对话框 | `desktop/bridge` 无 ChooseDirectory/ShowInFolder；前端无调用 |
| 11 签名 | 只有 NSIS 骨架；无签名脚本/更新公钥注入 |

## 3. 范围

### 3.1 本轮实现
- **导出分支**：在 plan export / repro bundle 的 environment manifest 中按 provider 输出；native 不写 image/digest，写 runtime_hash/base_os/network_enforced；docker 写 image/digest。
- **规划器门禁**：在 `attachSandboxCapabilities` 中读取计划 runtime spec 的 GPU/Python 版本需求（从节点/上下文可解析部分），生成 `gpu_unavailable` / `python_version_unavailable` 缺失项；不误伤无需求计划。
- **原生对话框**：`desktop/bridge` 增加 `ChooseDirectory`（Wails runtime.OpenDirectoryDialog）与 `ShowInFolder`（explorer /select）；前端 `desktopBridge` 暴露 `chooseDirectory/showInFolder`，在计划库/导出入口提供“选择仓库目录/在文件夹中显示”能力。
- **签名脚手架**：`desktop/build/sign.ps1`（signtool 签名 exe/安装包）、`update-manifest.example.json`、main_wails 从 `RA_UPDATE_PUBLIC_KEY` 注入 updater 公钥；README 说明。

### 3.2 不在本轮
- Windows 真机签名/安装/更新服务器。
- 真实 WSL2/OpenSandbox 联调。
- 完整 UI 重做。

## 4. 关键设计

### 4.1 导出分支
- 读取 `plan.EnvironmentManifest`；若为空，按 `plan.Meta.SandboxCapabilities` 构造最小 manifest。
- native：`Image=""`、`ImageDigest=""`、`RuntimeHash` 必填（缺失时保留空并标注），`Isolation=appcontainer|lowil`。
- docker：`Image/ImageDigest` 保留，`RuntimeHash` 为空。
- 导出 JSON/zip 的 `environment.json` 与 manifest 一致；审计事件记录 provider/isolation。

### 4.2 门禁
- 从计划节点/上下文中解析 `runtime_spec`（`gpu_required`、`gpu_type`、`python_version`）。
- GPU：`caps.GPU==false || !caps.GPUEnforced` → `gpu_unavailable`。
- Python：`caps.PythonVersions` 非空且请求版本不在列表 → `python_version_unavailable`。
- 仅在能明确解析需求时添加，避免误阻断。

### 4.3 对话框
- `ChooseDirectory(title string) (string,error)`：Wails runtime.OpenDirectoryDialog；非 Wails 返回空字符串不报错。
- `ShowInFolder(path string) error`：Windows `explorer.exe /select,<path>`；非 Windows 返回 unsupported。
- 前端 helper：`desktopBridge.chooseDirectory/showInFolder`，浏览器模式 no-op。

## 5. 测试策略（容器内）
1. backend：导出 manifest 单测、能力门禁单测、全量 build/vet/test。
2. desktop：bridge 对话框方法单测（注入 fake dialog）、Windows 交叉编译。
3. docker-sandbox：全量 test + Windows 交叉编译。
4. 前端：test/build/lint。
5. E2E：四套脚本。

## 6. 完成定义
- [x] G1 导出分支
- [x] G2 规划器门禁
- [x] G3 原生对话框
- [x] G4 签名脚手架
- [x] G5 回归
- [x] FIX-REPORT-ROUND16

## 7. 风险
| 风险 | 对策 |
|---|---|
| 导出格式破坏既有 E2E | 保留现有字段，新增字段 omitempty；repro-bundle E2E 复跑 |
| 门禁误伤 | 仅在明确解析到需求时触发；单测覆盖无需求场景 |
| Wails 对话框在非 Windows 不可用 | bridge 注入 fake；非 Wails 返回空/unsupported |
| 签名脚本无法在容器执行 | 只做脚本与文档，交叉编译不受影响 |
