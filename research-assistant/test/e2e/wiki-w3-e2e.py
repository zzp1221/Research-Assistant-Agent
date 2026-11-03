#!/usr/bin/env python3
"""W3 E2E: automatic LLM TLDR + link adjudication against the real provider."""
import json
import os
import shutil
import signal
import subprocess
import time
import urllib.error
import urllib.request

ROOT = "/tmp/wiki-w3"
BACKEND_BIN = os.environ.get("BACKEND_BIN", "/tmp/e2e/ra-backend")


def request(base, method, path, payload=None, timeout=240):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "w3-user")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            body = response.read().decode("utf-8")
            return response.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(body)
        except Exception:
            return exc.code, {"error": body}


def wait_health(base, timeout=120):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(base + "/api/health", timeout=3) as response:
                if response.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(1)
    return False


def load_key():
    key = os.environ.get("REAL_LLM_API_KEY", "").strip()
    if key:
        return key
    for line in open("/src/research-assistant/backend.env", encoding="utf-8"):
        line = line.strip()
        if line.startswith("OPENAI_API_KEY="):
            return line.split("=", 1)[1].strip()
    raise AssertionError("missing OPENAI_API_KEY")


def env_for(port, mode):
    env = dict(os.environ)
    env.update({
        "APP_ADDR": f":{port}",
        "APP_DATA_DIR": os.path.join(ROOT, f"data-{port}"),
        "PLAN_STORE_PATH": os.path.join(ROOT, f"data-{port}", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, f"data-{port}", "memory"),
        "WIKI_DIR": os.path.join(ROOT, f"data-{port}", "wiki"),
        "MEMORY_ENABLED": "true", "MEMORY_MODE": "on",
        "WIKI_ENABLED": "true", "WIKI_MODE": "on", "WIKI_LLM_MODE": mode,
        "OPENAI_BASE_URL": os.environ.get("REAL_LLM_BASE_URL", "https://api.ai-pixel.online/v1"),
        "OPENAI_API_KEY": load_key(),
        "OPENAI_MODEL_NAME": os.environ.get("REAL_LLM_MODEL", "deepseek-v4.1-flash"),
        "ALLOW_LEGACY_USER_HEADER": "true", "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "", "GIN_MODE": "release",
    })
    return env


def start(port, mode):
    os.makedirs(os.path.join(ROOT, f"data-{port}"), exist_ok=True)
    log = open(os.path.join(ROOT, f"backend-{port}.log"), "w", encoding="utf-8")
    process = subprocess.Popen([BACKEND_BIN], env=env_for(port, mode), stdout=log, stderr=subprocess.STDOUT)
    base = f"http://127.0.0.1:{port}"
    if not wait_health(base):
        process.kill()
        raise AssertionError(f"backend {port} not healthy")
    return process, log, base


def stop(process, log):
    try:
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=20)
    except Exception:
        process.kill()
    log.close()


def main():
    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(ROOT, exist_ok=True)

    process, log, base = start(18086, "all")
    try:
        status, created = request(base, "POST", "/api/wiki/papers", {
            "title": "Attention Is All You Need", "year": 2017, "arxiv_id": "1706.03762",
            "abstract": "The Transformer replaces recurrent layers with multi-head self-attention. It reaches 27.3 BLEU on WMT14 English-German translation and trains faster than RNN baselines.",
        })
        if status != 201:
            raise AssertionError(f"create paper failed: {status} {created}")
        paper = created["paper"]
        if not paper.get("tldr") or not paper.get("tldr_inferred"):
            raise AssertionError(f"automatic TLDR missing: {paper}")
        print("tldr:", paper["tldr"][:160].replace("\n", " "))

        status, memories = request(base, "GET", "/api/memories?q=TLDR")
        if status != 200 or memories.get("total", 0) < 1:
            raise AssertionError(f"tldr memory record missing: {memories}")

        # A second paper with a similar abstract exercises LLM link adjudication.
        status, created_b = request(base, "POST", "/api/wiki/papers", {
            "title": "BERT: Pre-training of Deep Bidirectional Transformers", "year": 2019, "arxiv_id": "1810.04805",
            "abstract": "BERT pre-trains deep bidirectional transformer encoders with self-attention. It improves natural language understanding benchmarks and uses large unlabeled text corpora.",
        })
        if status != 201:
            raise AssertionError(f"create paper B failed: {status} {created_b}")
        status, job = request(base, "POST", "/api/wiki/build", {"mode": "full"})
        if status != 202:
            raise AssertionError(f"build failed: {status} {job}")
        deadline = time.time() + 180
        build_job = {}
        while time.time() < deadline:
            status, build_job = request(base, "GET", f"/api/wiki/build/{job['job_id']}")
            if status == 200 and build_job.get("status") in ("completed", "failed"):
                break
            time.sleep(0.5)
        if build_job.get("status") != "completed":
            raise AssertionError(f"build job failed: {build_job}")
        status, links = request(base, "GET", "/api/wiki/links?status=pending_review")
        if status != 200:
            raise AssertionError(f"list links failed: {status} {links}")
        llm_links = [item for item in links.get("links", []) if item.get("source") == "llm"]
        if not llm_links:
            raise AssertionError(f"expected LLM-adjudicated link, got: {links}")
        print("llm_link:", llm_links[0]["type"], llm_links[0]["status"], llm_links[0]["confidence"])
        print("PASS w3_auto_tldr_and_link_adjudication")
    finally:
        stop(process, log)

    # WIKI_LLM_MODE=off must not generate a TLDR.
    process, log, base = start(18087, "off")
    try:
        status, created = request(base, "POST", "/api/wiki/papers", {
            "title": "Attention Is All You Need", "year": 2017, "arxiv_id": "1706.03762",
            "abstract": "The Transformer replaces recurrent layers with multi-head self-attention.",
        })
        if status != 201:
            raise AssertionError(f"off-mode create failed: {status} {created}")
        if created["paper"].get("tldr"):
            raise AssertionError(f"off mode must not generate TLDR: {created['paper']}")
        print("PASS w3_llm_mode_off")
    finally:
        stop(process, log)


if __name__ == "__main__":
    main()
