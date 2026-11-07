#!/usr/bin/env python3
"""Deterministic OpenAI-compatible mock provider for memory stress tests.

Records every request (full messages) to MOCK_LOG as JSONL so the harness can
assert exactly what was injected into each prompt. The answer rule supports the
"needle" protocol: a user message mentioning "needle <ID>" is answered with the
value captured from [[VALUE:<ID>=...]] markers found anywhere in the prompt.
That makes memory recall measurable without relying on LLM behaviour.
"""
import json
import os
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOG_PATH = os.environ.get("MOCK_LOG", "/tmp/mock_requests.jsonl")
FAIL_SUBSTR = os.environ.get("MOCK_FAIL_SUBSTR", "")
FAIL_FIRST = int(os.environ.get("MOCK_FAIL_FIRST", "0"))
FAIL_WITH_500 = os.environ.get("MOCK_FAIL_WITH_500", "true").lower() == "true"
DELAY_MS = int(os.environ.get("MOCK_DELAY_MS", "0"))
MODE = os.environ.get("MOCK_MODE", "recall")

_lock = threading.Lock()
_counter = {"n": 0}


def record(entry):
    with _lock:
        _counter["n"] += 1
        entry["seq"] = _counter["n"]
        with open(LOG_PATH, "a", encoding="utf-8") as handle:
            handle.write(json.dumps(entry, ensure_ascii=False) + "\n")


def last_user(messages):
    for message in reversed(messages):
        if message.get("role") == "user":
            return str(message.get("content", ""))
    return ""


def answer_for(joined, user_text):
    match = re.search(r"(LT|LP|CP|MB|LC|SC|RB|PF)-[A-Z0-9-]{1,16}", user_text)
    if match and ("needle" in user_text.lower() or "针" in user_text or "VALUE" in user_text.upper()):
        needle_id = match.group(0).upper()
        value = re.search(r"\[\[VALUE:" + re.escape(needle_id) + r"=([^\]]*)\]\]", joined)
        if value:
            return "VALUE:" + value.group(1)
        return "UNKNOWN:" + needle_id
    if "\u6c47\u603b" in user_text or "summar" in user_text.lower():
        return "\u5df2\u5b8c\u6210\u6c47\u603b\u3002"
    return "\u597d\u7684\uff0c\u6211\u5df2\u8bb0\u5f55\u3002"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        pass

    def _send(self, status, payload):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path in ("/health", "/v1/models"):
            self._send(200, {"ok": True, "data": [{"id": "mock-model"}]})
            return
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self._send(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw.decode("utf-8"))
        except Exception:
            self._send(400, {"error": "invalid json"})
            return
        messages = payload.get("messages") or []
        user_text = last_user(messages)
        joined = "\n".join(str(m.get("content", "")) for m in messages)
        record({
            "ts": time.time(),
            "model": payload.get("model"),
            "message_count": len(messages),
            "max_tokens": payload.get("max_tokens"),
            "messages": [{"role": m.get("role"), "content": m.get("content", "")} for m in messages],
        })
        if DELAY_MS > 0:
            time.sleep(DELAY_MS / 1000.0)
        if FAIL_SUBSTR and FAIL_SUBSTR in user_text:
            with _lock:
                _counter.setdefault("failures", 0)
                _counter["failures"] += 1
                failures = _counter["failures"]
            if not FAIL_FIRST or failures <= FAIL_FIRST:
                if FAIL_WITH_500:
                    self._send(500, {"error": {"message": "mock injected failure", "type": "mock_error"}})
                else:
                    self._send(503, {"error": {"message": "mock injected outage", "type": "mock_error"}})
                return
        content = answer_for(joined, user_text) if MODE == "recall" else "OK"
        prompt_chars = sum(len(str(m.get("content", ""))) for m in messages)
        completion_tokens = max(1, len(content) // 4)
        self._send(200, {
            "id": "chatcmpl-mock",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": payload.get("model") or "mock-model",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
            "usage": {
                "prompt_tokens": max(1, prompt_chars // 4),
                "completion_tokens": completion_tokens,
                "total_tokens": max(1, prompt_chars // 4) + completion_tokens,
            },
        })


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9099
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"mock listening on {port}", flush=True)
    server.serve_forever()
