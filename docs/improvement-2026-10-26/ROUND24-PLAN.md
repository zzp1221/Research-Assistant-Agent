# 实施计划：NSIS 版本参数化 + 安装包编译烟测 + 回归（2026-10-31 第二十四轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / debian:bookworm-slim + nsis）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第二十三轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — NSIS 版本
1. installer.nsi !ifndef VERSION/版本字段。
2. README 说明。

## 阶段 P2 — 编译烟测
1. installer-smoke.sh。
2. 容器内 makensis 执行。

## 阶段 P3 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. compose + E2E。
4. 清理 + FIX-REPORT-ROUND24。
