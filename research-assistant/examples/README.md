# examples

可直接运行、带验收条件的示例。每个示例都明确说明运行边界、成功条件和验收方式，用于验证产品执行链，不等同于论文的完整训练复现。

## 相关架构图

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

示例运行产生的 JSON/截图请写入本地 `reports/`（默认不提交）。

| 示例 | 流程 | 运行环境 | 状态 |
|---|---|---|---|
| [Attention 论文复现](paper-reproduction/) | API -> Planner -> Scheduler -> Agents -> Docker Sandbox -> Artifacts | CPU smoke；可选 GPU 透传 | 可运行 |
| [Intent Router AutoResearch](autoresearch/intent_router/) | ResearchSpec -> baseline -> candidate -> Keep/Reject -> independent validation | CPU，Python 标准库 | 可运行 |
| [真实仓库 AutoResearch](autoresearch/real_repositories/) | Upload -> exact Git revision -> repeated search -> Coding Agent -> hidden validation | Remote Docker CPU | 任务包可运行 |
| [Benchmark Agent 分类](benchmark-agent/classification/) | Dataset audit -> leakage-safe split -> metric/reward contract -> hidden recomputation | Go test，无外部依赖 | Go test 覆盖 |
| [检索 Adapter](scientific-autoresearch/retrieval/) | experiment.spec/v1 -> 候选搜索 -> Holdout | CPU，Python 依赖 | 可运行 |

## 通用前置条件

- 已按 [research-assistant/README.md](../README.md) 启动后端与沙箱。
- `backend.env` 中配置了可用的 LLM。
- 需要 Docker 的示例必须保证 Docker daemon 可访问。

## 运行方式

大多数示例通过项目根目录的 Makefile 或 Python 脚本运行：

```bash
cd research-assistant
make example-paper-reproduction
make example-autoresearch
```

具体参数与验收条件见各示例目录的 README。
