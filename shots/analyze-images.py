from PIL import Image, ImageStat
import os, json
base = r"D:/项目/Research Assistant agent"
paths = [
    base + "/shots/explore-final2/a.png",
    base + "/shots/explore-final2/b.png",
    base + "/shots/explore-final2/c.png",
    base + "/shots/explore-final2/sheet.png",
    base + "/shots/explorer/page.png",
    base + "/shots/explorer/card-01.png/page.png",
    base + "/shots/explorer/card-02.png/page.png",
    base + "/shots/explorer/card-03.png/page.png",
]
for p in paths:
    im = Image.open(p).convert("RGB")
    st = ImageStat.Stat(im)
    colors = im.getcolors(maxcolors=1000000) or []
    top = sorted(colors, reverse=True)[:4]
    rel = os.path.relpath(p, base).replace("\\", "/")
    print(json.dumps({
        "file": rel,
        "size": im.size,
        "mean": [round(x,1) for x in st.mean],
        "stddev": [round(x,1) for x in st.stddev],
        "unique_colors": len(colors),
        "top": [{"rgb": c[1], "pixels": c[0]} for c in top],
    }, ensure_ascii=False))
