"""Real LLM-backed intent recognition service.

This service is optional. It calls an OpenAI-compatible chat completions
endpoint (DeepSeek by default) and returns a structured intent classification.
There is no keyword mock fallback: if the model is not configured or every
attempt fails, the endpoint returns an explicit 503/502 error.
"""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

app = FastAPI(title="Intent Recognition Service", version="2.0.0")

ALLOWED_INTENTS = {
    "General",
    "Code_Execution",
    "Paper_Reproduction",
    "Framework_Evaluation",
    "AutoResearch",
    "Custom_Benchmark",
    "Literature_Review",
    "Data_Analysis",
}


class IntentRequest(BaseModel):
    text: str = Field(min_length=1, max_length=20000)
    context: str | None = Field(default=None, max_length=20000)


class IntentResponse(BaseModel):
    intent_type: str
    confidence: float
    entities: dict[str, Any]
    reasoning: str | None = None
    model: str
    latency_ms: int
    attempts: int


class LLMConfig(BaseModel):
    api_key: str
    base_url: str
    model: str
    timeout_seconds: float = 60.0
    max_attempts: int = 3
    backoff_ms: int = 500


def load_llm_config() -> LLMConfig:
    api_key = os.environ.get("OPENAI_API_KEY", "").strip()
    base_url = os.environ.get("OPENAI_BASE_URL", "https://api.deepseek.com/v1").strip().rstrip("/")
    model = os.environ.get("OPENAI_MODEL_NAME", "deepseek-chat").strip()
    return LLMConfig(
        api_key=api_key,
        base_url=base_url,
        model=model,
        timeout_seconds=float(os.environ.get("INTENT_SERVICE_TIMEOUT_SECONDS", "60")),
        max_attempts=max(1, min(8, int(os.environ.get("INTENT_SERVICE_MAX_ATTEMPTS", "3")))),
        backoff_ms=max(10, min(60000, int(os.environ.get("INTENT_SERVICE_BACKOFF_MS", "500")))),
    )


def _chat_completions_url(base_url: str) -> str:
    base_url = base_url.strip().rstrip("/")
    if base_url.endswith("/chat/completions"):
        return base_url
    return base_url + "/chat/completions"


def _extract_json_object(raw: str) -> str:
    raw = raw.strip()
    fence = chr(96) * 3
    if raw.startswith(fence):
        lines = raw.splitlines()
        raw = chr(10).join(lines[1:]) if len(lines) > 1 else raw
        raw = raw.rsplit(fence, 1)[0]
    start = raw.find("{")
    end = raw.rfind("}")
    if start >= 0 and end > start:
        return raw[start : end + 1]
    return raw


def _post_chat(config: LLMConfig, payload: dict[str, Any]) -> str:
    request = urllib.request.Request(
        _chat_completions_url(config.base_url),
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer " + config.api_key,
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=config.timeout_seconds) as response:
            raw = response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")[:500]
        raise RuntimeError(f"LLM HTTP {exc.code}: {body}") from exc
    except (urllib.error.URLError, TimeoutError) as exc:
        raise RuntimeError(f"LLM request failed: {exc}") from exc

    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("LLM returned non-JSON response") from exc
    choices = parsed.get("choices") or []
    if not choices:
        raise RuntimeError("LLM returned no choices")
    message = choices[0].get("message") or {}
    content = message.get("content")
    if not isinstance(content, str) or not content.strip():
        raise RuntimeError("LLM returned empty content")
    return content


def _parse_intent(content: str, model: str, latency_ms: int, attempts: int) -> IntentResponse:
    try:
        payload = json.loads(_extract_json_object(content))
    except json.JSONDecodeError as exc:
        raise RuntimeError("LLM returned invalid intent JSON") from exc
    intent_type = str(payload.get("intent_type", "")).strip()
    if intent_type not in ALLOWED_INTENTS:
        raise RuntimeError(f"unsupported intent_type {intent_type!r}")
    try:
        confidence = float(payload.get("confidence", 0.8))
    except (TypeError, ValueError):
        confidence = 0.8
    confidence = max(0.0, min(1.0, confidence))
    entities = payload.get("entities")
    if not isinstance(entities, dict):
        entities = {}
    reasoning = payload.get("reasoning")
    if not isinstance(reasoning, str):
        reasoning = ""
    return IntentResponse(
        intent_type=intent_type,
        confidence=confidence,
        entities=entities,
        reasoning=reasoning or None,
        model=model,
        latency_ms=latency_ms,
        attempts=attempts,
    )


