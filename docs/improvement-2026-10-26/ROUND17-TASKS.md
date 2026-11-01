# 任务清单：自动更新闭环 + Remote Provider 集成 + 导出审计 + Windows 回归脚本（2026-10-31 第十七轮）

## P0 规格与基线
- [x] 复核第十六轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 Update Manager
- [x] updater.Manager
- [x] 单测
- [x] bridge CheckForUpdate/DownloadUpdate
- [x] main_wails 注入
- [x] RuntimePanel 更新区块

## P2 Remote E2E
- [x] mock OpenSandbox
- [x] sandbox remote 进程
- [x] create/exec/delete 断言
- [x] 容器内运行

## P3 导出审计
- [x] 找到导出成功路径
- [x] recordAuditEvent provider/isolation
- [x] 验证

## P4 Windows 回归脚本
- [x] runner-probe.ps1
- [x] README

## P5 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND17
