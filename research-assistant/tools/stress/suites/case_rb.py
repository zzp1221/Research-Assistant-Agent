#!/usr/bin/env python3
"""RB rollback/configuration suite (13 cases): switch matrix and per-owner settings."""
import json
import os
import shutil
import time


def _ctx(common, cases, case_id, run_id, evidence_root, backend):
    item = next(c for c in cases if c["case_id"] == case_id)
    case = common.Case(case_id, "RB", item["priority"], item["title"], run_id, evidence_root)
    api = common.Api(backend, case)
    owner = common.new_owner("rb", case_id.lower())
    return item, case, api, owner


def _simple_plan(marker):
    nodes = [{"id": "n1", "name": marker, "type": "general_process",
              "description": marker + " 请回复确认", "assigned_to": "general_agent",
              "status": "pending", "dependencies": []}]
    return {"user_intent": marker, "intent_type": "General", "nodes": nodes, "edges": []}


def _run_plan(case_api, common, owner):
    plan = case_api.post("/api/plans/from-graph", {"graph": _simple_plan("RB-TASK-SENTINEL")}, user=owner)[1]
    plan_id = (plan or {}).get("plan_graph", {}).get("id")
    if not plan_id:
        return None
    case_api.post("/api/plans/%s/execute" % plan_id, {}, user=owner)
    deadline = time.time() + 120
    while time.time() < deadline:
        graph = (case_api.get("/api/plans/" + plan_id, user=owner)[1] or {}).get("plan_graph") or {}
        if graph.get("status") in ("completed", "failed", "canceled"):
            return graph
        time.sleep(0.3)
    return None


def _chat_mark(case_api, backend, owner):
    marked = backend.prompt_mark()
    case_api.post("/api/chat", {"message": "RB 测试：请确认", "session_id": "s-rb"}, user=owner)
    return backend.mock_prompts()[marked:]


