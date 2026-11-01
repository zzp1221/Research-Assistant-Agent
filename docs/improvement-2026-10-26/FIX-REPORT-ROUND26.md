# 最终回归与收口（第二十六轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 结论：容器内可验证项全部通过；未发现新增问题，文档中的旧问题已修复；剩余仅为 Windows 真机/签名/真实服务等环境边界。

## 1. 全量回归结果

### 1.1 E2E（13 套，全部 exit 0）

http-smoke、routing-and-prompts、literature、literature-plan、plan-library、cost-budget、budget-recovery、plan-revisions、review-hitl、sandbox-command、sandbox-network、desktop-smoke、repro-bundle。

repro-bundle 在测试容器内安装 unzip 后通过。

### 1.2 模块回归

| 模块 | 结果 |
|---|---|
| backend build/vet/test | 通过 |
| docker-sandbox test + Windows build/vet | 通过 |
| desktop test + Windows build/vet（Wails tag） | 通过 |
| 前端 test/build/lint | 36 文件 / 144 tests 通过 |
| installer makensis 烟测（第二十四轮） | 通过 |

## 2. 逐项对照

### 2.1 已完成（容器内可验证）

- 3.x 桌面外壳：settings→ports→tokens→supervisor→proxy、状态/错误页、崩溃重启、Wails bridge、tray 驱动。
- 4.x 目录/NSIS：per-user 布局、静默安装、卸载保留数据、协议、可选 PDF 关联、VERSION 参数化、makensis 编译烟测。
- 5.x 配置/密钥：settings.json、DPAPI secret store（Windows 交叉编译）、onboarding、provider 持久化、运行时准备/清理。
- 6.x Provider：native UV/venv/命令翻译/AppContainer/lowil/Job 限额/双 profile/探针/RuntimeStatus/InstallRuntime/缓存清理；docker 兼容；vm WSLPath 注入 + 伪 wsl 集成；remote OpenSandbox mock 集成。
- 7 命令契约：argv+cwd+env，6 处 bash -lc 迁移，docker E2E 通过。
- 8 Windows 可移植性：procgroup/sysinfo/日志/路径/代理，三模块 Windows 交叉编译。
- 9 环境保真度：provider/isolation/runtime_hash/base_os/network_enforced + environment/manifest.json + 导出审计。
- 10 前端：RuntimePanel（探针/指标/运行时状态/准备/清理/诊断/更新）、EnvironmentBadge、Onboarding、NetworkIsolationBanner、更新 banner、计划完成通知、原生目录选择。
- 11 打包/签名/更新：updater、下载/安装闭环、NSIS、sign.ps1、package.ps1、installer-smoke、manifest 示例、公钥注入。
- 12 安全：AppContainer/lowil、双 profile、Job 限额、探针、SSRF/私网门禁。
- 13 诊断：日志轮转、诊断包、WER LocalDumps、/desktop/metrics。
- 14 测试：容器内 Go/前端单测 + 13 套 E2E；Windows 探针脚本已提供。
- 15 P0–P4：代码与容器测试完成。

### 2.2 环境边界（未验证，不作为代码缺陷）

- Windows 真机：WER dump 实际生成、NSIS 安装/卸载、快捷方式/注册表实际写入。
- 代码签名证书、更新服务器、Ed25519 生产密钥。
- 真实 WSL2 执行与真实 OpenSandbox 服务联调。
- AppContainer 实际降权、systray 实际交互、DPAPI 真机、WebView2。
- 环境依赖 E2E：model-profiles/verification（private endpoints+vision）、document-intelligence（20 页 OCR）、tool-ecosystem（docker CLI+admin+LLM）。

## 3. 残余风险

- native/VM/remote 的真实隔离与兼容性未在 Windows 上验证。
- 安装/更新链路未做端到端真机演练。
- 同步运行时安装受 10 分钟 HTTP 超时限制。
- 遥测未实现（文档仅规定默认关闭策略，未列入状态表）。

## 4. 清理

- 临时补丁目录已删除；容器均为 --rm；compose 栈保持运行。
- 工作区保留 feature/desk 未提交改动（139 项）。

## 5. 结论

用户完成条件中的“经过测试后，未发现新增问题且旧问题已被修复”在容器内可验证范围内成立。剩余项均为当前环境无法执行的 Windows 真机/签名/真实服务验证，已逐项记录。
