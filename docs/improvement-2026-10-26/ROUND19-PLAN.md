# 实施计划：崩溃转储注册 + 打包流水线 + 全量 E2E 回归（2026-10-31 第十九轮）

- 依据：docs/design/2026-10-30-windows-desktop-client.md + spec.md
- 基线：feature/desk
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：spec.md 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十八轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 崩溃转储
1. crashdump Targets/RegistryPath。
2. Windows Enable/Disable。
3. main_wails 接线。
4. 单测 + Windows 交叉编译。

## 阶段 P2 — 打包流水线
1. package.ps1。
2. build/README.md。
3. 参数/路径检查。

## 阶段 P3 — 全量 E2E
1. 运行既有 E2E 套件。
2. 记录结果、修复真实回归。
3. 标注环境限制。

## 阶段 P4 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像（如需要）+ compose + E2E。
4. 清理 + FIX-REPORT-ROUND19。
