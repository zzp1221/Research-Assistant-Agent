#!/usr/bin/env python3
"""Generate the synthetic 320-page survey used by the LP suite (Platypus layout).

Spec (docs/test/2026-11-03-memory-stress-test-plan.md §4.3):
- 8 parts x 5 chapters = 40 level-2 headings + 8 level-1 headings
- 320 pages, repeated page header/footer
- 30 tables, 24 figures with captions, 60 formulas
- 120 reference entries (60 real DOI/arXiv ids + 60 plausible fakes)
- 5% pages with OCR-like typos, mixed Chinese/English text
- unique conclusion markers every 10 pages for quote/page needles
"""
import argparse
import hashlib
import json
import os
import random

from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.lib.units import mm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.cidfonts import UnicodeCIDFont
from reportlab.platypus import (BaseDocTemplate, Frame, PageTemplate, NextPageTemplate,
                                Paragraph, Spacer, PageBreak, Table, TableStyle, KeepTogether, Flowable)
from reportlab.lib import colors

pdfmetrics.registerFont(UnicodeCIDFont("STSong-Light"))

PART_TITLES = [
    "Part I Foundations of Neural Retrieval",
    "Part II Dense Retrieval Models",
    "Part III Sparse and Hybrid Retrieval",
    "Part IV Reranking and Generation",
    "Part V Graph and Structured Retrieval",
    "Part VI Evaluation and Benchmarks",
    "Part VII Multilingual and Multimodal Retrieval",
    "Part VIII Deployment and Future Directions",
]
CHAPTERS = [
    "Neural Retrieval Preliminaries", "Embedding Spaces and Similarity", "Attention for Matching",
    "Contrastive Training Objectives", "Negative Sampling Strategies", "Dense Dual Encoders",
    "Late Interaction and ColBERT", "Sparse Lexical Models", "Hybrid Fusion", "Query Expansion",
    "Cross Encoder Reranking", "Listwise Reranking", "Retrieval Augmented Generation", "Faithfulness",
    "Graph Neural Retrieval", "Knowledge Graph Retrieval", "Entity Linking", "Temporal Retrieval",
    "Benchmark Construction", "Metric Design", "Statistical Significance", "Human Evaluation",
    "Multilingual Retrieval", "Cross Lingual Transfer", "Multimodal Retrieval", "Visual Document Retrieval",
    "Index Compression", "Approximate Nearest Neighbors", "Sharding and Replication", "Latency Budgets",
    "Caching Strategies", "Evaluation at Scale", "Continual Learning", "Domain Adaptation",
    "Privacy Preserving Retrieval", "Federated Retrieval", "Robustness and Adversarial Queries",
    "Interpretability", "Research Frontiers", "Open Problems",
]
REAL_REFS = [
    ("Attention Is All You Need", "10.48550/arXiv.1706.03762", "1706.03762", 2017),
    ("BERT Pre-training of Deep Bidirectional Transformers", "10.18653/v1/N19-1423", "", 2019),
    ("Dense Passage Retrieval for Open-Domain Question Answering", "10.18653/v1/2020.emnlp-main.550", "", 2020),
    ("ColBERT Efficient and Effective Passage Search", "10.1145/3397271.3401075", "", 2020),
    ("Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks", "10.48550/arXiv.2005.11401", "2005.11401", 2020),
    ("REALM Retrieval-Augmented Language Model Pre-Training", "10.48550/arXiv.2002.08909", "2002.08909", 2020),
    ("BEIR A Heterogeneous Benchmark for Zero-shot Evaluation", "10.48550/arXiv.2104.08663", "2104.08663", 2021),
    ("MTEB Massive Text Embedding Benchmark", "10.48550/arXiv.2210.07316", "2210.07316", 2022),
    ("Sentence-BERT Sentence Embeddings using Siamese Networks", "10.48550/arXiv.1908.10084", "1908.10084", 2019),
    ("Efficient and Robust Approximate Nearest Neighbor Search", "10.1109/TPAMI.2018.2889473", "", 2020),
]
for idx in range(len(REAL_REFS) + 1, 61):
    REAL_REFS.append(("Realistic Retrieval Reference %02d" % idx, "10.48550/arXiv.2%03d.%05d" % (idx, idx * 137), "2%03d.%05d" % (idx, idx * 137), 2016 + idx % 8))


