# 规格：运行时缓存清理 + 回归（2026-10-31 第二十五轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（6.3.1 运行时缓存与磁盘占用清理、10 RuntimePanel）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 专有代码以 GOOS=windows 交叉编译 + 纯逻辑单测验证。
> 基线：feature/desk（含第二十四轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND25.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 运行时缓存清理 | native CleanupRuntimeCache（拒绝安装中；删除 venv/runtime 并重建空目录）；sandbox POST /api/v1/runtime/cleanup；shell POST /desktop/runtime/cleanup；bridge cleanupRuntimes；UI 按钮 |
| G2 | 单测 | CleanRuntimeCache 临时目录测试；shell cleanup httptest；Windows 交叉编译 |
| G3 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十五轮）

| 文档描述 | 实际 |
|---|---|
| 6.3.1 缓存与磁盘占用清理 | RuntimeStatus 只展示占用，没有清理入口 |
| 10 RuntimePanel | 有运行时状态/准备按钮，没有清理按钮 |

## 3. 范围

### 3.1 本轮实现
- native CleanRuntimeCache + Provider.CleanupRuntimeCache。
- sandbox/shell/bridge/UI 清理链路。
- 单测/组件测试。

### 3.2 不在本轮
- 自动清理策略/配额。
- Windows 真机验证。

## 4. 关键设计

### 4.1 清理
删除 RuntimeRoot 与 VenvRoot 后重建空目录；安装中返回明确错误。清理后 RuntimeStatus 重新扫描（venv=0、disk 约为 0）。

### 4.2 UI
RuntimePanel 增加“清理运行时缓存”按钮；安装中禁用；点击后更新状态并显示错误。

## 5. 测试策略（容器内）
1. native：CleanRuntimeCache 临时目录（文件被删、目录重建）。
2. shell：httptest POST 路径/鉴权/返回。
3. 三模块全量 + Windows 交叉编译。
4. 前端 test/build/lint。
5. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 缓存清理
- [x] G2 单测
- [x] G3 回归
- [x] FIX-REPORT-ROUND25

## 7. 风险
| 风险 | 对策 |
|---|---|
| 误删用户数据 | 只删 RuntimeRoot/VenvRoot；UI 二次确认 |
| 与安装并发 | installMu + installing 检查 |
