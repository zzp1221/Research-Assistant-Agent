from PIL import Image, ImageStat
import os, json
base = r"D:/项目/Research Assistant agent"
for name in ["home-1440x900.png","node-detail-1440x900.png","home-390x844.png","plan-library-1440x900.png","cost-1440x900.png","literature-1440x900.png"]:
    p = base + "/shots/app-a/" + name
    if not os.path.exists(p):
        print(json.dumps({"file": name, "missing": True})); continue
    im = Image.open(p).convert("RGB")
    st = ImageStat.Stat(im)
    print(json.dumps({"file": name, "size": im.size, "mean": [round(x,1) for x in st.mean], "stddev": [round(x,1) for x in st.stddev]}, ensure_ascii=False))
