# 任务清单：导出/复现包 Provider 分支 + 规划器细粒度门禁 + 原生对话框 + 签名脚本（2026-10-31 第十六轮）

## P0 规格与基线
- [x] 复核第十五轮代码与文档差异
- [x] 写 spec.md / plan.md / tasks.md
- [x] 容器内基线 build

## P1 导出分支
- [x] 复核 export manifest
- [x] native/docker 分支输出
- [x] 单测 + repro-bundle E2E

## P2 规划器门禁
- [x] GPU 需求解析
- [x] Python 版本门禁
- [x] 单测

## P3 原生对话框
- [x] bridge ChooseDirectory/ShowInFolder
- [x] 前端 helper/UI
- [x] 单测

## P4 签名脚手架
- [x] sign.ps1
- [x] update-manifest.example.json
- [x] RA_UPDATE_PUBLIC_KEY
- [x] 文档

## P5 回归与清理
- [x] docker-sandbox 容器 test
- [x] desktop 容器 test
- [x] backend 容器 test
- [x] Windows 交叉编译
- [x] 前端 test/build/lint
- [x] 重建镜像 + compose + E2E
- [x] 清理 + FIX-REPORT-ROUND16
