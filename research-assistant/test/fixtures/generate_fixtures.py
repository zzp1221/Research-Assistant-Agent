#!/usr/bin/env python3
"""Generate deterministic PDF fixtures for the N-07 document-intelligence E2E.

Run on a workstation with Pillow and Microsoft Edge installed. The generated
PDFs are committed as test fixtures; the E2E itself runs inside containers.
"""
import base64
import io
import os
import subprocess
import sys
import tempfile

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.abspath(__file__))
EDGE = r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"


def font(size):
    for candidate in [r"C:\Windows\Fonts\msyh.ttc", r"C:\Windows\Fonts\simsun.ttc", r"C:\Windows\Fonts\arial.ttf"]:
        if os.path.exists(candidate):
            return ImageFont.truetype(candidate, size)
    return ImageFont.load_default()


def page_image(lines, width=1240, height=1754):
    image = Image.new("RGB", (width, height), "white")
    draw = ImageDraw.Draw(image)
    y = 120
    for text, size in lines:
        draw.text((110, y), text, fill="black", font=font(size))
        y += size + 28
    return image


def save_scanned(path, pages):
    images = []
    for index in range(pages):
        lines = [
            ("SCANNED PAPER TITLE", 64),
            (f"Page {index + 1}", 40),
            ("This is a scanned document about graph neural networks.", 34),
            ("The experiment reports accuracy of 0.91 on the benchmark.", 34),
            ("Deep learning models improve performance on citation networks.", 34),
        ]
        images.append(page_image(lines))
    images[0].save(path, "PDF", resolution=150, save_all=True, append_images=images[1:])


def save_chinese_scan(path, pages=2):
    images = []
    for index in range(pages):
        image = Image.new("RGB", (1240, 1754), "white")
        draw = ImageDraw.Draw(image)
        draw.text((110, 130), "中文扫描论文测试", fill="black", font=font(72))
        draw.text((110, 260), f"第 {index + 1} 页", fill="black", font=font(44))
        draw.text((110, 360), "本文研究图神经网络在文献检索中的应用。", fill="black", font=font(40))
        draw.text((110, 440), "实验结果表明该方法能够提升检索准确率。", fill="black", font=font(40))
        images.append(image)
    images[0].save(path, "PDF", resolution=150, save_all=True, append_images=images[1:])


def image_data_uri(image):
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    return "data:image/png;base64," + base64.b64encode(buffer.getvalue()).decode()


def render_html_to_pdf(html, out_path):
    with tempfile.TemporaryDirectory() as temp:
        html_path = os.path.join(temp, "fixture.html")
        profile = os.path.join(temp, "profile")
        with open(html_path, "w", encoding="utf-8") as handle:
            handle.write(html)
        command = [
            EDGE, "--headless=new", "--disable-gpu", "--no-sandbox",
            f"--user-data-dir={profile}", "--no-pdf-header-footer",
            f"--print-to-pdf={out_path}", "file:///" + html_path.replace("\\", "/"),
        ]
        subprocess.run(command, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def text_fixture_html():
    columns = "".join(
        f"<p>Paragraph {index}: This two column section discusses graph neural network message passing, aggregation and evaluation with enough detail for layout detection. Comma separated clauses, numbers 1, 2, 3, and citations make it realistic.</p>"
        for index in range(1, 9)
    )
    table_rows = "".join(
        f"<tr><td>{model}</td><td>{acc}</td><td>{f1}</td></tr>"
        for model, acc, f1 in [("GNN", "0.91", "0.88"), ("CNN", "0.85", "0.80"), ("MLP", "0.72", "0.69")]
    )
    figure_image = page_image([("Figure placeholder", 48), ("Architecture diagram", 36)], 620, 360)
    pages = []
    pages.append(f"""
    <div class="page"><h1>Attention Mechanisms for Graph Learning</h1>
    <p class="authors">Ada Lovelace, Alan Turing</p>
    <h2>Abstract</h2><p>We study attention mechanisms for graph neural networks and evaluate them on citation benchmarks with reproducible settings.</p>
    <h2>1 Introduction</h2><p>Graph neural networks aggregate neighbourhood information. This paper introduces an attention based aggregation scheme.</p></div>""")
    pages.append(f'<div class="page two-col">{columns}</div>')
    pages.append(f'<div class="page two-col">{columns}</div>')
    pages.append(f"""
    <div class="page"><h2>4 Experiments</h2>
    <p>Table 2: Accuracy comparison</p>
    <table><thead><tr><th>Model</th><th>Accuracy</th><th>F1</th></tr></thead><tbody>{table_rows}</tbody></table>
    <p>The GNN model achieves the best accuracy on the benchmark.</p></div>""")
    pages.append(f"""
    <div class="page"><h2>5 Architecture</h2>
    <p>Figure 3: Architecture overview</p>
    <img class="figure" src="{image_data_uri(figure_image)}" />
    <p>Figure 3 shows the overall architecture of the proposed model.</p></div>""")
    for index in range(6, 11):
        pages.append(f'<div class="page"><h2>{index} Discussion</h2><p>Page {index} continues the discussion of limitations, reproducibility and future work for graph learning systems.</p></div>')
    return """<!doctype html><html><head><meta charset="utf-8"><style>
    @page { size: A4; margin: 18mm 16mm; }
    body { font-family: Arial, sans-serif; font-size: 11pt; color: #111; }
    .page { page-break-after: always; }
    .page:last-child { page-break-after: auto; }
    h1 { font-size: 22pt; } h2 { font-size: 14pt; }
    .authors { font-size: 12pt; }
    .two-col { column-count: 2; column-gap: 12mm; }
    table { border-collapse: collapse; width: 100%; } td, th { border: 1px solid #444; padding: 4px 10px; text-align: left; }
    .figure { width: 70%; display: block; margin: 8mm auto; }
    </style></head><body>""" + "".join(pages) + "</body></html>"


def mixed_fixture_html():
    scanned = page_image([("SCANNED PAGE", 64), ("This page is an image without a text layer.", 34)])
    uri = image_data_uri(scanned)
    pages = []
    for index in range(2):
        pages.append(f'<div class="page"><img class="full" src="{uri}" /></div>')
    for index in range(3, 11):
        pages.append(f'<div class="page"><h2>{index} Text Page</h2><p>Page {index} has a real text layer and discusses retrieval augmented research assistants.</p></div>')
    return """<!doctype html><html><head><meta charset="utf-8"><style>
    @page { size: A4; margin: 0; }
    body { margin: 0; font-family: Arial, sans-serif; font-size: 12pt; }
    .page { page-break-after: always; width: 210mm; height: 297mm; overflow: hidden; }
    .page:last-child { page-break-after: auto; }
    .full { width: 100%; height: 100%; object-fit: contain; }
    h2 { margin: 18mm 16mm 0; } p { margin: 4mm 16mm; }
    </style></head><body>""" + "".join(pages) + "</body></html>"


def main():
    os.makedirs(ROOT, exist_ok=True)
    save_scanned(os.path.join(ROOT, "scanned-20pages.pdf"), 20)
    save_chinese_scan(os.path.join(ROOT, "chinese-scan-2pages.pdf"))
    render_html_to_pdf(text_fixture_html(), os.path.join(ROOT, "text-10pages.pdf"))
    render_html_to_pdf(mixed_fixture_html(), os.path.join(ROOT, "mixed-10pages.pdf"))
    for name in ["scanned-20pages.pdf", "chinese-scan-2pages.pdf", "text-10pages.pdf", "mixed-10pages.pdf"]:
        path = os.path.join(ROOT, name)
        print(name, os.path.getsize(path))


if __name__ == "__main__":
    main()
