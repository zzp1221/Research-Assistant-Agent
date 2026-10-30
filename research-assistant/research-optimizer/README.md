# research-optimizer

Python 研究优化器，为 Go Harness 提供数据特征、候选排序和跨任务经验。它不执行实验，也不决定科学验收。

## 在系统中的位置

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）

Research Optimizer 是 Go Harness 的候选排序顾问：Go 侧冻结 ResearchSpec、执行候选、做 Keep/Reject 与 Holdout 验收；Python 侧只根据规范化样本和历史经验给出候选优先级，不接触实验工作区。

## 职责

- 从冻结数据中提取有界特征（`POST /v1/profile`，schema `experiment.features/v1`）。
- 根据当前路线统计与历史经验选择下一个候选（`POST /v1/select`）。
- 记录候选结果与验证状态（`POST /v1/experience/outcome`、`POST /v1/experience/validation`）。
- 使用 SQLite 持久化经验，只把已验证 campaign 作为跨任务先验。

## 搜索策略

当前策略版本：`hierarchical-contextual-ucb-uct/v2`

1. Go 先对每个合法 Model 组合用默认配置运行一次（`bounded-exhaustive/v1`）。
2. Route UCB 在 Model 参数树之间分配预算。
3. Top-K Beam 加探索通道构成活跃前沿。
4. UCT-style 分数选择树内父路径。
5. Virtual visit 避免并发 Agent 重复选择同一路线。
6. 真实 Validation Reward 更新调度统计；候选分数和根节点默认分数保持不变。

该策略没有模拟 rollout 或价值网络，不是完整 MCTS；连续贝叶斯优化和 Hyperband 需要独立协议，当前不会用离散枚举冒充。

## API

| Method | Endpoint | 说明 |
|---|---|---|
| `POST` | `/v1/profile` | 构建数据集特征 |
| `POST` | `/v1/select` | 选择下一个候选 |
| `POST` | `/v1/experience/outcome` | 记录候选结果 |
| `POST` | `/v1/experience/validation` | 记录验证状态 |
| `GET` | `/v1/stats` | 查询经验库统计 |
| `GET` | `/health` | 健康检查 |

`/health` 无需鉴权；设置 `RESEARCH_OPTIMIZER_API_TOKEN` 后，其余端点都需要 Bearer Token。未设置 Token 时按无鉴权本地开发处理。

## 运行

```bash
RESEARCH_OPTIMIZER_API_TOKEN=local-optimizer-token \
RESEARCH_OPTIMIZER_DB_PATH=/tmp/research-assistant-experiences.sqlite3 \
python3 -m research_optimizer.service
```

或 `make run-optimizer`（默认把 SQLite 写到 `/tmp/research-assistant-experiences.sqlite3`）。监听端口由 `RESEARCH_OPTIMIZER_PORT`（默认 8090）控制。

## 测试

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -p 'test_*.py'
```

容器内 8 个测试全部通过；`/health`、`/v1/stats` 鉴权与 `/v1/profile` 契约探针均通过。最近结果见 [docs/verification.md](../../docs/verification.md)。

## 文件

| 文件 | 职责 |
|---|---|
| `research_optimizer/policy.py` | UCB/UCT/Beam 候选选择 |
| `research_optimizer/profiling.py` | 数据特征提取与指纹 |
| `research_optimizer/store.py` | SQLite 经验库 |
| `research_optimizer/service.py` | HTTP 服务与路由 |
| `tests/` | 策略、服务与存储测试 |

## 边界

优化器只输出候选优先级建议。Go Harness 保留候选合法性、预算、evaluator 执行、Keep/Reject、回滚和 Holdout 接受权。服务不挂载实验工作区，只接收经过 Go 校验的规范化样本。
