# 任务清单：VM Provider 可测试化 + 更新安装流程 + NSIS 文件关联（2026-10-31 第十八轮）

## P0 规格与基线
- [x] 复核第十七轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 VM Provider 可测试化
- [x] Config WSLPath/AllowUnsupportedPlatform
- [x] Provider 使用配置
- [x] 伪 wsl 集成测试
- [x] Windows 交叉编译

## P2 更新安装
- [x] Manager.Install + Launcher
- [x] 单测
- [x] bridge InstallUpdate
- [x] RuntimePanel 安装按钮

## P3 NSIS
- [x] 可选 PDF 关联
- [x] 文档

## P4 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND18
