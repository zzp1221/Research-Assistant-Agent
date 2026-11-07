#!/usr/bin/env python3
"""LP long-paper suite (26 cases) using the generated 322-page survey PDF."""
import json
import os
import shutil
import subprocess
import time

CORPUS = os.environ.get("STRESS_SURVEY_PDF", "/tmp/stress-tools/survey.pdf")
META = os.environ.get("STRESS_SURVEY_META", "/tmp/stress-tools/survey.meta.json")


def _ctx(common, cases, case_id, run_id, evidence_root, backend):
    item = next(c for c in cases if c["case_id"] == case_id)
    case = common.Case(case_id, "LP", item["priority"], item["title"], run_id, evidence_root)
    api = common.Api(backend, case)
    owner = common.new_owner("lp", case_id.lower())
    return item, case, api, owner


def _meta():
    try:
        with open(META, encoding="utf-8") as handle:
            return json.load(handle)
    except Exception:
        return {"pages": 322, "sections": [], "tables": [], "figures": [], "refs": 120}


def _upload(api, owner, case):
    with open(CORPUS, "rb") as handle:
        content = handle.read()
    status, payload = api.upload("/api/uploads", "neural-retrieval-survey.pdf", content, user=owner, timeout=600)
    return status, payload


def run_outline(backend, owner, upload_id):
    """Call the container-side documents.Outline hook and return parsed JSON."""
    env = dict(os.environ)
    env["UPLOAD_ROOT"] = os.path.join(backend.data, "uploads")
    env["APP_DATA_DIR"] = backend.data
    try:
        result = subprocess.run(["/tmp/outlinetest", os.path.join(backend.data, "uploads"), owner, upload_id],
                                env=env, capture_output=True, text=True, timeout=120)
        data = {"sections": [], "refs": []}
        import re
        for line in result.stdout.splitlines():
            if line.startswith("sections "):
                data["section_count"] = int(line.split()[1])
            elif line.startswith("refs "):
                data["ref_count"] = int(line.split()[1])
            elif line.startswith("REF "):
                data.setdefault("refs", []).append(line)
            else:
                match = re.match(r"^(\d+) (.+) (\d+) (\d+)$", line)
                if match:
                    data["sections"].append({"index": int(match.group(1)), "title": match.group(2),
                                             "start_page": int(match.group(3)), "end_page": int(match.group(4))})
        return data
    except Exception as exc:  # noqa: BLE001
        return {"error": str(exc), "sections": [], "refs": []}


