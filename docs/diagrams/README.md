# 架构与流程图

简体中文 | [English](README.en.md)

本目录包含四张可编辑的 draw.io 图及其 SVG 预览，以及三张 Archify 生成的交互式 HTML 图。draw.io 图由 `generate_diagrams.py` 中的布局数据生成，便于在代码审查中同步修改；Archify 图由仓库源码证据生成，见下文。

## 文件

| 文件 | 用途 |
|---|---|
| `architecture.drawio` / `.svg` | 系统分层架构：客户端与桌面外壳、React 工作台、Go 后端、专业 Agent、记忆与工具层、沙箱 Provider、持久化与外部依赖 |
| `runtime-flow.drawio` / `.svg` | 一次研究任务的运行时序：创建计划、revision/审阅、调度、Agent/工具/沙箱执行、Artifact、记忆、SSE 与终态 |
| `agent-execution-flow.drawio` / `.svg` | Agent 执行细节：任务路由、AgentProfile、记忆注入、工具调用、沙箱执行、Artifact 校验，以及 Claim Evidence / AutoResearch 子流程 |
| `memory-system.drawio` / `.svg` | 记忆系统架构：记忆类型、写入管道、检索/注入、W3 摘要、W2/W2.5 Link 构建、Memory/Paper Wiki 持久化与 owner 隔离 |
| `system-architecture.html` | Archify 交互式系统架构图：桌面/服务端形态、控制面/执行面、Provider 能力边界与外部依赖 |
| `multi-agent-workflow.html` | Archify 交互式多角色 Agent 执行工作流：路由、审批、工具、沙箱与证据链 |
| `memory-system.html` | Archify 交互式记忆系统架构：记忆分层、检索/注入、W3 摘要、W2/W2.5 Link 与持久化边界 |
| `generate_diagrams.py` | 使用同一份布局数据生成四张 `.drawio` 与 `.svg` |

## 重新生成

```bash
python3 docs/diagrams/generate_diagrams.py
```

脚本会覆盖同名 `.drawio` 与 `.svg`。修改布局时只改脚本中的 `architecture()`、`runtime_flow()`、`agent_flow()`、`memory_system()` 数据，不要手工编辑生成文件。

## Archify 交互式图

`system-architecture.html`、`multi-agent-workflow.html` 与 `memory-system.html` 是自包含单文件（内联 SVG/样式/脚本），直接用浏览器打开即可；支持明暗主题、缩放平移与 PNG/SVG 导出。三图由 Archify 从仓库源码生成，节点带 `sources` 证据引用。

生成目录约定为 `.archify/<type>-<slug>-<timestamp>/`，保留 `candidate.json`、`.html` 与 `.finalize-summary.json`。架构变更后重新生成并覆盖同名文件：

```bash
node <archify>/bin/archify.mjs finalize architecture \
  .archify/architecture-<slug>/candidate.json \
  docs/diagrams/system-architecture.html --quality showcase --json
```

> 具体路径以本机安装的 Archify 包为准；候选 JSON 的 `meta.output` 必须指向 `docs/diagrams/<name>.html`。

## 编辑方式

1. 用 diagrams.net 或 VS Code Draw.io Integration 打开 `.drawio`；
2. 修改节点/连线后保存；
3. 导出 SVG 覆盖同名 `.svg`，或修改 `generate_diagrams.py` 后重新运行。

## 图形约定

- 蓝色：前端 / 普通服务节点
- 绿色：专业 Agent
- 橙色圆柱：持久化状态
- 紫色：外部依赖（LLM、Research Optimizer、文献数据源）
- 黄色菱形/便签：判定、边界与注意事项
- 虚线箭头：异步回传、事件或结果流

## 维护要求

- 架构、Provider、Agent 或持久化边界变化时，四张 draw.io 与三张 Archify 图必须同步更新。
- README 中的架构链接指向本目录；不要移动文件名。
- 生成文件较大（Archify HTML 约 0.8 MB），提交前确认没有本地绝对路径或密钥。
