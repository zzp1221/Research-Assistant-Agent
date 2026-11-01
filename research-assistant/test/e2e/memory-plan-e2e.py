#!/usr/bin/env python3
"""Plan memory E2E: verifies plan episode memory and follow-up parent linkage."""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18082"
ROOT = "/workspace/e2e-plan"


def request(method, path, payload=None):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "u1")
    req.add_header("X-Session-Id", "s-plan")
    try:
        with urllib.request.urlopen(req, timeout=90) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        return exc.code, {"error": exc.read().decode("utf-8", "replace")}


def wait_health(timeout=90):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/api/health", timeout=3) as response:
                if response.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def main():
    os.makedirs(ROOT, exist_ok=True)
    shutil.rmtree(os.path.join(ROOT, "data"), ignore_errors=True)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": ":18082",
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "MEMORY_ENABLED": "true",
        "MEMORY_MODE": "on",
        "OPENAI_BASE_URL": "http://127.0.0.1:9098/v1",
        "OPENAI_API_KEY": "test-key",
        "OPENAI_MODEL_NAME": "mock-model",
        "ALLOW_LEGACY_USER_HEADER": "true",
        "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "",
        "PROMPT_CONTEXT_V2": "true",
        "BENCHMARK_PRIVATE_ROOT": os.path.join(ROOT, "private"),
        "GIN_MODE": "release",
        "MOCK_CHAT_LOG": os.path.join(ROOT, "requests.jsonl"),
    })
    mock = subprocess.Popen([sys.executable, "/workspace/research-assistant/test/mock/mock_chat_server.py", "9098"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    backend_log = open(os.path.join(ROOT, "backend.log"), "w", encoding="utf-8")
    backend = subprocess.Popen([os.path.join(ROOT, "ra-backend")], env=env, stdout=backend_log, stderr=subprocess.STDOUT)
    try:
        if not wait_health():
            raise AssertionError("backend did not become healthy")
        status, created = request("POST", "/api/memories", {
            "kind": "profile", "title": "用户偏好", "content": "用户喜欢蓝色",
            "pinned": True, "importance": 10,
        })
        if status not in (200, 201):
            raise AssertionError(f"create memory failed: {status} {created}")

        status, plan_a = request("POST", "/api/plan", {"intent": "运行一段 Python 代码并分析结果", "session_id": "s-plan"})
        if status != 200:
            raise AssertionError(f"plan A failed: {status} {plan_a}")
        plan_a_id = plan_a.get("plan_graph", {}).get("id")
        if not plan_a_id:
            raise AssertionError(f"plan A missing id: {plan_a}")
        status, memories = request("GET", "/api/memories?kind=episode&limit=100")
        if status != 200:
            raise AssertionError(f"list memories failed: {status} {memories}")
        ids = [item.get("id") for item in memories.get("memories", [])]
        if "mem-plan-" + plan_a_id not in ids:
            raise AssertionError(f"plan episode memory missing: {ids}")

        status, plan_b = request("POST", "/api/plan", {"intent": "继续，再详细一点", "session_id": "s-plan"})
        if status != 200:
            raise AssertionError(f"plan B failed: {status} {plan_b}")
        graph_b = plan_b.get("plan_graph", {})
        if graph_b.get("parent_plan_id") != plan_a_id:
            raise AssertionError(f"parent plan id mismatch: {graph_b.get('parent_plan_id')} != {plan_a_id}")
        context = graph_b.get("context") or {}
        summary = context.get("parent_plan_summary") or ""
        if "运行一段 Python 代码" not in summary:
            raise AssertionError(f"parent summary missing: {summary}")
        memory_context = context.get("memory_context") or ""
        if "用户喜欢蓝色" not in memory_context:
            raise AssertionError(f"plan memory context missing pinned profile: {memory_context}")
        print(f"PASS plan_memory episode={plan_a_id} parent={graph_b.get('parent_plan_id')} memory_context_len={len(memory_context)}")
        return 0
    finally:
        for process in (backend, mock):
            try:
                process.send_signal(signal.SIGTERM)
                process.wait(timeout=10)
            except Exception:
                try:
                    process.kill()
                except Exception:
                    pass
        backend_log.close()


if __name__ == "__main__":
    sys.exit(main())
