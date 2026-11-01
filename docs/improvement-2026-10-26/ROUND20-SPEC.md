# 规格：运行时设置持久化 + 运行时状态 + 诊断/日志入口（2026-10-31 第二十轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（5.1 settings、6.3.1 运行时管理、10 前端、13 诊断）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑单测验证。
> 基线：feature/desk（含第十九轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND20.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 运行时设置持久化 | bridge GetRuntimeSettings/UpdateProvider 读写 settings.json；provider 校验；运行中重启；前端 RuntimePanel 通过 bridge 读写；单测 |
| G2 | 运行时状态 | native Provider RuntimeStatus（Python 版本/venv 数/磁盘）；sandbox /api/v1/runtime/status；shell /desktop/runtime；RuntimePanel 展示；单测 |
| G3 | 诊断/日志入口 | RuntimePanel 增加“导出诊断包”“打开日志目录”按钮，使用 bridge；错误可读 |
| G4 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十轮）

| 文档描述 | 实际 |
|---|---|
| 5.1 provider 设置 | RuntimePanel 只写 localStorage，不写 settings.json，重启后失效 |
| 6.3.1 运行时管理 | RuntimeManager 有 venv 缓存，但没有对外状态接口；UI 无法展示安装/磁盘占用 |
| 10 诊断入口 | bridge 有 ExportDiagnostics/OpenLogs，但 RuntimePanel 没有按钮 |
| 13 诊断包 | 已有生成逻辑，缺少常规入口 |

## 3. 范围

### 3.1 本轮实现
- bridge：RuntimeSettingsView、GetRuntimeSettings、UpdateProvider（校验 auto/native/docker/vm/remote，保存并重启）。
- native Provider：RuntimeStatus（扫描 Python 运行时/venv/磁盘）。
- sandbox main：GET /api/v1/runtime/status（native）或 501（其他）。
- shell：FetchRuntimeStatus + /desktop/runtime。
- 前端：RuntimePanel 读取/保存 provider，展示运行时状态、导出诊断包、打开日志目录。
- 单测/组件测试。

### 3.2 不在本轮
- 真实运行时下载进度（只展示已安装状态）。
- 真实 Windows 安装/签名/WSL2。

## 4. 关键设计

### 4.1 设置持久化
bridge 读取 settings.json，UpdateProvider 校验枚举后 Save；若 shell 运行中则 Restart，使 SANDBOX_PROVIDER 生效。失败返回明确错误。

### 4.2 运行时状态
native RuntimeStatus 扫描 RuntimeRoot/VenvRoot：Python 版本目录名、venv 数量、总磁盘 MB。sandbox 只对 native 暴露；docker/vm/remote 返回 supported=false。

### 4.3 诊断/日志
RuntimePanel 按钮调用 bridge.ExportDiagnostics（显示 path）与 bridge.OpenLogs；浏览器模式提示仅桌面版可用。

## 5. 测试策略（容器内）
1. desktop：go test ./...（bridge settings、shell runtime status、native status? native in sandbox）。
2. docker-sandbox：go test ./...（RuntimeStatus 临时目录）。
3. backend：build/vet/test。
4. 前端：test/build/lint。
5. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 设置持久化
- [x] G2 运行时状态
- [x] G3 诊断/日志入口
- [x] G4 回归
- [x] FIX-REPORT-ROUND20

## 7. 风险
| 风险 | 对策 |
|---|---|
| 重启 shell 影响测试 | 单测用 nil shell；真实重启只在运行中 |
| runtime 状态扫描大目录慢 | 限制扫描深度/忽略错误 |
| 前端 bridge 不存在 | localStorage 回退 + 提示 |
