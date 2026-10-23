# intent_recognition

真实 LLM 驱动的意图识别服务 + 双路由训练工程。

## 服务

`main.py` 调用 OpenAI-compatible chat completions（默认 DeepSeek），要求模型返回严格 JSON：

```json
{"intent_type":"Paper_Reproduction","confidence":0.9,"entities":{"paper_title":"Apriori"}}
```

- `POST /predict`：`{"text":"...","context":"可选"}` -> `intent_type/confidence/entities/model/latency_ms/attempts`
- `GET /health`：返回 `ok`、`configured`、`model`、`base_url`

允许的 `intent_type`：`General`、`Code_Execution`、`Paper_Reproduction`、`Framework_Evaluation`、`AutoResearch`、`Custom_Benchmark`、`Literature_Review`、`Data_Analysis`。

服务内置指数退避重试（默认 3 次）与单次超时（默认 60s）；没有关键词 mock fallback。未配置 `OPENAI_API_KEY` 时 `/predict` 返回 503。

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