def classify_intent(text: str, context: str | None = None) -> IntentResponse:
    config = load_llm_config()
    if not config.api_key:
        raise HTTPException(status_code=503, detail="OPENAI_API_KEY is not configured")

    system_prompt = """你是科研任务意图分类器。只返回严格 JSON，不要 Markdown。
JSON 必须包含 intent_type、confidence、entities、reasoning。
intent_type 只能是以下之一：General, Code_Execution, Paper_Reproduction, Framework_Evaluation, AutoResearch, Custom_Benchmark, Literature_Review, Data_Analysis。
判断规则：
1. 纯绘图/画图/对比图请求，如果没有明确框架名称或框架对比/benchmark 语义，intent_type=Code_Execution，entities.needs_plot=true，output_mode=plot。
2. Framework_Evaluation 只适用于下列框架白名单中的真实框架之间的对比/选型/benchmark：langchain, llamaindex, haystack, dspy, autogen, crewai, langgraph。只有同时出现白名单框架名 + benchmark/对比/选型语义时才判 Framework_Evaluation；如果还要求图表，entities.needs_plot=true，output_mode=plot。
   重要：比较两个函数、算法、标准库（例如 bisect vs sorted、二分法 vs 摩尔排序、numpy vs pandas 的某个函数）属于 Code_Execution，不是 Framework_Evaluation；这些名称绝不能放进 entities.frameworks。
3. 明确复现论文、论文仓库或论文实验，intent_type=Paper_Reproduction。
4. 明确 AutoResearch/自动实验，intent_type=AutoResearch。
5. 其他总结、解释、知识问答，intent_type=General。
6. entities.frameworks 只能包含白名单框架名；没有白名单框架时省略该字段。
entities 是字符串键值对象，可提取 frameworks（字符串数组）、framework_count、needs_plot（布尔）、needs_benchmark（布尔）、output_mode（plot/report）、paper_title、repo_url、topic、needs_report 等。
示例：
用户：帮我画一个正弦函数和余弦函数的对比图
输出：{"intent_type":"Code_Execution","confidence":0.95,"entities":{"needs_plot":true,"output_mode":"plot"},"reasoning":"纯绘图请求，没有框架对比语义"}
用户：对比 LangChain 和 LlamaIndex 的响应时间柱状图
输出：{"intent_type":"Framework_Evaluation","confidence":0.93,"entities":{"frameworks":["langchain","llamaindex"],"needs_plot":true,"needs_benchmark":true,"output_mode":"plot"},"reasoning":"框架性能对比并要求图表"}
"""
    user_prompt = "用户请求：" + chr(10) + text
    if context:
        user_prompt += chr(10) + chr(10) + "补充上下文：" + chr(10) + context

    payload = {
        "model": config.model,
        "temperature": 0.0,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_prompt},
        ],
    }
    started = time.monotonic()
    last_error: Exception | None = None
    for attempt in range(1, config.max_attempts + 1):
        try:
            content = _post_chat(config, payload)
            return _parse_intent(content, config.model, int((time.monotonic() - started) * 1000), attempt)
        except Exception as exc:
            last_error = exc
            if attempt >= config.max_attempts:
                break
            time.sleep((config.backoff_ms / 1000.0) * (2 ** (attempt - 1)))
    raise HTTPException(status_code=502, detail=f"intent recognition failed: {last_error}")


@app.post("/predict", response_model=IntentResponse)
def predict_intent(request: IntentRequest) -> IntentResponse:
    return classify_intent(request.text, request.context)


@app.get("/health")
def health_check() -> dict[str, Any]:
    config = load_llm_config()
    configured = bool(config.api_key)
    return {
        "status": "ok" if configured else "degraded",
        "ok": configured,
        "service": "intent-recognition",
        "provider": "openai-compatible",
        "base_url": config.base_url,
        "model": config.model,
        "configured": configured,
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="0.0.0.0", port=int(os.environ.get("INTENT_SERVICE_PORT", "8000")))
