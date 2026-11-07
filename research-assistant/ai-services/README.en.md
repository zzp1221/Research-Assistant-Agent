# ai-services

[简体中文](README.md) | English

Optional Python AI services: a real-LLM intent-recognition service plus BERT/Qwen training scripts.

## Services

| Directory | Description |
|---|---|
| `intent_recognition/` | FastAPI intent service (real OpenAI-compatible calls), BERT/Qwen training scripts, and dual-router dataset |

## Positioning

- `intent-recognition` is an optional Compose service; host port `127.0.0.1:18000` maps to container `8000`.
- The backend defaults to `INTENT_ROUTER_MODE=hybrid`: rule fast path + this service's semantic classification + post-validation; if the service is unavailable or confidence is low, it falls back to rules.
- The service has no keyword mock: without `OPENAI_API_KEY`, `/predict` returns 503 and `/health` reports `degraded`.
- New schema: `POST /predict` also returns `complexity` (L0–L4), `capabilities`, `route_hint`, `complexity_confidence`, and `complexity_reason`; the Go `ComplexityRouter` makes the final decision after attachment/paper context.

## Quick start (Docker Compose)

```bash
cd research-assistant
docker compose build intent-recognition
docker compose up -d intent-recognition
curl -s http://127.0.0.1:18000/health
curl -s -X POST http://127.0.0.1:18000/predict \
  -H 'Content-Type: application/json' \
  -d '{"text":"请复现 Apriori 论文的频繁项集算法"}'
```

Environment variables come from `backend.env`:

- `OPENAI_API_KEY` (required)
- `OPENAI_BASE_URL` (defaults to `https://api.deepseek.com/v1`)
- `OPENAI_MODEL_NAME` (defaults to `deepseek-chat`)
- `INTENT_SERVICE_TIMEOUT_SECONDS` / `INTENT_SERVICE_MAX_ATTEMPTS` / `INTENT_SERVICE_BACKOFF_MS`

## Training

```bash
cd intent_recognition/training
pip install -r requirements.txt
python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

Training scripts read `train.jsonl`, `dev.jsonl`, `test.jsonl`, and `manifest.json` under `data/dual_router_v1/`.

## Tests

```bash
docker run --rm -v "$PWD/ai-services/intent_recognition:/app" -w /app python:3.12-slim \
  sh -c "pip install -q -r requirements.txt && python -m unittest test_service.py -v"
```

## Boundaries

- Training scripts need extra dependencies and a GPU/CPU environment; they are not part of `make test`.
- Intent recognition only assists the planning entry; the final DAG is still validated by the Planner.
- When the real provider occasionally returns invalid JSON or times out, the service returns 502/503 and the Go backend falls back to rules; these failures are recorded in [docs/verification.md](../../docs/verification.md).
