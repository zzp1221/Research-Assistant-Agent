# 变更记录

本文件按时间倒序汇总仓库的主要提交与交付内容，供 README 只保留稳定说明。逐轮设计、缺陷清单与验收证据仍在 [design](design/) 与 [improvement-2026-10-26](improvement-2026-10-26/) 中。

## 2026-11-01

| 提交 | 内容 |
|---|---|
| 工作区（feature/memory） | 落地 Agent 记忆系统 M0–M3：internal/memory（JSONL+快照+BM25+RRF/MMR+预算+脱敏）、/api/memories、server session/ChatSession、chat 多轮+摘要+citations、prompts core/retrieved section、scheduler 注入、计划 episode/ParentPlanID/MemoryRefs、AutoResearch trial 记忆、document 记忆、consolidation、前端 MemoryPanel；真实 LLM 30 轮科研长任务与计划 A/B 验证，并修复 createPlan nil pointer。详见 [MEMORY-IMPLEMENTATION](improvement-2026-11-01/MEMORY-IMPLEMENTATION.md) 与 [MEMORY-TEST-REPORT](improvement-2026-11-01/MEMORY-TEST-REPORT.md)。 |

## 2026-10-31

| 提交 | 内容 |
|---|---|
| 工作区（最终收口） | 13 套 E2E + 四模块全量回归通过；容器内可验证项完成；残余仅为 Windows 真机/签名/真实服务环境边界。详见 [FIX-REPORT-ROUND26](improvement-2026-10-26/FIX-REPORT-ROUND26.md)。 |
| 工作区（第二十五轮） | native 运行时缓存清理（CleanRuntimeCache/CleanupRuntimeCache）+ sandbox/shell/bridge/UI 清理链路、安装中拒绝、二次确认。详见 [FIX-REPORT-ROUND25](improvement-2026-10-26/FIX-REPORT-ROUND25.md)。 |
| 工作区（第二十四轮） | NSIS VERSION 参数化（VIProductVersion/DisplayVersion）、installer-smoke.sh 容器内 makensis 编译烟测、LegalCopyright。详见 [FIX-REPORT-ROUND24](improvement-2026-10-26/FIX-REPORT-ROUND24.md)。 |
| 工作区（第二十三轮） | 更新可用 banner（Wails ra:update-available）、native SystemPackages 能力门禁（runtimeProbe + 计划前置）。详见 [FIX-REPORT-ROUND23](improvement-2026-10-26/FIX-REPORT-ROUND23.md)。 |
| 工作区（第二十二轮） | RuntimeManager EnsureRuntimeWithProgress（python/venv/dependencies/freeze/done）、native 安装状态与 RuntimeStatus、RuntimePanel 安装阶段轮询。详见 [FIX-REPORT-ROUND22](improvement-2026-10-26/FIX-REPORT-ROUND22.md)。 |
| 工作区（第二十一轮） | native InstallRuntime + sandbox/shell/bridge/UI 触发链、RuntimePanel Docker 升级提示、设计文档 P2/P3/P4 阶段标题修正。详见 [FIX-REPORT-ROUND21](improvement-2026-10-26/FIX-REPORT-ROUND21.md)。 |
| 工作区（第二十轮） | RuntimePanel provider 持久化（bridge Get/Update）、native RuntimeStatus + /api/v1/runtime/status + /desktop/runtime、诊断包/日志目录 UI 入口。详见 [FIX-REPORT-ROUND20](improvement-2026-10-26/FIX-REPORT-ROUND20.md)。 |
| 工作区（第十九轮） | WER LocalDumps 崩溃转储注册（shell/backend/sandbox）、desktop/build/package.ps1 打包流水线、全量既有 E2E 回归与环境限制记录。详见 [FIX-REPORT-ROUND19](improvement-2026-10-26/FIX-REPORT-ROUND19.md)。 |
| 工作区（第十八轮） | VM Provider WSLPath 注入 + 伪 wsl 集成测试、updater.Manager.Install + bridge/RuntimePanel 安装闭环、NSIS 可选 PDF 关联。详见 [FIX-REPORT-ROUND18](improvement-2026-10-26/FIX-REPORT-ROUND18.md)。 |
| 工作区（第十七轮） | 自动更新闭环（updater.Manager + bridge 检查/下载 + RuntimePanel 更新区块）、remote Provider mock OpenSandbox 集成测试、导出审计 provider/isolation、Windows 真机探针/启动残留脚本。详见 [FIX-REPORT-ROUND17](improvement-2026-10-26/FIX-REPORT-ROUND17.md)。 |
| 工作区（第十六轮） | 导出/复现包按 provider 分支（native runtime_hash / docker image digest + environment/manifest.json）、GPU/Python 细粒度能力门禁、原生目录选择/在文件夹中显示、签名脚本与更新公钥注入。详见 [FIX-REPORT-ROUND16](improvement-2026-10-26/FIX-REPORT-ROUND16.md)。 |
| 工作区（第十五轮） | vm Provider WSL2 执行路径（路径/argv 翻译、create/exec/delete/usage）、规划器能力快照与 `image_prepare→runtime_probe`、`/desktop/metrics` 本地指标、崩溃恢复提示条。详见 [FIX-REPORT-ROUND15](improvement-2026-10-26/FIX-REPORT-ROUND15.md)。 |
| 工作区（第十四轮） | AppContainer 双 profile 网络（离线/在线 internetClient）、vm/remote Provider（WSL2 检测 + OpenSandbox 适配）、诊断包崩溃转储、systray Windows 托盘。详见 [FIX-REPORT-ROUND14](improvement-2026-10-26/FIX-REPORT-ROUND14.md)。 |
| 工作区（第十三轮） | 桌面外壳产品化：`desktop/shell` 编排（端口/token/supervisor/proxy/错误页/崩溃重启）、Wails 入口与 bridge、首次启动向导、诊断包、Ed25519 签名更新包、NSIS 静默安装/卸载保留数据；native Windows Runner 补齐 AppContainer/lowil/Job 限额/附录 D 探针与 runtime_hash 回填。详见 [FIX-REPORT-ROUND13](improvement-2026-10-26/FIX-REPORT-ROUND13.md)。 |
| 工作区（第十二轮） | Windows 桌面客户端与去 Docker 化基础：跨平台 procgroup/sysinfo、LOG_DIR/APP_DATA_DIR/RA_CONFIG_PATH、日志轮转、代理保留、Command argv 契约、Provider 抽象、native uv/venv 骨架、desktop supervisor/proxy/settings、运行模式页与环境徽章。详见 [FIX-REPORT-ROUND12](improvement-2026-10-26/FIX-REPORT-ROUND12.md)。 |

