# 项目文档

本目录集中放置跨模块的架构与流程图，以及项目级说明。模块内部文档仍放在各自目录的 `README.md`。

## 架构与流程图（draw.io）

| 图 | draw.io 源文件 | SVG 预览 | 说明 |
|---|---|---|---|
| 系统架构图 | [architecture.drawio](diagrams/architecture.drawio) | [architecture.svg](diagrams/architecture.svg) | 前端、后端、Agent、沙箱、优化器、LLM 与持久化状态的分层关系 |
| 运行流程图 | [runtime-flow.drawio](diagrams/runtime-flow.drawio) | [runtime-flow.svg](diagrams/runtime-flow.svg) | 从用户目标到 PlanGraph、调度、沙箱执行、Artifact 与 SSE 的完整时序 |
| Agent 执行流程图 | [agent-execution-flow.drawio](diagrams/agent-execution-flow.drawio) | [agent-execution-flow.svg](diagrams/agent-execution-flow.svg) | RoutedTaskExecutor 的 Agent 路由、沙箱调用、Artifact 校验，以及 Claim Evidence / AutoResearch 子循环 |

### 预览

![系统架构图](diagrams/architecture.svg)

![运行流程图](diagrams/runtime-flow.svg)

![Agent 执行流程图](diagrams/agent-execution-flow.svg)

打开方式：

- 使用 [diagrams.net](https://app.diagrams.net/) 或 VS Code 的 Draw.io Integration 插件打开 `.drawio` 文件；
- 修改后可从 draw.io 导出新的 SVG/PNG，或运行 `python3 docs/diagrams/generate_diagrams.py` 重新生成当前版本的 SVG 预览。

## 其他文档

- [运行期模型配置与通用视觉校验设计](design/runtime-model-config-and-visual-verification.md)：per-user + 全局默认模型配置、视觉能力探测与路由、verify_result 通用图像校验（inconclusive/fail 语义）
- [第五轮修复报告（Agent 链路优化 + 复杂度路由）](improvement-2026-10-26/FIX-REPORT-ROUND5.md)：A/B/C/D 四个优化点与 L0-L4 路由的落地、差异、验证证据与回滚开关
- [意图复杂度路由与计划生成选择设计](design/intent-complexity-routing.md)：方案 C，把复杂度判断合并进 intent 分类；L0-L4 路由、模板变体、skeleton + agent 增强、验收与风险
- [Agent 链路优化设计（General / Prompt / Repair / Token 预算）](design/2026-10-27-agent-chain-optimization.md)：四个具体优化点的“问题在哪、为什么、怎么做”、代码位置与验收清单
- [N-04 真实文献与网页检索 + N-07 文档智能：改进设计与实施方案](design/n04-n07-literature-and-document-intelligence.md)：基于现有 literature/documents 能力的增量设计，含现状、问题、方案、阶段与验收
- [UI 升级设计说明](ui-research-console.md)：科研仪器风格的视觉与交互规范、三个方向的取舍与实现记录
- [缺陷修复文档（2026-10-25）](defect-remediation-2026-10-25.md)：D1–D10 的「为什么修 / 哪里有问题 / 怎么修」，含落地顺序与验收清单
- [根 README](../README.md)：项目定位、快速开始、API 摘要与运行边界
- [research-assistant/README.md](../research-assistant/README.md)：产品主实现
- [research-assistant/docs/phase1.md](../research-assistant/docs/phase1.md)：PDF 阅读、划词翻译与结果可读性
- [research-assistant/docs/phase2.md](../research-assistant/docs/phase2.md)：数据驱动 catalog 与前端回归测试
- [reports/README.md](../reports/README.md)：验收报告目录说明（报告默认本地生成，不提交）

## 文档约定

- 架构/流程变更时，先更新 `docs/diagrams/*.drawio`，再同步 SVG 预览与相关 README。
- `plan.md`、`spec.md`、`tasks.md` 和 `reports/` 下的运行产物属于开发过程资料，已加入 `.gitignore`，不进入版本库。
- README 中只保留稳定的使用说明与边界，不粘贴会快速过期的运行日志。