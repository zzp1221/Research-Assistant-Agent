# 变更记录

本文件按时间倒序汇总仓库的主要提交与交付内容，供 README 只保留稳定说明。逐轮设计、缺陷清单与验收证据仍在 [design](design/) 与 [improvement-2026-10-26](improvement-2026-10-26/) 中。

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
