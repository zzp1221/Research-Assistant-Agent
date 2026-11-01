# 实施计划：AppContainer 双 profile 网络 + vm/remote Provider + 崩溃转储 + 托盘（2026-10-31 第十四轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核第十三轮代码与文档差异，写 spec/plan/tasks。
2. 容器内基线：docker-sandbox / desktop build。

## 阶段 P1 — native 双 profile
1. `appcontainer_windows.go`：profile 名称与 capability 列表、online/offline 创建。
2. `runSpec.NetworkOn`、`rawLaunch.Profile`、`windowsRunner` 选择 profile。
3. `Provider.SetNetwork` 与 `Capabilities` 更新。
4. 纯逻辑单测（profile 选择、capability SID 常量）。

## 阶段 P2 — vm Provider
1. `engine/vm/detect.go`：`wsl -l -v` 解析。
2. `engine/vm/provider.go`：Capabilities/Create/Exec/Delete/Usage/Reap。
3. 单测：解析样例、能力矩阵、不可用错误。

## 阶段 P3 — remote Provider
1. 读 `OpenSandboxEngine` 接口。
2. `engine/remote/provider.go` 适配。
3. 单测（使用 httptest 模拟 OpenSandbox）。

## 阶段 P4 — main 选择
1. `newSandboxService` 支持 vm/remote。
2. health/capabilities 返回真实 provider。
3. 编译 + 单测。

## 阶段 P5 — 崩溃转储
1. `diagnostics.Options.CrashDir`、`collectCrashes`。
2. zip 写入 `crashes.json` + dump 内容。
3. 单测。

## 阶段 P6 — 托盘
1. `tray_windows.go` Win32 实现。
2. `tray_other.go` 内存实现。
3. Windows 交叉编译。

## 阶段 P7 — 回归与清理
1. 三模块容器 build/vet/test。
2. Windows 交叉编译。
3. 前端 test/build/lint。
4. 重建镜像 + compose + 四套 E2E。
5. 清理 + FIX-REPORT-ROUND14。