## 2026-10-30

| 提交 | 内容 |
|---|---|
| 工作区（第十一轮） | 落地 N-13 Agent/工具生态：真 AllowedTools 白名单、ToolRegistry/ToolInvoker、native/HTTP/MCP stdio/MCP HTTP 传输、tool_call 节点与 tool_call 审批、ToolCallRecord 审计与回放、AgentProfile/PromptVersion/ModelProfile 绑定、Prompt 评测/回滚、设置页工具/Agent tab、计划图 tool_call 展示。容器内 E2E 44/44。详见 [FIX-REPORT-ROUND11](improvement-2026-10-26/FIX-REPORT-ROUND11.md)。 |
| 工作区（第十轮复核） | 独立复核 N-01/N-02/N-03/N-05；修复 review gate 草稿绕过、普通执行未绑定最新 revision、revision 状态投影、manifest revision 类型、禁用节点跳过、导出 artifact 脱敏、双门禁审批顺序、复制计划审阅门禁、单任务预算/usage、post-task 预算检查时序、收件箱分页与审阅面板交互等 12 类问题。详见 [FIX-REPORT-ROUND10](improvement-2026-10-26/FIX-REPORT-ROUND10.md)。 |

## 2026-10-28

| 提交 | 内容 |
|---|---|
| 工作区（第八轮） | 落地 N-01/N-02/N-03：计划库（项目/搜索/过滤/keyset 分页/重命名/归档/复制）、可复现导出包 v2（manifest/checksums/environment recipe/provenance/脱敏/大小上限/静态校验）、成本与预算控制台（profile 计价、预留/结算、80% 预警、soft/hard 闸门、成本 API/UI）。详见 [FIX-REPORT-ROUND8](improvement-2026-10-26/FIX-REPORT-ROUND8.md)。 |
| `af87d5c` | 更新前端视觉回归的过期选择器并刷新 Linux 基线快照。 |
| `4e07ece` | 统一 Go 源码格式；修复 UTF-8 检查脚本对自身源码的误报。 |
| `6a9c960` | 将各 README 更新到当前功能与测试状态；补充 round7 测试说明。 |
| `5242ccd` | 新增 round7 大规模 E2E：deep API、真实 LLM 场景、论文复现、AutoResearch、任务执行、文档 assist、sandbox 深度测试；同步更新前端 E2E 选择器。 |
| `ee676f3` | 落地 N-04 文献检索/N-07 文档智能、运行期模型配置、L0–L4 复杂度路由、单一 Prompt 组装、Repair 会话与 Token 预算。 |
| `0f605c2` | 新增 N-04/N-07 改进设计与实施方案。 |
| `95d7ab7` | 完成 R-01..R-17 剩余问题修复：沙箱网络与资源硬限制、API 加固、可观测性、文档/文献能力与前端审批体验。 |
| `5fad692` / `7faf7a3` | 合并 top-10 优先级修复：身份 Cookie、审批超时、真实绘图、规划器节点治理、SSE 与错误处理。 |
| `252cdf0` | 新增 2026-10-26 改进审计、剩余问题清单与功能建议。 |

