#!/usr/bin/env python3
"""Paper Wiki E2E: identity, points, links, owner isolation, rebuild and rollback.

Runs the backend binary and a deterministic environment inside the test container.
No LLM is required: this covers the deterministic Paper Wiki surface.
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

BASE = "http://127.0.0.1:18081"
ROOT = "/tmp/wiki-e2e"
BACKEND_BIN = os.environ.get("BACKEND_BIN", "/tmp/e2e/ra-backend")


def request(method, path, payload=None, user="u1", base=BASE):
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Id", user)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            body = response.read().decode("utf-8")
            return response.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(body)
        except Exception:
            return exc.code, {"error": body}


def wait_health(base=BASE, timeout=90):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(base + "/api/health", timeout=3) as response:
                if response.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def env_for(port, wiki_enabled="true"):
    env = dict(os.environ)
    env.update({
        "APP_ADDR": f":{port}",
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "WIKI_DIR": os.path.join(ROOT, "data", "wiki"),
        "MEMORY_ENABLED": "true",
        "MEMORY_MODE": "on",
        "WIKI_ENABLED": wiki_enabled,
        "WIKI_MODE": "on",
        "ALLOW_LEGACY_USER_HEADER": "true",
        "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "",
        "GIN_MODE": "release",
        "PROMPT_CONTEXT_V2": "true",
    })
    return env


def start_backend(port, wiki_enabled="true"):
    log = open(os.path.join(ROOT, f"backend-{port}.log"), "a", encoding="utf-8")
    process = subprocess.Popen([BACKEND_BIN], env=env_for(port, wiki_enabled), stdout=log, stderr=subprocess.STDOUT)
    if not wait_health(f"http://127.0.0.1:{port}"):
        process.kill()
        raise AssertionError(f"backend on {port} did not become healthy")
    return process, log


def stop_backend(process, log):
    try:
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=10)
    except Exception:
        process.kill()
    log.close()


def expect(condition, message):
    if not condition:
        raise AssertionError(message)


def main():
    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(os.path.join(ROOT, "data"), exist_ok=True)
    backend, log = start_backend(18081)
    try:
        # 1) Identity: DOI and arXiv resolve to the same paper id.
        status, first = request("POST", "/api/wiki/papers", {
            "title": "Attention Is All You Need", "year": 2017,
            "doi": "10.48550/arXiv.1706.03762", "arxiv_id": "1706.03762",
            "authors": ["Ashish Vaswani"],
        })
        expect(status == 201, f"create paper failed: {status} {first}")
        paper = first["paper"]
        expect(paper["doi"] == "10.48550/arxiv.1706.03762", f"doi missing: {paper}")
        status, second = request("POST", "/api/wiki/papers", {"title": "Attention Is All You Need", "year": 2017, "arxiv_id": "1706.03762v5"})
        expect(status == 201, f"second create failed: {status} {second}")
        expect(second["paper"]["paper_id"] == paper["paper_id"], "DOI and arXiv must share one paper id")

        status, listed = request("GET", "/api/wiki/papers")
        expect(status == 200 and listed["total"] == 1, f"list expected 1 paper: {listed}")

        # 2) Owner isolation and hidden benchmark.
        status, _ = request("GET", f"/api/wiki/papers/{paper['paper_id']}", user="u2")
        expect(status == 404, f"cross-owner detail status={status}")
        status, other_list = request("GET", "/api/wiki/papers", user="u2")
        expect(status == 200 and other_list["total"] == 0, f"cross-owner list leaked: {other_list}")
        status, _ = request("POST", "/api/wiki/papers", {"title": "Hidden Benchmark Secret Paper"})
        expect(status == 404, f"hidden paper must be rejected, status={status}")

        # 3) Points: user note + list + soft delete.
        status, created_point = request("POST", f"/api/wiki/papers/{paper['paper_id']}/points", {"text": "复现时注意 WMT14 BLEU 27.3"})
        expect(status == 201, f"create point failed: {status} {created_point}")
        point_id = created_point["point"]["record"]["id"]
        status, points = request("GET", f"/api/wiki/papers/{paper['paper_id']}/points")
        expect(status == 200 and points["total"] == 1, f"points total: {points}")
        status, _ = request("DELETE", f"/api/wiki/points/{point_id}")
        expect(status == 200, f"delete point status={status}")
        status, points = request("GET", f"/api/wiki/papers/{paper['paper_id']}/points")
        expect(points["total"] == 0, f"soft-deleted point still listed: {points}")

        # 4) Links from shared dataset entities + build + reject.
        status, paper_a = request("POST", "/api/wiki/papers", {"title": "Paper A", "arxiv_id": "1706.00001", "entities": [{"type": "dataset", "value": "wmt14"}]})
        status2, paper_b = request("POST", "/api/wiki/papers", {"title": "Paper B", "arxiv_id": "1706.00002", "entities": [{"type": "dataset", "value": "wmt14"}]})
        expect(status == 201 and status2 == 201, "create link papers failed")
        status, job = request("POST", "/api/wiki/build", {"mode": "full"})
        expect(status == 202, f"build status={status} {job}")
        deadline = time.time() + 30
        while time.time() < deadline:
            status, build_job = request("GET", f"/api/wiki/build/{job['job_id']}")
            if status == 200 and build_job.get("status") in ("completed", "failed"):
                break
            time.sleep(0.2)
        expect(build_job.get("status") == "completed", f"build job: {build_job}")
        status, links = request("GET", f"/api/wiki/papers/{paper_a['paper']['paper_id']}/links")
        expect(status == 200 and links["total"] == 1, f"expected one link: {links}")
        link = links["links"][0]
        expect(link["type"] == "uses_same_dataset", f"link type: {link}")
        expect(link["from_paper"] < link["to_paper"], "symmetric direction must be normalized")
        status, rejected = request("POST", f"/api/wiki/links/{link['id']}/reject", {"reason": "different split"})
        expect(status == 200 and rejected["link"]["status"] == "archived", f"reject: {rejected}")

        status, stats = request("GET", "/api/wiki/stats")
        expect(status == 200 and stats["papers"] >= 3, f"stats: {stats}")

        # 5) Rebuild after deleting the wiki directory: PaperPage from memory facts,
        #    Link decisions survive because links live in memory.
        status, _ = request("POST", "/api/memories", {
            "kind": "fact", "title": "论文：Rebuild Paper",
            "content": "标题：Rebuild Paper；作者：；年份：2020；会议：；DOI：10.9999/rebuild；arXiv：2001.00001；仓库：",
            "tags": ["paper", "metadata"], "importance": 8,
        })
        expect(status in (200, 201), f"create rebuild fact: {status}")
        stop_backend(backend, log)
        # A SIGTERM'd process may leave the advisory lock behind; the restarted
        # backend would then fall back to an in-memory store. Remove stale locks.
        for lock_name in (".memory.lock", ".wiki.lock"):
            lock_path = os.path.join(ROOT, "data", "memory" if lock_name == ".memory.lock" else "wiki", lock_name)
            if os.path.exists(lock_path):
                os.remove(lock_path)
        shutil.rmtree(os.path.join(ROOT, "data", "wiki"), ignore_errors=True)
        backend, log = start_backend(18081)
        status, job = request("POST", "/api/wiki/build", {"mode": "full"})
        expect(status == 202, f"rebuild status={status}")
        deadline = time.time() + 30
        while time.time() < deadline:
            status, build_job = request("GET", f"/api/wiki/build/{job['job_id']}")
            if status == 200 and build_job.get("status") in ("completed", "failed"):
                break
            time.sleep(0.2)
        status, listed = request("GET", "/api/wiki/papers?q=rebuild")
        if listed.get("total") != 1:
            print("rebuild job:", build_job)
            print("papers:", listed)
            with open(os.path.join(ROOT, "backend-18081.log"), encoding="utf-8") as handle:
                print(handle.read()[-3000:])
        expect(status == 200 and listed["total"] == 1, f"paper not rebuilt from memory: {listed}")
        status, links_after = request("GET", "/api/wiki/links")
        expect(status == 200 and any(item["status"] == "archived" for item in links_after["links"]), "rejected link decision must survive rebuild")

        print("PASS wiki_identity points links owner_isolation rebuild")
    finally:
        stop_backend(backend, log)

    # 6) WIKI_ENABLED=false rollback: routes are absent.
    disabled, disabled_log = start_backend(18082, wiki_enabled="false")
    try:
        status, _ = request("GET", "/api/wiki/papers", base="http://127.0.0.1:18082")
        expect(status == 404, f"WIKI_ENABLED=false must hide routes, status={status}")
        status, _ = request("GET", "/api/health", base="http://127.0.0.1:18082")
        expect(status == 200, "backend health must stay ok with wiki disabled")
        print("PASS wiki_disabled_rollback")
    finally:
        stop_backend(disabled, disabled_log)


if __name__ == "__main__":
    main()
