# 真实仓库 AutoResearch 任务包

该目录包含四个真实开源仓库的有界 AutoResearch 任务包。每个任务包记录上游 commit、公开搜索评测器和模型不可见的 Holdout，用于验证“真实仓库 + 真实执行 + 隐藏验收”的完整链路。

## 任务包

| 目标 | 仓库 | 任务包 |
|---|---|---|
| rank-bm25 | https://github.com/dorianbrown/rank_bm25 | `rank_bm25/` |
| Tenacity | https://github.com/jd/tenacity | `tenacity/` |
| LightRAG | https://github.com/HKUDS/LightRAG | `lightrag/` |
| Microsoft GraphRAG | https://github.com/microsoft/graphrag | `graphrag/`、`graphrag_v2/` |

每个任务包包含：

```text
<target>/
├── autoresearch.json       # 可编辑/保护范围、命令、指标和预算
├── evaluator.py            # 公开搜索契约
└── holdout_evaluator.py    # 模型隐藏的最终契约
```

## 冻结字段

当前规范还会冻结：

- `repository_revision`：40 位上游 commit。
- `search_runs`：baseline 与每个候选的 evaluator 重复次数。
- `search_aggregation`：`mean`、`median` 或方向相关的 `worst`。
- `target_score`：Keep 候选达到目标后确定性停止搜索。
- `validation_runs`：搜索结束后启动的独立验证进程数。

## 运行

```bash
python3 examples/autoresearch/run_campaign.py \
  --repository https://github.com/HKUDS/LightRAG.git \
  --package examples/autoresearch/real_repositories/lightrag \
  --output /tmp/lightrag_run.json \
  --base-url http://localhost:8080 \
  --max-trials 8 \
  --wall-minutes 5 \
  --validation-runs 3
```

运行器上传三个文件、创建八节点 AutoResearch 计划、等待终态，并保存完整 `plan_graph`。控制台摘要包含请求 revision、实际 revision、获取方式、目标停止、搜索统计和隐藏验证统计。

## 验收口径

- 计划完成不等于通过：最终报告必须满足预声明的隐藏阈值和完整性检查。
- 公开搜索分数只用于候选排序，不能替代 Holdout。
- 结果应记录实际 commit；如果无法固定 revision，报告必须明确说明。
- 失败记录也应保留，不能只展示成功样本。

## 说明

这些是模块级工程实验，不是上游项目的完整 benchmark。分数是任务包 evaluator 输出的固定用例比例，不代表模型或方法的普遍性能。
