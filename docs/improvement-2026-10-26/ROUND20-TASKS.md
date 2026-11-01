# 任务清单：运行时设置持久化 + 运行时状态 + 诊断/日志入口（2026-10-31 第二十轮）

## P0 规格与基线
- [x] 复核第十九轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 设置持久化
- [x] bridge GetRuntimeSettings/UpdateProvider
- [x] 单测
- [x] RuntimePanel 读取/保存

## P2 运行时状态
- [x] native RuntimeStatus
- [x] sandbox endpoint
- [x] shell FetchRuntimeStatus + /desktop/runtime
- [x] RuntimePanel 展示
- [x] 单测

## P3 诊断/日志
- [x] RuntimePanel 按钮
- [x] 状态/错误

## P4 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND20
