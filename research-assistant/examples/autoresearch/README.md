# AutoResearch 示例

该目录演示预算受限的自动研究：在冻结的评测器、数据和文件范围内，让 Agent 生成候选，真实执行后按指标 Keep/Reject，最后用独立进程或隐藏 Holdout 复验。

## 示例

| 目录 | 说明 | 运行环境 |
|---|---|---|
| [intent_router](intent_router/) | 六分类意图路由的代码补丁搜索，验证 Harness 本身 | CPU，Python 标准库 |
| [real_repositories](real_repositories/) | rank-bm25、Tenacity、LightRAG、GraphRAG、Mem0 的真实仓库任务包 | Remote Docker CPU |

## 通用协议

每个任务包至少包含：

- `autoresearch.json`：可编辑文件、保护文件、命令、指标、预算和验证次数。
- `evaluator.py`：公开搜索评测器，运行前冻结 SHA-256。
- `holdout_evaluator.py`：模型不可见的最终评测器（可选；当前 `run_campaign.py` 要求该文件存在）。

搜索流程：

```text
冻结 ResearchSpec
  -> 运行 baseline
  -> 生成候选（代码补丁或参数配置）
  -> 真实 evaluator
  -> Keep / Reject / 回滚
  -> 达到目标或预算耗尽
  -> 独立验证 / 隐藏 Holdout
```

## 运行器

`run_campaign.py` 通过产品 API 上传任务包并创建八节点 AutoResearch 计划：

```bash
python3 examples/autoresearch/run_campaign.py \
  --repository https://github.com/OWNER/REPOSITORY.git \
  --package examples/autoresearch/real_repositories/lightrag \
  --base-url http://localhost:8080 \
  --max-trials 8 \
  --wall-minutes 5 \
  --validation-runs 3 \
  --output /tmp/lightrag_run.json
```

脚本会等待终态并输出计划摘要，包括 revision、搜索统计和验证统计。`--output` 为必填参数。

## 边界

- 示例验证的是执行链和 Harness 行为，不是上游项目的完整 benchmark。
- 搜索分数只代表给定候选空间和预算内观察到的最佳结果。
- 公开评测重放不等于隐藏验证；只有模型不可见的 Holdout 才能标记为 `hidden_holdout`。
