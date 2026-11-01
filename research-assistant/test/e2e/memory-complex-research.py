#!/usr/bin/env python3
"""Complex research conversation + long task E2E against the real LLM."""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18084"
ROOT = "/workspace/e2e-complex"
CHAT_SESSION = "research-chat"
PLAN_SESSION = "research-plan"


def request(method, path, payload=None, timeout=180):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "research-user")
    req.add_header("X-Session-Id", PLAN_SESSION)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        return exc.code, {"error": exc.read().decode("utf-8", "replace")}


def wait_health(timeout=120):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/api/health", timeout=3) as response:
                if response.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(1)
    return False


def chat(message):
    status, payload = request("POST", "/api/chat", {"message": message, "session_id": CHAT_SESSION})
    if status != 200:
        raise AssertionError(f"chat failed status={status} body={payload}")
    return payload


def require_contains(text, needles, label):
    lower = text.lower()
    missing = [needle for needle in needles if needle.lower() not in lower]
    if missing:
        raise AssertionError(f"{label} missing {missing}: {text[:400]}")


def main():
    key = os.environ.get("REAL_LLM_API_KEY", "").strip()
    base = os.environ.get("REAL_LLM_BASE_URL", "https://api.ai-pixel.online/v1").strip()
    model = os.environ.get("REAL_LLM_MODEL", "deepseek-v4.1-flash").strip()
    if not key:
        raise AssertionError("REAL_LLM_API_KEY is required")
    os.makedirs(ROOT, exist_ok=True)
    shutil.rmtree(os.path.join(ROOT, "data"), ignore_errors=True)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": ":18084",
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "MEMORY_ENABLED": "true",
        "MEMORY_MODE": "on",
        "OPENAI_BASE_URL": base,
        "OPENAI_API_KEY": key,
        "OPENAI_MODEL_NAME": model,
        "ALLOW_LEGACY_USER_HEADER": "true",
        "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "",
        "PROMPT_CONTEXT_V2": "true",
        "BENCHMARK_PRIVATE_ROOT": os.path.join(ROOT, "private"),
        "GIN_MODE": "release",
    })
    log = open(os.path.join(ROOT, "backend.log"), "w", encoding="utf-8")
    backend = subprocess.Popen([os.path.join(ROOT, "ra-backend")], env=env, stdout=log, stderr=subprocess.STDOUT)
    try:
        if not wait_health():
            raise AssertionError("backend did not become healthy")
        status, created = request("POST", "/api/memories", {
            "kind": "profile", "title": "Attention 复现项目上下文",
            "content": "项目：Attention Is All You Need 复现；论文 arXiv:1706.03762；基线 WMT14 EN-DE BLEU 27.3；环境 Python 3.11 + PyTorch 2.4 + A100 40GB；上次失败原因 torch 版本冲突，修复方式固定 torch==2.4.0。",
            "pinned": True, "importance": 10,
        })
        if status not in (200, 201):
            raise AssertionError(f"create memory failed: {status} {created}")

        first = chat("我要复现 Attention Is All You Need，论文 arXiv:1706.03762，基线 WMT14 EN-DE BLEU 27.3。请确认你记住了这些信息。")
        chat("我的环境是 Python 3.11、PyTorch 2.4、A100 40GB。请记住这个环境。")
        chat("上次复现失败是 torch 版本冲突，后来固定 torch==2.4.0 才成功。请记住这个失败经验。")
        for index in range(4, 27):
            chat(f"这是研究对话第{index}轮：请基于上面的论文、环境和失败经验，继续给我一个简短的复现检查建议，不要编造实验数据。")
        env_answer = chat("我的复现环境是什么？请列出 Python、PyTorch、GPU。")
        print("env_answer:", env_answer.get("response", "")[:160].replace("\n", " "))
        require_contains(env_answer.get("response", ""), ["3.11", "2.4", "A100"], "environment recall")
        fail_answer = chat("上次复现失败的原因和修复方式是什么？请直接回答。")
        print("fail_answer:", fail_answer.get("response", "")[:160].replace("\n", " "))
        require_contains(fail_answer.get("response", ""), ["torch", "2.4.0"], "failure recall")
        baseline_answer = chat("论文的基线指标是多少？请给出指标名和数值。")
        print("baseline_answer:", baseline_answer.get("response", "")[:160].replace("\n", " "))
        require_contains(baseline_answer.get("response", ""), ["27.3"], "baseline recall")
        arxiv_answer = chat("论文的 arXiv 编号是多少？请直接回答编号。")
        print("arxiv_answer:", arxiv_answer.get("response", "")[:120].replace("\n", " "))
        require_contains(arxiv_answer.get("response", ""), ["1706.03762"], "arxiv recall")
        if not env_answer.get("citations"):
            raise AssertionError("expected memory citations on environment answer")

        status, stats = request("GET", "/api/memories/stats")
        if status != 200 or stats.get("total", 0) < 1:
            raise AssertionError(f"memory stats failed: {status} {stats}")
        status, search = request("POST", "/api/memories/search", {"query": "torch 2.4.0"})
        if status != 200 or not search.get("results"):
            raise AssertionError(f"memory search failed: {status} {search}")
        print(f"PASS real_llm_complex_chat turns=30 memory_total={stats.get('total')} search_hits={len(search.get('results', []))}")

        status, plan_a = request("POST", "/api/plan", {
            "intent": "复现 Attention Is All You Need 论文，使用 Python 3.11 + PyTorch 2.4 + A100 40GB，基线 WMT14 EN-DE BLEU 27.3；请生成完整的论文复现计划。",
            "session_id": PLAN_SESSION,
        }, timeout=300)
        if status != 200:
            raise AssertionError(f"plan A failed: {status} {plan_a}")
        graph_a = plan_a.get("plan_graph", {})
        plan_a_id = graph_a.get("id")
        if not plan_a_id:
            raise AssertionError(f"plan A missing id: {plan_a}")
        status, plan_b = request("POST", "/api/plan", {
            "intent": "继续，基于上面的复现计划再详细一点，补充失败恢复步骤。",
            "session_id": PLAN_SESSION,
        }, timeout=300)
        if status != 200:
            raise AssertionError(f"plan B failed: {status} {plan_b}")
        graph_b = plan_b.get("plan_graph", {})
        if graph_b.get("parent_plan_id") != plan_a_id:
            raise AssertionError(f"parent plan mismatch: {graph_b.get('parent_plan_id')} != {plan_a_id}")
        context_b = graph_b.get("context") or {}
        memory_context = context_b.get("memory_context") or ""
        require_contains(memory_context, ["27.3"], "plan memory context")
        require_contains(context_b.get("parent_plan_summary") or "", ["复现"], "parent summary")
        print(f"PASS real_llm_plan plan_a={plan_a_id} parent={graph_b.get('parent_plan_id')} nodes_a={len(graph_a.get('nodes') or [])} nodes_b={len(graph_b.get('nodes') or [])} memory_context_len={len(memory_context)}")
        return 0
    finally:
        try:
            backend.send_signal(signal.SIGTERM)
            backend.wait(timeout=15)
        except Exception:
            try:
                backend.kill()
            except Exception:
                pass
        log.close()


if __name__ == "__main__":
    sys.exit(main())
