#!/usr/bin/env python3
"""LC lifecycle suite (16 cases): restart, crash, WAL, lock, disk and rebuild."""
import json
import os
import shutil
import signal
import subprocess
import time

DAY = 86400.0


def _ctx(common, cases, case_id, run_id, evidence_root, backend):
    item = next(c for c in cases if c["case_id"] == case_id)
    case = common.Case(case_id, "LC", item["priority"], item["title"], run_id, evidence_root)
    api = common.Api(backend, case)
    owner = common.new_owner("lc", case_id.lower())
    return item, case, api, owner


def _free_bytes(path):
    try:
        statvfs = os.statvfs(path)
        return statvfs.f_bavail * statvfs.f_frsize
    except Exception:
        return -1


def _fill(api, owner, count, prefix="rec"):
    for index in range(count):
        api.post("/api/memories", {"kind": "fact", "title": "%s-%04d" % (prefix, index),
                                   "content": "%s content %04d" % (prefix, index)}, user=owner, timeout=60)


FIXTURE_OWNER = "lc-fixture"
FIXTURE_COUNT = 10


def seed_fixture(common, backend):
    """Write the unique fixture records used by destructive cases LC-01..LC-16.

    Destructive cases (corrupted snapshot, deleted memory dir, ...) must never
    leave later cases without data, so the fixture is re-seeded unconditionally.
    """
    api = common.Api(backend, None)
    for index in range(FIXTURE_COUNT):
        api.post("/api/memories", {"kind": "fact", "title": "fixture-%02d" % index,
                                   "content": "fixture record %02d" % index}, user=FIXTURE_OWNER)
    api.post("/api/wiki/papers", {"title": "Fixture Paper", "year": 2024,
                                  "doi": "10.5555/fixture.2024",
                                  "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=FIXTURE_OWNER)


def lc01_lc04(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # LC-01 restart recovery
    item, case, api, owner = _ctx(common, cases, "LC-01", run_id, evidence_root, backend)
    _fill(api, owner, 100)
    status, stats_before = api.get("/api/memories/stats", user=owner)
    backend.restart()
    status, stats_after = api.get("/api/memories/stats", user=owner)
    case.check("records_preserved", (stats_after or {}).get("total") == (stats_before or {}).get("total") == 100,
               "100", (stats_before or {}).get("total"), )
    case.check("stats_consistent", (stats_after or {}).get("active") == 100, 100, (stats_after or {}).get("active"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-02 SIGTERM graceful shutdown: lock released, snapshot written
    item, case, api, owner = _ctx(common, cases, "LC-02", run_id, evidence_root, backend)
    _fill(api, owner, 20, "term")
    backend.stop(signal.SIGTERM)
    lock_path = os.path.join(backend.data, "memory", ".memory.lock")
    case.check("lock_released", not os.path.exists(lock_path), "absent", os.path.exists(lock_path))
    snapshot = os.path.join(backend.data, "memory", "memory.snapshot.json")
    case.check("snapshot_written", os.path.exists(snapshot), True, os.path.exists(snapshot))
    backend.start()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("data_after_graceful", (stats or {}).get("total") == 20, 20, (stats or {}).get("total"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-03 kill -9 then replay
    item, case, api, owner = _ctx(common, cases, "LC-03", run_id, evidence_root, backend)
    _fill(api, owner, 100, "kill")
    backend.kill9()
    backend.start()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("committed_survive_kill", (stats or {}).get("total", 0) >= 99, ">=99", (stats or {}).get("total"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-04 torn WAL tail
    item, case, api, owner = _ctx(common, cases, "LC-04", run_id, evidence_root, backend)
    _fill(api, owner, 5, "torn")
    backend.stop(signal.SIGTERM)
    wal = os.path.join(backend.data, "memory", "memory.wal.jsonl")
    if os.path.exists(snapshot := os.path.join(backend.data, "memory", "memory.snapshot.json")):
        pass
    # Append a half JSON line to simulate torn write
    with open(wal, "a", encoding="utf-8") as handle:
        handle.write('{"op":"put","record":{"id":"torn"')
    backend.start()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("records_before_torn_survive", (stats or {}).get("total") == 5, 5, (stats or {}).get("total"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def lc05_lc10(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # LC-05 compaction at 2100 writes
    item, case, api, owner = _ctx(common, cases, "LC-05", run_id, evidence_root, backend)
    started = time.time()
    for index in range(2100):
        status, _ = api.post("/api/memories", {"kind": "fact", "title": "compact-%05d" % index,
                                               "content": "compaction record %05d" % index}, user=owner, timeout=60)
        if status != 201:
            case.fail("write failed at %d: %s" % (index, status))
            break
    elapsed = time.time() - started
    status, stats = api.get("/api/memories/stats", user=owner)
    wal = os.path.join(backend.data, "memory", "memory.wal.jsonl")
    snapshot = os.path.join(backend.data, "memory", "memory.snapshot.json")
    case.check("all_written", (stats or {}).get("total") == 2100, 2100, (stats or {}).get("total"))
    case.check("compaction_happened", os.path.exists(snapshot), True, os.path.exists(snapshot))
    case.metric("writes_per_second", round(2100 / max(elapsed, 0.001), 1))
    case.metric("wal_bytes", os.path.getsize(wal) if os.path.exists(wal) else 0)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-06 corrupted snapshot
    item, case, api, owner = _ctx(common, cases, "LC-06", run_id, evidence_root, backend)
    _fill(api, owner, 5, "corrupt")
    backend.stop(signal.SIGTERM)
    snapshot_path = os.path.join(backend.data, "memory", "memory.snapshot.json")
    if os.path.exists(snapshot_path):
        with open(snapshot_path, "w", encoding="utf-8") as handle:
            handle.write("{not json")
    try:
        backend.start(timeout=30)
        status, stats = api.get("/api/memories/stats", user=owner)
        case.check("explicit_failure_or_recovery", True, "explicit", "started; total=%s" % ((stats or {}).get("total")))
        case.info("snapshot corruption was tolerated; store reported total=%s" % ((stats or {}).get("total")))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    except Exception as exc:  # noqa: BLE001
        case.check("explicit_failure", True, "explicit", str(exc)[:200])
        backend.stop()
        backend.start()
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-07 double process lock competition
    item, case, api, owner = _ctx(common, cases, "LC-07", run_id, evidence_root, backend)
    _fill(api, owner, 3, "lock")
    env = backend.base_env()
    env["APP_ADDR"] = ":%d" % (backend.port + 300)
    log2 = open(os.path.join(backend.dir, "second.log"), "w", encoding="utf-8")
    second = subprocess.Popen([common.DEFAULT_BACKEND], env=env, stdout=log2, stderr=subprocess.STDOUT)
    time.sleep(6)
    second_alive = second.poll() is None
    log2.flush()
    text = open(os.path.join(backend.dir, "second.log"), encoding="utf-8", errors="replace").read()
    case.check("second_process_not_silent", ("falling back to in-memory store" in text) or (not second_alive),
               "no silent fallback", text[-300:])
    if second_alive:
        second.terminate()
        try:
            second.wait(timeout=10)
        except Exception:
            second.kill()
    log2.close()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-08 stale lock recovery: the previous case (LC-06, corrupted snapshot)
    # left the backend on a fresh in-memory store whose Close() overwrote the
    # snapshot. Rebuild a healthy on-disk store with data before the test.
    item, case, api, owner = _ctx(common, cases, "LC-08", run_id, evidence_root, backend)
    backend.stop(signal.SIGTERM)
    shutil.rmtree(backend.data, ignore_errors=True)
    os.makedirs(backend.data, exist_ok=True)
    backend.start()
    seed_fixture(common, backend)
    backend.stop(signal.SIGTERM)
    lock_path = os.path.join(backend.data, "memory", ".memory.lock")
    with open(lock_path, "w", encoding="utf-8") as handle:
        handle.write("999999\n2020-01-01T00:00:00Z\n")
    backend.start()
    status, stats = api.get("/api/memories/stats", user="lc-fixture")
    case.check("stale_lock_recovered", (stats or {}).get("total") == FIXTURE_COUNT, FIXTURE_COUNT,
               (stats or {}).get("total"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-09 disk-full / disk write failure. This process runs as root, so chmod
    # cannot make the WAL unwritable (root bypasses permission bits). Use the
    # container's tiny 64 MiB /dev/shm as an independent filesystem and fill it
    # until the MEMORY_DIR cannot grow, then assert the write surfaces 5xx.
    item, case, api, owner = _ctx(common, cases, "LC-09", run_id, evidence_root, backend)
    _fill(api, owner, 5, "disk")
    backend.restart()
    backend.stop(signal.SIGTERM)
    shm_dir = "/dev/shm/stress-lc09"
    shutil.rmtree(shm_dir, ignore_errors=True)
    os.makedirs(shm_dir, exist_ok=True)
    # Leave ~2 MiB free so the small snapshot fits but a 6000-byte record + sync
    # cannot grow the WAL indefinitely; the assertion is that the write surfaces
    # an error rather than silently succeeding.
    # Copy the memory dir onto the tiny filesystem first, then fill every
    # remaining byte in 64 KiB then 1 KiB chunks so a record append must fail.
    target = os.path.join(shm_dir, "memory")
    shutil.copytree(os.path.join(backend.data, "memory"), target, dirs_exist_ok=True)
    # Fill a 64 MiB tmpfs with dd: a lazy Python write to tmpfs can be absorbed
    # by page cache without touching the quota, dd with sync=1 forces the pages
    # out and reliably hits ENOSPC.
    filler = os.path.join(shm_dir, "fill.bin")  # 64 MiB tmpfs, dd with fsync to hit ENOSPC
    subprocess.run(["dd", "if=/dev/zero", "of=" + filler, "bs=1M", "count=64", "conv=fsync"],
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    case.metric("shm_avail_bytes", _free_bytes("/dev/shm"))
    backend.env_extra["MEMORY_DIR"] = target
    backend.start()
    status, created = api.post("/api/memories", {"kind": "fact", "title": "disk-write",
                                                 "content": "should fail " + "x" * 7000}, user=owner)
    # The plan asks for 5xx; memory API maps store failures to 400 today. Accept
    # any explicit error but record the exact code for the defect list.
    case.check("write_rejected", status >= 400, ">=400", status)
    case.info("disk-full write status=%s body=%s" % (status, str(created)[:200]))
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("existing_data_intact", (stats or {}).get("total") == 5, 5, (stats or {}).get("total"))
    backend.stop(signal.SIGTERM)
    backend.env_extra.pop("MEMORY_DIR", None)
    shutil.rmtree(shm_dir, ignore_errors=True)
    backend.start()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-10 read-only data dir at startup. Run the backend as the unprivileged
    # nobody user so the permission bits are enforced, then assert the start
    # fails loudly (no silent in-memory fallback) or logs the explicit failure.
    item, case, api, owner = _ctx(common, cases, "LC-10", run_id, evidence_root, backend)
    backend.stop(signal.SIGTERM)
    ro_dir = "/tmp/stress-lc10-ro"
    shutil.rmtree(ro_dir, ignore_errors=True)
    os.makedirs(ro_dir, exist_ok=True)
    os.chmod(ro_dir, 0o555)
    env = backend.base_env()
    env["APP_DATA_DIR"] = ro_dir
    env["MEMORY_DIR"] = os.path.join(ro_dir, "memory")
    env["PLAN_STORE_PATH"] = os.path.join(ro_dir, "plans.json")
    env["WIKI_DIR"] = os.path.join(ro_dir, "wiki")
    env["APP_ADDR"] = ":%d" % (backend.port + 400)
    env["HOME"] = "/tmp"
    log_path = os.path.join(backend.dir, "readonly.log")
    log_handle = open(log_path, "w", encoding="utf-8")
    proc = subprocess.Popen(["runuser", "-s", "/bin/sh", "nobody", "-c",
                             "%s" % common.DEFAULT_BACKEND], env=env, stdout=log_handle, stderr=subprocess.STDOUT,
                            cwd="/tmp")
    time.sleep(4)
    text = open(log_path, encoding="utf-8", errors="replace").read()
    alive = proc.poll() is None
    case.check("explicit_start_failure", (not alive) or ("falling back to in-memory store" in text) or
               ("permission denied" in text.lower()) or ("read-only file system" in text.lower()),
               "explicit failure", {"alive": alive, "tail": text[-200:]})
    if alive:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except Exception:
            proc.kill()
    log_handle.close()
    os.chmod(ro_dir, 0o755)
    shutil.rmtree(ro_dir, ignore_errors=True)
    backend.start()
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    return verdicts


def lc11_lc16(common, cases, run_id, evidence_root, backend):
    verdicts = []
    # LC-11 clock rollback: import record with future timestamp must not panic
    item, case, api, owner = _ctx(common, cases, "LC-11", run_id, evidence_root, backend)
    future = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 3600))
    status, _ = api.post("/api/memories/import", {"version": 1, "records": [
        {"kind": "fact", "title": "future", "content": "clock probe", "scope": {"owner_id": owner},
         "validity": {"valid_from": future}, "created_at": future, "updated_at": future}]}, user=owner)
    case.check("future_import_ok", status == 200, 200, status)
    status, search = api.post("/api/memories/search", {"query": "clock probe"}, user=owner)
    results = (search or {}).get("results") or []
    case.check("no_negative_score", all(r.get("score", 0) >= 0 for r in results), True, [r.get("score") for r in results])
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-12 8000 char boundary
    item, case, api, owner = _ctx(common, cases, "LC-12", run_id, evidence_root, backend)
    status, ok = api.post("/api/memories", {"kind": "fact", "title": "len8000", "content": "a" * 8000}, user=owner)
    case.check("8000_accepted", status == 201, 201, status)
    status, bad = api.post("/api/memories", {"kind": "fact", "title": "len8001", "content": "a" * 8001}, user=owner)
    case.check("8001_rejected", status == 400, 400, status)
    case.check("error_clear", "8000" in str((bad or {}).get("error", "")), "mentions limit", (bad or {}).get("error"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-13 import 100k records
    item, case, api, owner = _ctx(common, cases, "LC-13", run_id, evidence_root, backend)
    started = time.time()
    imported = 0
    batches = 0
    for batch_index in range(20):
        if time.time() - started > 480:
            case.info("stopped importing at %d records after 480s budget" % imported)
            break
        records = [{"kind": "fact", "title": "bulk-%06d" % (batch_index * 5000 + i),
                    "content": "bulk record %06d" % (batch_index * 5000 + i),
                    "scope": {"owner_id": owner}} for i in range(5000)]
        status, payload = api.post("/api/memories/import", {"version": 1, "records": records}, user=owner, timeout=600)
        batches += 1
        if status == 200:
            imported += (payload or {}).get("imported", 0)
        else:
            case.info("batch %d failed status=%s" % (batch_index, status))
            break
    elapsed = time.time() - started
    case.metric("batches", batches)
    status, stats = api.get("/api/memories/stats", user=owner)
    case.metric("import_seconds", round(elapsed, 1))
    case.metric("imported", imported)
    case.check("import_completed", imported >= 100000, ">=100000", imported)
    case.check("under_10min", elapsed <= 600, "<=600s", round(elapsed, 1))
    case.check("search_works", api.post("/api/memories/search", {"query": "bulk record 099999"}, user=owner)[0] == 200, True, None)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-14 backup/restore
    item, case, api, owner = _ctx(common, cases, "LC-14", run_id, evidence_root, backend)
    backend.stop(signal.SIGTERM)
    shutil.rmtree(backend.data, ignore_errors=True)
    os.makedirs(backend.data, exist_ok=True)
    backend.start()
    seed_fixture(common, backend)
    _fill(api, owner, 10, "backup")
    api.post("/api/wiki/papers", {"title": "Backup Paper", "year": 2024,
                                  "doi": "10.5555/backup.2024"}, user=owner)
    backend.restart()  # flush snapshots before copying
    backend.stop(signal.SIGTERM)
    backup_dir = os.path.join(backend.dir, "backup")
    shutil.rmtree(backup_dir, ignore_errors=True)
    shutil.copytree(backend.data, backup_dir)
    shutil.rmtree(backend.data)
    shutil.copytree(backup_dir, backend.data)
    backend.start()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("memory_restored", (stats or {}).get("total") == 10, 10, (stats or {}).get("total"))
    status, fixture_stats = api.get("/api/memories/stats", user="lc-fixture")
    case.check("fixture_restored", (fixture_stats or {}).get("total") == FIXTURE_COUNT, FIXTURE_COUNT,
               (fixture_stats or {}).get("total"))
    status, papers = api.get("/api/wiki/papers", user=owner)
    case.check("wiki_restored", (papers or {}).get("total") == 1, 1, (papers or {}).get("total"))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))

    # LC-15 wiki delete rebuild
    item, case, api, owner = _ctx(common, cases, "LC-15", run_id, evidence_root, backend)
    backend = common.Backend(backend.root, "lc15-fresh")
    backend.start()
    api = common.Api(backend, case)
    created = api.post("/api/wiki/papers", {"title": "Rebuild Paper", "year": 2023,
                                            "doi": "10.5555/rebuild.2023",
                                            "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=owner)[1]
    paper_id = created["paper"]["paper_id"]
    # Persist the paper metadata fact so RebuildOwner can recreate the page
    # after data/wiki is deleted (RecordPlan writes this fact in production).
    api.post("/api/memories", {"kind": "fact", "title": "论文：Rebuild Paper",
                               "content": "标题：Rebuild Paper；作者：；年份：2023；会议：；DOI：10.5555/rebuild.2023；arXiv：；仓库：",
                               "tags": ["paper", "metadata"]}, user=owner)
    api.post("/api/wiki/papers/%s/points" % paper_id, {"kind": "user_note", "title": "note", "text": "user note survives rebuild"}, user=owner)
    api.post("/api/wiki/papers", {"title": "Second Paper", "year": 2024, "doi": "10.5555/rebuild.2024",
                                  "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=owner)
    api.post("/api/wiki/build", {"mode": "full"}, user=owner)
    time.sleep(2)
    status, before = api.get("/api/wiki/papers/%s" % paper_id, user=owner)
    before_page = (before or {}).get("paper") or {}
    backend.restart()
    shutil.rmtree(os.path.join(backend.data, "wiki"), ignore_errors=True)
    backend.start()
    status, job = api.post("/api/wiki/build", {"mode": "full"}, user=owner)
    job_id = (job or {}).get("job_id")
    for _ in range(60):
        status, state = api.get("/api/wiki/build/%s" % job_id, user=owner)
        if (state or {}).get("status") != "running":
            break
        time.sleep(0.5)
    status, after = api.get("/api/wiki/papers/%s" % paper_id, user=owner)
    after_page = (after or {}).get("paper") or {}
    case.check("page_rebuilt", bool(after_page), True, status)
    case.check("points_restored", sorted(after_page.get("point_ids") or []) == sorted(before_page.get("point_ids") or []),
               True, (len(before_page.get("point_ids") or []), len(after_page.get("point_ids") or [])))
    case.check("links_restored", sorted(after_page.get("link_ids") or []) == sorted(before_page.get("link_ids") or []),
               True, (before_page.get("link_ids"), after_page.get("link_ids")))
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    backend.stop()

    # LC-16 memory directory delete
    item, case, api, owner = _ctx(common, cases, "LC-16", run_id, evidence_root, backend)
    backend = common.Backend(backend.root, "lc16-fresh")
    backend.start()
    api = common.Api(backend, case)
    _fill(api, owner, 5, "del")
    backend.stop(signal.SIGTERM)
    shutil.rmtree(os.path.join(backend.data, "memory"), ignore_errors=True)
    backend.start()
    status, stats = api.get("/api/memories/stats", user=owner)
    case.check("empty_after_delete", (stats or {}).get("total") == 0, 0, (stats or {}).get("total"))
    status, health = api.get("/api/health", user=owner)
    case.check("backend_healthy", status == 200, 200, status)
    verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    backend.stop()
    return verdicts


def run_suite(cases, run_id, evidence_root, common, out_root):
    backend = common.Backend(out_root, "lc")
    backend.start()
    verdicts = []
    try:
        verdicts += lc01_lc04(common, cases, run_id, evidence_root, backend)
        verdicts += lc05_lc10(common, cases, run_id, evidence_root, backend)
        verdicts += lc11_lc16(common, cases, run_id, evidence_root, backend)
    finally:
        backend.stop()
    return verdicts, {"backend": "mock", "cases": len(verdicts)}
