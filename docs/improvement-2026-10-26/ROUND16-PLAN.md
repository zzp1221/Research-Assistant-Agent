# 实施计划：导出/复现包 Provider 分支 + 规划器细粒度门禁 + 原生对话框 + 签名脚本（2026-10-31 第十六轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十五轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — 导出分支
1. 复核 plan_export/repro bundle 的 environment manifest。
2. 按 provider 分支输出。
3. 单测 + repro-bundle E2E。

## 阶段 P2 — 规划器门禁
1. 解析 GPU/Python 需求。
2. 生成 gpu_unavailable/python_version_unavailable。
3. 单测。

## 阶段 P3 — 原生对话框
1. bridge ChooseDirectory/ShowInFolder。
2. 前端 helper + UI 入口。
3. 单测。

## 阶段 P4 — 签名脚手架
1. sign.ps1。
2. update-manifest.example.json。
3. RA_UPDATE_PUBLIC_KEY 注入。
4. 文档。

## 阶段 P5 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像 + compose + 四套 E2E。
4. 清理 + FIX-REPORT-ROUND16。
