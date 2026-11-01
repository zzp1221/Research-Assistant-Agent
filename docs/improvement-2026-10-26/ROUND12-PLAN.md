# 实施计划：Windows 桌面客户端与去 Docker 化（2026-10-31 第十二轮）

- 依据：`docs/design/2026-10-30-windows-desktop-client.md` + `spec.md`
- 基线：`master @ a9419b2`，分支 `feature/desk`
- 测试环境：容器内（golang:1.26.1-bookworm / node:22-bookworm-slim / 运行中的 research-assistant compose 栈）
- 完成定义：`spec.md` 第 6 节
- 状态：已完成（2026-10-31）

## 阶段 P0 — 规格、审计与基线
1. 复核设计文档与实际代码差异，写 spec/plan/tasks（差异见 spec 第 2 节）。
2. 归档第十一轮 spec/plan/tasks 到 `docs/improvement-2026-10-26/ROUND11-*.md`。
3. 容器内基线：backend `go build ./...` 通过。

## 阶段 P1 — 跨平台基础（文档 8.1–8.4）
1. `internal/procgroup`：Unix 进程组 + Windows Job Object；替换 repo_prepare 两处。
2. `internal/sysinfo`：Linux/Darwin/Windows 内存与磁盘；reproduction_mode 改用。
3. `capabilities_disk_windows.go`：GetDiskFreeSpaceExW。
4. `internal/apppaths`：LOG_DIR/APP_DATA_DIR/RA_CONFIG_PATH；appconfig 使用。
5. `internal/logging`：20MB×5 轮转（backend + sandbox）。
6. `internal/proxyenv`：RA_KEEP_SYSTEM_PROXY + Windows 系统代理。
7. workspace 大小写不敏感 + `internal/winpath`；uploads/workspace 数据目录回退。
8. cmd/app SANDBOX_URL 放宽；cmd/api APP_ADDR。
9. P1 单测。

## 阶段 P2 — 命令契约（文档第 7 节）
1. backend Command + ExecCommandV2/StreamV2 + legacyShell。
2. docker-sandbox CommandRequest 扩展 argv/cwd/env/shell/timeout/stdin/max_output_kb。
3. docker 引擎 cwd/env/shell/python 映射、输出截断。
4. 6 处 bash -lc 迁移：autoresearch_eval、benchmark_harness x2、coder_exec、coder_runtime、paper_debug_harness。
5. coder_image 宿主侧 `__init__.py` + Command。
6. P2 单测。

## 阶段 P3 — Provider 与 native（文档第 6 节）
1. engine Provider 接口 + 类型。
2. docker Provider 适配器。
3. native runtime（uv/venv/envHash）。
4. native translate。
5. native runner（Job Object + lowil；AppContainer 占位）+ 非 Windows stub。
6. main SANDBOX_PROVIDER 选择 + health capabilities。
7. P3 单测。

## 阶段 P4 — 桌面外壳（文档第 3/4/5 节）
1. desktop go.mod / paths。
2. settings（JSON + DPAPI + 内存 store）。
3. supervisor（单实例/端口/健康/重启/退出 + Job Object）。
4. proxy（SSE/Host/Origin/SPA）。
5. main stub + Wails 入口 + NSIS 骨架。
6. P4 单测。

## 阶段 P5 — 前端
1. contracts 类型 + getSandboxCapabilities。
2. EnvironmentBadge。
3. RuntimePanel + SettingsPanel tab。
4. 组件测试。

## 阶段 P6 — 容器回归、E2E、清理
1. backend/docker-sandbox/desktop 容器 build/vet/test。
2. 三模块 Windows 交叉编译。
3. 前端 test/build/lint。
4. 重建 backend/sandbox/frontend 镜像 + compose 起栈。
5. http-smoke、desktop-smoke、sandbox-command、sandbox-network。
6. 清理临时容器/镜像/缓存/产物；写 FIX-REPORT-ROUND12。

## 风险与对策
- Windows API 编译差异：x/sys/windows + build tag stub，反复 GOOS=windows 验证。
- 命令契约回归：旧方法保留，docker E2E 覆盖。
- native 无法在 Linux 执行：纯逻辑单测 + 交叉编译，真机验收记录环境限制。
- Wails 依赖：desktop 独立 module，单测不依赖 Wails。