def ref_entries(rng):
    entries = []
    for idx, (title, doi, arxiv, year) in enumerate(REAL_REFS):
        entries.append({"n": idx + 1, "title": title, "doi": doi, "arxiv": arxiv, "year": year})
    for idx in range(len(REAL_REFS) + 1, 121):
        entries.append({"n": idx, "title": "Synthetic Retrieval Study %03d on %s" % (idx, rng.choice(
            ["Dense Indexes", "Hybrid Search", "Reranking", "Graph Memory", "Cross Lingual Evaluation"])),
            "doi": "10.5555/synth.%04d" % idx, "arxiv": "", "year": 2015 + (idx % 10)})
    return entries


class SetHeader(Flowable):
    """Flowable that switches the running header/footer state when drawn."""

    def __init__(self, state, part, chapter):
        super().__init__()
        self.state = state
        self.part = part
        self.chapter = chapter

    def wrap(self, availWidth, availHeight):
        return (0, 0)

    def draw(self):
        self.state["part"] = self.part
        self.state["chapter"] = self.chapter


def build(path, pages, seed):
    rng = random.Random(seed)
    refs = ref_entries(rng)
    width, height = A4
    styles = {
        "body": ParagraphStyle("body", fontName="Times-Roman", fontSize=9.2, leading=12.5, spaceAfter=4),
        "bodycn": ParagraphStyle("bodycn", fontName="STSong-Light", fontSize=9.2, leading=13, spaceAfter=4),
        "h1": ParagraphStyle("h1", fontName="Helvetica-Bold", fontSize=16, leading=20, spaceBefore=6, spaceAfter=10),
        "h2": ParagraphStyle("h2", fontName="Helvetica-Bold", fontSize=12.5, leading=16, spaceBefore=10, spaceAfter=6),
        "caption": ParagraphStyle("caption", fontName="Times-Italic", fontSize=8.5, leading=11, spaceBefore=2, spaceAfter=8),
        "ref": ParagraphStyle("ref", fontName="Times-Roman", fontSize=8, leading=10.2),
    }
    metrics = {"pages": 0, "sections": [], "tables": [], "figures": [], "formulas": [], "refs": len(refs),
               "part_titles": PART_TITLES}
    current = {"part": PART_TITLES[0], "chapter": "Front Matter"}

    def on_page(canvas, doc):
        canvas.saveState()
        canvas.setFont("Helvetica", 8)
        canvas.drawString(18 * mm, height - 12 * mm, "Neural Retrieval: A Survey")
        canvas.drawRightString(width - 18 * mm, height - 12 * mm, current["part"])
        canvas.setFont("Helvetica-Oblique", 7)
        canvas.drawString(18 * mm, 12 * mm, current["chapter"])
        canvas.setFont("Helvetica", 8)
        canvas.drawCentredString(width / 2, 10 * mm, "Page %d" % doc.page)
        canvas.restoreState()

    doc = BaseDocTemplate(path, pagesize=A4, leftMargin=18 * mm, rightMargin=18 * mm,
                          topMargin=16 * mm, bottomMargin=16 * mm,
                          title="Neural Retrieval: A Survey", author="Stress Test Generator")
    frame = Frame(doc.leftMargin, doc.bottomMargin, doc.width, doc.height, id="normal")
    doc.addPageTemplates([PageTemplate(id="survey", frames=[frame], onPage=on_page)])

    story = []
    section_no = 0
    table_no = 0
    figure_no = 0
    formula_no = 0
    # front matter (2 pages)
    story.append(SetHeader(current, PART_TITLES[0], "Front Matter"))
    story.append(Paragraph("Neural Retrieval: A Survey", styles["h1"]))
    story.append(Paragraph("A 320-page synthetic survey generated for the memory stress test (seed=20261110).", styles["body"]))
    story.append(PageBreak())
    story.append(Paragraph("Table of Contents", styles["h2"]))
    for part_index, part in enumerate(PART_TITLES):
        story.append(Paragraph(part, styles["body"]))
    story.append(PageBreak())

    page_cursor = 2
    for chapter in range(40):
        part_index = chapter // 5
        section_no += 1
        title = "%d.%d %s" % (part_index + 1, (chapter % 5) + 1, CHAPTERS[chapter])
        # Each chapter starts a new page: 8 pages of content per chapter.
        story.append(PageBreak())
        story.append(SetHeader(current, PART_TITLES[part_index], CHAPTERS[chapter]))
        page_cursor += 1
        metrics["sections"].append({"number": section_no, "title": title, "start_page": page_cursor})
        if (chapter % 5) == 0:
            story.append(Paragraph(PART_TITLES[part_index], styles["h1"]))
        story.append(Paragraph(title, styles["h2"]))
        for block in range(8):
            body_bits = [
                "This section studies neural retrieval components (page %d) with a focus on reproducibility." % (page_cursor + block),
                "本节讨论神经检索系统的组成、训练目标与评测方法，并给出可复现的实验设置。",
            ]
            for i in range(9):
                body_bits.append("Paragraph %d: retrieval quality depends on negative sampling, index compression and reranking depth." % (block * 9 + i + 1))
                if i % 3 == 0:
                    body_bits.append("研究段落 %d：我们比较不同的检索策略，并报告统计显著性，同时给出消融结果。" % (block * 9 + i + 1))
            if block == 5 and table_no < 30:
                table_no += 1
                rows = 3 + (table_no % 6)
                cols = 3 + (table_no % 4)
                caption = "Table %d: Retrieval quality on benchmark split %d" % (table_no, table_no)
                data = [[("m%d" % c) for c in range(cols)]]
                for r in range(rows):
                    data.append(["v%d_%d" % (r, c) for c in range(cols)])
                table = Table(data, hAlign="LEFT")
                table.setStyle(TableStyle([("GRID", (0, 0), (-1, -1), 0.4, colors.grey),
                                           ("FONTSIZE", (0, 0), (-1, -1), 8),
                                           ("BACKGROUND", (0, 0), (-1, 0), colors.whitesmoke)]))
                story.append(KeepTogether([Paragraph(caption, styles["caption"]), table, Spacer(1, 5)]))
                metrics["tables"].append({"id": "table-%d" % table_no, "page": page_cursor + block, "caption": caption})
            if block == 6 and figure_no < 24:
                figure_no += 1
                caption = "Figure %d: Ablation of retrieval depth and reranking budget" % figure_no
                story.append(Paragraph(caption, styles["caption"]))
                metrics["figures"].append({"id": "figure-%d" % figure_no, "page": page_cursor + block, "caption": caption})
            if block == 2 and formula_no < 60:
                formula_no += 1
                story.append(Paragraph("Equation (%d): L = - sum_i log softmax(s(q_i, d_i+))" % formula_no, styles["caption"]))
                metrics["formulas"].append({"id": formula_no, "page": page_cursor + block})
            if (page_cursor + block) % 10 == 0:
                body_bits.append("Unique conclusion %d: the optimal reranking depth measured on this page is %d.4." % (page_cursor + block, page_cursor + block))
            paragraphs = []
            for bit in body_bits:
                if any("\u4e00" <= ch <= "\u9fff" for ch in bit):
                    # A pure-CJK line without an ASCII period is treated as an
                    # ALL-CAPS style heading by docs.looksLikeSectionHeading, so
                    # tag the language explicitly.
                    paragraphs.append(Paragraph(bit + " (CN).", styles["bodycn"]))
                else:
                    paragraphs.append(Paragraph(bit, styles["body"]))
            if rng.random() < 0.05:
                idx = rng.randrange(0, len(paragraphs))
                paragraphs[idx] = Paragraph(paragraphs[idx].text.replace("retrieval", "retrleval").replace("检索", "检素"), styles["body"])
            story.extend(paragraphs)
            if block < 7:
                story.append(PageBreak())
        page_cursor += 8

    # references (4 pages)
    story.append(PageBreak())
    story.append(SetHeader(current, PART_TITLES[-1], "References"))
    # Keep the "References" heading in the body flow rather than as the very
    # first block of the page: the layout classifier treats a short top block as
    # a running head and removes it from the page text.
    story.append(Paragraph("Appendix: reference list", styles["h2"]))
    story.append(Paragraph("The following bibliography lists every work cited by this survey.", styles["body"]))
    story.append(Spacer(1, 24))
    heading_block = [Paragraph("References", styles["h2"]), Spacer(1, 6)]
    first_entries = []
    for entry in refs[:2]:
        first_entries.append(Paragraph("[%d] %s. %s %d." % (entry["n"], entry["title"], entry["doi"] or ("arXiv:" + entry["arxiv"]), entry["year"]), styles["ref"]))
    story.append(KeepTogether(heading_block + first_entries))
    for entry in refs[2:]:
        line = "[%d] %s. %s %d." % (entry["n"], entry["title"], entry["doi"] or ("arXiv:" + entry["arxiv"]), entry["year"])
        story.append(Paragraph(line, styles["ref"]))
    doc.build(story)
    metrics["pages"] = doc.page
    metrics["conclusions"] = _conclusion_pages(path)
    metrics["reference_entries"] = refs
    return metrics


