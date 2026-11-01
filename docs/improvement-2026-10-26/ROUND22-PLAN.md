# 实施计划：运行时安装进度 + UI 轮询 + 回归（2026-10-31 第二十二轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第二十一轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 安装阶段
1. RuntimeManager EnsureRuntimeWithProgress。
2. native Provider 安装状态字段。
3. RuntimeStatus 输出。
4. 伪 uv 单测。

## 阶段 P2 — UI 轮询
1. RuntimePanel 安装期间轮询。
2. 展示 stage/error。

## 阶段 P3 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像（sandbox/frontend）+ compose + E2E。
4. 清理 + FIX-REPORT-ROUND22。
