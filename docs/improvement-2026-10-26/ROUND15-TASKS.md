# 任务清单：VM Provider 执行 + 规划器能力门禁 + 本地指标/崩溃恢复提示（2026-10-31 第十五轮）

## P0 规格与基线
- [x] 复核第十四轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 vm 执行
- [x] ToWSLPath/FromWSLPath
- [x] BuildWSLArgs
- [x] Provider Create/Exec/Delete/Usage/Reap
- [x] 单测
- [x] Windows 交叉编译

## P2 规划器能力门禁
- [x] 找到 plan creation 路径
- [x] 写入 Plan.Meta.SandboxCapabilities
- [x] native image_prepare→runtime_probe
- [x] 后端单测

## P3 本地指标
- [x] metrics 解析
- [x] /desktop/metrics
- [x] 单测

## P4 崩溃恢复提示
- [x] Status LastRestartAt
- [x] DesktopStatusBanner
- [x] RuntimePanel 指标
- [x] 组件测试

## P5 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND15
