# 规格：运行时安装进度 + UI 轮询 + 回归（2026-10-31 第二十二轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（5.4 Python 运行时下载进度、6.3.1 运行时管理）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑/伪 uv 单测验证。
> 基线：feature/desk（含第二十一轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND22.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 安装阶段进度 | RuntimeManager.EnsureRuntimeWithProgress 上报 python/venv/dependencies/freeze/done；native Provider 暴露 installing/stage/install_error；伪 uv 单测 |
| G2 | UI 进度轮询 | RuntimePanel 安装期间轮询 /desktop/runtime，展示阶段与错误；组件/构建通过 |
| G3 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十二轮）

| 文档描述 | 实际 |
|---|---|
| 5.4 下载进度 | 第二十一轮只显示“准备中”，没有阶段信息 |
| 6.3.1 运行时管理 | RuntimeStatus 只有结果；安装过程不可观测 |

## 3. 范围

### 3.1 本轮实现
- RuntimeManager 增加 EnsureRuntimeWithProgress，原 EnsureRuntime 保持兼容。
- native Provider 记录安装状态（installing/stage/error），RuntimeStatus 返回。
- RuntimePanel 安装期间每 2s 轮询 /desktop/runtime，显示阶段。
- 伪 uv 单测验证阶段顺序。

### 3.2 不在本轮
- 下载百分比/字节进度/断点续传（uv 不提供稳定百分比）。
- Windows 真机验证。

## 4. 关键设计

### 4.1 阶段
python → venv → dependencies（仅有 requirements 时）→ freeze → done；失败 stage=error，install_error 保留消息。

### 4.2 状态
RuntimeStatus 增加 installing/stage/install_error（omitempty），sandbox/shell 已有的 /runtime/status 直接透传；UI 只读。

## 5. 测试策略（容器内）
1. native：伪 uv 测试阶段顺序与 InstallRuntime 状态。
2. desktop：go test（shell runtime 透传）。
3. 前端：test/build/lint。
4. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 安装阶段
- [x] G2 UI 轮询
- [x] G3 回归
- [x] FIX-REPORT-ROUND22

## 7. 风险
| 风险 | 对策 |
|---|---|
| 轮询与安装请求并发 | 状态用 mutex；UI 只读 |
| 阶段名称变化 | 测试固定阶段集合，文档同步 |
