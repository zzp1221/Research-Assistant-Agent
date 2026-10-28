# intent_recognition

真实 LLM 驱动的意图识别服务 + 双路由训练工程。

## 服务

`main.py` 调用 OpenAI-compatible chat completions（默认 DeepSeek），要求模型返回严格 JSON：

```json
{
  "intent_type": "Paper_Reproduction",
  "confidence": 0.9,
  "entities": {"paper_title": "Apriori"},
  "complexity": "L2",
  "capabilities": ["paper_parse", "repo_discovery"],
  "route_hint": "standard_template",
  "complexity_confidence": 0.82,
  "complexity_reason": "explicit paper reproduction request"
}
```

- `POST /predict`：`{"text":"...","context":"可选"}` -> `intent_type/confidence/entities/reasoning/model/latency_ms/attempts`，并返回 `complexity/capabilities/route_hint/complexity_confidence/complexity_reason`。
- `GET /health`：返回 `ok`、`configured`、`model`、`base_url`。
- 缺失 complexity 字段时 Go 侧按规则计算；未知枚举降级 L2/standard_template，LLM 不得把规则等级降级。

允许的 `intent_type`：`General`、`Code_Execution`、`Paper_Reproduction`、`Framework_Evaluation`、`AutoResearch`、`Custom_Benchmark`、`Literature_Review`、`Data_Analysis`。

服务内置指数退避重试（默认 3 次）与单次超时（默认 60s）；没有关键词 mock fallback。未配置 `OPENAI_API_KEY` 时 `/predict` 返回 503。

## 主后端混合路由

`research-assistant` 后端通过 `INTENT_ROUTER_MODE` 使用本服务：

- `rule`：仅规则（默认 fallback）
- `llm`：仅模型，失败回退规则
- `hybrid`：规则快路径 + 模型语义分类 + 后校验（Compose 默认）
- `shadow`：只记录模型与规则分歧，不改变执行

相关变量：`INTENT_SERVICE_URL`、`INTENT_LLM_CONFIDENCE_THRESHOLD`、`INTENT_CLIENT_TIMEOUT_SECONDS`。规则快路径处理明确论文复现、AutoResearch、Custom_Benchmark 和高风险任务；其余请求调用本服务；模型超时或低置信度回退规则。后校验会把“无框架实体的绘图请求”从 `Framework_Evaluation` 降级为 `Code_Execution`，并保留 `needs_plot=true`。

## 运行

```bash
pip install -r requirements.txt
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=https://api.deepseek.com/v1
export OPENAI_MODEL_NAME=deepseek-chat
python main.py
```

Docker：

```bash
docker build -t research-assistant-intent .
docker run --rm -p 18000:8000 --env-file ../backend.env research-assistant-intent
```

## 测试

```bash
python -m unittest test_service.py -v
```

2026-10-28 验证：单元测试 8/8 通过；运行中的 `/predict` 在真实 provider 上出现过 502（LLM 返回非法 JSON）与超时，Go 后端会回退规则，详见本地 `reports/eval-2026-10-28/REPORT.md`。

## 训练

```bash
cd training
pip install -r requirements.txt
python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

训练数据位于 `data/dual_router_v1/`，字段与 split 说明见 `manifest.json`。

## 边界

- 训练脚本需要额外依赖与 GPU/CPU 环境，默认不参与 `make test`。
- 该服务是可选路径，主后端默认使用规则意图路由。
