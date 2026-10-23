# ai-services

可选的 Python AI 服务集合。包含真实 LLM 驱动的意图识别服务，以及 BERT/Qwen 训练脚本。

## 服务

| 目录 | 说明 |
|---|---|
| `intent_recognition/` | FastAPI 意图识别服务（真实 DeepSeek/OpenAI-compatible 调用）、BERT/Qwen 训练脚本与双路由数据集 |

## 定位

- `intent-recognition` 是 **可选** 的 Compose 服务，端口 `127.0.0.1:18000`。
- 主后端仍使用确定性的规则意图路由，不依赖本服务；该服务用于需要独立意图分类能力的场景。
- 服务不再使用关键词 mock：未配置 `OPENAI_API_KEY` 时 `/predict` 明确返回 503，`/health` 返回 `degraded`。

## 快速开始（Docker Compose）

```bash
cd research-assistant
docker compose build intent-recognition
docker compose up -d intent-recognition
curl -s http://127.0.0.1:18000/health
curl -s -X POST http://127.0.0.1:18000/predict \
  -H 'Content-Type: application/json' \
  -d '{"text":"请复现 Apriori 论文的频繁项集算法"}'
```

环境变量来自 `backend.env`：

- `OPENAI_API_KEY`（必填）
- `OPENAI_BASE_URL`（默认 `https://api.deepseek.com/v1`）
- `OPENAI_MODEL_NAME`（默认 `deepseek-chat`）
- `INTENT_SERVICE_TIMEOUT_SECONDS` / `INTENT_SERVICE_MAX_ATTEMPTS` / `INTENT_SERVICE_BACKOFF_MS`

## 训练

```bash
cd intent_recognition/training
pip install -r requirements.txt
python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

训练脚本读取 `data/dual_router_v1/` 下的 `train.jsonl`、`dev.jsonl`、`test.jsonl` 与 `manifest.json`。

## 测试

```bash
docker run --rm -v "$PWD/ai-services/intent_recognition:/app" -w /app python:3.12-slim \
  sh -c "pip install -q -r requirements.txt && python -m unittest test_service.py -v"
```

## 边界

- 训练脚本需要额外依赖和 GPU/CPU 环境，默认不参与 `make test`。
- 意图识别只是规划入口的辅助能力，最终 DAG 仍由 Planner 校验。
