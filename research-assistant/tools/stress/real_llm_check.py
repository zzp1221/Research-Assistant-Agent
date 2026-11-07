#!/usr/bin/env python3
"""Supplemental real-LLM quality checks (model deepseek-v4.1-flash).

The main suites use the deterministic mock for structural assertions. These
checks exercise the real provider on the three quality-critical paths:
memory recall in chat, long-paper question answering, and cross-paper relation
answering. Results are written as evidence under a separate run id.
"""
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common  # noqa: E402

REAL_PORT = int(os.environ.get("REAL_PORT", "18400"))
BASE = "http://127.0.0.1:%d" % REAL_PORT
ROOT = "/tmp/stress-real"
MODEL = os.environ.get("REAL_LLM_MODEL", "deepseek-v4.1-flash")
KEY = os.environ.get("REAL_LLM_API_KEY", "")
URL = os.environ.get("REAL_LLM_BASE_URL", "https://api.ai-pixel.online/v1")


def main():
    if not KEY.strip():
        raise SystemExit("Set REAL_LLM_API_KEY before running real-LLM checks")
    run_id = sys.argv[1] if len(sys.argv) > 1 else "stress-2026-11-03-real-llm"
    evidence_root = os.path.join(ROOT, "evidence", run_id)
    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(os.path.join(ROOT, "data"), exist_ok=True)
    env = dict(os.environ)
    env.update({
        "APP_ADDR": ":%d" % REAL_PORT,
        "APP_DATA_DIR": os.path.join(ROOT, "data"),
        "PLAN_STORE_PATH": os.path.join(ROOT, "data", "plans.json"),
        "MEMORY_DIR": os.path.join(ROOT, "data", "memory"),
        "WIKI_DIR": os.path.join(ROOT, "data", "wiki"),
        "MEMORY_ENABLED": "true", "MEMORY_MODE": "on",
        "WIKI_ENABLED": "true", "WIKI_MODE": "on",
        "WIKI_LLM_MODE": "off", "WIKI_PDF_REFS": "true",
        "ALLOW_LEGACY_USER_HEADER": "true", "REQUIRE_API_AUTH": "false",
        "API_AUTH_TOKEN": "", "GIN_MODE": "release",
        "OPENAI_BASE_URL": URL, "OPENAI_API_KEY": KEY, "OPENAI_MODEL_NAME": MODEL,
        "BENCHMARK_PRIVATE_ROOT": os.path.join(ROOT, "private"),
        "MODEL_PROFILE_ENCRYPTION_KEY": "cmVzZWFyY2gtYXNzaXN0YW50LW1vZGVsLXByb2ZpbGUta2V5LTE=",
    })
    log = open(os.path.join(ROOT, "backend.log"), "w", encoding="utf-8")
    backend = subprocess.Popen([common.DEFAULT_BACKEND], env=env, stdout=log, stderr=subprocess.STDOUT)
    verdicts = []
    try:
        if not wait_health():
            raise SystemExit("real-LLM backend did not become healthy")
        owner = "stress-real-main"
        print("[real] backend healthy", flush=True)
        api = common.Api(None)
        api.backend = type("B", (), {"base": BASE})()

        # --- R1: chat memory recall with citations (LT-01/02 evidence)
        case = common.Case("REAL-LT-01", "LT", "P0", "真实 LLM 长对话记忆召回（补充证据）", run_id, evidence_root)
        api.case = case
        status, created = api.post("/api/memories", {"kind": "profile", "title": "用户与项目基线",
                                                     "content": "用户叫小明；项目 WMT14 EN-DE 基线 BLEU 27.3；环境 PyTorch 2.4 + A100 40GB",
                                                     "pinned": True, "importance": 10}, user=owner)
        case.check("seed_memory", status == 201, 201, status)
        first = api.post("/api/chat", {"message": "我叫什么？项目基线 BLEU 是多少？请简短回答。", "session_id": "real-chat"}, user=owner, timeout=300)[1]
        answer1 = str((first or {}).get("response", ""))
        case.check("recall_name", "小明" in answer1, True, answer1[:200])
        case.check("recall_baseline", "27.3" in answer1, True, answer1[:200])
        case.check("has_citations", bool((first or {}).get("citations")), True, len((first or {}).get("citations") or []))
        for index in range(2, 27):
            api.post("/api/chat", {"message": "第%d轮：请基于上面的项目背景继续给出简短的复现建议。" % index,
                                   "session_id": "real-chat"}, user=owner, timeout=300)
        last = api.post("/api/chat", {"message": "我最开始说我叫什么？项目基线是多少？", "session_id": "real-chat"}, user=owner, timeout=300)[1]
        answer2 = str((last or {}).get("response", ""))
        case.check("compressed_recall_name", "小明" in answer2, True, answer2[:200])
        case.check("compressed_recall_baseline", "27.3" in answer2, True, answer2[:200])
        verdicts.append(case.finish(api, None, []))

        # --- R2: long-paper QA with real LLM
        case = common.Case("REAL-LP-23", "LP", "P0", "真实 LLM 长论文问答（补充证据）", run_id, evidence_root)
        api.case = case
        survey = "/tmp/stress-tools/survey.pdf"
        if os.path.exists(survey):
            with open(survey, "rb") as handle:
                payload = handle.read()
            status, uploaded = api.upload("/api/uploads", "neural-retrieval-survey.pdf", payload, user=owner, timeout=600)
            upload_id = (uploaded or {}).get("id")
            case.check("upload", status == 201, 201, status)
            status, paper = api.post("/api/wiki/papers", {"title": "Neural Retrieval: A Survey", "year": 2026,
                                                          "doi": "10.5555/real.survey", "document_id": upload_id}, user=owner, timeout=300)
            paper_id = ((paper or {}).get("paper") or {}).get("paper_id")
            case.check("paper_page", status == 201, 201, status)
            answers = []
            checks = []
            for question, needle in (
                    ("这篇论文的第 4.2 节主要讨论什么？请给出章节标题。", "Embedding Spaces"),
                    ("Table 7 的最佳 F1 是多少？如果没有明确数值请说明。", "Table 7"),
                    ("这篇论文的参考文献一共列出多少条？", "120")):
                reply = api.post("/api/chat", {"message": question, "session_id": "real-paper"}, user=owner, timeout=300)[1]
                answer = str((reply or {}).get("response", ""))
                answers.append(answer)
                case.info("Q: %s -> %s" % (question, answer[:200]))
                checks.append((question, needle, answer))
            case.check("q1_section_title", "4.2" in answers[0] or "Embedding Spaces" in answers[0], True, answers[0][:150])
            case.check("q3_reference_count", "120" in answers[2], True, answers[2][:150])
            citations = 0
            # Re-ask one question and keep the citation payload for the record.
            last = api.post("/api/chat", {"message": "这篇论文的参考文献一共列出多少条？", "session_id": "real-paper"},
                            user=owner, timeout=300)[1]
            citations = len((last or {}).get("wiki_citations") or [])
            case.metric("wiki_citations", citations)
            case.check("wiki_citations_present", citations >= 1, ">=1", citations)
        else:
            case.blocked("survey corpus missing")
        verdicts.append(case.finish(api, None, []))

        # --- R3: cross-paper relation answer
        case = common.Case("REAL-CP-17", "CP", "P0", "真实 LLM 跨论文关系问答（补充证据）", run_id, evidence_root)
        api.case = case
        a = api.post("/api/wiki/papers", {"title": "Dense Passage Retrieval", "year": 2020,
                                          "doi": "10.18653/v1/2020.emnlp-main.550",
                                          "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=owner)[1]
        b = api.post("/api/wiki/papers", {"title": "ColBERT", "year": 2020, "doi": "10.1145/3397271.3401075",
                                          "entities": [{"type": "dataset", "value": "WMT14 EN-DE"}]}, user=owner)[1]
        api.post("/api/wiki/build", {"mode": "full"}, user=owner)
        time.sleep(2)
        reply = api.post("/api/chat", {"message": "Dense Passage Retrieval 和 ColBERT 之间是什么关系？请引用论文标题。",
                                       "session_id": "real-cross"}, user=owner, timeout=300)[1]
        answer = str((reply or {}).get("response", ""))
        case.check("mentions_titles", ("Dense Passage Retrieval" in answer) or ("ColBERT" in answer), True, answer[:200])
        citations = len((reply or {}).get("wiki_citations") or [])
        case.metric("wiki_citations", citations)
        case.check("wiki_citations_present", citations >= 1, ">=1", citations)
        verdicts.append(case.finish(api, None, []))
    finally:
        backend.terminate()
        try:
            backend.wait(timeout=15)
        except Exception:
            backend.kill()
        log.close()
    summary = {}
    for verdict in verdicts:
        summary[verdict["status"]] = summary.get(verdict["status"], 0) + 1
    print(json.dumps({"run_id": run_id, "counts": summary, "cases": [v["case_id"] for v in verdicts]}, ensure_ascii=False))


def wait_health(timeout=120):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/api/health", timeout=3) as response:
                if response.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


if __name__ == "__main__":
    main()
