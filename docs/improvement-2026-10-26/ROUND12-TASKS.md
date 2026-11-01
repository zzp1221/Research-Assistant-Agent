# 任务清单：Windows 桌面客户端与去 Docker 化（2026-10-31 第十二轮）

## P0 规格与基线
- [x] 复核设计文档与实际代码差异
- [x] 归档第十一轮 spec/plan/tasks
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内 backend 基线 go build

## P1 跨平台基础
- [x] procgroup（Unix + Windows Job Object）
- [x] repo_prepare 两处替换
- [x] sysinfo（Linux/Darwin/Windows）
- [x] reproduction_mode 改用 sysinfo
- [x] capabilities_disk_windows 真实实现
- [x] apppaths（LOG_DIR/APP_DATA_DIR/RA_CONFIG_PATH）
- [x] appconfig 使用 RA_CONFIG_PATH
- [x] logging 轮转（backend + sandbox）
- [x] proxyenv（RA_KEEP_SYSTEM_PROXY + Windows 系统代理）
- [x] workspace 大小写不敏感 + 长路径
- [x] uploads/workspace 回退 APP_DATA_DIR
- [x] cmd/app SANDBOX_URL 放宽；cmd/api APP_ADDR
- [x] P1 单测

## P2 命令契约
- [x] backend Command 类型 + V2 方法
- [x] docker-sandbox CommandRequest 扩展
- [x] docker 引擎 cwd/env/shell/python 映射
- [x] autoresearch_eval 改 Argv
- [x] benchmark_harness x2 改 Argv
- [x] coder_exec 改 Argv
- [x] coder_runtime 改 Argv
- [x] paper_debug_harness 改 Argv
- [x] coder_image 宿主侧准备 + Command
- [x] legacyShell 适配与测试
- [x] P2 单测

## P3 Provider 与 native
- [x] engine Provider 接口与类型
- [x] docker Provider 适配器
- [x] native runtime（uv/venv/envHash）
- [x] native translate
- [x] native runner（Job Object + lowil；AppContainer 占位）
- [x] native engine 平台分支
- [x] main SANDBOX_PROVIDER 选择 + health 能力
- [x] P3 单测

## P4 桌面外壳
- [x] desktop go.mod / paths
- [x] settings（JSON + DPAPI 接口）
- [x] supervisor（单实例/端口/健康/重启/退出）
- [x] proxy（SSE/Host/Origin/SPA）
- [x] main.go Wails 入口 + stub
- [x] NSIS 脚本骨架
- [x] P4 单测

## P5 前端
- [x] contracts 类型
- [x] EnvironmentBadge
- [x] RuntimePanel + SettingsPanel tab
- [x] 组件测试

## P6 回归与清理
- [x] backend 容器 build/vet/test
- [x] docker-sandbox 容器 build/vet/test
- [x] desktop 容器 test
- [x] 三模块 GOOS=windows build
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose 起栈
- [x] http-smoke + desktop-smoke + sandbox-command + sandbox-network
- [x] 清理临时产物/镜像
- [x] FIX-REPORT-ROUND12