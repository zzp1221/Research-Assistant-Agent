# 实施计划：自动更新闭环 + Remote Provider 集成 + 导出审计 + Windows 回归脚本（2026-10-31 第十七轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十六轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — Update Manager
1. updater.Manager Check/Download/Status。
2. 单测（httptest + ed25519）。
3. bridge CheckForUpdate/DownloadUpdate。
4. main_wails 注入。
5. RuntimePanel 更新区块。

## 阶段 P2 — Remote E2E
1. Node mock OpenSandbox。
2. 启动 sandbox remote 进程。
3. create/exec/delete 断言。
4. 容器内运行。

## 阶段 P3 — 导出审计
1. 找到 exportPlan/zip 成功路径。
2. recordAuditEvent provider/isolation/format。
3. 单测/接口验证。

## 阶段 P4 — Windows 回归脚本
1. runner-probe.ps1。
2. README 说明。

## 阶段 P5 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像 + compose + E2E。
4. 清理 + FIX-REPORT-ROUND17。
