# 任务清单：NSIS 版本参数化 + 安装包编译烟测 + 回归（2026-10-31 第二十四轮）

## P0 规格与基线
- [x] 复核第二十三轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 NSIS 版本
- [x] !ifndef VERSION
- [x] VIProductVersion/FileVersion/DisplayVersion
- [x] README

## P2 编译烟测
- [x] installer-smoke.sh
- [x] 容器内 makensis

## P3 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] compose + E2E
- [x] 清理 + FIX-REPORT-ROUND24
