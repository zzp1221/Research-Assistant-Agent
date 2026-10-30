# 项目文档

本目录集中放置跨模块的架构图、设计文档与项目级说明；模块内部文档仍在各自目录的 `README.md`。

## 架构与流程图

| 图 | draw.io 源文件 | SVG 预览 | 说明 |
|---|---|---|---|
| 系统架构图 | [architecture.drawio](diagrams/architecture.drawio) | [architecture.svg](diagrams/architecture.svg) | 前端、后端、Agent、沙箱、优化器、LLM 与持久化状态的分层关系 |
| 运行流程图 | [runtime-flow.drawio](diagrams/runtime-flow.drawio) | [runtime-flow.svg](diagrams/runtime-flow.svg) | 从用户目标到 PlanGraph、调度、沙箱执行、Artifact 与 SSE 的完整时序 |
| Agent 执行流程图 | [agent-execution-flow.drawio](diagrams/agent-execution-flow.drawio) | [agent-execution-flow.svg](diagrams/agent-execution-flow.svg) | RoutedTaskExecutor 的 Agent 路由、沙箱调用、Artifact 校验，以及 Claim Evidence / AutoResearch 子循环 |

### Archify 交互式图

| 图 | 文件 | 说明 |
|---|---|---|
| 系统架构图 | [system-architecture.html](diagrams/system-architecture.html) | 17 个组件、持久化数据卷边界、沙箱控制面/执行面与外部依赖 |
| 多角色 Agent 执行工作流 | [multi-agent-workflow.html](diagrams/multi-agent-workflow.html) | 7 类 Agent 的 assigned_to 路由、调度审批、沙箱执行与证据链 |

两图为自包含 HTML（内联 SVG/样式/脚本），浏览器直接打开，支持明暗主题、缩放平移与 PNG/SVG 导出。

### 预览

![系统架构图](diagrams/architecture.svg)

![运行流程图](diagrams/runtime-flow.svg)

![Agent 执行流程图](diagrams/agent-execution-flow.svg)

打开方式：

