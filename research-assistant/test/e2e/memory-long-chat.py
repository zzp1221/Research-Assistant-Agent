#!/usr/bin/env python3
"""Long-chat E2E: verifies server history, context compaction and memory injection."""
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:18081"
ROOT = "/workspace/e2e"
LOG = os.path.join(ROOT, "chat_requests.jsonl")


def request(method, path, payload=None, headers=None):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "u1")
    for key, value in (headers or {}).items():
        req.add_header(key, value)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        return exc.code, {"error": body}


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


def chat(message, session_id):
    status, payload = request("POST", "/api/chat", {"message": message, "session_id": session_id})
    if status != 200:
        raise AssertionError(f"chat failed status={status} body={payload}")
    return payload


def main():
    os.makedirs(ROOT, exist_ok=True)
    if os.path.exists(LOG):
        os.remove(LOG)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": ":18081",
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "MEMORY_ENABLED": "true",
        "MEMORY_MODE": "on",
        "MEMORY_CONSOLIDATE_INTERVAL_SECONDS": "0",
        "OPENAI_BASE_URL": "http://127.0.0.1:9099/v1",
        "OPENAI_API_KEY": "test-key",
        "OPENAI_MODEL_NAME": "mock-model",
        "ALLOW_LEGACY_USER_HEADER": "true",
        "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "",
        "PROMPT_CONTEXT_V2": "true",
        "BENCHMARK_PRIVATE_ROOT": os.path.join(ROOT, "private"),
        "GIN_MODE": "release",
        "MOCK_CHAT_LOG": LOG,
    })
    mock = subprocess.Popen([sys.executable, "/workspace/research-assistant/test/mock/mock_chat_server.py", "9099"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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
            raise AssertionError(f"create memory failed status={status} body={created}")

        first = chat("我叫小明，我喜欢蓝色", "s-long")
        if first.get("session_id") != "s-long":
            raise AssertionError(f"session id mismatch: {first}")
        second = chat("我喜欢什么颜色？", "s-long")
        if "蓝色" not in second.get("response", ""):
            raise AssertionError(f"memory answer missing color: {second}")
        if not second.get("citations"):
            raise AssertionError(f"expected citations, got {second}")

        for index in range(1, 31):
            marker = "第30条" if index == 30 else f"第{index}条"
            chat(f"这是第{index}条消息，请记住{marker}。", "s-long")

        status, stats = request("GET", "/api/memories/stats")
        if status != 200 or stats.get("total", 0) < 1:
            raise AssertionError(f"memory stats failed: {status} {stats}")
        status, search = request("POST", "/api/memories/search", {"query": "蓝色"})
        if status != 200 or not search.get("results"):
            raise AssertionError(f"memory search failed: {status} {search}")

        with open(LOG, encoding="utf-8") as handle:
            requests = [json.loads(line) for line in handle if line.strip()]
        if len(requests) != 32:
            raise AssertionError(f"expected 32 model requests, got {len(requests)}")
        second_messages = requests[1]["messages"]
        joined_second = "\n".join(m["content"] for m in second_messages)
        if "我叫小明" not in joined_second:
            raise AssertionError("second request lost the first user turn")
        if "好的，我记住了" not in joined_second:
            raise AssertionError("second request lost the first assistant turn")
        if "[core memory]" not in joined_second or "用户喜欢蓝色" not in joined_second:
            raise AssertionError("second request missing core memory")
        summary_seen = False
        max_messages = 0
        for entry in requests:
            max_messages = max(max_messages, entry["message_count"])
            joined = "\n".join(m["content"] for m in entry["messages"])
            if "[earlier turns summary]" in joined:
                summary_seen = True
            if "[core memory]" not in joined or "用户喜欢蓝色" not in joined:
                raise AssertionError("a request is missing core memory")
        if not summary_seen:
            raise AssertionError("no request contained the compressed history summary")
        if max_messages > 30:
            raise AssertionError(f"history was not bounded: max messages={max_messages}")
        last_joined = "\n".join(m["content"] for m in requests[-1]["messages"])
        if "第30条" not in last_joined:
            raise AssertionError("last request lost the current user message")
        print(f"PASS chat_multi_turn requests={len(requests)} max_messages={max_messages} summary={summary_seen}")
        print(f"PASS memory_stats total={stats.get('total')} search_hits={len(search.get('results', []))}")
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
