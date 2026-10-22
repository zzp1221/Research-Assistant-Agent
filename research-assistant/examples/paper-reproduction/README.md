# Attention 论文复现示例

把 `Attention Is All You Need` 的轻量复现整理为一条可复用验收路径。示例固定使用 `harvardnlp/annotated-transformer`，以 smoke 模式运行注意力结构消融，重点验证从计划生成到沙箱产物回传的完整链路。

## 验证内容

```text
POST /api/plan
  -> Planner / PlanStore
  -> POST /api/plans/:id/execute
  -> Scheduler
  -> Librarian / Coder / Sandbox / Data Agents
  -> Docker Sandbox
  -> Artifacts and event history
```

成功运行必须满足：

- 计划意图为 `Paper_Reproduction`，所有 DAG 节点进入 `completed`。
- 系统选择的仓库为 `https://github.com/harvardnlp/annotated-transformer`。
- 至少生成 `repo_url`、`run_metrics` 和 `comparison_report` 三个关键产物。
- 事件历史包含 `plan_started`、`artifact_created` 和 `plan_completed`。

## 运行

先启动完整服务，然后：

```bash
cd research-assistant
python3 examples/paper-reproduction/run.py \
  --output /tmp/attention-paper-reproduction.json
```

脚本只使用 Python 标准库。它会检查服务健康状态、创建并执行计划、轮询终态、验证关键产物与事件，最后输出不包含 API Key 的运行摘要。远端部署可指定地址：

```bash
python3 examples/paper-reproduction/run.py \
  --base-url http://YOUR_SERVER:8080 \
  --output /tmp/attention-paper-reproduction.json
```

通过 Makefile：

```bash
RESEARCH_ASSISTANT_API_URL=http://localhost:8080 make example-paper-reproduction
```

## 集成测试

```bash
cd backend
REAL_PAPER_REPRO_TEST=1 \
SANDBOX_URL=http://localhost:8082 \
go test ./tests -run TestRealPaperReproductionFlow -v -count=1
```

该测试需要 LLM 配置、联网仓库访问和已启动的 Docker Sandbox，默认 `go test ./...` 会跳过。

离线测试（不访问 LLM、网络或 Docker）：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s examples/paper-reproduction -p 'test_*.py'
```

## 验收指标

一次成功运行应观察到：

| 项目 | 期望 |
|---|---|
| 计划状态 | 全部节点 `completed`，0 失败 |
| 仓库 | `harvardnlp/annotated-transformer` |
| 运行时 | PyTorch CPU，固定 seed |
| 产物 | 至少包含 `repo_url`、`run_metrics`、`comparison_report` |
| 事件 | `plan_started`、`artifact_created`、`plan_completed` |

## 范围

这是结构级前向 smoke test。系统会克隆并扫描指定仓库，但消融入口由系统生成，不会下载 WMT14、训练完整 Transformer 或计算 BLEU。CPU 延迟只适用于本次小张量微基准。
