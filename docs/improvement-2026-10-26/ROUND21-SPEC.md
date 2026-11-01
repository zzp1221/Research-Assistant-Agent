# 规格：运行时安装触发 + Docker 升级提示 + 阶段状态修正（2026-10-31 第二十一轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（5.4 运行时下载、6.3.1 运行时管理、P4 docker 升级提示、15 阶段状态）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑/伪 uv 单测验证。
> 基线：feature/desk（含第二十轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND21.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 运行时安装触发 | native Provider InstallRuntime（EnsureRuntime + 状态）；sandbox POST /api/v1/runtime/install；shell POST /desktop/runtime/install；bridge installRuntime；RuntimePanel “准备 Python 运行时”；伪 uv 单测 |
| G2 | Docker 升级提示 | RuntimePanel 检测 capabilities.provider=docker 且当前偏好非 docker 时显示“检测到 Docker，建议使用容器”提示与一键切换 |
| G3 | 阶段状态修正 | 文档 15 节 P2/P3/P4 标题从“未开始”改为实际状态（部分完成），与状态表一致 |
| G4 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十一轮）

| 文档描述 | 实际 |
|---|---|
| 5.4 首次启动下载 Python 运行时 | onboarding 只有说明，没有触发安装的接口/按钮 |
| 6.3.1 运行时管理 | RuntimeStatus 只展示已安装缓存，不能触发 EnsureRuntime |
| P4 docker 自动升级提示 | RuntimePanel 有 provider 列表，但没有检测到 Docker 的提示 |
| 15 阶段标题 | P2/P3/P4 标题仍是“未开始”，与状态表不符 |

## 3. 范围

### 3.1 本轮实现
- native Provider InstallRuntime(ctx, pythonVersion)：调用 RuntimeManager.EnsureRuntime 并返回 RuntimeStatus。
- sandbox POST /api/v1/runtime/install；非 native 返回 supported=false。
- shell InstallRuntime + POST /desktop/runtime/install。
- bridge InstallRuntime；前端 RuntimePanel “准备 Python 运行时”按钮与状态。
- RuntimePanel Docker 升级提示与一键切换。
- 文档 15 节标题修正。
- 伪 uv 脚本单测（EnsureRuntime/InstallRuntime）。

### 3.2 不在本轮
- 真实 uv 下载进度百分比/断点续传（只显示“准备中”与结果）。
- 真实 Windows/WSL2。

## 4. 关键设计

### 4.1 Runtime install
- native InstallRuntime 直接调用现有 RuntimeManager.EnsureRuntime；失败返回错误与日志路径（RuntimeManager 已写 requirements lock）。
- sandbox 只对 native 暴露；其他 provider supported=false。
- shell 通过 sandbox URL/token POST；bridge 透传；UI 显示 preparing/ready/error。

### 4.2 Docker prompt
- capabilities.provider == docker 且本地 provider 偏好不是 docker → 显示提示条与“切换到 Docker”按钮（调用 chooseProvider('docker')）。

### 4.3 文档
- 15 P2/P3/P4 标题改为“部分完成”，与状态表一致。

## 5. 测试策略（容器内）
1. native：伪 uv 脚本测试 EnsureRuntime 与 InstallRuntime。
2. shell：httptest 测试 InstallRuntime。
3. desktop/sandbox/backend 全量。
4. 前端 test/build/lint。
5. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 运行时安装
- [x] G2 Docker 提示
- [x] G3 阶段状态
- [x] G4 回归
- [x] FIX-REPORT-ROUND21

## 7. 风险
| 风险 | 对策 |
|---|---|
| 伪 uv 脚本需模拟 venv 布局 | 测试内创建 bin/python 或 Scripts/python.exe |
| 安装阻塞请求 | UI 显示 preparing；后续可改异步 |
| Docker 提示误伤 | 仅在 capabilities 明确 docker 且偏好非 docker 时显示 |
