# 规格：更新可用提示 + 系统包能力门禁 + 回归（2026-10-31 第二十三轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（11 自动更新、6.2 能力门禁、10 前端）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑单测验证。
> 基线：feature/desk（含第二十二轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND23.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 更新可用提示 | main_wails 已 emit ra:update-available；前端新增 hook + banner 监听 Wails runtime 事件，显示版本/Release Notes，可关闭；组件测试 |
| G2 | 系统包能力门禁 | runtimeProbe 在 native provider + SystemPackages 非空时生成 system_package_unavailable；计划工件同样前置检查；单测 |
| G3 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十三轮）

| 文档描述 | 实际 |
|---|---|
| 11 自动更新 | main_wails 启动检查后 emit 事件，但前端没有监听/提示 |
| 6.2 能力门禁 | runtimeProbe 已检查 GPU/磁盘/镜像/Python，没有检查 SystemPackages |
| 10 前端 | 没有更新可用 banner |

## 3. 范围

### 3.1 本轮实现
- 前端 useUpdateAvailable hook + UpdateAvailableBanner；监听 window.runtime.EventsOn('ra:update-available')；展示版本/notes，可关闭。
- 集成到 ResearchAssistantApp 工作台顶部。
- runtimeProbe + attachSandboxCapabilities 增加 system_package_unavailable 门禁（native）。
- 单测/组件测试。

### 3.2 不在本轮
- 真实更新服务器/签名。
- Windows 真机。

## 4. 关键设计

### 4.1 更新 banner
EventsOn 回调 payload 为 updater.Manifest（version/url/sha256/signature/release_notes_url）。无 runtime 环境（浏览器）时静默返回 null。banner 显示“发现新版本 X”与 Release Notes 链接，可关闭。

### 4.2 系统包门禁
若 spec.SystemPackages 非空且 provider 为 native（无 OCI/系统包管理），新增 MissingRequirement{Kind:"system_package_unavailable"}，remediation 建议 Docker/远程或宿主预装。计划工件存在时同样在 attachSandboxCapabilities 前置检查并 blocked。

## 5. 测试策略（容器内）
1. 前端：hook/组件测试（mock window.runtime.EventsOn）、test/build/lint。
2. backend：runtimeProbe/plan gate 单测、全量。
3. docker-sandbox/desktop：全量 + Windows 交叉编译。
4. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 更新提示
- [x] G2 系统包门禁
- [x] G3 回归
- [x] FIX-REPORT-ROUND23

## 7. 风险
| 风险 | 对策 |
|---|---|
| Wails runtime 事件 API 在测试不可用 | hook 可选链 + 注入 fake runtime |
| 门禁误伤 | 仅 native + SystemPackages 非空 |
