# 规格：NSIS 版本参数化 + 安装包编译烟测 + 回归（2026-10-31 第二十四轮）

> 依据：docs/design/2026-10-30-windows-desktop-client.md（4.1 安装布局、11 打包）。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 真机安装仍不可执行。
> 基线：feature/desk（含第二十三轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND24.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | NSIS 版本参数化 | installer.nsi 支持 /DVERSION（默认 0.9.0），VIProductVersion/FileVersion/DisplayVersion 使用该值 |
| G2 | 安装包编译烟测 | 新增 installer-smoke.sh：暂存 dummy exe/webview，调用 makensis，校验 Setup.exe 生成；容器内通过 |
| G3 | 回归 | 三模块 build/vet/test；前端 test/build/lint；compose 四套 E2E + repro-bundle |

硬性完成条件：
1. docker-sandbox、desktop、backend 容器内 build/vet/test 通过；
2. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过（desktop 含 Wails tag）；
3. 前端 npm test/build/lint 通过；
4. compose 栈 E2E 全部通过；
5. 安装包 makensis 烟测通过；清理临时产物/镜像。

## 2. 现状复核（2026-10-31 第二十四轮）

| 文档描述 | 实际 |
|---|---|
| 11 打包 | package.ps1 传 /DVERSION，但 installer.nsi 完全忽略该参数，版本固定 0.9.0 |
| 安装包验证 | 没有 makensis 编译烟测 |

## 3. 范围

### 3.1 本轮实现
- installer.nsi 增加 VERSION 默认值与 !ifndef，版本信息使用参数。
- package.ps1 保持传参；README 说明三段式版本。
- installer-smoke.sh：dummy 文件 + makensis + 输出校验。
- 容器内 Debian + nsis 执行烟测。

### 3.2 不在本轮
- Windows 真机安装/卸载验收。
- 代码签名。

## 4. 关键设计

### 4.1 NSIS 版本
!ifndef VERSION → 默认 "0.9.0"；VIProductVersion "${VERSION}.0"（要求主版本为三段）；DisplayVersion/FileVersion 使用 ${VERSION}。

### 4.2 烟测
临时目录创建 ResearchAssistant.exe/backend.exe/sandbox.exe/uv.exe/webview/index.html，复制 installer.nsi，执行 makensis /DVERSION=0.0.1，断言 ResearchAssistant-Setup.exe 存在且非空。

## 5. 测试策略（容器内）
1. Debian 容器安装 nsis，执行 installer-smoke.sh。
2. 三模块全量 + Windows 交叉编译。
3. 前端 test/build/lint。
4. E2E：四套 + repro-bundle。

## 6. 完成定义
- [x] G1 版本参数化
- [x] G2 编译烟测
- [x] G3 回归
- [x] FIX-REPORT-ROUND24

## 7. 风险
| 风险 | 对策 |
|---|---|
| makensis 对 Linux 路径处理差异 | 烟测脚本在临时目录执行，路径相对 |
| apt 安装 nsis 失败 | 记录环境限制，不阻塞其他测试 |
