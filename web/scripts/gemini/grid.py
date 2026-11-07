"""Grid overlay for marking joints: python grid.py <green-bg image> <out.jpg> [step]"""
import sys
from PIL import Image, ImageDraw, ImageFont
from sprites import key_green, to_image
im = to_image(key_green(sys.argv[1]))
step = int(sys.argv[3]) if len(sys.argv) > 3 else 50
bg = Image.new("RGBA", im.size, (255, 255, 255, 255)); bg.alpha_composite(im)
d = ImageDraw.Draw(bg)
try:
    f = ImageFont.truetype("arial.ttf", 14)
except Exception:
    f = None
W, H = im.size
for x in range(0, W, step):
    d.line([(x, 0), (x, H)], fill=(255, 0, 0, 110) if x % 100 == 0 else (0, 0, 255, 60), width=1)
    if x % 100 == 0:
        d.text((x + 2, 2), str(x), fill=(200, 0, 0), font=f); d.text((x + 2, H - 18), str(x), fill=(200, 0, 0), font=f)
for y in range(0, H, step):
    d.line([(0, y), (W, y)], fill=(255, 0, 0, 110) if y % 100 == 0 else (0, 0, 255, 60), width=1)
    if y % 100 == 0:
        d.text((2, y + 2), str(y), fill=(200, 0, 0), font=f); d.text((W - 36, y + 2), str(y), fill=(200, 0, 0), font=f)
bg.convert("RGB").save(sys.argv[2], quality=92)
print(im.size)
