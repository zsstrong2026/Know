# -*- coding: utf-8 -*-
from PIL import Image
import os

SRC = r"C:\Users\zs\AppData\Roaming\LobsterAI\openclaw\state\media\inbound\6bb9d977-83ae-414e-982c-f59bb1a73029.png"
FRONTEND = r"D:\wiki\WeKnora-main\frontend"

im = Image.open(SRC).convert("RGBA")
w, h = im.size

# 1) make near-white/light-gray background transparent
px = im.load()
def transparentize(p):
    r, g, b, a = p
    if r > 228 and g > 228 and b > 228:
        return (r, g, b, 0)
    return p
im2 = Image.new("RGBA", (w, h), (0, 0, 0, 0))
px2 = im2.load()
for y in range(h):
    for x in range(w):
        px2[x, y] = transparentize(px[x, y])

# 2) content bounds (ignore transparent)
bbox = im2.getbbox()
print("content bbox after transparency:", bbox)
if bbox:
    im2 = im2.crop(bbox)

# 3) Full lockup logo (icon + text) -> weknora.png, width 480
logo = im2.copy()
lw, lh = logo.size
target_logo_w = 480
logo = logo.resize((target_logo_w, max(1, int(lh * target_logo_w / lw))), Image.LANCZOS)
logo_out = os.path.join(FRONTEND, "src", "assets", "img", "weknora.png")
logo.save(logo_out, "PNG")
print("logo saved:", logo_out, logo.size)

# 4) favicon icon = dragon emblem only (left ~ x 291-659 before crop of content, but re-derive: crop left 62% of content)
fav = im2.copy()
fw, fh = fav.size
# icon is the left cluster; use left 62% of content width
icon_w = int(fw * 0.62)
icon = fav.crop((0, 0, icon_w, fh))
# make square by centering on square canvas
side = max(icon.size)
sq = Image.new("RGBA", (side, side), (0, 0, 0, 0))
sq.paste(icon, ((side - icon.width) // 2, (side - icon.height) // 2))
# favicon sizes
fav = sq.resize((256, 256), Image.LANCZOS)
fav.save(os.path.join(FRONTEND, "public", "favicon.png"), "PNG")
fav.save(os.path.join(FRONTEND, "public", "favicon.ico"), "ICO", sizes=[(256,256),(128,128),(64,64),(48,48),(32,32),(16,16)])
print("favicon saved:", os.path.join(FRONTEND, "public", "favicon.ico"))
print("done")
