#!/usr/bin/env python3
"""Extra probes for LC-07 (silent in-memory fallback) and RB-03 (wiki off)."""
import json
import os
import subprocess
import sys
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common  # noqa: E402


def lc07(out, run_id):
    evidence_root = os.path.join(out, "evidence", run_id)
    root = os.path.join(out, "lc07")
    os.makedirs(root, exist_ok=True)
    env = {"MEMORY_DIR": os.path.join(root, "data", "memory"),
           "PLAN_STORE_PATH": os.path.join(root, "data", "plans.json"),
           "APP_DATA_DIR": os.path.join(root, "data"),
           "WIKI_DIR": os.path.join(root, "data", "wiki")}
    backend = common.Backend(out, "lc07-a", env=env)
    backend.start()
    api = common.Api(backend, None)
    owner = "stress-lc-lc-07-probe"
    api.post("/api/memories", {"kind": "fact", "title": "lock", "content": "lock probe"}, user=owner)
    case = common.Case("LC-07", "LC", "P0", "双进程锁竞争", run_id, evidence_root)
    case_dir = os.path.join(backend.dir, "second.log")
    env2 = backend.base_env()
    env2["APP_ADDR"] = ":%d" % (backend.port + 700)
    log2 = open(case_dir, "w", encoding="utf-8")
    second = subprocess.Popen([common.DEFAULT_BACKEND], env=env2, stdout=log2, stderr=subprocess.STDOUT)
    time.sleep(6)
    alive = second.poll() is None
    log2.flush()
    text = open(case_dir, encoding="utf-8", errors="replace").read()
    fell_back = "falling back to in-memory store" in text
    second_stats = None
    if alive:
        try:
            with urllib.request.urlopen("http://127.0.0.1:%d/api/memories/stats" % (backend.port + 700), timeout=5) as resp:
                second_stats = json.loads(resp.read().decode())
        except Exception:
            pass
        second.terminate()
        try:
            second.wait(timeout=10)
        except Exception:
            second.kill()
    log2.close()
    case.metric("second_alive", alive)
    case.metric("silent_fallback_log", fell_back)
    case.metric("second_stats_total", (second_stats or {}).get("total"))
    case.check("no_silent_split_store", not (alive and fell_back), True,
               {"alive": alive, "fallback_log": fell_back, "second_total": (second_stats or {}).get("total")})
    if alive and fell_back:
        case.info("H-10/R-16 confirmed: the second backend silently fell back to an in-memory store and served a divergent view")
        case.errors.append("E11")
    verdict = case.finish(api, backend, backend.mock_prompts())
    backend.stop()
    return verdict


def rb03(out, run_id):
    evidence_root = os.path.join(out, "evidence", run_id)
    backend = common.Backend(out, "rb03-probe", env={"WIKI_ENABLED": "false"})
    backend.start()
    api = common.Api(backend, None)
    owner = "stress-rb-rb-03-probe"
    case = common.Case("RB-03", "RB", "P0", "WIKI_ENABLED=false", run_id, evidence_root)
    case.check("wiki_route_404", api.get("/api/wiki/stats", user=owner)[0] == 404, 404,
               api.get("/api/wiki/stats", user=owner)[0])
    status, chat = api.post("/api/chat", {"message": "hello", "session_id": "rb03"}, user=owner)
    case.check("chat_ok", status == 200, 200, status)
    verdict = case.finish(api, backend, backend.mock_prompts())
    backend.stop()
    return verdict


def main():
    out = sys.argv[1]
    run_id = sys.argv[2]
    print(json.dumps([lc07(out, run_id), rb03(out, run_id)], ensure_ascii=False)[:600])


if __name__ == "__main__":
    main()
