#!/usr/bin/env python3
"""Shared harness for the memory stress suites.

Everything runs inside a Linux container: a backend binary is started per
suite (or per destructive case) with an isolated APP_DATA_DIR and a
deterministic OpenAI-compatible mock provider. Every HTTP exchange and the
full mock-captured prompt is written to the evidence directory of the case.
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

DEFAULT_BACKEND = os.environ.get("STRESS_BACKEND_BIN", "/tmp/ra-backend")
MOCK_SERVER = os.environ.get("STRESS_MOCK_SERVER", os.path.join(os.path.dirname(os.path.abspath(__file__)), "mock_server.py"))


def now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%S%z", time.localtime())


def pick_port(start=18200):
    """Pick a free TCP port in the container (best effort)."""
    import socket
    for port in range(start, start + 500):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind(("127.0.0.1", port))
                return port
            except OSError:
                continue
    raise RuntimeError("no free port")


class Api:
    def __init__(self, backend, case=None):
        self.backend = backend
        self.case = case

    def request(self, method, path, payload=None, user="u1", headers=None, timeout=120, raw=False):
        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        req = urllib.request.Request(self.backend.base + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        req.add_header("X-User-Id", user)
        for key, value in (headers or {}).items():
            req.add_header(key, value)
        started = time.time()
        if self.case is not None:
            self.case.record_request(method, path, payload, user)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                body = resp.read()
                status = resp.status
                resp_headers = dict(resp.headers)
        except urllib.error.HTTPError as exc:
            body = exc.read()
            status = exc.code
            resp_headers = dict(exc.headers or {})
        latency_ms = round((time.time() - started) * 1000, 2)
        if raw:
            parsed = body
        else:
            try:
                parsed = json.loads(body.decode("utf-8")) if body else {}
            except Exception:
                parsed = {"raw": body.decode("utf-8", "replace")}
        if self.case is not None:
            self.case.record_response(method, path, status, parsed if not raw else "<binary>", latency_ms)
        return status, parsed

    def get(self, path, **kwargs):
        return self.request("GET", path, **kwargs)

    def post(self, path, payload=None, **kwargs):
        return self.request("POST", path, payload, **kwargs)

    def put(self, path, payload=None, **kwargs):
        return self.request("PUT", path, payload, **kwargs)

    def patch(self, path, payload=None, **kwargs):
        return self.request("PATCH", path, payload, **kwargs)

    def delete(self, path, **kwargs):
        return self.request("DELETE", path, **kwargs)

    def upload(self, path, filename, content, user="u1", timeout=180):
        boundary = "----stress" + uuid.uuid4().hex
        body = b""
        body += ("--" + boundary + "\r\n").encode()
        body += ('Content-Disposition: form-data; name="file"; filename="' + filename + '"\r\n').encode()
        body += b"Content-Type: application/pdf\r\n\r\n"
        body += content + b"\r\n"
        body += ("--" + boundary + "--\r\n").encode()
        req = urllib.request.Request(self.backend.base + path, data=body, method="POST")
        req.add_header("Content-Type", "multipart/form-data; boundary=" + boundary)
        req.add_header("X-User-Id", user)
        started = time.time()
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                payload = json.loads(resp.read().decode("utf-8"))
                status = resp.status
        except urllib.error.HTTPError as exc:
            status = exc.code
            try:
                payload = json.loads(exc.read().decode("utf-8"))
            except Exception:
                payload = {"error": "upload failed"}
        if self.case is not None:
            self.case.record_request("POST", path, {"filename": filename, "bytes": len(content)}, user)
            self.case.record_response("POST", path, status, payload, round((time.time() - started) * 1000, 2))
        return status, payload


class Backend:
    """One backend + one deterministic mock, isolated data directory."""

    def __init__(self, root, name="suite", env=None, mock_mode="recall", start_mock=True, base_port=None):
        self.root = root
        self.name = name
        self.dir = os.path.join(root, name)
        self.data = os.path.join(self.dir, "data")
        self.port = base_port or pick_port()
        self.base = "http://127.0.0.1:%d" % self.port
        self.mock_port = pick_port(self.port + 1)
        self.mock_log = os.path.join(self.dir, "mock_requests.jsonl")
        self.backend_log = os.path.join(self.dir, "backend.log")
        self.env_extra = dict(env or {})
        self.mock_mode = mock_mode
        self.start_mock = start_mock
        self.proc = None
        self.mock = None
        self.log_handle = None
        os.makedirs(self.data, exist_ok=True)

    def base_env(self):
        env = dict(os.environ)
        env.update({
            "APP_ADDR": ":%d" % self.port,
            "APP_DATA_DIR": self.data,
            "PLAN_STORE_PATH": os.path.join(self.data, "plans.json"),
            "MEMORY_DIR": os.path.join(self.data, "memory"),
            "WIKI_DIR": os.path.join(self.data, "wiki"),
            "MEMORY_ENABLED": "true",
            "MEMORY_MODE": "on",
            "WIKI_ENABLED": "true",
            "WIKI_MODE": "on",
            "WIKI_LLM_MODE": "off",
            "WIKI_PDF_REFS": "true",
            "ALLOW_LEGACY_USER_HEADER": "true",
            "REQUIRE_API_AUTH": "false",
            "API_AUTH_TOKEN": "",
            "GIN_MODE": "release",
            "PROMPT_CONTEXT_V2": "true",
            "PROMPT_CONTEXT_WINDOW": "128000",
            "PROMPT_MAX_OUTPUT_TOKENS": "8192",
            "PROMPT_SAFETY_TOKENS": "2000",
            "BENCHMARK_PRIVATE_ROOT": os.path.join(self.dir, "private"),
            "OPENAI_BASE_URL": "http://127.0.0.1:%d/v1" % self.mock_port,
            "OPENAI_API_KEY": "stress-key",
            "OPENAI_MODEL_NAME": "mock-model",
            "MOCK_LOG": self.mock_log,
            "MOCK_MODE": self.mock_mode,
            "PLAN_MAX_ACTIVE_PER_USER": "500",
            "PLAN_RATE_LIMIT_PER_MINUTE": "6000",
            "PLAN_RATE_LIMIT_BURST": "1000",
            "PLAN_MAX_CONCURRENT_TASKS": "4",
            "MODEL_PROFILE_ENCRYPTION_KEY": "cmVzZWFyY2gtYXNzaXN0YW50LW1vZGVsLXByb2ZpbGUta2V5LTE=",
        })
        env.update(self.env_extra)
        return env

    def start(self, timeout=90):
        if self.start_mock:
            mock_env = self.base_env()
            self.mock = subprocess.Popen([sys.executable, MOCK_SERVER, str(self.mock_port)], env=mock_env,
                                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.log_handle = open(self.backend_log, "a", encoding="utf-8")
        self.proc = subprocess.Popen([DEFAULT_BACKEND], env=self.base_env(),
                                     stdout=self.log_handle, stderr=subprocess.STDOUT)
        if not self.wait_health(timeout):
            raise RuntimeError("backend %s did not become healthy; log: %s" % (self.name, self.read_log_tail()))
        return self

    def wait_health(self, timeout=90):
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                with urllib.request.urlopen(self.base + "/api/health", timeout=2) as resp:
                    if resp.status == 200:
                        return True
            except Exception:
                pass
            if self.proc is not None and self.proc.poll() is not None:
                return False
            time.sleep(0.15)
        return False

    def read_log_tail(self, size=8000):
        try:
            with open(self.backend_log, "rb") as handle:
                data = handle.read()
            return data[-size:].decode("utf-8", "replace")
        except Exception:
            return ""

    def stop(self, sig=signal.SIGTERM, timeout=15):
        if self.proc is not None and self.proc.poll() is None:
            try:
                self.proc.send_signal(sig)
                self.proc.wait(timeout=timeout)
            except Exception:
                try:
                    self.proc.kill()
                except Exception:
                    pass
        if self.mock is not None and self.mock.poll() is None:
            try:
                self.mock.terminate()
                self.mock.wait(timeout=5)
            except Exception:
                try:
                    self.mock.kill()
                except Exception:
                    pass
        if self.log_handle is not None:
            try:
                self.log_handle.close()
            except Exception:
                pass
            self.log_handle = None

    def kill9(self):
        if self.proc is not None and self.proc.poll() is None:
            self.proc.kill()
            try:
                self.proc.wait(timeout=10)
            except Exception:
                pass

    def restart(self, timeout=90):
        self.stop(signal.SIGTERM)
        return self.start(timeout=timeout)

    def mock_prompts(self):
        entries = []
        try:
            with open(self.mock_log, encoding="utf-8") as handle:
                for line in handle:
                    line = line.strip()
                    if line:
                        entries.append(json.loads(line))
        except FileNotFoundError:
            pass
        return entries

    def prompt_mark(self):
        return len(self.mock_prompts())


class Case:
    """One test case: evidence directory + assertions + verdict."""

    def __init__(self, case_id, suite, priority, title, run_id, evidence_root):
        self.case_id = case_id
        self.suite = suite
        self.priority = priority
        self.title = title
        self.run_id = run_id
        self.dir = os.path.join(evidence_root, case_id)
        os.makedirs(self.dir, exist_ok=True)
        self.started_at = now_iso()
        self.assertions = []
        self.notes = []
        self.metrics = {}
        self.status = "pass"
        self.errors = []
        self._req = open(os.path.join(self.dir, "requests.jsonl"), "w", encoding="utf-8")
        self._resp = open(os.path.join(self.dir, "responses.jsonl"), "w", encoding="utf-8")

    def record_request(self, method, path, payload, user):
        summary = payload
        if isinstance(payload, dict) and isinstance(payload.get("records"), list):
            records = payload["records"]
            summary = {"version": payload.get("version"), "records": len(records),
                       "sample": records[0] if records else None}
        encoded = json.dumps(summary, ensure_ascii=False)
        if len(encoded) > 2000:
            summary = {"truncated": True, "chars": len(encoded), "preview": encoded[:2000]}
        self._req.write(json.dumps({"ts": time.time(), "method": method, "path": path,
                                    "user": user, "payload": summary}, ensure_ascii=False) + "\n")
        self._req.flush()

    def record_response(self, method, path, status, body, latency_ms):
        self._resp.write(json.dumps({"ts": time.time(), "method": method, "path": path, "status": status,
                                     "latency_ms": latency_ms, "body": body}, ensure_ascii=False) + "\n")
        self._resp.flush()

    def check(self, name, condition, expected=True, actual=None, evidence=None):
        ok = bool(condition)

        def plain(value):
            if isinstance(value, set):
                return sorted(value, key=str)
            if isinstance(value, dict):
                return {str(k): plain(v) for k, v in value.items()}
            if isinstance(value, (list, tuple)):
                return [plain(item) for item in value]
            return value

        self.assertions.append({"name": name, "expected": plain(expected), "actual": plain(actual) if actual is not None else condition,
                                "pass": ok, "evidence": evidence})
        if not ok:
            self.status = "fail"
        return ok

    def fail(self, message, error_type="E1"):
        self.status = "fail"
        if error_type not in self.errors:
            self.errors.append(error_type)
        self.notes.append(message)
        self.assertions.append({"name": "exception", "expected": "no error", "actual": message,
                                "pass": False, "error_type": error_type})
        return False

    def blocked(self, message):
        self.status = "blocked"
        self.notes.append(message)

    def info(self, message):
        self.notes.append(message)

    def metric(self, name, value):
        self.metrics[name] = value

    def finish(self, api=None, backend=None, prompts=None):
        if api is not None:
            try:
                status, export = api.get("/api/memories/export", timeout=60)
                if status == 200:
                    with open(os.path.join(self.dir, "memory_export.json"), "w", encoding="utf-8") as handle:
                        json.dump(export, handle, ensure_ascii=False, indent=2)
            except Exception:
                pass
            try:
                status, stats = api.get("/api/wiki/stats", timeout=30)
                if status == 200:
                    with open(os.path.join(self.dir, "wiki_stats.json"), "w", encoding="utf-8") as handle:
                        json.dump(stats, handle, ensure_ascii=False, indent=2)
            except Exception:
                pass
            try:
                status, papers = api.get("/api/wiki/papers?limit=200", timeout=30)
                if status == 200:
                    with open(os.path.join(self.dir, "wiki_papers.json"), "w", encoding="utf-8") as handle:
                        json.dump(papers, handle, ensure_ascii=False, indent=2)
            except Exception:
                pass
        if backend is not None:
            with open(os.path.join(self.dir, "backend.log"), "w", encoding="utf-8") as handle:
                handle.write(backend.read_log_tail(200000))
        if prompts:
            with open(os.path.join(self.dir, "prompts.jsonl"), "w", encoding="utf-8") as handle:
                for entry in prompts:
                    handle.write(json.dumps(entry, ensure_ascii=False) + "\n")
        timedout = False
        for item in self.assertions:
            if item.get("error_type") == "TIMEOUT":
                timedout = True
        if self.status == "pass" and not self.assertions:
            self.status = "not_executed"
        verdict = {
            "case_id": self.case_id, "suite": self.suite, "run_id": self.run_id,
            "priority": self.priority, "title": self.title,
            "started_at": self.started_at, "finished_at": now_iso(),
            "status": self.status, "error_types": self.errors,
            "assertions": self.assertions, "metrics": self.metrics, "notes": "\n".join(self.notes),
        }
        with open(os.path.join(self.dir, "verdict.json"), "w", encoding="utf-8") as handle:
            json.dump(verdict, handle, ensure_ascii=False, indent=2)
        with open(os.path.join(self.dir, "case.json"), "w", encoding="utf-8") as handle:
            json.dump({"case_id": self.case_id, "suite": self.suite, "priority": self.priority,
                       "title": self.title, "run_id": self.run_id}, handle, ensure_ascii=False, indent=2)
        self._req.close()
        self._resp.close()
        return verdict


def load_needles(path):
    needles = []
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                needles.append(json.loads(line))
    return needles


def est_tokens(text):
    return max(1, len(text) // 4)


def write_json(path, payload):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, ensure_ascii=False, indent=2)


def read_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def poll(builder_fn, timeout=120, interval=0.4):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = builder_fn()
        if last is not None:
            return last
        time.sleep(interval)
    return last


def new_owner(prefix, case_id):
    return ("stress-%s-%s-%s" % (prefix, case_id, uuid.uuid4().hex[:6])).lower()
