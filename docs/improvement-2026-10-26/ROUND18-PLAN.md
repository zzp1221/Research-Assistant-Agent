# 实施计划：VM Provider 可测试化 + 更新安装流程 + NSIS 文件关联（2026-10-31 第十八轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十七轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — VM Provider 可测试化
1. Config WSLPath/AllowUnsupportedPlatform。
2. Provider Exec/Create 使用配置。
3. 伪 wsl 集成测试（Create/Exec/Delete/Usage）。
4. Windows 交叉编译。

## 阶段 P2 — 更新安装
1. Manager.Install + Launcher。
2. 单测（fake launcher、缺失文件、非 Windows）。
3. bridge InstallUpdate。
4. RuntimePanel 安装按钮。

## 阶段 P3 — NSIS
1. 可选 PDF 关联 section。
2. 文档说明。

## 阶段 P4 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像 + compose + E2E。
4. 清理 + FIX-REPORT-ROUND18。
