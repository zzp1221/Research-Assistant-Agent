#!/usr/bin/env python3
"""Paper Wiki long-chat / long-task E2E against the real LLM.

Covers: 30+ chat turns with context compaction, memory recall, wiki context
injection (TLDR + user note), citations, and two follow-up plans in one session.
"""
import json
import os
import shutil
import signal
import subprocess
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18085"
ROOT = "/tmp/wiki-long-chat"
CHAT_SESSION = "wiki-chat"
PLAN_SESSION = "wiki-plan"


def request(method, path, payload=None, timeout=240, session=None):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "wiki-user")
    if session:
        req.add_header("X-Session-Id", session)
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
    status, payload = request("POST", "/api/chat", {"message": message, "session_id": CHAT_SESSION}, timeout=240)
    if status != 200:
        raise AssertionError(f"chat failed status={status} body={str(payload)[:500]}")
    return payload


def require_contains(text, needles, label):
    lower = (text or "").lower()
    missing = [needle for needle in needles if needle.lower() not in lower]
    if missing:
        raise AssertionError(f"{label} missing {missing}: {(text or '')[:400]}")


def load_key():
    key = os.environ.get("REAL_LLM_API_KEY", "").strip()
    if key:
        return key
    path = "/src/research-assistant/backend.env"
    if os.path.exists(path):
        for line in open(path, encoding="utf-8"):
            line = line.strip()
            if line.startswith("OPENAI_API_KEY="):
                return line.split("=", 1)[1].strip()
    raise AssertionError("REAL_LLM_API_KEY is required")


