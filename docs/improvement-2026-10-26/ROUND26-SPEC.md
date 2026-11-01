# 规格：全量回归与收口（2026-10-31 第二十六轮）

> 依据：用户完成条件“经过测试后，未发现新增问题且旧问题已被修复”。
> 原则：以实际代码与运行中的容器为准；文档与实现不一致处记录在第 2 节。
> 约束：功能测试只在容器内执行；Windows 真机/签名/真实 WSL2/远程服务不可执行，记录为环境边界。
> 基线：feature/desk（含第十二至二十五轮未提交改动）。
> 状态：已完成（2026-10-31）；证据见 `docs/improvement-2026-10-26/FIX-REPORT-ROUND26.md`。

## 1. 本轮目标与验收

| 编号 | 目标 | 验收信号 |
|---|---|---|
| G1 | 全量 E2E 回归 | 13 个既有非环境依赖 E2E 套件通过（http-smoke、routing-and-prompts、literature、literature-plan、plan-library、cost-budget、budget-recovery、plan-revisions、review-hitl、sandbox-command、sandbox-network、desktop-smoke、repro-bundle） |
| G2 | 四模块回归 | backend/docker-sandbox/desktop build+vet/test；前端 test/build/lint |
| G3 | 收口 | FIX-REPORT-ROUND26：逐项对照文档状态表，列出已完成/环境边界/残余风险；更新 CHANGELOG/verification/design 链接 |
| G4 | 目标评估 | 若 G1/G2 全绿且残余仅为 Windows 真机/签名/真实服务，则标记目标完成；否则保持 active 并记录阻塞 |

硬性完成条件：
1. G1 的 13 套 E2E 全部 exit 0（repro-bundle 容器内安装 unzip）。
2. backend/docker-sandbox/desktop 全量 test 通过；前端 36 文件/144 tests + build + lint。
3. 三模块 GOOS=windows GOARCH=amd64 go build ./... 通过。
4. 文档收口完成，无临时产物。

## 2. 现状复核（2026-10-31 第二十六轮）

| 文档状态 | 实际 |
|---|---|
| 3–15 节代码项 | 除 Windows 真机验证/签名/真实 WSL2/远程服务外，代码与容器测试已完成 |
| 环境限制 E2E | model-profiles/verification 需 private endpoints+vision、document-intelligence 需 20 页 OCR、tool-ecosystem 需 docker CLI/LLM |
| 残余 | 真实 WER dump、NSIS 安装/卸载、签名、更新服务器、AppContainer/systray/DPAPI/WebView2、WSL2/OpenSandbox 联调 |

## 3. 范围

### 3.1 本轮执行
- 13 套 E2E（repro-bundle 前 apt 安装 unzip）。
- backend/docker-sandbox/desktop 全量 + Windows 交叉编译。
- 前端 test/build/lint。
- 文档收口与目标评估。

### 3.2 不在本轮
- 新增功能。
- Windows 真机/签名/真实服务。

## 4. 关键设计
- E2E 使用运行中的 compose 栈与 test/e2e 脚本；每套单独 exit code。
- 失败即记录输出并判断是回归还是环境限制；真实回归必须修复后才能标记完成。

## 5. 测试策略（容器内）
1. E2E 批处理（13 套）。
2. 四模块全量。
3. 文档核对。

## 6. 完成定义
- [x] G1 13 套 E2E
- [x] G2 四模块回归
- [x] G3 收口报告
- [x] G4 目标评估
- [x] FIX-REPORT-ROUND26

## 7. 风险
| 风险 | 对策 |
|---|---|
| 某 E2E 因环境失败 | 记录具体错误；仅真实回归才修复 |
| 全量耗时 | 后台任务 + 逐套 exit code |