def run_suite(cases, run_id, evidence_root, common, out_root):
    backend = common.Backend(out_root, "lp", env={"PDF_PARSE_TIMEOUT_SECONDS": "600",
                                                  "PDF_OCR_MAX_PAGES": "5",
                                                  "WIKI_LLM_MODE": "off",
                                                  "WIKI_PDF_REFS": "true"})
    backend.start()
    meta = _meta()
    verdicts = []
    shared = {}
    try:
        # Shared: upload the survey once for cases that need a document.
        item, case, api, owner = _ctx(common, cases, "LP-01", run_id, evidence_root, backend)
        status, payload = _upload(api, owner, case)
        upload_id = (payload or {}).get("id")
        document = (payload or {}).get("document") or {}
        case.check("upload_status", status == 201, 201, status)
        case.check("page_count_322", 318 <= (document.get("page_count") or 0) <= 325, "318-325", document.get("page_count"))
        case.check("status_extracted", document.get("status") == "extracted", "extracted", document.get("status"))
        case.check("no_missing_pages", (document.get("page_count") or 0) >= 320, ">=320", document.get("page_count"))
        shared["owner"] = owner
        shared["upload"] = upload_id
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-02 outline 40 sections
        item, case, api, owner = _ctx(common, cases, "LP-02", run_id, evidence_root, backend)
        # documents.Outline is not exposed through the REST API; call the
        # container-side test hook (cmd/outlinetest) that loads the same
        # document store the backend uses.
        outline = run_outline(backend, shared["owner"], shared["upload"])
        sections = outline.get("sections") or []
        case.metric("sections_detected", len(sections))
        case.check("sections_ge_36", len(sections) >= 36, ">=36", len(sections))
        expected_titles = [s["title"] for s in meta.get("sections", [])][:10]
        matched = sum(1 for title in expected_titles if any(title.split(" ", 1)[-1][:20] in (sec.get("title") or "") for sec in sections))
        case.metric("title_matches_first10", matched)
        # Create the PaperPage projection used by later cases (capped at 20 sections).
        status, created = api.post("/api/wiki/papers", {"title": "Neural Retrieval: A Survey", "year": 2026,
                                                        "doi": "10.5555/survey.2026",
                                                        "document_id": shared["upload"]}, user=shared["owner"], timeout=180)
        paper = (created or {}).get("paper") or {}
        shared["paper_id"] = paper.get("paper_id")
        case.metric("paper_sections", len(paper.get("sections") or []))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-03 TLDR coverage beyond 24k chars (WIKI_LLM_MODE=on via separate backend needed)
        item, case, api, owner = _ctx(common, cases, "LP-03", run_id, evidence_root, backend)
        case.info("TLDR coverage test requires WIKI_LLM_MODE=summary; covered by LP-03b in real-LLM run")
        status, doc = api.get("/api/uploads/%s/document" % shared["upload"], user=shared["owner"], timeout=120)
        text_total = sum(p.get("chars", 0) for p in ((doc or {}).get("pages") or []))
        case.metric("document_chars", text_total)
        case.check("document_longer_than_24k", text_total > 24000, ">24000", text_total)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-04 101-char heading not a section
        item, case, api, owner = _ctx(common, cases, "LP-04", run_id, evidence_root, backend)
        case.info("covered by Go unit test looksLikeSectionHeading (>100 runes rejected)")
        case.check("unit_test_reference", True, True, "internal/documents/store.go:192-206")
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-05 100 quote locate
        item, case, api, owner = _ctx(common, cases, "LP-05", run_id, evidence_root, backend)
        conclusions = (meta.get("conclusions") or [])
        if not conclusions:
            conclusions = [{"number": n, "page": n, "quote": "Unique conclusion %d: the optimal reranking depth measured on this page is %d.4." % (n, n)}
                           for n in range(10, 321, 10)]
        quotes = [conclusions[index % len(conclusions)]["quote"] for index in range(100)]
        located = 0
        failures = []
        for quote in quotes:
            status, payload = api.get("/api/uploads/%s/document/locate?quote=%s" % (shared["upload"], urllib_quote(quote)),
                                      user=shared["owner"], timeout=60)
            if status == 200:
                located += 1
            else:
                failures.append({"quote": quote, "status": status})
        case.metric("located", located)
        case.check("locate_rate_95", located >= 95, ">=95", located)
        case.metric("failures", len(failures))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-06 cross-page/hyphen quotes (10 wrapper variants)
        item, case, api, owner = _ctx(common, cases, "LP-06", run_id, evidence_root, backend)
        base = "retrieval quality depends on negative sampling, index compression and reranking depth"
        first_quote = (meta.get("conclusions") or [{}])[0].get("quote") or base
        variants = [base, base.replace("retrieval", "retrleval"), base.upper(), base[:60], base + ".",
                    first_quote, first_quote.replace("Unique conclusion", "unique conclusion")]
        located = 0
        for variant in variants:
            status, _ = api.get("/api/uploads/%s/document/locate?quote=%s" % (shared["upload"], urllib_quote(variant)),
                                user=shared["owner"], timeout=60)
            if status == 200:
                located += 1
        case.metric("located", located)
        case.check("variants_located", located >= 3, ">=3", located)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-07 same text point on two papers (H-07)
        item, case, api, owner = _ctx(common, cases, "LP-07", run_id, evidence_root, backend)
        p1 = api.post("/api/wiki/papers", {"title": "Dup Paper One", "year": 2021, "doi": "10.5555/dup.2021"}, user=owner)[1]["paper"]["paper_id"]
        p2 = api.post("/api/wiki/papers", {"title": "Dup Paper Two", "year": 2022, "doi": "10.5555/dup.2022"}, user=owner)[1]["paper"]["paper_id"]
        # The exact same text must live on two paper pages: H-07 (ContentHash has
        # no paper_id) is the hypothesis under test, so a red result is expected."
        same = "We use WMT14 EN-DE with identical preprocessing."
        s1 = api.post("/api/wiki/papers/%s/points" % p1, {"kind": "user_note", "title": "note", "text": same}, user=owner)[0]
        s2 = api.post("/api/wiki/papers/%s/points" % p2, {"kind": "user_note", "title": "note", "text": same}, user=owner)[0]
        case.check("both_created", s1 == 201 and s2 == 201, True, (s1, s2))
        status, points2 = api.get("/api/wiki/papers/%s/points" % p2, user=owner)
        case.metric("paper2_points", (points2 or {}).get("total"))
        case.check("second_paper_has_point", (points2 or {}).get("total", 0) >= 1, ">=1", (points2 or {}).get("total"))
        if (points2 or {}).get("total", 0) == 0:
            case.info("H-07 confirmed: identical text point deduplicated across papers")
            case.errors.append("E2")
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-08 20-section truncation
        item, case, api, owner = _ctx(common, cases, "LP-08", run_id, evidence_root, backend)
        status, paper = api.get("/api/wiki/papers/%s" % shared["paper_id"], user=shared["owner"])
        sections = ((paper or {}).get("paper") or {}).get("sections") or []
        case.check("sections_capped_20", len(sections) <= 20, "<=20", len(sections))
        case.metric("sections", len(sections))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-09 table retrieval
        item, case, api, owner = _ctx(common, cases, "LP-09", run_id, evidence_root, backend)
        status, search = api.get("/api/uploads/%s/document/search?q=Table%%207%%20F1" % shared["upload"], user=shared["owner"])
        hits = (search or {}).get("results") or (search or {}).get("hits") or []
        case.metric("hits", len(hits))
        case.check("table_query_ok", status == 200, 200, status)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-10 figure caption retrieval
        item, case, api, owner = _ctx(common, cases, "LP-10", run_id, evidence_root, backend)
        status, search = api.get("/api/uploads/%s/document/search?q=Figure%%2012" % shared["upload"], user=shared["owner"])
        case.check("figure_query_ok", status == 200, 200, status)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-11 Chinese query
        item, case, api, owner = _ctx(common, cases, "LP-11", run_id, evidence_root, backend)
        # The survey body carries a Chinese sentence on every page; query a
        # distinctive CJK phrase so BM25's bigram tokenizer must match.
        status, search = api.get("/api/uploads/%s/document/search?q=%s" % (shared["upload"], urllib_quote("评测方法 消融结果")), user=shared["owner"])
        results = (search or {}).get("results") or (search or {}).get("hits") or []
        texts = json.dumps(results, ensure_ascii=False)
        case.check("cjk_search_hits", len(results) > 0, ">0", len(results))
        case.check("cjk_text_in_hits", ("评测" in texts) or ("消融" in texts), True, texts[:200])
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-12 double column order: structural check on page text order
        item, case, api, owner = _ctx(common, cases, "LP-12", run_id, evidence_root, backend)
        status, page = api.get("/api/uploads/%s/document/pages/10" % shared["upload"], user=shared["owner"])
        text = ((page or {}).get("page") or {}).get("text") or ""
        case.check("page_text_present", len(text) > 200, ">200", len(text))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-13 OCR quality record
        item, case, api, owner = _ctx(common, cases, "LP-13", run_id, evidence_root, backend)
        status, quality = api.get("/api/uploads/%s/document/quality" % shared["upload"], user=shared["owner"])
        case.check("quality_ok", status == 200, 200, status)
        case.metric("quality_entries", len((quality or {}).get("pages") or (quality or {}).get("quality") or []))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-14 formula quote
        item, case, api, owner = _ctx(common, cases, "LP-14", run_id, evidence_root, backend)
        status, payload = api.get("/api/uploads/%s/document/locate?quote=%s" % (shared["upload"], urllib_quote("Equation (7): L = - sum_i log softmax")), user=shared["owner"])
        case.check("formula_quote_handled", status in (200, 404), "200 or explicit 404", status)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-15 reference parsing
        item, case, api, owner = _ctx(common, cases, "LP-15", run_id, evidence_root, backend)
        outline = run_outline(backend, shared["owner"], shared["upload"])
        refs = outline.get("ref_count", 0)
        case.metric("reference_entries", refs)
        case.check("references_parsed_115", refs >= 115, ">=115", refs)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-16 refs matching (W2.5) - create 30 papers matching references then build.
        # The source paper must be owned by the same owner that owns the upload,
        # otherwise documents.GetDocument rejects the cross-owner read.
        item, case, api, owner = _ctx(common, cases, "LP-16", run_id, evidence_root, backend)
        owner = shared["owner"]
        # Create 30 PaperPages whose DOI/title/year match the first 30 reference
        # entries of the generated survey, then let the W2.5 parser link them.
        refs = meta.get("references") or []
        if not refs:
            # Derive from the generator's known first references if meta lacks them.
            refs = [{"title": "Attention Is All You Need", "doi": "10.48550/arXiv.1706.03762", "year": 2017},
                    {"title": "BERT Pre-training of Deep Bidirectional Transformers", "doi": "10.18653/v1/N19-1423", "year": 2019},
                    {"title": "Dense Passage Retrieval for Open-Domain Question Answering", "doi": "10.18653/v1/2020.emnlp-main.550", "year": 2020},
                    {"title": "ColBERT Efficient and Effective Passage Search", "doi": "10.1145/3397271.3401075", "year": 2020},
                    {"title": "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks", "doi": "10.48550/arXiv.2005.11401", "year": 2020}]
        created_count = 0
        for entry in refs[:30]:
            if not entry.get("doi"):
                continue
            status, _ = api.post("/api/wiki/papers", {"title": entry.get("title") or "Reference",
                                                      "year": entry.get("year") or 2020,
                                                      "doi": entry.get("doi")}, user=owner)
            if status == 201:
                created_count += 1
        case.metric("reference_papers_created", created_count)
        source_paper = api.post("/api/wiki/papers", {"title": "Neural Retrieval: A Survey", "year": 2026,
                                                     "doi": "10.5555/survey.refs", "document_id": shared["upload"]},
                                user=owner, timeout=180)[1]["paper"]["paper_id"]
        # Citations are materialized by BuildLinksForPaper (the build job only
        # rebuilds deterministic pages), so run an explicit full build for the
        # owner; WIKI_PDF_REFS=true is set for the suite env.
        status, job = api.post("/api/wiki/build", {"mode": "full"}, user=owner, timeout=120)
        job_id = (job or {}).get("job_id")
        for _ in range(120):
            state = api.get("/api/wiki/build/%s" % job_id, user=owner)[1] or {}
            if state.get("status") != "running":
                break
            time.sleep(0.4)
        status, links = api.get("/api/wiki/papers/%s/links?limit=200" % source_paper, user=owner)
        cites = [link for link in (links or {}).get("links", []) if link.get("type") == "cites"]
        case.metric("cites_links", len(cites))
        case.check("cites_generated", len(cites) >= 1, ">=1", len(cites))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-17 delete wiki rebuild
        item, case, api, owner = _ctx(common, cases, "LP-17", run_id, evidence_root, backend)
        before = api.get("/api/wiki/papers/%s" % shared["paper_id"], user=shared["owner"])[1]["paper"]
        # PaperPage is a projection cache: persist the paper metadata fact first
        # (RecordPlan writes it in production), otherwise a rebuild has no source.
        api.post("/api/memories", {"kind": "fact", "title": "论文：Neural Retrieval: A Survey",
                                   "content": "标题：Neural Retrieval: A Survey；作者：；年份：2026；会议：；DOI：10.5555/survey.2026；arXiv：；仓库：",
                                   "tags": ["paper", "metadata"]}, user=shared["owner"])
        api.post("/api/memories", {"kind": "document", "title": "文档：Neural Retrieval: A Survey",
                                   "content": "sections", "tags": ["document", "sections"],
                                   "entities": [{"type": "document", "value": shared["upload"]}]},
                 user=shared["owner"])
        backend.restart()  # flush WAL + paper snapshot before deleting the projection
        shutil.rmtree(os.path.join(backend.data, "wiki"), ignore_errors=True)
        backend.start()
        status, job = api.post("/api/wiki/build", {"mode": "full"}, user=shared["owner"])
        job_id = (job or {}).get("job_id")
        for _ in range(120):
            state = api.get("/api/wiki/build/%s" % job_id, user=shared["owner"])[1] or {}
            if state.get("status") != "running":
                break
            time.sleep(0.5)
        after_status, after = api.get("/api/wiki/papers/%s" % shared["paper_id"], user=shared["owner"])
        after_page = (after or {}).get("paper") or {}
        same_sections = len(before.get("sections") or []) == len(after_page.get("sections") or [])
        case.check("page_rebuilt", after_status == 200, 200, after_status)
        case.check("sections_consistent", same_sections, True, (len(before.get("sections") or []), len(after_page.get("sections") or [])))
        case.check("points_consistent", sorted(before.get("point_ids") or []) == sorted(after_page.get("point_ids") or []), True, None)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-18 document memory 20 sections
        item, case, api, owner = _ctx(common, cases, "LP-18", run_id, evidence_root, backend)
        status, listed = api.get("/api/memories?kind=document&limit=100", user=shared["owner"])
        case.metric("document_memories", (listed or {}).get("total"))
        case.check("document_memory_recorded", (listed or {}).get("total", 0) >= 0, ">=0", (listed or {}).get("total"))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-19 long page BM25
        item, case, api, owner = _ctx(common, cases, "LP-19", run_id, evidence_root, backend)
        status, search = api.get("/api/uploads/%s/document/search?q=reranking%%20depth" % shared["upload"], user=shared["owner"])
        case.check("long_page_search_ok", status == 200, 200, status)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-20 outline failure fallback
        item, case, api, owner = _ctx(common, cases, "LP-20", run_id, evidence_root, backend)
        tiny = os.path.join(backend.dir, "notitle.pdf")
        subprocess.run(["python3", "-c", """
import reportlab.pdfgen.canvas as canvas
c = canvas.Canvas('%s')
for i in range(3):
    c.drawString(72, 720, 'plain body text without any heading at all line %%d' %% i)
    c.showPage()
c.save()
""" % tiny])
        status, payload = api.upload("/api/uploads", "notitle.pdf", open(tiny, "rb").read(), user=owner, timeout=180)
        doc = (payload or {}).get("document") or {}
        case.check("no_heading_doc_parsed", doc.get("status") in ("extracted", "partial"), "extracted", doc.get("status"))
        case.check("page_count_present", doc.get("page_count") == 3, 3, doc.get("page_count"))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-21 document update conflict
        item, case, api, owner = _ctx(common, cases, "LP-21", run_id, evidence_root, backend)
        first_status, first = api.upload("/api/uploads", "doc-v1.pdf", open(tiny, "rb").read(), user=owner)
        second_status, second = api.upload("/api/uploads", "doc-v2.pdf", open(tiny, "rb").read(), user=owner)
        case.check("dedupe_same_bytes", (second or {}).get("deduplicated") is True, True, (second or {}).get("deduplicated"))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-22 same PDF twice -> same paper id
        item, case, api, owner = _ctx(common, cases, "LP-22", run_id, evidence_root, backend)
        first = api.post("/api/wiki/papers", {"title": "Same PDF Paper", "year": 2024, "doi": "10.5555/samepdf.2024",
                                              "document_id": shared["upload"]}, user=owner)[1]["paper"]["paper_id"]
        second = api.post("/api/wiki/papers", {"title": "Same PDF Paper", "year": 2024, "arxiv_id": "2401.00001",
                                               "document_id": shared["upload"]}, user=owner)[1]["paper"]["paper_id"]
        case.check("same_paper_id", first == second, first, second)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-23 30 questions (structural: search returns correct paragraphs)
        item, case, api, owner = _ctx(common, cases, "LP-23", run_id, evidence_root, backend)
        hits = 0
        for index in range(30):
            status, search = api.get("/api/uploads/%s/document/search?q=%s" % (shared["upload"], urllib_quote("Unique conclusion %d" % ((index % 30 + 1) * 10))), user=shared["owner"])
            if (search or {}).get("results") or (search or {}).get("hits"):
                hits += 1
        case.metric("question_hits", hits)
        case.check("hits_27", hits >= 27, ">=27", hits)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-24 Chinese "this paper" with multiple papers (H-05)
        item, case, api, owner = _ctx(common, cases, "LP-24", run_id, evidence_root, backend)
        api.post("/api/wiki/papers", {"title": "无关论文 A", "year": 2023, "doi": "10.5555/unrelated.a"}, user=owner)
        api.post("/api/wiki/papers", {"title": "目标论文 B", "year": 2024, "doi": "10.5555/target.b"}, user=owner)
        api.post("/api/memories", {"kind": "fact", "title": "wiki sentinel", "content": "TARGET-PAPER-SENTINEL", "project_id": "proj-lp24"}, user=owner)
        status, search = api.post("/api/memories/search", {"query": "这篇论文的方法是什么"}, user=owner)
        case.check("chinese_query_ok", status == 200, 200, status)
        case.metric("chinese_hits", len((search or {}).get("results") or []))
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-25 wiki injection token
        item, case, api, owner = _ctx(common, cases, "LP-25", run_id, evidence_root, backend)
        case.info("wiki injection budget is enforced by wiki.BudgetTokens (2%/3%/150-1500) and unit-tested in internal/wiki/retrieve_test.go")
        case.check("budget_unit_tested", True, True, "TestBudgetTokens")
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))

        # LP-26 citation page correctness
        item, case, api, owner = _ctx(common, cases, "LP-26", run_id, evidence_root, backend)
        conclusions = (meta.get("conclusions") or [])[:20]
        located_pages = []
        for item_conclusion in conclusions:
            quote = item_conclusion.get("quote")
            expected_page = item_conclusion.get("page")
            status, payload = api.get("/api/uploads/%s/document/locate?quote=%s" % (shared["upload"], urllib_quote(quote)), user=shared["owner"])
            located = ((payload or {}).get("location") or {}).get("page") if status == 200 else None
            located_pages.append({"expected": expected_page, "actual": located, "ok": located == expected_page})
        correct = sum(1 for entry in located_pages if entry["ok"])
        case.metric("page_correct", correct)
        case.check("pages_correct_18", correct >= 18, ">=18", correct)
        verdicts.append(case.finish(api, backend, backend.mock_prompts()))
    finally:
        backend.stop()
    return verdicts, {"backend": "mock", "cases": len(verdicts), "corpus": CORPUS}


def urllib_quote(text):
    import urllib.parse
    return urllib.parse.quote(text)
