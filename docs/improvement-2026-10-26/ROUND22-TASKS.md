# 任务清单：运行时安装进度 + UI 轮询 + 回归（2026-10-31 第二十二轮）

## P0 规格与基线
- [x] 复核第二十一轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 安装阶段
- [x] EnsureRuntimeWithProgress
- [x] Provider 安装状态
- [x] RuntimeStatus 输出
- [x] 伪 uv 单测

## P2 UI 轮询
- [x] RuntimePanel 轮询
- [x] 展示 stage/error

## P3 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND22
