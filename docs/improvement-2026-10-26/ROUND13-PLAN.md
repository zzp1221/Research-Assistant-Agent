# 实施计划：桌面外壳产品化 + 发布工程基础 + Windows Runner（2026-10-31 第十三轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格与基线
1. 复核文档状态表与实际代码，写 spec/plan/tasks（差异见 spec 第 2 节）。
2. 归档第十二轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND12-*.md`（已存在，核对）。
3. 容器内基线：desktop/backend/docker-sandbox build 与 test。

## 阶段 P1 — desktop/shell 编排
1. `shell.Config/State/Shell`：端口、token、settings、env、supervisor、proxy 生命周期。
2. 错误页 HTML（日志路径、诊断、重试）。
3. 崩溃重启（指数退避、最多 3 次）与事件回调。
4. 单实例激活回调。
5. 单测：启动成功/失败/重启/停止/状态。

## 阶段 P2 — diagnostics
1. `Tail(path, maxBytes)`、`RedactSettings`、`CollectSystemInfo`。
2. `Build(ctx, Options) (path, error)` 生成 zip。
3. 单测：zip 条目齐全、日志截断、密钥脱敏、失败计划降级为空。

## 阶段 P3 — updater
1. `Manifest` 解析与 semver 比较。
2. `Check`（httptest）。
3. `DownloadAndVerify`：大小限制、sha256、ed25519、原子 rename。
4. 单测：正常/版本相同/哈希不符/签名不符/超限。

## 阶段 P4 — onboarding + tray
1. `onboarding`：FirstRun、Validate、TestConnection、Complete。
2. `tray`：接口 + 内存实现 + 状态模型。
3. 单测。

## 阶段 P5 — Wails 入口与 NSIS
1. `main_wails.go`：AssetServer→proxy、OnStartup→shell.Start、OnShutdown→shell.Stop、错误页。
2. `main.go` stub 输出诊断信息。
3. NSIS：静默安装、卸载保留数据、协议/文件关联。

## 阶段 P6 — Windows Runner（P2 核心）
1. `isolation.go` + `probe.go` 纯逻辑与单测。
2. `runner_windows.go` 接入限额与隔离选择。
3. `appcontainer_windows.go`、`lowil_windows.go`、`acl_windows.go`。
4. `GOOS=windows` 交叉编译与 vet。

## 阶段 P7 — 前端
1. `contracts/desktop.ts`、`services/desktopBridge.ts`。
2. `features/onboarding/Onboarding.tsx` + 测试。
3. 计划完成通知 hook。
4. `NetworkIsolationBanner` + RuntimePanel 探针结果。
5. `npm test/build/lint`。

## 阶段 P8 — 回归与清理
1. desktop/backend/docker-sandbox 容器 build/vet/test。
2. 三模块 Windows 交叉编译。
3. 前端 test/build/lint。
4. 重建 backend/sandbox/frontend 镜像 + compose 起栈。
5. 四套 E2E。
6. 清理临时容器/镜像/缓存；写 FIX-REPORT-ROUND13。
