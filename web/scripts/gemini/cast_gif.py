"""Animated grid of every action of a character: python cast_gif.py <id> <out.gif> [tile]"""
import json
import os
import sys
from PIL import Image, ImageDraw, ImageFont

OUT = os.environ.get("SPRITE_OUT", r"C:/Users/g2772/AppData/Local/Temp/claude/H--visual-editor/3c658b1f-3dd5-412a-b8a9-facf7f2aa2a4/scratchpad/shots/gemini")
LABEL = {"door-idle": "門口等待", "desk-rest": "桌前休息", "desk-type": "打字", "desk-chin": "思考", "desk-mouse": "滑滑鼠",
         "desk-ask": "舉手發問", "desk-cheer": "完成歡呼", "desk-slump": "失敗沮喪", "desk-sleep": "額度用完睡覺",
         "sofa-sit": "沙發坐", "sofa-sleep": "沙發睡", "hike-walk": "爬山", "hike-summit": "登頂", "stream-wade": "溯溪",
         "fish-sit": "釣魚", "camp-sit": "露營", "bbq-grill": "烤肉", "cycle-ride": "騎單車"}
ORDER = list(LABEL)
cid, dst = sys.argv[1], sys.argv[2]
tile = int(sys.argv[3]) if len(sys.argv) > 3 else 256
man = json.load(open(os.path.join(OUT, cid, f"{cid}.json")))
acts = [a for a in ORDER if a in man["actions"]]
cols = 6
rows = (len(acts) + cols - 1) // cols
try:
    font = ImageFont.truetype("msjh.ttc", 20)
except Exception:
    font = ImageFont.load_default()
frames = []
for f in range(8):
    canvas = Image.new("RGB", (cols * tile, rows * (tile + 28)), (238, 236, 244))
    d = ImageDraw.Draw(canvas)
    for i, a in enumerate(acts):
        im = Image.open(os.path.join(OUT, cid, "hi", a, f"{f:02d}.png")).resize((tile, tile), Image.LANCZOS)
        x, y = (i % cols) * tile, (i // cols) * (tile + 28)
        bg = Image.new("RGBA", (tile, tile), (238, 236, 244, 255))
        bg.alpha_composite(im)
        canvas.paste(bg.convert("RGB"), (x, y + 28))
        d.text((x + 8, y + 3), LABEL[a], fill=(60, 56, 80), font=font)
    frames.append(canvas.convert("P", palette=Image.ADAPTIVE, colors=255))
frames[0].save(dst, save_all=True, append_images=frames[1:], duration=100, loop=0, optimize=False)
print(dst, len(acts))
