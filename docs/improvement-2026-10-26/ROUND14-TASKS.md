# 任务清单：AppContainer 双 profile 网络 + vm/remote Provider + 崩溃转储 + 托盘（2026-10-31 第十四轮）

## P0 规格与基线
- [x] 复核第十三轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 native 双 profile
- [x] profile 名称/capability
- [x] runSpec.NetworkOn / rawLaunch.Profile
- [x] SetNetwork / Capabilities
- [x] 单测

## P2 vm Provider
- [x] wsl 解析
- [x] Provider 实现
- [x] 单测

## P3 remote Provider
- [x] 适配 OpenSandbox
- [x] 单测

## P4 main 选择
- [x] vm/remote 分支
- [x] health/capabilities
- [x] 编译

## P5 崩溃转储
- [x] CrashDir/collect
- [x] zip 条目
- [x] 单测

## P6 托盘
- [x] tray_windows.go
- [x] tray_other.go
- [x] Windows 交叉编译

## P7 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND14
