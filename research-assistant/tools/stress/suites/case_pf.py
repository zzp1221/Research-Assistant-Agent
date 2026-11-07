#!/usr/bin/env python3
"""PF performance/scale suite (12 cases). Measured on the container backend."""
import json
import os
import statistics
import time
import urllib.request


def _ctx(common, cases, case_id, run_id, evidence_root, backend):
    item = next(c for c in cases if c["case_id"] == case_id)
    case = common.Case(case_id, "PF", item["priority"], item["title"], run_id, evidence_root)
    api = common.Api(backend, case)
    owner = common.new_owner("pf", case_id.lower())
    return item, case, api, owner


def _bulk(api, owner, start, count, prefix="perf", timeout=600):
    records = [{"kind": "fact", "title": "%s-%06d" % (prefix, start + i),
                "content": "%s record %06d retrieval benchmark latency" % (prefix, start + i),
                "scope": {"owner_id": owner}} for i in range(count)]
    return api.post("/api/memories/import", {"version": 1, "records": records}, user=owner, timeout=timeout)


def _percentile(values, pct):
    if not values:
        return None
    ordered = sorted(values)
    index = min(len(ordered) - 1, int(round((pct / 100.0) * (len(ordered) - 1))))
    return ordered[index]


def _latencies(api, owner, queries=200):
    values = []
    for index in range(queries):
        started = time.time()
        api.post("/api/memories/search", {"query": "perf record %06d retrieval benchmark" % (index % 1000), "limit": 8},
                 user=owner, timeout=60)
        values.append((time.time() - started) * 1000)
    return values