- 使用 [diagrams.net](https://app.diagrams.net/) 或 VS Code 的 Draw.io Integration 插件打开 `.drawio` 文件；
- 修改后可从 draw.io 导出新的 SVG/PNG，或运行 `python3 docs/diagrams/generate_diagrams.py` 重新生成当前版本的 SVG 预览。

## 项目状态

- [CHANGELOG.md](CHANGELOG.md)：按提交整理的变更记录（2026-10-22 起）。
- [verification.md](verification.md)：最近一次构建/测试/验收状态、已知缺陷与复跑命令。
- [../reports/README.md](../reports/README.md)：本地验收报告目录说明（报告默认本地生成，不提交）。

## 设计文档

- [运行期模型配置与通用视觉校验设计](design/runtime-model-config-and-visual-verification.md)：per-user + 全局默认模型配置、视觉能力探测与路由、verify_result 通用图像校验（inconclusive/fail 语义）
- [意图复杂度路由与计划生成选择设计](design/intent-complexity-routing.md)：方案 C，把复杂度判断合并进 intent 分类；L0-L4 路由、模板变体、skeleton + agent 增强、验收与风险
- [Agent 链路优化设计（General / Prompt / Repair / Token 预算）](design/2026-10-27-agent-chain-optimization.md)：四个具体优化点的“问题在哪、为什么、怎么做”、代码位置与验收清单
- [N-04 真实文献与网页检索 + N-07 文档智能：改进设计与实施方案](design/n04-n07-literature-and-document-intelligence.md)：基于现有 literature/documents 能力的增量设计，含现状、问题、方案、阶段与验收
- [N-01/N-02/N-03：计划库、可复现导出与成本预算控制台](design/n01-n03-plan-library-reproducibility-cost.md)：基于现有计划列表/导出/usage 的增量设计，含索引、manifest/checksum、预算闸门与验收
- [N-05 计划审阅与人在回路](design/n05-plan-review-hitl.md)：revision、执行前审阅、结构化提问、审批收件箱与 diff 的设计
- [N-13 Agent/工具生态（MCP）](design/n13-agent-tool-ecosystem-mcp.md)：工具注册/权限/审计、MCP 接入、自定义 Agent 与 Prompt 版本的设计
- [文献库与 PDF 阅读体验改版](design/2026-10-27-literature-ui-redesign.md)：文献库与 PDF 阅读的方向探索、关键改动与交互动效
- [UI 升级设计说明](ui-research-console.md)：科研仪器风格的视觉与交互规范、三个方向的取舍与实现记录
- [缺陷修复文档（2026-10-25）](defect-remediation-2026-10-25.md)：D1–D10 的「为什么修 / 哪里有问题 / 怎么修」，含落地顺序与验收清单
- [ADR 模板](adr/000-template.md)：新增架构决策记录时复制该模板

## 轮次报告（2026-10-26 改进审计及后续）

| 文档 | 内容 |
|---|---|
| [01 最值得先修的 10 个点](improvement-2026-10-26/01-top-10-priorities.md) | 审计基线：用户可见阻断、安全、资源与可复现性问题 |
| [02 剩余未修问题清单](improvement-2026-10-26/02-remaining-issues.md) | R-01..R-17 的来源与影响 |
| [03 新增功能建议](improvement-2026-10-26/03-new-feature-proposals.md) | 从研究原型走向研究平台的功能建议 |
| [Top-10 修复报告](improvement-2026-10-26/FIX-REPORT.md) | 2026-10-26 Top-10 优先级缺陷的修复与实测 |
| [第二轮修复报告（R-01 ~ R-17）](improvement-2026-10-26/FIX-REPORT-ROUND2.md) | 沙箱网络与硬限制、API 加固、可观测性、文档与文献能力 |
| [第三轮修复报告（运行期模型配置与视觉校验）](improvement-2026-10-26/FIX-REPORT-ROUND3.md) | modelprofile/modelresolver、动态模型、视觉探测与 verify_result 落地 |
| [第四轮修复报告（N-04 文献 + N-07 文档智能）](improvement-2026-10-26/FIX-REPORT-ROUND4.md) | 文献 provider/文献库/引用图、PDF OCR/版面/表格/图注/质量报告 |
| [第五轮修复报告（Agent 链路优化 + 复杂度路由）](improvement-2026-10-26/FIX-REPORT-ROUND5.md) | A/B/C/D 四个优化点与 L0-L4 路由的落地、差异、验证证据与回滚开关 |
| [第六轮大规模测试报告](improvement-2026-10-26/TEST-REPORT-ROUND6.md) | 全量回归、专项测试、缺陷与清理记录 |
| [文献检索相关性修复报告](improvement-2026-10-26/FIX-REPORT-LITERATURE-RELEVANCE.md) | relevance 跨 provider 排序、候选池与 provider 超时的修复与验证 |
| [ROUND4 规格](improvement-2026-10-26/ROUND4-SPEC.md) / [计划](improvement-2026-10-26/ROUND4-PLAN.md) / [任务](improvement-2026-10-26/ROUND4-TASKS.md) | 第四轮的需求、实施计划与任务拆分 |

## 其他文档

- [根 README](../README.md)：项目定位、快速开始、API 摘要与运行边界
- [research-assistant/README.md](../research-assistant/README.md)：产品主实现
- [research-assistant/docs/phase1.md](../research-assistant/docs/phase1.md)：PDF 阅读、划词翻译与结果可读性
- [research-assistant/docs/phase2.md](../research-assistant/docs/phase2.md)：数据驱动 catalog 与前端回归测试

## 文档约定

- 架构/流程变更时，先更新 `docs/diagrams/*.drawio`，再同步 SVG 预览、Archify HTML（`system-architecture.html`、`multi-agent-workflow.html`）与相关 README。
- `plan.md`、`spec.md`、`tasks.md` 和 `reports/` 下的运行产物属于开发过程资料，已加入 `.gitignore`，不进入版本库。
- README 只保留稳定的使用说明与边界；按提交整理的变更写入 [CHANGELOG.md](CHANGELOG.md)，测试结论写入 [verification.md](verification.md)，避免把临时运行日志粘进 README。