def main():
    key = load_key()
    base = os.environ.get("REAL_LLM_BASE_URL", "https://api.ai-pixel.online/v1").strip()
    model = os.environ.get("REAL_LLM_MODEL", "deepseek-v4.1-flash").strip()
    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(os.path.join(ROOT, "data"), exist_ok=True)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": ":18085",
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "WIKI_DIR": os.path.join(ROOT, "data", "wiki"),
        "MEMORY_ENABLED": "true",
        "MEMORY_MODE": "on",
        "WIKI_ENABLED": "true",
        "WIKI_MODE": "on",
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
    backend = subprocess.Popen([os.environ.get("BACKEND_BIN", "/tmp/e2e/ra-backend")], env=env, stdout=log, stderr=subprocess.STDOUT)
    try:
        if not wait_health():
            raise AssertionError("backend did not become healthy")

        # Pinned project memory + wiki paper page.
        status, created = request("POST", "/api/memories", {
            "kind": "profile", "title": "Attention 复现项目上下文",
            "content": "项目：Attention Is All You Need 复现；论文 arXiv:1706.03762；基线 WMT14 EN-DE BLEU 27.3；环境 Python 3.11 + PyTorch 2.4 + A100 40GB；上次失败原因 torch 版本冲突，修复方式固定 torch==2.4.0。",
            "pinned": True, "importance": 10,
        })
        if status not in (200, 201):
            raise AssertionError(f"create memory failed: {status} {created}")

        status, paper = request("POST", "/api/wiki/papers", {
            "title": "Attention Is All You Need", "year": 2017,
            "arxiv_id": "1706.03762", "doi": "10.48550/arXiv.1706.03762",
            "authors": ["Ashish Vaswani", "Noam Shazeer"],
            "tldr": "Transformer 用纯注意力替代 RNN/CNN，在 WMT14 EN-DE 达到 27.3 BLEU。",
            "entities": [{"type": "dataset", "value": "wmt14"}],
        })
        if status != 201:
            raise AssertionError(f"create wiki paper failed: {status} {paper}")
        paper_id = paper["paper"]["paper_id"]
        status, note = request("POST", f"/api/wiki/papers/{paper_id}/points", {"text": "复现配置：batch_size=2048，随机种子 seed=42。"})
        if status != 201:
            raise AssertionError(f"create wiki note failed: {status} {note}")

        # 30+ turn conversation with real LLM.
        first = chat("我叫小明，我喜欢蓝色。我正在复现 Attention Is All You Need，请记住这些信息。")
        require_contains(first.get("response", ""), [], "first turn")
        second = chat("我叫什么？喜欢什么颜色？")
        require_contains(second.get("response", ""), ["小明", "蓝色"], "profile recall")
        for index in range(3, 31):
            chat(f"这是第{index}轮研究对话：请基于上面的论文和环境，继续给我一句简短的复现检查建议，不要编造实验数据。")
        final = chat("我最开始说我叫什么？喜欢什么颜色？我正在复现哪篇论文？它的基线 BLEU 是多少？")
        print("final_answer:", final.get("response", "")[:220].replace("\n", " "))
        require_contains(final.get("response", ""), ["小明", "蓝色", "Attention", "27.3"], "post-compaction recall")

        # Wiki-specific facts: TLDR only exists on the PaperPage, batch_size only
        # in a wiki user_note. Both prove wiki context injection after compaction.
        tldr = chat("这篇论文的 TLDR 是什么？请用一句话回答。")
        print("tldr_answer:", tldr.get("response", "")[:200].replace("\n", " "))
        require_contains(tldr.get("response", ""), ["Transformer", "注意力"], "wiki tldr recall")
        if not tldr.get("wiki_citations"):
            raise AssertionError(f"expected wiki citations on tldr answer: {tldr}")
        batch = chat("复现这篇论文时 batch_size 和随机种子分别是多少？请直接回答数值。")
        print("batch_answer:", batch.get("response", "")[:200].replace("\n", " "))
        require_contains(batch.get("response", ""), ["2048", "42"], "wiki user_note recall")

        status, stats = request("GET", "/api/memories/stats")
        if status != 200 or stats.get("total", 0) < 1:
            raise AssertionError(f"memory stats failed: {status} {stats}")
        status, wiki_stats = request("GET", "/api/wiki/stats")
        if status != 200 or wiki_stats.get("papers", 0) < 1 or wiki_stats.get("points", 0) < 1:
            raise AssertionError(f"wiki stats failed: {status} {wiki_stats}")
        print(f"PASS real_llm_wiki_chat turns=32 memory_total={stats.get('total')} wiki_papers={wiki_stats.get('papers')} wiki_points={wiki_stats.get('points')}")

        # Long task: plan A + follow-up plan B in the same session.
        status, plan_a = request("POST", "/api/plan", {
            "intent": "复现 Attention Is All You Need 论文，使用 Python 3.11 + PyTorch 2.4 + A100 40GB，基线 WMT14 EN-DE BLEU 27.3；请生成完整的论文复现计划。",
            "session_id": PLAN_SESSION,
        }, timeout=300, session=PLAN_SESSION)
        if status != 200:
            raise AssertionError(f"plan A failed: {status} {str(plan_a)[:400]}")
        graph_a = plan_a.get("plan_graph", {})
        plan_a_id = graph_a.get("id")
        if not plan_a_id:
            raise AssertionError(f"plan A missing id: {plan_a}")
        status, plan_b = request("POST", "/api/plan", {
            "intent": "继续，基于上面的复现计划再详细一点，补充失败恢复步骤。",
            "session_id": PLAN_SESSION,
        }, timeout=300, session=PLAN_SESSION)
        if status != 200:
            raise AssertionError(f"plan B failed: {status} {str(plan_b)[:400]}")
        graph_b = plan_b.get("plan_graph", {})
        if graph_b.get("parent_plan_id") != plan_a_id:
            raise AssertionError(f"parent plan mismatch: {graph_b.get('parent_plan_id')} != {plan_a_id}")
        context_b = graph_b.get("context") or {}
        memory_context = context_b.get("memory_context") or ""
        require_contains(memory_context, ["27.3"], "plan memory context")
        print(f"PASS real_llm_wiki_plan plan_a={plan_a_id} parent={graph_b.get('parent_plan_id')} nodes_a={len(graph_a.get('nodes') or [])} nodes_b={len(graph_b.get('nodes') or [])} memory_context_len={len(memory_context)}")
        return 0
    finally:
        try:
            backend.send_signal(signal.SIGTERM)
            backend.wait(timeout=20)
        except Exception:
            try:
                backend.kill()
            except Exception:
                pass
        log.close()


if __name__ == "__main__":
    raise SystemExit(main())