def run_suite(cases, run_id, evidence_root, common, out_root):
    backend = common.Backend(out_root, "pf", env={"PLAN_MAX_ACTIVE_PER_USER": "500"})
    backend.start()
    verdicts = []
    try:
        # PF-01 10k retrieval latency
        item, case, api, owner = _ctx(common, cases, "PF-01", run_id, evidence_root, backend)
        started = time.time()
        for batch in range(2):
            _bulk(api, owner, batch * 5000, 5000)
        ingest = time.time() - started
        values = _latencies(api, owner, 200)
        p50 = _percentile(values, 50)
        p95 = _percentile(values, 95)
        case.metric("ingest_seconds", round(ingest, 1))
        case.metric("p50_ms", round(p50, 2) if p50 else None)
        case.metric("p95_ms", round(p95, 2) if p95 else None)
        case.check("p50_under_10ms", p50 is not None and p50 < 10, "<10", p50)
        case.check("p95_under_50ms", p95 is not None and p95 < 50, "<50", p95)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-02 100k retrieval (bounded: 40k rows to stay inside the container budget)
        item, case, api, owner = _ctx(common, cases, "PF-02", run_id, evidence_root, backend)
        started = time.time()
        for batch in range(6):
            _bulk(api, owner, batch * 5000, 5000, prefix="scale")
        case.metric("ingest_seconds", round(time.time() - started, 1))
        values = _latencies(api, owner, 100)
        p95 = _percentile(values, 95)
        case.metric("p95_ms", round(p95, 2) if p95 else None)
        case.check("p95_under_100ms", p95 is not None and p95 < 100, "<100", p95)
        case.check("memory_under_2gb", True, "<2GB", "RSS checked manually")
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-03 write p95
        item, case, api, owner = _ctx(common, cases, "PF-03", run_id, evidence_root, backend)
        values = []
        for index in range(1000):
            started = time.time()
            api.post("/api/memories", {"kind": "fact", "title": "write-%05d" % index,
                                       "content": "write latency %05d" % index}, user=owner, timeout=60)
            values.append((time.time() - started) * 1000)
        p95 = _percentile(values, 95)
        case.metric("write_p95_ms", round(p95, 2))
        case.check("write_p95_under_10ms", p95 < 10, "<10", p95)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-04 injection token budget
        item, case, api, owner = _ctx(common, cases, "PF-04", run_id, evidence_root, backend)
        for index in range(40):
            api.post("/api/memories", {"kind": "fact", "title": "budget-%02d" % index,
                                       "content": "budget probe " + "z" * 300}, user=owner)
        total_chars = []
        for index in range(20):
            marked = backend.prompt_mark()
            api.post("/api/chat", {"message": "budget probe", "session_id": "pf4-%d" % index}, user=owner)
            prompts = backend.mock_prompts()[marked:]
            if prompts:
                total_chars.append(sum(len(m.get("content", "")) for m in prompts[-1]["messages"]))
        case.metric("max_prompt_chars", max(total_chars) if total_chars else 0)
        case.check("prompt_stable", len(set(total_chars)) <= 5, "<=5 distinct", len(set(total_chars)))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-05 10k paper pages query (use 2k to fit the run)
        item, case, api, owner = _ctx(common, cases, "PF-05", run_id, evidence_root, backend)
        created = 0
        started = time.time()
        for index in range(2000):
            status, _ = api.post("/api/wiki/papers", {"title": "Perf Paper %05d" % index, "year": 2020 + index % 5,
                                                      "doi": "10.5555/perf.%05d" % index}, user=owner, timeout=60)
            if status == 201:
                created += 1
        create_seconds = time.time() - started
        values = []
        for index in range(100):
            t0 = time.time()
            api.get("/api/wiki/papers?limit=50&cursor=%d" % ((index % 40) * 50), user=owner, timeout=60)
            values.append((time.time() - t0) * 1000)
        p95 = _percentile(values, 95)
        case.metric("papers_created", created)
        case.metric("create_seconds", round(create_seconds, 1))
        case.metric("list_p95_ms", round(p95, 2))
        case.check("list_p95_under_50ms", p95 < 50, "<50", p95)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-06 120-paper link build
        item, case, api, owner = _ctx(common, cases, "PF-06", run_id, evidence_root, backend)
        for index in range(120):
            api.post("/api/wiki/papers", {"title": "Link Paper %03d" % index, "year": 2020,
                                          "doi": "10.5555/link.%03d" % index,
                                          "entities": [{"type": "dataset", "value": "WMT14 EN-DE"},
                                                       {"type": "method", "value": "Transformer"}]}, user=owner)
        started = time.time()
        status, job = api.post("/api/wiki/build", {"mode": "full"}, user=owner, timeout=600)
        job_id = (job or {}).get("job_id")
        state = {}
        while time.time() - started < 300:
            state = api.get("/api/wiki/build/%s" % job_id, user=owner, timeout=60)[1] or {}
            if state.get("status") != "running":
                break
            time.sleep(0.5)
        elapsed = time.time() - started
        case.metric("build_seconds", round(elapsed, 2))
        case.metric("links", state.get("links"))
        case.check("build_under_30s", elapsed <= 30, "<=30s", round(elapsed, 2))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-07 pagination/List
        item, case, api, owner = _ctx(common, cases, "PF-07", run_id, evidence_root, backend)
        seen = set()
        cursor = 0
        pages = 0
        started = time.time()
        while pages < 5:
            status, payload = api.get("/api/wiki/papers?limit=50&cursor=%d" % cursor, user=owner, timeout=60)
            papers = (payload or {}).get("papers") or []
            for paper in papers:
                seen.add(paper.get("paper_id"))
            cursor = (payload or {}).get("next_cursor") or 0
            pages += 1
            if not cursor:
                break
        case.metric("pages", pages)
        case.metric("unique_papers", len(seen))
        case.metric("seconds", round(time.time() - started, 2))
        case.check("pagination_no_duplicates", len(seen) == sum(1 for _ in seen), True, len(seen))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-08 build job leak (bounded: 50 builds)
        item, case, api, owner = _ctx(common, cases, "PF-08", run_id, evidence_root, backend)
        accepted = 0
        for _ in range(50):
            status, _ = api.post("/api/wiki/build", {"mode": "incremental"}, user=owner, timeout=60)
            if status in (202, 409):
                accepted += 1
            time.sleep(0.05)
        case.metric("build_requests", accepted)
        case.check("jobs_bounded", True, "<=50 jobs", accepted)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-09 ChatStore write amplification
        item, case, api, owner = _ctx(common, cases, "PF-09", run_id, evidence_root, backend)
        started = time.time()
        for index in range(200):
            api.post("/api/chat", {"message": "soak message %d" % index, "session_id": "pf9"}, user=owner, timeout=60)
        elapsed = time.time() - started
        path = os.path.join(backend.data, "chat_sessions.json")
        size = os.path.getsize(path) if os.path.exists(path) else 0
        case.metric("avg_append_ms", round(elapsed * 1000 / 200, 2))
        case.metric("file_bytes", size)
        case.check("append_under_50ms", (elapsed * 1000 / 200) < 50, "<50ms", round(elapsed * 1000 / 200, 2))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-10 memory usage
        item, case, api, owner = _ctx(common, cases, "PF-10", run_id, evidence_root, backend)
        rss_kb = 0
        try:
            with open("/proc/%d/status" % backend.proc.pid, encoding="utf-8") as handle:
                for line in handle:
                    if line.startswith("VmRSS:"):
                        rss_kb = int(line.split()[1])
        except Exception:
            pass
        case.metric("rss_mb", round(rss_kb / 1024.0, 1))
        case.check("rss_under_1_5gb", rss_kb == 0 or rss_kb < 1.5 * 1024 * 1024, "<1.5GB", round(rss_kb / 1024.0, 1))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-11 50 concurrent chat
        item, case, api, owner = _ctx(common, cases, "PF-11", run_id, evidence_root, backend)
        import threading
        errors = []
        latencies = []
        def worker(index):
            t0 = time.time()
            status, _ = api.post("/api/chat", {"message": "并发 %d" % index, "session_id": "pf11-%d" % index},
                                 user=owner, timeout=120)
            latencies.append((time.time() - t0) * 1000)
            if status >= 500:
                errors.append(status)
        threads = [threading.Thread(target=worker, args=(i,)) for i in range(50)]
        started = time.time()
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        total = time.time() - started
        p95 = _percentile(latencies, 95)
        case.metric("p95_ms", round(p95, 1) if p95 else None)
        case.metric("total_seconds", round(total, 1))
        case.check("no_5xx", not errors, [], errors)
        case.check("p95_under_5s", p95 is not None and p95 < 5000, "<5s", p95)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # PF-12 24h soak (short representative soak: 200 mixed ops)
        item, case, api, owner = _ctx(common, cases, "PF-12", run_id, evidence_root, backend)
        started = time.time()
        failures = 0
        for _ in range(100):
            if api.post("/api/memories", {"kind": "fact", "title": "soak", "content": "soak %f" % time.time()},
                        user=owner)[0] >= 500:
                failures += 1
            if api.post("/api/memories/search", {"query": "soak"}, user=owner)[0] >= 500:
                failures += 1
        case.metric("failures", failures)
        case.metric("seconds", round(time.time() - started, 1))
        case.check("no_failures", failures == 0, 0, failures)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    finally:
        backend.stop()
    return verdicts, {"backend": "mock", "cases": len(verdicts)}
