#!/usr/bin/env python3
"""Real-LLM E2E: runs the memory-enabled backend against the configured provider."""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18083"
ROOT = "/workspace/e2e-real"


def request(method, path, payload=None, timeout=120):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "real-user")
    req.add_header("X-Session-Id", "real-session")
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
    status, payload = request("POST", "/api/chat", {"message": message, "session_id": "real-session"})
    if status != 200:
        raise AssertionError(f"chat failed status={status} body={payload}")
    return payload


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
        "APP_ADDR": ":18083",
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
            "kind": "profile", "title": "用户偏好", "content": "用户喜欢蓝色",
            "pinned": True, "importance": 10,
        })
        if status not in (200, 201):
            raise AssertionError(f"create memory failed: {status} {created}")

        first = chat("我叫小明，我喜欢蓝色。请只回复你记住了。")
        print("turn1:", first.get("response", "")[:80].replace("\n", " "))
        second = chat("我刚才说我叫什么名字？请直接回答名字。")
        print("turn2:", second.get("response", "")[:80].replace("\n", " "))
        if "小明" not in second.get("response", ""):
            raise AssertionError(f"multi-turn name recall failed: {second}")
        third = chat("我喜欢什么颜色？请直接回答颜色。")
        print("turn3:", third.get("response", "")[:80].replace("\n", " "))
        if "蓝色" not in third.get("response", ""):
            raise AssertionError(f"core memory color recall failed: {third}")
        if not third.get("citations"):
            raise AssertionError(f"expected citations, got {third}")

        for index in range(1, 13):
            chat(f"这是第{index}条普通消息，内容仅用于测试上下文压缩。请简短回复收到。")
        final = chat("我最开始说我叫什么名字？请直接回答名字。")
        print("final:", final.get("response", "")[:100].replace("\n", " "))
        if "小明" not in final.get("response", ""):
            raise AssertionError(f"compressed-history recall failed: {final}")

        status, stats = request("GET", "/api/memories/stats")
        if status != 200 or stats.get("total", 0) < 1:
            raise AssertionError(f"memory stats failed: {status} {stats}")
        status, search = request("POST", "/api/memories/search", {"query": "蓝色"})
        if status != 200 or not search.get("results"):
            raise AssertionError(f"memory search failed: {status} {search}")
        print(f"PASS real_llm model={model} turns=15 memory_total={stats.get('total')} search_hits={len(search.get('results', []))}")
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
