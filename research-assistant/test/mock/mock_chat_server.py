#!/usr/bin/env python3
"""Deterministic OpenAI-compatible mock for the memory long-chat E2E test."""
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOG_PATH = os.environ.get("MOCK_CHAT_LOG", "/tmp/chat_requests.jsonl")


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
        if self.path == "/health":
            self._send(200, {"ok": True})
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
        record = {
            "ts": time.time(),
            "model": payload.get("model"),
            "message_count": len(messages),
            "messages": [{"role": m.get("role"), "content": m.get("content", "")} for m in messages],
        }
        with open(LOG_PATH, "a", encoding="utf-8") as handle:
            handle.write(json.dumps(record, ensure_ascii=False) + "\n")

        last_user = ""
        for message in reversed(messages):
            if message.get("role") == "user":
                last_user = message.get("content", "")
                break
        joined = "\n".join(str(m.get("content", "")) for m in messages)
        if "颜色" in last_user and "蓝色" in joined:
            content = "你之前说过喜欢蓝色。"
        elif "第30条" in last_user:
            content = "我记住了第30条，并且仍然记得你喜欢的颜色。"
        else:
            content = "好的，我记住了。"

        prompt_chars = sum(len(str(m.get("content", ""))) for m in messages)
        prompt_tokens = max(1, prompt_chars // 4)
        completion_tokens = max(1, len(content) // 4)
        self._send(200, {
            "id": "chatcmpl-mock",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": payload.get("model") or "mock-model",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
            "usage": {
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
                "prompt_tokens_details": {"cached_tokens": 0},
            },
        })


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9099
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"mock chat server listening on {port}", flush=True)
    server.serve_forever()