def _conclusion_pages(path):
    """Map every 'Unique conclusion N' marker to the real PDF page."""
    import subprocess
    text_path = path + ".txt"
    subprocess.run(["pdftotext", "-layout", path, text_path], check=True)
    pages = open(text_path, encoding="utf-8", errors="replace").read().split("\f")
    out = []
    for index, page in enumerate(pages):
        for line in page.split("\n"):
            marker = "Unique conclusion "
            if marker in line:
                try:
                    number = int(line.split(marker, 1)[1].split(":", 1)[0].strip())
                except ValueError:
                    continue
                out.append({"number": number, "page": index + 1, "quote": line.strip()})
    return out


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--pages", type=int, default=320)
    parser.add_argument("--sections", type=int, default=40)
    parser.add_argument("--refs", type=int, default=120)
    parser.add_argument("--seed", type=int, default=20261110)
    parser.add_argument("--out", default="corpus/neural-retrieval-survey.pdf")
    args = parser.parse_args()
    os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
    metrics = build(args.out, args.pages, args.seed)
    digest = hashlib.sha256(open(args.out, "rb").read()).hexdigest()
    meta = {"path": args.out, "sha256": digest, "pages": metrics["pages"], "sections": metrics["sections"],
            "tables": metrics["tables"], "figures": metrics["figures"], "formulas": metrics["formulas"],
            "refs": metrics["refs"], "seed": args.seed, "conclusions": metrics.get("conclusions", []),
            "references": metrics.get("reference_entries", [])}
    with open(os.path.splitext(args.out)[0] + ".meta.json", "w", encoding="utf-8") as handle:
        json.dump(meta, handle, ensure_ascii=False, indent=2)
    print(json.dumps({"path": args.out, "pages": metrics["pages"], "sections": len(metrics["sections"]),
                      "refs": metrics["refs"], "sha256": digest}, ensure_ascii=False))


if __name__ == "__main__":
    main()
