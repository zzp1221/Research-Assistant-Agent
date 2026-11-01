# 实施计划：全量回归与收口（2026-10-31 第二十六轮）

- 依据：用户完成条件 + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第二十五轮状态。
2. 写 spec/plan/tasks。
3. 确认 compose 栈健康。

## 阶段 P1 — 全量 E2E
1. 13 套批处理（repro 安装 unzip）。
2. 记录每套 exit code 与失败输出。

## 阶段 P2 — 四模块回归
1. backend 全量。
2. docker-sandbox 全量 + Windows 交叉编译。
3. desktop 全量 + Windows 交叉编译。
4. 前端 test/build/lint。

## 阶段 P3 — 收口
1. FIX-REPORT-ROUND26。
2. CHANGELOG/verification/design。
3. 目标评估与清理。
