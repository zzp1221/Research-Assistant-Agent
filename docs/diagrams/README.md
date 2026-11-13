# 架构与流程图

简体中文 | [English](README.en.md)

本目录包含四张可编辑的 draw.io 图及其 SVG 预览，以及三张独立维护的 Archify 交互式 HTML 图。根 README 直接展示系统、记忆、Agent 三张概览图；详细运行时序另见 `runtime-flow`。

## 文件

| 文件 | 用途 |
|---|---|
| `architecture.drawio` / `.svg` | 系统分层架构：客户端与桌面外壳、React 工作台、Go 后端、专业 Agent、记忆与工具层、沙箱 Provider、持久化与外部依赖 |
| `runtime-flow.drawio` / `.svg` | 一次研究任务的运行时序：创建计划、revision/审阅、调度、Agent/工具/沙箱执行、Artifact、记忆、SSE 与终态 |
| `agent-execution-flow.drawio` / `.svg` | Agent 架构：任务与上下文绑定、内置 runner 与领域循环、自定义 AgentProfile、模型/工具/沙箱执行、产物验收与状态回传 |
| `memory-system.drawio` / `.svg` | 记忆架构：事件抽取与校验、受审案例、会话与活动窗口、Memory/Paper Wiki、检索与共享预算、资料块与模型预检 |
| `system-architecture.html` | Archify 交互式系统架构图：桌面/服务端形态、控制面/执行面、Provider 能力边界与外部依赖 |
| `multi-agent-workflow.html` | Archify 交互式多角色 Agent 执行工作流：路由、审批、工具、沙箱与证据链 |
| `memory-system.html` | Archify 交互式记忆系统架构：记忆分层、检索/注入、W3 摘要、W2/W2.5 Link 与持久化边界 |
| `generate_diagrams.py` | 使用同一份布局数据生成四张 `.drawio` 与 `.svg` |
| `overview_diagrams.py` | 系统、记忆、Agent 三张概览图的布局与渲染；节点、端口和折线坐标在 draw.io / SVG 中保持一致 |

## 重新生成

```bash
python3 docs/diagrams/generate_diagrams.py
```

仅重画 README 中的三张概览图：

```bash
python3 docs/diagrams/generate_diagrams.py architecture memory-system agent-execution-flow
```

脚本会覆盖所选图的 `.drawio` 与 `.svg`。三张概览图在 `overview_diagrams.py` 中维护，详细运行时序在 `generate_diagrams.py` 的 `runtime_flow()` 中维护。概览节点附有仓库相对源码路径，连线使用明确的正交端口与折点；SVG 使用原生文字，避免依赖 `foreignObject`。

## Archify 交互式图

`system-architecture.html`、`multi-agent-workflow.html` 与 `memory-system.html` 是自包含单文件（内联 SVG/样式/脚本），直接用浏览器打开即可；支持明暗主题、缩放平移与 PNG/SVG 导出。三图由 Archify 从仓库源码生成，节点带 `sources` 证据引用。

HTML 视图不由上述 Python 脚本生成，尚未同步本轮上下文、记忆和 Agent 概览调整；当前 README 以 `.drawio` / `.svg` 为准。

生成目录约定为 `.archify/<type>-<slug>-<timestamp>/`，保留 `candidate.json`、`.html` 与 `.finalize-summary.json`。架构变更后重新生成并覆盖同名文件：

```bash
node <archify>/bin/archify.mjs finalize architecture \
  .archify/architecture-<slug>/candidate.json \
  docs/diagrams/system-architecture.html --quality showcase --json
```

> 具体路径以本机安装的 Archify 包为准；候选 JSON 的 `meta.output` 必须指向 `docs/diagrams/<name>.html`。

## 编辑方式

1. 用 diagrams.net 或 VS Code Draw.io Integration 打开 `.drawio`，节点和连线均可编辑。
2. 持续维护时，修改布局脚本并重新生成对应的 `.drawio` / `.svg`。
3. 如果直接在 draw.io 中修改，需同步导出 SVG，并把调整回写布局数据，避免下次生成覆盖。

## 图形约定

- 蓝色：前端 / 普通服务节点
- 绿色：专业 Agent
- 橙色：持久化状态与产物
- 紫色：记忆、上下文、模型与研究服务
- 红色：审批、校验或隔离执行边界
- 概览图实线：主要数据或执行路径；虚线：可选调用、派生或辅助关系
- 详细运行时序中的黄色节点表示判定与边界，虚线表示事件或结果回传

## 维护要求

- 架构、Provider、Agent 或持久化边界变化时，同步更新受影响的 draw.io 与 SVG，并核对 README 说明；独立 HTML 视图需要单独生成与校验，注明未同步范围。
- README 中的架构链接指向本目录；不要移动文件名。
- 生成文件较大（Archify HTML 约 0.8 MB），提交前确认没有本地绝对路径或密钥。
