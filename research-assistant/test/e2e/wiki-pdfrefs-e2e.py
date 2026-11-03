#!/usr/bin/env python3
"""W2.5 E2E: PDF references -> cites links, gated by WIKI_PDF_REFS."""
import hashlib
import json
import os
import shutil
import signal
import subprocess
import time
import urllib.error
import urllib.request

ROOT = "/tmp/wiki-pdfrefs"
BACKEND_BIN = os.environ.get("BACKEND_BIN", "/tmp/e2e/ra-backend")


def request(base, method, path, payload=None, timeout=120):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", "u1")
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


def write_document(root):
    owner_hash = hashlib.sha256(b"u1").hexdigest()[:32]
    directory = os.path.join(root, "data", "uploads", owner_hash, "doc-refs")
    os.makedirs(directory, exist_ok=True)
    document = {
        "version": "document.text/v2",
        "upload_id": "doc-refs",
        "status": "extracted",
        "page_count": 2,
        "total_pages": 2,
        "characters": 420,
        "pages": [
            {"number": 1, "text": "Paper A body text.\nReferences\n[1] A. Vaswani, N. Shazeer. Attention Is All You Need. NeurIPS, 2017. arXiv:1706.03762.\n[2] J. Devlin, M. Chang. BERT: Pre-training of Deep Bidirectional Transformers. NAACL, 2019. doi:10.18653/v1/N19-1423.", "chars": 250},
            {"number": 2, "text": "[3] L. Smith. A Study of Things. Journal of Tests, 2020.", "chars": 60},
        ],
        "extracted_at": "2026-11-03T00:00:00Z",
    }
    with open(os.path.join(directory, "document.json"), "w", encoding="utf-8") as handle:
        json.dump(document, handle)


def start(port, pdf_refs):
    root = os.path.join(ROOT, str(port))
    os.makedirs(os.path.join(root, "data"), exist_ok=True)
    write_document(root)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": f":{port}",
        "APP_DATA_DIR": os.path.join(root, "data"),
        "UPLOAD_ROOT": os.path.join(root, "data", "uploads"),
        "PLAN_STORE_PATH": os.path.join(root, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(root, "data", "memory"),
        "WIKI_DIR": os.path.join(root, "data", "wiki"),
        "MEMORY_ENABLED": "true", "MEMORY_MODE": "on",
        "WIKI_ENABLED": "true", "WIKI_MODE": "on", "WIKI_LLM_MODE": "off",
        "WIKI_PDF_REFS": "true" if pdf_refs else "false",
        "ALLOW_LEGACY_USER_HEADER": "true", "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "", "GIN_MODE": "release",
    })
    log = open(os.path.join(root, "backend.log"), "w", encoding="utf-8")
    process = subprocess.Popen([BACKEND_BIN], env=env, stdout=log, stderr=subprocess.STDOUT)
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


def create_fixture_papers(base):
    status, target = request(base, "POST", "/api/wiki/papers", {
        "title": "Attention Is All You Need", "year": 2017, "arxiv_id": "1706.03762",
        "doi": "10.48550/arXiv.1706.03762",
    })
    if status != 201:
        raise AssertionError(f"create target A failed: {status} {target}")
    status, bert = request(base, "POST", "/api/wiki/papers", {
        "title": "BERT: Pre-training of Deep Bidirectional Transformers", "year": 2019,
        "doi": "10.18653/v1/N19-1423",
    })
    if status != 201:
        raise AssertionError(f"create target B failed: {status} {bert}")
    status, source = request(base, "POST", "/api/wiki/papers", {
        "title": "Paper A", "year": 2021, "document_id": "doc-refs",
    })
    if status != 201:
        raise AssertionError(f"create source failed: {status} {source}")
    return source["paper"]["paper_id"], target["paper"]["paper_id"], bert["paper"]["paper_id"]


def build_and_wait(base):
    status, job = request(base, "POST", "/api/wiki/build", {"mode": "full"})
    if status != 202:
        raise AssertionError(f"build failed: {status} {job}")
    deadline = time.time() + 60
    build_job = {}
    while time.time() < deadline:
        status, build_job = request(base, "GET", f"/api/wiki/build/{job['job_id']}")
        if status == 200 and build_job.get("status") in ("completed", "failed"):
            break
        time.sleep(0.3)
    if build_job.get("status") != "completed":
        raise AssertionError(f"build job failed: {build_job}")
    return build_job


def main():
    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(ROOT, exist_ok=True)

    process, log, base = start(18088, True)
    try:
        source_id, target_a, target_b = create_fixture_papers(base)
        build_and_wait(base)
        status, links = request(base, "GET", f"/api/wiki/papers/{source_id}/links?type=cites")
        if status != 200:
            raise AssertionError(f"list links failed: {status} {links}")
        pdf_links = [item for item in links.get("links", []) if item.get("source") == "pdf_references"]
        if len(pdf_links) != 2:
            with open(os.path.join(ROOT, "18088", "backend.log"), encoding="utf-8") as handle:
                print(handle.read()[-2500:])
            raise AssertionError(f"expected 2 pdf_references cites links, got: {links}")
        targets = {item["to_paper"] for item in pdf_links}
        if targets != {target_a, target_b}:
            raise AssertionError(f"unexpected targets: {targets}")
        for item in pdf_links:
            if item["type"] != "cites" or item["status"] != "active":
                raise AssertionError(f"unexpected link: {item}")
            if not item.get("evidence") or not item["evidence"][0].get("quote"):
                raise AssertionError(f"missing evidence: {item}")
        print(f"PASS w2_5_pdf_references links={len(pdf_links)}")
    finally:
        stop(process, log)

    # Default/off switch must not create pdf_references links.
    process, log, base = start(18089, False)
    try:
        source_id, _, _ = create_fixture_papers(base)
        build_and_wait(base)
        status, links = request(base, "GET", f"/api/wiki/papers/{source_id}/links")
        pdf_links = [item for item in links.get("links", []) if item.get("source") == "pdf_references"]
        if pdf_links:
            raise AssertionError(f"WIKI_PDF_REFS=false must not parse references: {pdf_links}")
        print("PASS w2_5_disabled_switch")
    finally:
        stop(process, log)


if __name__ == "__main__":
    main()
