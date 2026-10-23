# 架构与流程图

本目录包含三张可编辑的 draw.io 图及其 SVG 预览。图形由 `generate_diagrams.py` 中的布局数据生成，便于在代码审查中同步修改。

## 文件

| 文件 | 用途 |
|---|---|
| `architecture.drawio` / `.svg` | 系统分层架构：客户端、React 工作台、Go 后端、专业 Agent、沙箱控制面/执行面、研究优化器、LLM 与持久化卷 |
| `runtime-flow.drawio` / `.svg` | 一次研究任务的运行时序：创建计划、审批、调度、Agent 执行、沙箱、Artifact、SSE 与终态 |
| `agent-execution-flow.drawio` / `.svg` | Agent 执行细节：任务路由、六类 Agent、沙箱调用、Artifact 校验，以及 Claim Evidence / AutoResearch 子流程 |
| `generate_diagrams.py` | 使用同一份布局数据生成 `.drawio` 与 `.svg` |

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
