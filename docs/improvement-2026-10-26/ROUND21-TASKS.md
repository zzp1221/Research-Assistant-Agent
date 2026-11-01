# 任务清单：运行时安装触发 + Docker 升级提示 + 阶段状态修正（2026-10-31 第二十一轮）

## P0 规格与基线
- [x] 复核第二十轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 运行时安装
- [x] native InstallRuntime
- [x] 伪 uv 单测
- [x] sandbox endpoint
- [x] shell InstallRuntime
- [x] bridge installRuntime
- [x] RuntimePanel 按钮/状态

## P2 Docker 提示
- [x] RuntimePanel 提示条
- [x] 一键切换

## P3 文档
- [x] 15 P2/P3/P4 标题修正

## P4 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND21
