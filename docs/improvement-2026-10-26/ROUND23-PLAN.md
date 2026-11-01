# 实施计划：更新可用提示 + 系统包能力门禁 + 回归（2026-10-31 第二十三轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第二十二轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 更新提示
1. useUpdateAvailable hook。
2. UpdateAvailableBanner 组件/测试。
3. ResearchAssistantApp 集成。

## 阶段 P2 — 系统包门禁
1. runtimeProbe SystemPackages。
2. attachSandboxCapabilities SystemPackages。
3. 单测。

## 阶段 P3 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像（backend/frontend）+ compose + E2E。
4. 清理 + FIX-REPORT-ROUND23。
