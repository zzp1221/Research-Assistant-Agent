# intent_recognition

[简体中文](README.md) | English

Real-LLM intent recognition service plus a dual-router training project.

## Service

`main.py` calls OpenAI-compatible chat completions (DeepSeek by default) and requires strict JSON:

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

- `POST /predict`: `{"text":"...","context":"optional"}` -> `intent_type/confidence/entities/reasoning/model/latency_ms/attempts` plus `complexity/capabilities/route_hint/complexity_confidence/complexity_reason`.
- `GET /health`: returns `status`, `ok`, `configured`, `model`, `base_url`.
- If complexity is missing, Go computes it by rule; unknown enums degrade to L2/standard_template, and the LLM may not downgrade a rule-derived level.

Allowed `intent_type` values: `General`, `Code_Execution`, `Paper_Reproduction`, `Framework_Evaluation`, `AutoResearch`, `Custom_Benchmark`, `Literature_Review`, `Data_Analysis`. `Literature_Review` and `Data_Analysis` are normalized to `General` on the Go side and handled by General plan shapes.

The service has exponential-backoff retries (default 3) and a per-call timeout (default 60s); there is no keyword mock fallback. Without `OPENAI_API_KEY`, `/predict` returns 503.

## Backend hybrid routing

The `research-assistant` backend uses this service through `INTENT_ROUTER_MODE`:

- `rule`: rules only (code default and fallback)
- `llm`: model only, fall back to rules on failure
- `hybrid`: rule fast path + model semantic classification + post-validation (Compose default)
- `shadow`: record model/rule disagreements without changing execution

Related variables: `INTENT_SERVICE_URL`, `INTENT_LLM_CONFIDENCE_THRESHOLD`, `INTENT_CLIENT_TIMEOUT_SECONDS`. The rule fast path handles explicit paper reproduction, AutoResearch, Custom_Benchmark, and high-risk tasks; other requests call this service; timeouts or low confidence fall back to rules. Post-validation downgrades "plot request without framework entities" from `Framework_Evaluation` to `Code_Execution` and keeps `needs_plot=true`.

## Run

```bash
pip install -r requirements.txt
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=https://api.deepseek.com/v1
export OPENAI_MODEL_NAME=deepseek-chat
python main.py
```

Docker:

```bash
docker build -t research-assistant-intent .
docker run --rm -p 18000:8000 --env-file ../backend.env research-assistant-intent
```

## Tests

```bash
python -m unittest test_service.py -v
```

All 8 unit tests pass; when the real provider returns invalid JSON or times out, the Go backend falls back to rules. See [docs/verification.md](../../../docs/verification.md) for recent verification and known issues.

## Training

```bash
cd training
pip install -r requirements.txt
python build_dataset.py
python train_bert.py
python train_qwen_lora.py
```

Training data lives under `data/dual_router_v1/`; field and split details are in `manifest.json`.

## Boundaries

- Training scripts need extra dependencies and a GPU/CPU environment; they are not part of `make test`.
- This service is optional; the Go backend always falls back to rule-based intent routing when it is unavailable.
