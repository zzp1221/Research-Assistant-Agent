# 规格：桌面外壳产品化 + 发布工程基础 + Windows Runner（2026-10-31 第十三轮）

> 依据：`docs/design/2026-10-30-windows-desktop-client.md`（状态表：P0/P1 部分完成，P2/P3/P4 未开始）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行（Go 使用 `golang:1.26.1-bookworm`，前端 `node:22-bookworm-slim`，E2E 使用运行中的 `research-assistant` compose 栈）。Windows 专有代码以 `GOOS=windows` 交叉编译 + 纯逻辑单测验证。
> 基线：`feature/desk`（工作区含第十二轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND13.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 桌面外壳真正可启动：加载设置、分配端口、注入环境、启动 backend/sandbox、启动 loopback 代理、暴露状态 | `desktop/shell` 单测覆盖启动/失败/退出/重启；错误页 HTML 包含日志路径与诊断按钮 |
| G2 | 首次启动向导 | `desktop/onboarding` 状态机 + 连接测试单测；前端 `Onboarding` 组件测试 |
| G3 | 诊断包 | `desktop/diagnostics` 生成 zip（日志尾部、脱敏设置、能力矩阵、系统信息、失败计划），单测验证内容与脱敏 |
| G4 | 自动更新（签名校验） | `desktop/updater` 版本比较、sha256、ed25519 验签、下载暂存单测（httptest + 测试密钥） |
| G5 | P2 Windows Runner 核心 | AppContainer/lowil/Job 限额/兼容性探针代码落地；`GOOS=windows` 编译通过；选择策略与探针聚合有纯逻辑单测 |
| G6 | 前端通知与徽章增强 | 计划完成通知桥接、lowil 黄色警告条、组件测试 |
| G7 | 回归 | backend/docker-sandbox/desktop 三模块容器内 build/vet/test；前端 test/build/lint；compose 四套 E2E 通过；无新增失败 |

硬性完成条件：
1. `desktop` 模块 `go test ./...` 通过，`GOOS=windows GOARCH=amd64 go build ./...` 通过；
2. `GOOS=windows GOARCH=amd64 go build -tags "desktop wails"` 通过（Wails 依赖可下载时）；
3. backend、docker-sandbox 容器内 `go build/vet/test` 通过；
4. 前端 `npm test`、`npm run build`、`npm run lint` 通过；
5. compose 栈重建后 `http-smoke.mjs`、`desktop-smoke.mjs`、`sandbox-command.mjs`、`sandbox-network.mjs` 通过；
6. 不引入新的编译、vet、测试失败；清理临时产物/镜像。

## 2. 现状复核（实际代码，2026-10-31 第十三轮）

| 文档描述 | 实际 |
|---|---|
| P0 桌面外壳「Wails 窗口/托盘/WebView2 集成未验证」 | 属实且更严重：`desktop/main_wails.go` 只调用 `wails.Run` 打开空窗口，未启动 supervisor/proxy/settings；无 shell 编排包 |
| 5.4 首次启动向导未实现 | 属实：desktop 与 frontend 均无 onboarding |
| 10 系统通知/原生对话框/独立错误页未完成 | 属实：无任何桥接或错误页 |
| 11 打包/签名/更新未开始 | 部分：仅 `desktop/build/installer.nsi` 骨架；无 updater 包 |
| 13 诊断包/崩溃转储/本地指标未完成 | 属实：无 diagnostics 包 |
| 6.3.3 Windows Runner appcontainer/lowil | 属实：`runner_windows.go` 只有普通进程 + KILL_ON_JOB_CLOSE，无 AppContainer/lowil/资源限额/探针 |
| 6.2 规划器前置能力门禁 | 部分：health/capabilities 有矩阵；planner 未接 |
| 9 runtime_hash 实际填充与导出分支 | 部分：EnvironmentManifest 字段已加，native 未填 runtime_hash |
| 12 loopback/token | 已有：proxy 有 Host/Origin 守卫；后端 token 中间件存在 |
| 14.3 desktop-smoke | 已存在且通过 |

## 3. 范围

### 3.1 本轮实现
- **desktop/shell**：`Shell` 编排（settings→ports→tokens→supervisor→proxy→状态/错误），崩溃自动重启（指数退避，最多 3 次），错误页（日志路径、诊断包、重试），`/desktop/state` 内部 API。
- **desktop/diagnostics**：zip 生成（manifest、日志尾部 5MB、脱敏设置、能力矩阵、系统信息、最近失败计划）。
- **desktop/updater**：`latest.json` 检查、semver 比较、sha256 + ed25519 验签、受限下载、原子暂存。
- **desktop/onboarding**：首启判定、凭据校验、最小 completion 连接测试、完成落盘。
- **desktop/tray**：托盘抽象 + 状态/通知接口（平台实现可后续接入；先定义契约与内存实现）。
- **desktop/main_wails.go**：真正接线（AssetServer 指向 proxy handler、OnStartup 启 shell、OnShutdown 停 shell、错误页）。
- **docker-sandbox native runner（P2 核心）**：`isolation.go`（auto→probe→appcontainer/lowil、GPU 强制 lowil）、`probe.go`（附录 D 7 项聚合）、`runner_windows.go` 接入 Job 限额（内存/进程数/CPU rate/UI 限制）、`appcontainer_windows.go`、`lowil_windows.go`、`acl_windows.go`。
- **前端**：`features/onboarding/Onboarding.tsx`、`contracts/desktop.ts`、`services/desktopBridge.ts`、计划完成通知 hook、`NetworkIsolationBanner`（lowil 黄色提示）、RuntimePanel 显示探针结果。
- **NSIS**：per-user 静默安装、卸载保留数据、协议/文件关联骨架完善。

