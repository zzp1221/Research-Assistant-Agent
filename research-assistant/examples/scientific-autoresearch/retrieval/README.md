# Retrieval Adapter 示例

用检索任务演示通用 `experiment.spec/v1` 如何工作。通用 Harness 不理解 BM25 或图检索，它只读取 Adapter 声明的方法分支、参数有限域、评测命令、指标、目标和预算。

## 输入

- `corpus.jsonl`：13 条混合工业知识，字段为 `id`、`text` 和可选 `links`。
- `queries.jsonl`：5 条搜索用例和 3 条 Holdout 用例，每条包含 `query` 与 `relevant_doc_ids`。
- 固定指标：`NDCG@1`。
- 候选方法：BM25、TF-IDF、BM25/TF-IDF RRF、显式关系边图增强检索。
- 预算：最多 6 个候选、180 秒；目标分数 `0.60`；Holdout 重复 2 次。

这里的 `graph_hybrid` 只使用数据中显式提供的关系边，不是 Microsoft GraphRAG，也不调用 LLM 建图。

## 在产品中运行

在 Web UI 同时上传 `corpus.jsonl` 和 `queries.jsonl`，输入：

```text
请在这批工业数据上做自动研究，比较 RAG 检索策略和超参数。
固定 NDCG@1，最多 6 次实验，总时长 3 分钟，目标分数 0.60，独立复验 2 次。
```

Planner 会生成 7 节点主链：

```text
适配研究数据 -> 冻结实验契约 -> 创建沙箱 -> 安装依赖
-> 候选搜索 -> Holdout 验收 -> 证据报告
```

候选树先比较方法默认配置，再围绕实际有信息的分支做单变量参数消融。Go Harness 根据 evaluator 输出决定 Keep/Reject，候选不能修改数据、runner、指标或目标。

## 结果文件

- `result.json`：一次真实运行的机器摘要。
- `optimizer_experience_result.json`：Research Optimizer 经验写入与复验记录。

结果只证明该小数据集上的链路行为，不代表生产数据最优。

## 边界

- 搜索集提升不等于全局最优。
- Holdout 未参与候选选择，因此比搜索分数更可信；但 3 条 Holdout 仍然很小。
- 真正工业使用应提供覆盖业务分布、失败成本和时间切片的标注评测集。
- 没有查询及相关文档/答案标注时，系统会拒绝宣称某个策略更优。
