# 实施计划：VM Provider 执行 + 规划器能力门禁 + 本地指标/崩溃恢复提示（2026-10-31 第十五轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十四轮代码与文档差异。
2. 写 spec/plan/tasks。
3. 容器内基线 build。

## 阶段 P1 — vm 执行
1. `translate.go`：ToWSLPath/FromWSLPath/BuildWSLArgs/DefaultDistro。
2. `provider.go`：Create/Exec/Delete/Usage/Reap。
3. 单测 + Windows 交叉编译。

## 阶段 P2 — 规划器能力门禁
1. 找到 plan creation 路径与 Plan.Metadata 写入点。
2. 能力矩阵 → MissingRequirement。
3. 后端单测。

## 阶段 P3 — 本地指标
1. shell metrics 客户端与 Prometheus 解析。
2. `/desktop/metrics`。
3. 单测。

## 阶段 P4 — 崩溃恢复提示
1. shell Status LastRestartAt。
2. DesktopStatusBanner + 测试。
3. RuntimePanel 指标区块。

## 阶段 P5 — 回归与清理
1. 三模块 build/vet/test + Windows 交叉编译。
2. 前端 test/build/lint。
3. 重建镜像 + compose + 四套 E2E。
4. 清理 + FIX-REPORT-ROUND15。
