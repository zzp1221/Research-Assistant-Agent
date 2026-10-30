# 架构与流程图

本目录包含三张可编辑的 draw.io 图及其 SVG 预览，以及两张 Archify 生成的交互式 HTML 图。draw.io 图由 `generate_diagrams.py` 中的布局数据生成，便于在代码审查中同步修改；Archify 图由仓库源码证据生成，见下文。

## 文件

| 文件 | 用途 |
|---|---|
| `architecture.drawio` / `.svg` | 系统分层架构：客户端、React 工作台、Go 后端、专业 Agent、沙箱控制面/执行面、研究优化器、LLM 与持久化卷 |
| `runtime-flow.drawio` / `.svg` | 一次研究任务的运行时序：创建计划、审批、调度、Agent 执行、沙箱、Artifact、SSE 与终态 |
| `agent-execution-flow.drawio` / `.svg` | Agent 执行细节：任务路由、六类 Agent、沙箱调用、Artifact 校验，以及 Claim Evidence / AutoResearch 子流程 |
| `system-architecture.html` | Archify 交互式系统架构图：17 个组件、持久化数据卷边界、沙箱控制面/执行面与外部依赖 |
| `multi-agent-workflow.html` | Archify 交互式多角色 Agent 执行工作流：7 类 Agent、调度审批、沙箱执行与证据链 |
| `generate_diagrams.py` | 使用同一份布局数据生成 `.drawio` 与 `.svg` |

## Archify 交互式图

`system-architecture.html` 与 `multi-agent-workflow.html` 是自包含单文件（内联 SVG/样式/脚本），直接用浏览器打开即可；支持明暗主题、缩放平移与 PNG/SVG 导出。两图由 Archify 从仓库源码生成，节点带 `sources` 证据引用；架构变更后建议重新生成并覆盖同名文件。

## 编辑方式

1. 用 diagrams.net 或 VS Code Draw.io Integration 打开 `.drawio`；
2. 修改节点/连线后保存；
3. 导出 SVG 覆盖同名 `.svg`，或修改 `generate_diagrams.py` 后重新运行：

```bash
python3 docs/diagrams/generate_diagrams.py
```

## 图形约定

- 蓝色：前端 / 普通服务节点
- 绿色：专业 Agent
- 橙色圆柱：持久化状态
- 紫色：外部依赖（LLM、Research Optimizer）
- 黄色菱形/便签：判定、边界与注意事项
- 虚线箭头：异步回传、事件或结果流