> 2026-10-28 验证结果与已知缺陷见 [verification.md](verification.md)。

## 2026-10-26

| 提交 | 内容 |
|---|---|
| `5dc8091` / `57ff9fb` | UI workbench 以“annotated journal proof”方向重构，补充真实 plan id 与节点检查器入口。 |
| `8c71c1e` / `90983d5` | 工作台改为科研仪器风格；计划图、执行侧栏与会话导航重做。 |

## 2026-10-25

| 提交 | 内容 |
|---|---|
| `7c7c750` / `acae947` | 修复 2026-10-25 评测中的 D1–D10 缺陷；详见 [defect-remediation-2026-10-25.md](defect-remediation-2026-10-25.md)。 |

## 2026-10-24

| 提交 | 内容 |
|---|---|
| `4b8df45` | CI 支持 Windows sandbox 构建，刷新移动端视觉基线。 |
| `984c2cb` / `4e1d927` | README 重组为架构、快速开始与本地开发三部分，并移除开发产物章节。 |
| `c5b5d31` | 文档化运行时权限与 ResearchContext。 |
| `de30720` / `9776dbd` | RC4 论文复现运行时：RuntimeSpec、镜像/依赖探测、EnvironmentManifest 与逐项审批。 |
| `2bb6cfa` | 非 RC4 路径补齐 ResearchContext、PlanOutcome 与结果语义。 |
| `f123dbf` / `7555c40` / `6a77bbd` | 新增任务类型注册表与规划器校验。 |
| `a2f5b52` / `542db57` | 文档与 `backend.env.example` 指向官方 DeepSeek 默认值。 |
| `29114de` / `ef9980a` | General 摘要计划允许最终回答校验，通用流程接入 Chat Agent 与真实上下文。 |
| `8912804` | 绘图产物改为 sandbox 内真实生成。 |
| `3274c99` / `745499c` | 移除 planner 假节点，新增混合意图路由与真实绘图执行。 |

## 2026-10-23

| 提交 | 内容 |
|---|---|
| `8d043e8` | 修复 PDF 缩放时 AI 翻译浮层越界。 |
| `737dc63` | 使用真实 DeepSeek provider 完成 P0 验收。 |
| `55d59bb` / `ccac32a` | 对齐移动端 Playwright 基线；合并前端 UI 刷新与 P0/P1 性能门禁。 |
| `75f15a7` / `99bcf8f` | 前端懒加载、bundle 预算、学术手稿风格工作台。 |
| `88a0564` / `498fb60` | 新增 draw.io 架构图、运行流程图与 Agent 执行流程图，并提供 SVG 预览。 |
| `c7c5baa` / `cc2a488` | Benchmark adapter 重试修复；数据驱动论文 catalog 与前端回归测试。 |
| `2f36666` / `1b18189` / `507ea4d` | Phase 1 真实 provider 验收、PDF 阅读/划词翻译与证据记录。 |
| `11d1241` | Phase 1：PDF 阅读、文档 assist 与可读结果。 |

## 2026-10-22

| 提交 | 内容 |
|---|---|
| `c87d2e4` | 关闭 P0/P1 剩余整改项。 |
| `391fb24` | 认证 SSE ticket，移除死 UI 路径。 |
| `906a19d` | 记录最终容器 E2E 证据。 |
| `02dc205` | 支持轻量论文 smoke 与仓库 AutoResearch 运行。 |
| `4c62106` / `039231f` | 限制 API 请求体、Compose 端口可配置、容器安全边界文档化。 |
| `f559784` | 加固执行边界与 scheduler 状态处理。 |
| `8d5ea95` | 整改前基线快照。 |

## 版本说明

仓库当前处于研究原型阶段，接口和行为可能调整，不承诺向后兼容。稳定使用说明见根 [README](../README.md) 与各模块 README；本轮及后续验证状态统一记录在 [verification.md](verification.md)。