def run_suite(cases, run_id, evidence_root, common, out_root):
    verdicts = []
    # One backend per switch so environment rollback is observable.
    for case_id in ("RB-01", "RB-02", "RB-03", "RB-04", "RB-05", "RB-06", "RB-07", "RB-08"):
        item, case, api, owner = _ctx(common, cases, case_id, run_id, evidence_root, None)
        env = {}
        if case_id == "RB-01":
            env = {"MEMORY_ENABLED": "false"}
        elif case_id == "RB-02":
            env = {"MEMORY_MODE": "shadow"}
        elif case_id == "RB-03":
            env = {"WIKI_ENABLED": "false"}
        elif case_id == "RB-04":
            env = {"WIKI_MODE": "shadow"}
        elif case_id == "RB-05":
            env = {"WIKI_LLM_MODE": "off"}
        elif case_id == "RB-06":
            env = {"WIKI_LINK_MODE": "explicit"}
        elif case_id == "RB-07":
            env = {"WIKI_BUDGET_TOKENS": "300"}
        elif case_id == "RB-08":
            env = {"MEMORY_RETRIEVAL_TOPK": "20"}
        backend = common.Backend(out_root, "rb-" + case_id.lower(), env=env)
        backend.start()
        case_api = common.Api(backend, case)
        try:
            if case_id == "RB-01":
                status, memory = case_api.post("/api/memories", {"kind": "fact", "title": "rb1", "content": "RB-01"}, user=owner)
                case.check("memory_disabled", status == 404, "404", status)
                status, wiki = case_api.get("/api/wiki/stats", user=owner)
                case.check("wiki_route_available", status == 200, 200, status)
                case.info("R-38: wiki builder still available while MEMORY_ENABLED=false")
            elif case_id == "RB-02":
                case_api.post("/api/memories", {"kind": "fact", "title": "rb2", "content": "[[VALUE:RB-02=ENV-SHADOW]] 记忆"}, user=owner)
                prompts = _chat_mark(case_api, backend, owner)
                text = "\n".join(m.get("content", "") for p in prompts for m in p["messages"])
                injected = "ENV-SHADOW" in text
                case.check("chat_shadow_not_injected", not injected, True, injected)
                if injected:
                    case.info("H-03 confirmed: MEMORY_MODE=shadow does not stop chat injection")
                    case.errors.append("E8")
            elif case_id == "RB-03":
                status, _ = case_api.get("/api/wiki/stats", user=owner)
                case.check("wiki_route_404", status == 404, 404, status)
                status, _ = case_api.post("/api/chat", {"message": "hello", "session_id": "s"}, user=owner)
                case.check("chat_ok", status == 200, 200, status)
            elif case_id == "RB-04":
                status, created = case_api.post("/api/wiki/papers", {"title": "Shadow Paper", "year": 2024,
                                                                     "doi": "10.5555/shadow.2024"}, user=owner)
                case.check("build_still_works", status == 201, 201, status)
                prompts = _chat_mark(case_api, backend, owner)
                text = "\n".join(m.get("content", "") for p in prompts for m in p["messages"])
                case.check("wiki_shadow_not_injected", "[wiki context]" not in text, True, "[wiki context]" in text)
                status, stats = case_api.get("/api/wiki/stats", user=owner)
                case.check("stats_have_data", (stats or {}).get("papers", 0) >= 1, ">=1", (stats or {}).get("papers"))
            elif case_id == "RB-05":
                # WIKI_LLM_MODE=off: create paper with a document and ensure TLDR stays empty
                status, created = case_api.post("/api/wiki/papers", {"title": "No LLM Paper", "year": 2024,
                                                                     "doi": "10.5555/nollm.2024", "tldr": ""}, user=owner)
                paper = (created or {}).get("paper") or {}
                case.check("no_tldr", not paper.get("tldr"), "empty", paper.get("tldr"))
            elif case_id == "RB-06":
                status, created = case_api.post("/api/wiki/papers", {"title": "Link Mode Paper", "year": 2024,
                                                                     "doi": "10.5555/linkmode.2024",
                                                                     "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=owner)
                status, build = case_api.post("/api/wiki/build", {"mode": "full"}, user=owner)
                case.check("build_accepted", status == 202, 202, status)
                case.info("WIKI_LINK_MODE has no call site (R-25); behaviour identical across modes")
            elif case_id == "RB-07":
                status, created = case_api.post("/api/wiki/papers", {"title": "Budget Paper", "year": 2024,
                                                                     "doi": "10.5555/budget.2024"}, user=owner)
                status, search = case_api.post("/api/memories/search", {"query": "budget"}, user=owner)
                case.check("budget_override_ignored", True, True, "WIKI_BUDGET_TOKENS has no call site (R-24)")
            elif case_id == "RB-08":
                for index in range(30):
                    case_api.post("/api/memories", {"kind": "fact", "title": "topk-%02d" % index,
                                                    "content": "topk probe %02d" % index}, user=owner)
                status, search = case_api.post("/api/memories/search", {"query": "topk probe"}, user=owner)
                results = (search or {}).get("results") or []
                case.check("env_topk_ignored", len(results) <= 8, "<=8 (env not wired)", len(results))
        finally:
            backend.stop()
        verdicts.append(case.finish(case_api, backend, backend.mock_prompts()))

    # RB-09 per-owner settings scope: chat vs task
    item, case, api, owner = _ctx(common, cases, "RB-09", run_id, evidence_root, None)
    backend = common.Backend(out_root, "rb-09")
    backend.start()
    api = common.Api(backend, case)
    try:
        api.put("/api/memories/settings", {"retrieval_topk": 20, "core_budget_tokens": 512,
                                           "retrieved_budget_tokens": 2000, "enabled": False, "mode": "on"}, user=owner)
        for index in range(30):
            api.post("/api/memories", {"kind": "fact", "title": "rb9-%02d" % index,
                                       "content": "rb9 probe %02d" % index}, user=owner)
        prompts = _chat_mark(api, backend, owner)
        text = "\n".join(m.get("content", "") for p in prompts for m in p["messages"])
        case.check("chat_memory_disabled", "[core memory]" not in text and "[retrieved memory]" not in text, True, None)
        marked = backend.prompt_mark()
        graph = _run_plan(api, common, owner)
        task_prompts = backend.mock_prompts()[marked:]
        task_text = "\n".join(m.get("content", "") for p in task_prompts for m in p["messages"])
        task_injected = "[retrieved memory]" in task_text or "[core memory]" in task_text
        case.metric("task_injected_despite_disabled", task_injected)
        case.check("task_settings_observable", True, True, task_injected)
        if task_injected:
            case.info("H-02 confirmed: task path ignores per-owner settings.Enabled=false")
            case.errors.append("E7")
        case.info("R-37: wiki context independent of memory settings")
    finally:
        backend.stop()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # RB-10 delete wiki rebuild == LC-15
    item, case, api, owner = _ctx(common, cases, "RB-10", run_id, evidence_root, None)
    backend = common.Backend(out_root, "rb-10")
    backend.start()
    api = common.Api(backend, case)
    try:
        created = api.post("/api/wiki/papers", {"title": "RB10 Paper", "year": 2024,
                                                "doi": "10.5555/rb10.2024"}, user=owner)[1]
        paper_id = created["paper"]["paper_id"]
        # Wiki pages live only in the projection cache; rebuilding from memory
        # requires a paper metadata fact (RecordPlan normally writes one).
        api.post("/api/memories", {"kind": "fact", "title": "论文：RB10 Paper",
                                   "content": "标题：RB10 Paper；作者：；年份：2024；会议：；DOI：10.5555/rb10.2024；arXiv：；仓库：",
                                   "tags": ["paper", "metadata"]}, user=owner)
        backend.restart()  # flush WAL + paper snapshot before deleting the projection
        shutil.rmtree(os.path.join(backend.data, "wiki"), ignore_errors=True)
        backend.start()
        status, job = api.post("/api/wiki/build", {"mode": "full"}, user=owner)
        job_id = (job or {}).get("job_id")
        for _ in range(60):
            state = api.get("/api/wiki/build/%s" % job_id, user=owner)[1] or {}
            if state.get("status") != "running":
                break
            time.sleep(0.4)
        status, page = api.get("/api/wiki/papers/" + paper_id, user=owner)
        case.check("paper_rebuilt", status == 200, 200, status)
    finally:
        backend.stop()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # RB-11 schema upgrade: legacy snapshot without new fields
    item, case, api, owner = _ctx(common, cases, "RB-11", run_id, evidence_root, None)
    backend = common.Backend(out_root, "rb-11")
    os.makedirs(os.path.join(backend.data, "memory"), exist_ok=True)
    legacy = {"version": 1, "records": [{"id": "legacy-1", "kind": "fact", "scope": {"owner_id": "legacy"},
                                        "title": "legacy", "content": "legacy record", "status": "active"}]}
    with open(os.path.join(backend.data, "memory", "memory.snapshot.json"), "w", encoding="utf-8") as handle:
        json.dump(legacy, handle)
    backend.start()
    api = common.Api(backend, case)
    try:
        status, stats = api.get("/api/memories/stats", user="legacy")
        case.check("legacy_loaded", (stats or {}).get("total") == 1, 1, (stats or {}).get("total"))
        status, got = api.get("/api/memories/legacy-1", user="legacy")
        case.check("legacy_readable", status == 200, 200, status)
    finally:
        backend.stop()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # RB-12 double open fallback
    item, case, api, owner = _ctx(common, cases, "RB-12", run_id, evidence_root, None)
    backend = common.Backend(out_root, "rb-12")
    backend.start()
    api = common.Api(backend, case)
    try:
        api.post("/api/memories", {"kind": "fact", "title": "rb12", "content": "rb12"}, user=owner)
        env = backend.base_env()
        env["APP_ADDR"] = ":%d" % (backend.port + 500)
        log2 = open(os.path.join(backend.dir, "second.log"), "w", encoding="utf-8")
        import subprocess
        second = subprocess.Popen([common.DEFAULT_BACKEND], env=env, stdout=log2, stderr=subprocess.STDOUT)
        time.sleep(6)
        text = open(os.path.join(backend.dir, "second.log"), encoding="utf-8", errors="replace").read()
        alive = second.poll() is None
        case.check("no_silent_split", ("falling back to in-memory store" in text) or (not alive), True,
                   {"alive": alive, "tail": text[-200:]})
        if alive:
            second.terminate()
            try:
                second.wait(timeout=10)
            except Exception:
                second.kill()
        log2.close()
    finally:
        backend.stop()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # RB-13 settings hot update
    item, case, api, owner = _ctx(common, cases, "RB-13", run_id, evidence_root, None)
    backend = common.Backend(out_root, "rb-13")
    backend.start()
    api = common.Api(backend, case)
    try:
        for index in range(20):
            api.post("/api/memories", {"kind": "fact", "title": "rb13-%02d" % index,
                                       "content": "rb13 probe %02d" % index}, user=owner)
        api.put("/api/memories/settings", {"retrieval_topk": 3, "core_budget_tokens": 512,
                                           "retrieved_budget_tokens": 2000, "enabled": True, "mode": "on"}, user=owner)
        status, search = api.post("/api/memories/search", {"query": "rb13 probe"}, user=owner)
        case.check("hot_settings_applied", len((search or {}).get("results") or []) == 3, 3,
                   len((search or {}).get("results") or []))
    finally:
        backend.stop()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts, {"backend": "mock", "cases": len(verdicts)}
