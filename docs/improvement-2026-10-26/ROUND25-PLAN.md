# 实施计划：运行时缓存清理 + 回归（2026-10-31 第二十五轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第二十四轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 缓存清理
1. CleanRuntimeCache + Provider.CleanupRuntimeCache。
2. sandbox endpoint。
3. shell/bridge/UI。
4. 单测。

## 阶段 P2 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像（sandbox/frontend）+ compose + E2E。
4. 清理 + FIX-REPORT-ROUND25。
