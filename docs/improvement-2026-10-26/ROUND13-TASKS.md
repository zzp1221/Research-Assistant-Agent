# 任务清单：桌面外壳产品化 + 发布工程基础 + Windows Runner（2026-10-31 第十三轮）

## P0 规格与基线
- [x] 复核文档与代码差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build/test

## P1 desktop/shell
- [x] Shell 编排（settings/ports/tokens/supervisor/proxy）
- [x] 错误页（日志/诊断/重试）
- [x] 崩溃重启（退避 3 次）
- [x] 单实例激活（supervisor 锁）
- [x] shell 单测

## P2 diagnostics
- [x] Tail/Redact/SystemInfo
- [x] Build zip
- [x] 单测

## P3 updater
- [x] Manifest + semver
- [x] Check
- [x] DownloadAndVerify（sha256+ed25519）
- [x] 单测

## P4 onboarding + tray
- [x] onboarding 状态机/连接测试
- [x] tray 接口与内存实现
- [x] 单测

## P5 Wails 入口 + NSIS
- [x] main_wails.go 接线（Bind + bridge）
- [x] main.go stub
- [x] NSIS 静默安装/卸载保留数据/协议注册

## P6 Windows Runner
- [x] isolation.go + 单测
- [x] probe.go + 单测
- [x] runner_windows.go 限额接入
- [x] appcontainer_windows.go
- [x] lowil_windows.go
- [x] workspace ACL / limits / raw process
- [x] Windows 交叉编译

## P7 前端
- [x] desktop contracts + bridge
- [x] Onboarding.tsx + 测试
- [x] 计划完成通知
- [x] NetworkIsolationBanner
- [x] RuntimePanel 探针结果
- [x] npm test/build/lint

## P8 回归与清理
- [x] desktop 容器 test + windows build
- [x] backend 容器 build/vet/test
- [x] docker-sandbox 容器 build/vet/test
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose
- [x] 四套 E2E
- [x] 清理 + FIX-REPORT-ROUND13
