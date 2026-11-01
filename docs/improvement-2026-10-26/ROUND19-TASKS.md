# 任务清单：崩溃转储注册 + 打包流水线 + 全量 E2E 回归（2026-10-31 第十九轮）

## P0 规格与基线
- [x] 复核第十八轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 崩溃转储
- [x] Targets/RegistryPath
- [x] Windows Enable/Disable
- [x] main_wails 接线
- [x] 单测 + Windows 交叉编译

## P2 打包流水线
- [x] package.ps1
- [x] build/README.md

## P3 全量 E2E
- [x] 运行既有 E2E 套件
- [x] 记录结果
- [x] 修复真实回归

## P4 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND19
