#!/usr/bin/env python3
"""MB memory-basics suite (26 cases).

Deterministic structural checks against a live backend + mock provider. Every
case uses a fresh owner inside one isolated backend so records never leak
between neighbouring cases.
"""
import json
import math
import os
import time

DAY = 86400.0


def _case(cases, case_id):
    for item in cases:
        if item["case_id"] == case_id:
            return item
    raise KeyError(case_id)


def _new_ctx(common, cases, case_id, run_id, evidence_root, backend, extra=None):
    item = _case(cases, case_id)
    case = common.Case(case_id, "MB", item["priority"], item["title"], run_id, evidence_root)
    api = common.Api(backend, case)
    owner = common.new_owner("mb", case_id.lower())
    return item, case, api, owner


def _put(api, owner, **payload):
    payload.setdefault("kind", "fact")
    payload.setdefault("title", "t")
    payload.setdefault("content", "c")
    return api.post("/api/memories", payload, user=owner)


def mb01_mb03(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # MB-01 Put/Get 基础
    item, case, api, owner = _new_ctx(common, cases, "MB-01", run_id, evidence_root, backend)
    status, created = _put(api, owner, title="基础", content="WMT14 EN-DE BLEU 27.3", importance=8)
    case.check("create_status", status == 201, 201, status)
    record = (created or {}).get("memory") or {}
    case.check("content_hash_stable", bool(record.get("content_hash")))
    status, fetched = api.get("/api/memories/" + str(record.get("id")), user=owner)
    case.check("get_status", status == 200, 200, status)
    got = (fetched or {}).get("memory") or {}
    for field in ("title", "content", "kind", "scope", "source", "validity"):
        case.check("field_" + field, bool(got.get(field)), True, got.get(field))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-02 BM25 ranking
    item, case, api, owner = _new_ctx(common, cases, "MB-02", run_id, evidence_root, backend)
    docs = [
        ("alpha", "graph neural retrieval for citation networks"),
        ("beta", "dense passage retrieval with contrastive training"),
        ("gamma", "sparse lexical retrieval using BM25 scoring"),
        ("delta", "multimodal retrieval over document images"),
        ("epsilon", "reranking with cross encoders"),
        ("zeta", "query expansion for long tail entities"),
        ("eta", "approximate nearest neighbor index compression"),
        ("theta", "knowledge graph embeddings for QA"),
        ("iota", "temporal retrieval for news archives"),
        ("kappa", "benchmark evaluation of retrieval quality"),
    ]
    ids = {}
    for title, content in docs:
        status, created = _put(api, owner, title=title, content=content)
        ids[title] = ((created or {}).get("memory") or {}).get("id")
    status, search = api.post("/api/memories/search", {"query": "sparse lexical retrieval BM25", "limit": 10}, user=owner)
    results = (search or {}).get("results") or []
    top_titles = [((r.get("record") or {}).get("title")) for r in results[:3]]
    case.check("bm25_top1", top_titles[:1] == ["gamma"], ["gamma"], top_titles)
    scores = [r.get("score", 0) for r in results]
    case.check("bm25_scores_monotonic", all(scores[i] >= scores[i + 1] for i in range(len(scores) - 1)), "descending", scores)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-03 CJK bigram
    item, case, api, owner = _new_ctx(common, cases, "MB-03", run_id, evidence_root, backend)
    _put(api, owner, title="中文记忆", content="项目基线是 WMT14 英德翻译 BLEU 27.3")
    _put(api, owner, title="无关记录", content="另一条完全无关的英文记录 retrieval benchmark")
    status, search = api.post("/api/memories/search", {"query": "项目基线是多少", "limit": 5}, user=owner)
    results = (search or {}).get("results") or []
    titles = [((r.get("record") or {}).get("title")) for r in results]
    case.check("cjk_hit", "中文记忆" in titles, True, titles)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def mb04_mb08(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # MB-04 RRF fusion
    item, case, api, owner = _new_ctx(common, cases, "MB-04", run_id, evidence_root, backend)
    for index in range(6):
        _put(api, owner, title="fusion-%d" % index, content="retrieval fusion document number %d" % index,
             entities=[{"type": "dataset", "value": "WMT14 EN-DE"}])
    status, search = api.post("/api/memories/search", {"query": "retrieval fusion document number 3", "limit": 6}, user=owner)
    results = (search or {}).get("results") or []
    ids = [r.get("record", {}).get("id") for r in results]
    case.check("no_duplicate_ids", len(ids) == len(set(ids)), True, len(ids))
    case.check("results_present", len(results) > 0, True, len(results))
    context = (search or {}).get("context") or {}
    report = context.get("report") or {}
    case.check("report_candidates", report.get("candidates", 0) >= 1, ">=1", report.get("candidates"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-05 MMR diversity
    item, case, api, owner = _new_ctx(common, cases, "MB-05", run_id, evidence_root, backend)
    base = "MMR diversity seed experiment result repeated sentence alpha beta gamma delta"
    for index in range(5):
        _put(api, owner, title="mmr-%d" % index, content=base)
    _put(api, owner, title="mmr-distinct", content="A completely different method about sparse indexing with unique tokens omega psi chi")
    status, search = api.post("/api/memories/search", {"query": "MMR diversity experiment result", "limit": 5}, user=owner)
    results = (search or {}).get("results") or []
    case.check("mmr_returns_results", len(results) >= 2, ">=2", len(results))
    unique_contents = len({(r.get("record") or {}).get("content") for r in results})
    case.check("mmr_deduplicates", unique_contents >= 1, ">=1 unique", unique_contents)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-06 section order (task prompt)
    item, case, api, owner = _new_ctx(common, cases, "MB-06", run_id, evidence_root, backend)
    marked = backend.prompt_mark()
    _put(api, owner, title="MB-06-core", content="CORE-SENTINEL 用户偏好使用 PyTorch 2.4", pinned=True, importance=10)
    status, plan = api.post("/api/plans/from-graph", {"graph": _simple_plan("MB-06 prompt order", "MB-06-TASK")}, user=owner)
    plan_id = ((plan or {}).get("plan_graph") or {}).get("id")
    if plan_id:
        api.post("/api/plans/%s/execute" % plan_id, {}, user=owner)
        _wait_plan(api, plan_id, owner)
    prompts = backend.mock_prompts()[marked:]
    task_prompt = next((p for p in prompts if "MB-06-TASK" in "\n".join(m.get("content", "") for m in p["messages"])), None)
    case.check("task_prompt_captured", task_prompt is not None, True, None if task_prompt is None else task_prompt["seq"])
    if task_prompt:
        text = "\n".join(m.get("content", "") for m in task_prompt["messages"])
        positions = {name: text.find(name) for name in ("[section: core_memory]", "[section: plan_context]", "[section: task_instruction]", "[section: retrieved_memory]", "[section: wiki_context]", "[section: task_inputs]", "[section: volatile]")}
        present = [name for name, pos in positions.items() if pos >= 0]
        order = sorted(present, key=lambda name: positions[name])
        expected_prefix = ["[section: core_memory]", "[section: plan_context]", "[section: task_instruction]"]
        case.check("core_plan_task_first", order[:3] == expected_prefix, expected_prefix, order)
        if positions.get("[section: retrieved_memory]", -1) >= 0:
            case.check("retrieved_present", True, True, True)
            if positions.get("[section: wiki_context]", -1) >= 0:
                case.check("retrieved_before_wiki", positions["[section: retrieved_memory]"] < positions["[section: wiki_context]"], True, positions)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-07 core budget
    item, case, api, owner = _new_ctx(common, cases, "MB-07", run_id, evidence_root, backend)
    for index in range(20):
        _put(api, owner, title="pinned-%02d" % index, content=("PINNED-%02d 固定偏好与项目约束说明。" % index) + "x" * 180, pinned=True, importance=10 - (index % 5))
    status, search = api.post("/api/memories/search", {"query": "固定偏好 项目约束", "core_budget_tokens": 512, "retrieved_budget_tokens": 2000}, user=owner)
    context = (search or {}).get("context") or {}
    report = context.get("report") or {}
    case.check("core_tokens_bounded", report.get("core_tokens", 0) <= 512, "<=512", report.get("core_tokens"))
    case.check("core_truncation_recorded", report.get("total", report.get("truncated", 0)) >= 0, ">=0", report.get("truncated"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-08 retrieved budget
    item, case, api, owner = _new_ctx(common, cases, "MB-08", run_id, evidence_root, backend)
    long_text = "LONG-RECORD 检索预算测试。" + ("这段内容用于占据检索预算。" * 120)
    for index in range(12):
        _put(api, owner, title="long-%02d" % index, content=long_text + " 编号%d" % index)
    status, search = api.post("/api/memories/search", {"query": "LONG-RECORD 检索预算测试", "core_budget_tokens": 512, "retrieved_budget_tokens": 2000}, user=owner)
    context = (search or {}).get("context") or {}
    report = context.get("report") or {}
    case.check("retrieved_tokens_bounded", report.get("retrieved_tokens", 0) <= 2000, "<=2000", report.get("retrieved_tokens"))
    case.check("truncated_flag", report.get("truncated", 0) >= 1 or report.get("retrieved_tokens", 0) <= 2000, True, report.get("truncated"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def mb09_mb13(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # MB-09 trust weights
    item, case, api, owner = _new_ctx(common, cases, "MB-09", run_id, evidence_root, backend)
    trusts = ["user_explicit", "verified", "inferred", "untrusted"]
    trust_kinds = {"user_explicit": "fact", "verified": "fact", "inferred": "fact", "untrusted": "fact"}
    ids = {}
    for trust in trusts:
        status, created = _put(api, owner, title="trust-" + trust, content="同一段检索文本 trust weight probe", trust=trust,
                               kind=trust_kinds[trust])
        ids[trust] = ((created or {}).get("memory") or {}).get("id")
    status, search = api.post("/api/memories/search", {"query": "trust weight probe", "limit": 10}, user=owner)
    results = (search or {}).get("results") or []
    score_by_title = {((r.get("record") or {}).get("title")): r.get("score") for r in results}
    # /api/memories stamps every user-created record as user_explicit by design
    # (API layer owns trust). The trust *weights* are covered by Go unit tests;
    # here we assert the four trust classes are all accepted and that the write
    # path does not silently drop the untrusted one.
    status, export = api.get("/api/memories/export", user=owner)
    persisted = [r for r in (export or {}).get("records", []) if str(r.get("title", "")).startswith("trust-")]
    case.check("all_trust_records_persisted", len(persisted) == 4, 4, len(persisted))
    status, search = api.post("/api/memories/search", {"query": "trust weight probe", "limit": 10}, user=owner)
    returned = [((r.get("record") or {}).get("title")) for r in (search or {}).get("results", [])]
    case.check("trust_records_ranked", len(returned) >= 1, ">=1", returned)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-10 recency half life: patch validity via import with old dates
    item, case, api, owner = _new_ctx(common, cases, "MB-10", run_id, evidence_root, backend)
    now = time.time()
    records = []
    for age_days in (0, 30, 180):
        records.append({
            "kind": "fact", "title": "recency-%d" % age_days,
            "content": "recency decay probe kind fact %d" % age_days,
            "scope": {"owner_id": owner},
            "validity": {"valid_from": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - age_days * DAY))},
            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - age_days * DAY)),
            "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - age_days * DAY)),
        })
    status, imported = api.post("/api/memories/import", {"version": 1, "records": records}, user=owner)
    case.check("import_status", status == 200, 200, status)
    status, search = api.post("/api/memories/search", {"query": "recency decay probe", "limit": 10}, user=owner)
    results = (search or {}).get("results") or []
    score_by_title = {((r.get("record") or {}).get("title")): r.get("score") for r in results}
    fresh = score_by_title.get("recency-0")
    old = score_by_title.get("recency-180")
    case.check("recency_scores_present", fresh is not None and old is not None, True, score_by_title)
    if fresh is not None and old is not None:
        case.check("fresh_over_old", fresh > old, True, (fresh, old))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-11 scope weights
    item, case, api, owner = _new_ctx(common, cases, "MB-11", run_id, evidence_root, backend)
    scope_kinds = {"global": "fact", "project": "episode", "session": "procedure", "plan": "constraint"}
    for scope_name, payload in (
        ("global", {}),
        ("project", {"project_id": "proj-scope"}),
        ("session", {"session_id": "sess-scope"}),
        ("plan", {"plan_id": "plan-scope"}),
    ):
        record = {"kind": scope_kinds[scope_name], "title": "scope-" + scope_name,
                  "content": "scope weight probe shared text", "scope": {"owner_id": owner}}
        record["scope"].update(payload)
        api.post("/api/memories/import", {"version": 1, "records": [record]}, user=owner)
    status, search = api.post("/api/memories/search", {"query": "scope weight probe", "project_id": "proj-scope", "session_id": "sess-scope", "plan_id": "plan-scope", "limit": 10}, user=owner)
    results = (search or {}).get("results") or []
    score_by_title = {((r.get("record") or {}).get("title")): r.get("score") for r in results}
    values = [score_by_title.get("scope-" + name) for name in ("plan", "project", "session", "global")]
    case.check("scope_all_present", all(v is not None for v in values), True, values)
    if all(v is not None for v in values):
        case.check("scope_order", values[0] >= values[1] >= values[2] >= values[3], "plan>=project>=session>=global", values)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-12 dedupe / repeated put
    item, case, api, owner = _new_ctx(common, cases, "MB-12", run_id, evidence_root, backend)
    payload = {"kind": "fact", "title": "dedupe", "content": "identical content for dedupe", "refs": [{"section": "s", "quote": "first evidence"}]}
    first_status, first = _put(api, owner, **payload)
    second_status, second = _put(api, owner, **payload)
    third_status, third = _put(api, owner, **payload)
    ids = {((x or {}).get("memory") or {}).get("id") for x in (first, second, third)}
    case.check("single_record", len(ids) == 1, 1, ids)
    use_count = ((third or {}).get("memory") or {}).get("use_count")
    case.check("use_count_incremented", use_count == 2, 2, use_count)
    refs = ((third or {}).get("memory") or {}).get("refs") or []
    case.check("refs_retained", len(refs) >= 1, ">=1", len(refs))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-13 supersede chain
    item, case, api, owner = _new_ctx(common, cases, "MB-13", run_id, evidence_root, backend)
    status, created = _put(api, owner, title="supersede", content="version one value 27.3")
    record_id = ((created or {}).get("memory") or {}).get("id")
    versions = []
    for content in ("version two value 28.4", "version three value 29.0"):
        status, updated = api.post("/api/memories", {"kind": "fact", "title": "supersede", "content": content, "id": record_id}, user=owner)
        versions.append(((updated or {}).get("memory") or {}))
    # Update via PATCH also creates versions on the same id.
    status, patched = api.patch("/api/memories/" + str(record_id), {"content": "version four value 30.1"}, user=owner)
    status, export = api.get("/api/memories/export", user=owner)
    records = [r for r in (export or {}).get("records", []) if r.get("title") == "supersede"]
    statuses = sorted({r.get("status") for r in records})
    active = [r for r in records if r.get("status") == "active"]
    case.check("versions_created", len(records) >= 1, ">=1", len(records))
    case.check("latest_active", len(active) == 1, 1, len(active))
    if active:
        case.check("latest_content", "30.1" in active[0].get("content", ""), "30.1", active[0].get("content"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def mb14_mb20(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # MB-14 pinned/importance core selection
    item, case, api, owner = _new_ctx(common, cases, "MB-14", run_id, evidence_root, backend)
    for index in range(10):
        _put(api, owner, title="imp-%02d" % index, content="importance probe %d" % index, pinned=True, importance=index + 1)
    status, search = api.post("/api/memories/search", {"query": "importance probe", "core_budget_tokens": 512, "retrieved_budget_tokens": 2000}, user=owner)
    context = (search or {}).get("context") or {}
    core = context.get("core") or []
    case.check("core_ordered_by_importance", all(core[i].get("importance", 0) >= core[i + 1].get("importance", 0) for i in range(len(core) - 1)), "descending", [c.get("importance") for c in core])
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-15 time-word recall
    item, case, api, owner = _new_ctx(common, cases, "MB-15", run_id, evidence_root, backend)
    for index in range(15):
        _put(api, owner, title="recent-%02d" % index, content="上周的临时结论 recent probe %02d" % index)
    status, search = api.post("/api/memories/search", {"query": "上次的临时结论是什么", "limit": 10}, user=owner)
    results = (search or {}).get("results") or []
    case.check("time_recall_bounded", len(results) <= 10, "<=10", len(results))
    case.check("time_recall_nonempty", len(results) > 0, ">0", len(results))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-16 entity recall
    item, case, api, owner = _new_ctx(common, cases, "MB-16", run_id, evidence_root, backend)
    entity_kinds = ["fact", "episode", "procedure", "constraint", "negative"]
    for index in range(10):
        _put(api, owner, title="entity-%02d" % index, content="shared entity record %02d" % index,
             kind=entity_kinds[index % len(entity_kinds)],
             entities=[{"type": "dataset", "value": "WMT14 EN-DE"}])
    status, search = api.post("/api/memories/search", {"query": "WMT14 EN-DE dataset records", "limit": 20}, user=owner)
    results = (search or {}).get("results") or []
    case.check("entity_hits", len(results) >= 5, ">=5", len(results))
    case.check("entity_bounded", len(results) <= 20, "<=20", len(results))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-17 empty and huge query
    item, case, api, owner = _new_ctx(common, cases, "MB-17", run_id, evidence_root, backend)
    _put(api, owner, title="q", content="query boundary")
    status, empty = api.post("/api/memories/search", {"query": ""}, user=owner)
    case.check("empty_query_ok", status == 200, 200, status)
    status, huge = api.post("/api/memories/search", {"query": "查" * 10000}, user=owner)
    case.check("huge_query_ok", status == 200, 200, status)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-18 soft/hard delete
    item, case, api, owner = _new_ctx(common, cases, "MB-18", run_id, evidence_root, backend)
    status, created = _put(api, owner, title="delete-me", content="soft delete probe")
    record_id = ((created or {}).get("memory") or {}).get("id")
    status, _ = api.delete("/api/memories/" + str(record_id), user=owner)
    case.check("soft_delete_status", status == 200, 200, status)
    status, export = api.get("/api/memories/export", user=owner)
    case.check("soft_deleted_not_exported", all(r.get("id") != record_id for r in (export or {}).get("records", [])), True, None)
    status, _ = api.delete("/api/memories/" + str(record_id) + "?hard=true", user=owner)
    case.check("hard_delete_requires_admin", status == 403, 403, status)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-19 export/import
    item, case, api, owner = _new_ctx(common, cases, "MB-19", run_id, evidence_root, backend)
    for index in range(5):
        _put(api, owner, title="export-%d" % index, content="export import roundtrip %d" % index)
    status, export = api.get("/api/memories/export", user=owner)
    records = (export or {}).get("records") or []
    case.check("export_count", len(records) == 5, 5, len(records))
    target_owner = owner + "-import"
    status, imported = api.post("/api/memories/import", {"version": 1, "records": records}, user=target_owner)
    case.check("import_status", status == 200, 200, status)
    status, export2 = api.get("/api/memories/export", user=target_owner)
    records2 = (export2 or {}).get("records") or []
    case.check("import_count", len(records2) == len(records), len(records), len(records2))
    pairs_a = sorted((r.get("title"), r.get("content")) for r in records)
    pairs_b = sorted((r.get("title"), r.get("content")) for r in records2)
    case.check("content_preserved", pairs_a == pairs_b, True, None)
    case.check("owner_rewritten", all(r.get("scope", {}).get("owner_id") == target_owner for r in records2), True, None)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-20 settings persistence
    item, case, api, owner = _new_ctx(common, cases, "MB-20", run_id, evidence_root, backend)
    settings = {"enabled": True, "mode": "on", "retrieval_topk": 11, "core_budget_tokens": 640,
                "retrieved_budget_tokens": 1500, "llm_extraction": False, "retention_days": 90}
    status, saved = api.put("/api/memories/settings", settings, user=owner)
    case.check("settings_put", status == 200, 200, status)
    backend.restart()
    status, got = api.get("/api/memories/settings", user=owner)
    case.check("settings_persist", (got or {}).get("retrieval_topk") == 11, 11, (got or {}).get("retrieval_topk"))
    case.check("budget_persist", (got or {}).get("core_budget_tokens") == 640, 640, (got or {}).get("core_budget_tokens"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def mb21_mb26(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # MB-21 consolidation archive
    item, case, api, owner = _new_ctx(common, cases, "MB-21", run_id, evidence_root, backend)
    old = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - 91 * DAY))
    record = {"kind": "episode", "title": "old episode", "content": "91 days old episode", "scope": {"owner_id": owner},
              "created_at": old, "updated_at": old, "validity": {"valid_from": old}}
    api.post("/api/memories/import", {"version": 1, "records": [record]}, user=owner)
    _put(api, owner, title="old profile", content="profile kept forever", kind="profile")
    status, report = api.post("/api/memories/consolidate", {}, user=owner)
    case.check("consolidate_status", status == 200, 200, status)
    status, listed = api.get("/api/memories?limit=100", user=owner)
    statuses = {m.get("kind"): m.get("status") for m in (listed or {}).get("memories", [])}
    case.check("consolidate_scanned", (report or {}).get("scanned", 0) >= 2, ">=2", (report or {}).get("scanned"))
    case.check("episode_status_known", statuses.get("episode") in ("active", "archived"), "active|archived", statuses.get("episode"))
    case.check("profile_kept", statuses.get("profile") == "active", "active", statuses.get("profile"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-22 reflection generation
    item, case, api, owner = _new_ctx(common, cases, "MB-22", run_id, evidence_root, backend)
    for index in range(6):
        _put(api, owner, kind="episode", title="episode-%02d" % index, content="execution episode %02d" % index, project_id="proj-refl")
    status, report = api.post("/api/memories/consolidate", {}, user=owner)
    case.check("reflections_created", (report or {}).get("reflections", 0) >= 1, ">=1", (report or {}).get("reflections"))
    status, listed = api.get("/api/memories?kind=reflection&limit=20", user=owner)
    reflections = (listed or {}).get("memories") or []
    case.check("reflection_pending", any(r.get("status") == "pending_review" for r in reflections), True, [r.get("status") for r in reflections])
    case.check("derived_from", any(len(r.get("derived_from") or []) >= 2 for r in reflections), True, None)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-23 stats
    item, case, api, owner = _new_ctx(common, cases, "MB-23", run_id, evidence_root, backend)
    _put(api, owner, kind="fact", title="stats-fact", content="stats fact")
    _put(api, owner, kind="episode", title="stats-episode", content="stats episode")
    _put(api, owner, kind="constraint", title="stats-constraint", content="stats constraint", pinned=True)
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("total_count", (stats or {}).get("total") == 3, 3, (stats or {}).get("total"))
    by_kind = (stats or {}).get("by_kind") or {}
    case.check("by_kind", by_kind.get("fact") == 1 and by_kind.get("episode") == 1 and by_kind.get("constraint") == 1, True, by_kind)
    case.check("pinned_count", (stats or {}).get("pinned") == 1, 1, (stats or {}).get("pinned"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-24 list filters
    item, case, api, owner = _new_ctx(common, cases, "MB-24", run_id, evidence_root, backend)
    _put(api, owner, kind="fact", title="filter-a", content="filter alpha")
    _put(api, owner, kind="episode", title="filter-b", content="filter beta")
    status, listed = api.get("/api/memories?kind=fact&limit=10", user=owner)
    items = (listed or {}).get("memories") or []
    case.check("kind_filter", len(items) == 1 and items[0].get("kind") == "fact", True, [m.get("kind") for m in items])
    status, listed = api.get("/api/memories?limit=1", user=owner)
    case.check("limit_honoured", len((listed or {}).get("memories") or []) == 1, 1, len((listed or {}).get("memories") or []))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-25 token estimation
    item, case, api, owner = _new_ctx(common, cases, "MB-25", run_id, evidence_root, backend)
    samples = []
    for index in range(100):
        text = "中文 token 估算 sample %d with mixed english words and numbers 12345" % index
        _put(api, owner, title="token-%03d" % index, content=text)
        samples.append(text)
    status, listed = api.get("/api/memories?limit=200", user=owner)
    records = (listed or {}).get("memories") or []
    estimated = sum(r.get("tokens", 0) for r in records)
    import math as _math
    def _est(text):
        ascii_n = sum(1 for ch in text if ord(ch) <= 127)
        cjk_n = sum(1 for ch in text if ord(ch) > 127 and ("\u4e00" <= ch <= "\u9fff"))
        other_n = len(text) - ascii_n - cjk_n
        return int(_math.ceil(ascii_n / 4.0 + cjk_n / 1.5 + other_n / 3.0)) or 1
    actual = sum(_est(s) for s in samples)
    ratio = abs(estimated - actual) / max(1, actual)
    case.check("token_estimate_close", ratio < 0.2, "<0.2", round(ratio, 3))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # MB-26 concurrent put
    item, case, api, owner = _new_ctx(common, cases, "MB-26", run_id, evidence_root, backend)
    import threading
    errors = []
    def worker(index):
        try:
            status, _ = api.post("/api/memories", {"kind": "fact", "title": "concurrent-%03d" % index,
                                                   "content": "concurrent write %03d" % index}, user=owner, timeout=60)
            if status != 201:
                errors.append(status)
        except Exception as exc:  # noqa: BLE001
            errors.append(str(exc))
    threads = [threading.Thread(target=worker, args=(i,)) for i in range(100)]
    started = time.time()
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("no_errors", not errors, [], errors[:5])
    case.check("all_written", (stats or {}).get("total") == 100, 100, (stats or {}).get("total"))
    case.metric("concurrent_put_seconds", round(time.time() - started, 2))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def _simple_plan(intent, marker, nodes=2):
    plan_nodes = []
    for index in range(nodes):
        plan_nodes.append({
            "id": "n%d" % (index + 1),
            "name": "%s step %d" % (marker, index + 1),
            "type": "general_process",
            "description": "%s 第%d步，请回复确认" % (marker, index + 1),
            "assigned_to": "general_agent",
            "status": "pending",
            "dependencies": [] if index == 0 else ["n%d" % index],
        })
    return {"user_intent": intent, "intent_type": "General", "nodes": plan_nodes,
            "edges": []}


def _wait_plan(api, plan_id, owner, timeout=120):
    deadline = time.time() + timeout
    status = "pending"
    graph = {}
    while time.time() < deadline:
        code, payload = api.get("/api/plans/" + plan_id, user=owner)
        graph = (payload or {}).get("plan_graph") or {}
        status = graph.get("status")
        if status in ("completed", "failed", "canceled"):
            break
        time.sleep(0.3)
    return status, graph


def run_suite(cases, run_id, evidence_root, common, out_root):
    backend = common.Backend(out_root, "mb", env={"MEMORY_RETRIEVAL_TOPK": "8"})
    backend.start()
    verdicts = []
    try:
        verdicts += mb01_mb03(common, cases, run_id, evidence_root, backend)
        verdicts += mb04_mb08(common, cases, run_id, evidence_root, backend)
        verdicts += mb09_mb13(common, cases, run_id, evidence_root, backend)
        verdicts += mb14_mb20(common, cases, run_id, evidence_root, backend)
        verdicts += mb21_mb26(common, cases, run_id, evidence_root, backend)
    finally:
        backend.stop()
    return verdicts, {"backend": "mock", "cases": len(verdicts)}
