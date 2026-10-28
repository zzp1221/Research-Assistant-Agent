#!/usr/bin/env python3
"""Deterministic OpenAI-compatible mock used by the container E2E suite.

Behaviour is selected by the model name so one server can play every scenario:

  *mock-vision*  - vision capable; probe answers alternate red/blue; verify passes
  *mock-fail*    - vision capable; verify returns verdict=fail
  *mock-garbage* - vision capable; returns non-JSON garbage
  *mock-main*    - text only; image requests are rejected with 400
  anything else  - text only, answers PONG

It never contacts a real provider and sends no user data anywhere.
"""
import base64
import json
import struct
import sys
import threading
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROBE_LOCK = threading.Lock()
PROBE_COUNTER = {"count": 0}


def png_chunk(kind: bytes, payload: bytes) -> bytes:
    return (
        struct.pack(">I", len(payload))
        + kind
        + payload
        + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
    )


def solid_png(red: int, green: int, blue: int, size: int = 64) -> bytes:
    raw = b"".join(b"\x00" + bytes([red, green, blue]) * size for _ in range(size))
    header = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + png_chunk(b"IHDR", header)
        + png_chunk(b"IDAT", zlib.compress(raw))
        + png_chunk(b"IEND", b"")
    )


VERDICTS = {
    "pass": {"verdict": "pass", "confidence": 0.91, "summary": "figure matches the request", "issues": [], "evidence": ["axes and legend readable"]},
    "fail": {"verdict": "fail", "confidence": 0.74, "summary": "figure is missing required labels", "issues": ["x axis has no unit label"], "evidence": ["axis ticks visible but no unit"]},
}


def decide(body: dict) -> tuple[int, dict]:
    model = str(body.get("model", ""))
    messages = body.get("messages") or []
    text_parts = []
    has_image = False
    for message in messages:
        content = message.get("content")
        if isinstance(content, str):
            text_parts.append(content)
            continue
        for part in content or []:
            if part.get("type") == "text":
                text_parts.append(part.get("text", ""))
            if part.get("type") == "image_url":
                has_image = True
    prompt = "\n".join(text_parts)

    if "dominant colour" in prompt and has_image:
        if "mock-main" in model or "mock-text" in model:
            return 400, {"error": {"message": "image_url content is not supported by this model"}}
        with PROBE_LOCK:
            PROBE_COUNTER["count"] += 1
            index = PROBE_COUNTER["count"]
        colour = "red" if index % 2 == 1 else "blue"
        return 200, completion(model, colour)

    lowered = prompt.lower()
    code_markers = ["python", "生成代码", "编写代码", "plot", "绘图", "折线图", "代码"]
    if any(marker in lowered for marker in code_markers) and not has_image:
        return 200, completion(
            model,
            "import matplotlib\n"
            "matplotlib.use('Agg')\n"
            "import matplotlib.pyplot as plt\n"
            "import numpy as np\n"
            "x = np.linspace(0, 2 * np.pi, 200)\n"
            "plt.figure(figsize=(6, 4))\n"
            "plt.plot(x, np.sin(x), label='series a')\n"
            "plt.plot(x, np.cos(x), label='series b')\n"
            "plt.title('Two series')\n"
            "plt.xlabel('x (rad)')\n"
            "plt.ylabel('value')\n"
            "plt.legend()\n"
            "plt.tight_layout()\n"
            "plt.savefig('/workspace/output_plot.png', dpi=120)\n"
            "print('saved output_plot.png')\n",
        )

    if has_image:
        if "mock-main" in model or "mock-text" in model:
            return 400, {"error": {"message": "image_url content is not supported by this model"}}
        if "garbage" in model:
            return 200, completion(model, "This figure looks fine to me, no JSON here.")
        if "fail" in model:
            return 200, completion(model, json.dumps(VERDICTS["fail"]))
        return 200, completion(model, json.dumps(VERDICTS["pass"]))

    return 200, completion(model, "PONG")


def completion(model: str, content: str) -> dict:
    return {
        "id": "chatcmpl-mock",
        "object": "chat.completion",
        "created": 1,
        "model": model,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # noqa: A003 - silence request spam
        return

    def _send(self, status: int, payload: dict) -> None:
        encoded = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self):  # noqa: N802
        if self.path.endswith("/health"):
            self._send(200, {"ok": True})
            return
        self._send(404, {"error": {"message": "not found"}})

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            self._send(400, {"error": {"message": "invalid json"}})
            return
        if "image_url" in json.dumps(body):
            # Touch the probe image encoder so the fixture stays honest.
            solid_png(255, 0, 0)
        status, payload = decide(body)
        self._send(status, payload)


def main() -> None:
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9099
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print("mock vision server on :%d" % port, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()