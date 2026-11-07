#!/usr/bin/env python3
"""Standalone CP-25 probe: Chinese 'this paper' wiki fallback on a clean backend."""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common  # noqa: E402


def main():
    out = sys.argv[1]
    run_id = sys.argv[2]
    evidence_root = os.path.join(out, "evidence", run_id)
    backend = common.Backend(out, "cp25-standalone", env={"WIKI_LLM_MODE": "off", "WIKI_PDF_REFS": "true"})
    backend.start()
    api = common.Api(backend, None)
    owner = "stress-cp-cp-25-standalone"
    case = common.Case("CP-25", "CP", "P0", "中文“这篇论文”回退", run_id, evidence_root)
    api.case = case
    try:
        status, first = api.post("/api/wiki/papers", {"title": "中文目标论文", "year": 2024,
                                                      "doi": "10.5555/cn.target",
                                                      "abstract": "本文提出一种新的检索方法",
                                                      "project_id": "proj-cp25"}, user=owner)
        case.check("target_created", status == 201, 201, status)
        status, second = api.post("/api/wiki/papers", {"title": "Unrelated English Paper", "year": 2023,
                                                       "doi": "10.5555/en.unrelated",
                                                       "abstract": "unrelated work",
                                                       "project_id": "proj-cp25"}, user=owner)
        case.check("unrelated_created", status == 201, 201, status)
        marked = backend.prompt_mark()
        status, chat = api.post("/api/chat", {"message": "这篇论文的方法是什么？", "session_id": "s-cp25",
                                              "project_id": "proj-cp25"}, user=owner)
        prompts = backend.mock_prompts()[marked:]
        text = "\n".join(m.get("content", "") for p in prompts for m in p["messages"])
        titles = [title for title in ("中文目标论文", "Unrelated English Paper") if title in text]
        case.metric("papers_in_wiki_context", titles)
        case.check("wiki_context_nonempty", len(titles) >= 1, ">=1", titles)
        case.check("no_unrelated_paper", "Unrelated English Paper" not in titles, True, titles)
        if "Unrelated English Paper" in titles:
            case.info("H-05 confirmed: CJK query fell back to recent papers and injected an unrelated paper")
            case.errors.append("E2")
    finally:
        verdict = case.finish(api, backend, backend.mock_prompts())
        backend.stop()
    print(json.dumps(verdict, ensure_ascii=False)[:400])


if __name__ == "__main__":
    main()