### 3.2 不在本轮
- 真实 Windows 真机安装、AppContainer 实际降权验证（Linux 容器无法执行，仅交叉编译 + 纯逻辑单测）。
- 代码签名证书、真实更新服务器。
- vm/remote Provider 实现（P4，保留接口）。
- macOS/Linux 客户端。

## 4. 关键设计

### 4.1 Shell 状态机
```
idle → starting → running
                 ↘ failed(error, logPath, diagnosticsAvailable)
running → restarting → running | failed
running → stopping → idle
```
- 启动顺序：settings 加载 → 端口探测（3 次重试）→ token 生成 → supervisor.Start（backend→sandbox 健康）→ proxy Listen。
- 崩溃重启：监听 supervisor 子进程退出（回调），sandbox 先重启、backend 后重启；最多 3 次，间隔 1s/2s/4s；超过则 failed 并保留日志路径。
- 退出：proxy 关闭 → supervisor.Stop（sandbox→backend→锁）。

### 4.2 诊断包
```
diagnostics-<timestamp>.zip
├─ manifest.json          app version, os/arch, generated_at, provider, isolation
├─ settings.redacted.json api_key 掩码、token 清空、路径保留
├─ health.json            backend /api/health + sandbox /api/v1/health
├─ system.json            CPU/内存/磁盘/Go 版本
├─ failed-plans.json      最近 3 个失败计划元数据（id/title/status/error）
└─ logs/{shell,backend,sandbox}.log  各尾部 ≤5MB
```
- 脱敏规则：`api_key`/`token`/`authorization`/`secret` 字段值替换为 `***`；URL 中的 userinfo 去除。

### 4.3 更新
- `Manifest{Version,URL,SHA256,Signature,ReleaseNotesURL}`。
- 验签：ed25519，签名对象 = `sha256(fileBytes)` 的十六进制字符串；公钥由构建注入 `-ldflags -X`。
- 下载限制 300MB、超时 10 分钟、先写 `.part` 再原子 rename；校验失败删除并记录。
- 版本比较：语义化 `major.minor.patch`，忽略预发布（stable 渠道）。

### 4.4 Windows Runner
- `SelectIsolation(requested, probe, gpuRequired)`：requested 明确则用；auto + GPU→lowil；auto + probe 全过→appcontainer；否则 lowil。
- 探针结果 `ProbeReport{AppContainer bool, Cases []ProbeCase, Reason string}`；任一安全项失败→不启用 AppContainer；兼容项失败→降级并记录。
- Job 限额：`ProcessMemoryLimit`、`ActiveProcessLimit=64`、`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`、CPU rate（`JOB_OBJECT_CPU_RATE_CONTROL_ENABLE|HARD_CAP`）。
- AppContainer：`CreateAppContainerProfile` + `SECURITY_CAPABILITIES` + `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`；工作区 ACL 只授当前用户 + AppContainer SID。
- lowil：`OpenProcessToken` + `DuplicateTokenEx` + `SetTokenInformation(TokenIntegrityLevel, Low)` + `CreateProcessAsUser`。

## 5. 测试策略（容器内）
1. desktop：`go test ./...`（新增 shell/diagnostics/updater/onboarding/tray 包全部覆盖），`GOOS=windows GOARCH=amd64 go build ./...`。
2. docker-sandbox：`go test ./...`（isolation/probe 纯逻辑），`GOOS=windows GOARCH=amd64 go build ./...`。
3. backend：`go build/vet/test`。
4. 前端：`npm test`、`npm run build`、`npm run lint`。
5. E2E：compose 栈四套脚本。

## 6. 完成定义
- [x] G1 shell 编排 + 错误页
- [x] G2 onboarding
- [x] G3 diagnostics
- [x] G4 updater
- [x] G5 Windows runner P2 核心
- [x] G6 前端通知/警告条
- [x] G7 全部回归通过
- [x] FIX-REPORT-ROUND13

## 7. 风险
| 风险 | 对策 |
|---|---|
| Wails 依赖下载/交叉编译失败 | 先用 `desktop` tag stub 保证 `go build ./...`；wails tag 单独验证，失败记录环境限制 |
| Windows API 结构体对齐 | 复用 x/sys/windows 定义；`GOOS=windows go vet` + 交叉编译 |
| 诊断包泄露密钥 | 白名单字段 + 单元测试断言 `sk-`/token 不出现 |
| 更新验签实现错误 | 使用标准库 ed25519，测试覆盖篡改/错签名/错哈希 |
| 前端 onboarding 破坏现有路由 | 默认关闭（仅桌面桥接声明需要时显示），组件测试 + 全量测试 |
