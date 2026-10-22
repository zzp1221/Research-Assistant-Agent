# agent

`agent` 包实现各专业 Agent 的输入契约、提示词编排、模型调用、工具执行与结果解析。Agent 不直接执行代码，而是通过 `internal/sandbox` 客户端把命令发送给沙箱服务。

## Agent 一览

| Agent | 主要文件 | 职责 |
|---|---|---|
| Chat | `chat.go` | 通用问答、轻量任务入口 |
| Librarian | `librarian.go` | 论文解析、资料检索、方法与声明提取、Claim Rubric 冻结 |
| Benchmark | `benchmark_agent.go`、`benchmark_dataset.go`、`benchmark_metric_contract.go`、`benchmark_harness.go` | 数据审计、split、泄漏检查、Metric/Reward 契约、隐藏指标重算 |
| Coder | `coder.go` | 仓库发现、依赖分析、代码生成与受限修复 |
| Research Coding | `research_coding_agent.go` | 论文仓库调试、Benchmark Adapter、代码补丁 AutoResearch |
| Data | `data.go` | 指标汇总、论文声明对比、证据图构建、报告与图表 |
| AutoResearch | `autoresearch.go` | ResearchSpec 冻结、候选执行、Keep/Reject、回滚、Holdout 验证 |
| Experiment Research | `experiment_research.go`、`experiment_retrieval_adapter.go`、`experiment_portable_adapter.go` | `experiment.spec/v1` 配置搜索与领域 Adapter |
| Ablation ToT | `ablation_tot.go` | 受限 Tree-of-Thoughts 消融设计 |
| Paper Debug | `paper_debug_harness.go` | 论文仓库调试状态机与受限补丁验证 |
| Claim Evidence | `claim_evidence.go` | 主张、准则与真实 Artifact 绑定 |

## 设计约定

1. Agent 只处理自己职责内的任务，跨领域工作通过 Scheduler 路由到其他 Agent。
2. 所有执行型 Agent 都通过沙箱客户端提交命令，不直接调用 `os/exec` 执行用户代码。
3. 结构化结果写入 Artifact，长文本和日志通过事件流返回。
4. 涉及论文主张、Benchmark 指标和 AutoResearch 决策时，必须使用 `models` 中的冻结契约，不允许模型自报最终指标。
5. 提示词集中在 `internal/prompts`，便于审计和版本化。

## 测试

```bash
go test ./internal/agent/...
```

大多数单元测试使用 `httptest` 或 mock 沙箱，不需要真实 LLM。标注为真实集成的测试需要环境变量开启，并会访问网络或 Docker。
