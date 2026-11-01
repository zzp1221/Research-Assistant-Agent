# 实施计划：运行时设置持久化 + 运行时状态 + 诊断/日志入口（2026-10-31 第二十轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十九轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 设置持久化
1. bridge RuntimeSettingsView/Get/Update。
2. 单测（临时 settings、非法 provider）。
3. RuntimePanel 读取/保存。

## 阶段 P2 — 运行时状态
1. native RuntimeStatus。
2. sandbox /api/v1/runtime/status。
3. shell FetchRuntimeStatus + /desktop/runtime。
4. RuntimePanel 展示。
5. 单测。

## 阶段 P3 — 诊断/日志
1. RuntimePanel 按钮。
2. 状态/错误展示。

## 阶段 P4 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像（sandbox/frontend）+ compose + E2E。
4. 清理 + FIX-REPORT-ROUND20。
