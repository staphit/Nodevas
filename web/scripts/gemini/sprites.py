"""Generated key images (green background) -> animated sprite frames + manifest.

    python sprites.py <id> <action>=<img>[,<img2>] ...

Writes into $SPRITE_OUT (default ../../shots/gemini):
    <id>/hi/<action>/00..07.png   384x384, straight alpha (previews)
    <id>/frames/<action>/00..07.png 192x192 (the app)
    <id>/<id>.png/.webp + <id>.json (sheet: rows = actions, cols = frames; manifest as the
    anime contract: frameW/H 192, scale 6, anchor, head box, props)
Motion is code-made from one key image (two for typing/walking/pedalling when given):
breathing, a hop on the walk, a small bob on the bike, alternating keys.
"""
import json
import math
import os
import sys

import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get("SPRITE_OUT", os.path.join(HERE, "..", "shots", "gemini"))
HI, LO, N, FPS = 384, 192, 8, 10

# per action: view, anchor (192 px frame), how big (target box at 384), motion, props the image includes
ACTIONS = {
    "door-idle": dict(view="front", anchor=(96, 174), height=300, motion="breathe", props=[]),
    "desk-type": dict(view="back", anchor=(96, 162), height=210, motion="alternate", props=[]),
    "hike-walk": dict(view="side", anchor=(96, 174), height=300, motion="hop", props=["pack"]),
    "cycle-ride": dict(view="side", anchor=(96, 174), width=330, motion="pedal", props=["bike"]),
}


def key_green(path):
    """Chroma-key a solid #00FF00 background (JPEG-tolerant) -> RGBA float array."""
    im = np.asarray(Image.open(path).convert("RGB"), dtype=np.float32) / 255.0
    r, g, b = im[..., 0], im[..., 1], im[..., 2]
    spill = g - np.maximum(r, b)
    lo, hi = 0.10, 0.34
    alpha = np.clip((hi - spill) / (hi - lo), 0.0, 1.0)
    # despill: a fringe pixel may not be greener than its red/blue allow
    g = np.where(spill > 0.02, np.minimum(g, np.maximum(r, b)), g)
    out = np.dstack([r, g, b, alpha])
    # drop specks: keep connected blobs bigger than 0.04% of the image
    mask = alpha > 0.5
    keep = _big_components(mask, int(mask.size * 0.0004))
    out[..., 3] *= keep
    return out


def _big_components(mask, min_area):
    h, w = mask.shape
    labels = np.zeros((h, w), dtype=np.int32)
    keep = np.zeros((h, w), dtype=np.float32)
    nxt = 0
    ys, xs = np.nonzero(mask)
    for y0, x0 in zip(ys, xs):
        if labels[y0, x0]:
            continue
        nxt += 1
        stack = [(y0, x0)]
        labels[y0, x0] = nxt
        pix = []
        while stack:
            y, x = stack.pop()
            pix.append((y, x))
            for dy, dx in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                yy, xx = y + dy, x + dx
                if 0 <= yy < h and 0 <= xx < w and mask[yy, xx] and not labels[yy, xx]:
                    labels[yy, xx] = nxt
                    stack.append((yy, xx))
        if len(pix) >= min_area:
            py, px = zip(*pix)
            keep[list(py), list(px)] = 1.0
    # soft edge pixels next to kept ones stay (alpha < .5 ring)
    from PIL import ImageFilter
    grown = Image.fromarray((keep * 255).astype(np.uint8)).filter(ImageFilter.MaxFilter(5))
    return np.asarray(grown, dtype=np.float32) / 255.0


def to_image(arr):
    a = np.clip(arr, 0, 1)
    return Image.fromarray((a * 255 + 0.5).astype(np.uint8), "RGBA")


def bbox(img):
    return img.getchannel("A").point(lambda v: 255 if v > 128 else 0).getbbox()


def fit(img, spec):
    """Crop to the figure, scale to the action's box (at 384)."""
    box = bbox(img)
    fig = img.crop(box)
    if "height" in spec:
        s = spec["height"] / fig.height
    else:
        s = spec["width"] / fig.width
    return fig.resize((max(1, round(fig.width * s)), max(1, round(fig.height * s))), Image.LANCZOS)


def place(fig, spec, dy=0.0, sx=1.0, sy=1.0):
    """Put the figure in a 384 frame: bottom centre on the anchor (x2)."""
    ax, ay = spec["anchor"][0] * 2, spec["anchor"][1] * 2
    w, h = max(1, round(fig.width * sx)), max(1, round(fig.height * sy))
    f = fig.resize((w, h), Image.LANCZOS) if (w, h) != fig.size else fig
    frame = Image.new("RGBA", (HI, HI), (0, 0, 0, 0))
    x = round(ax - w / 2)
    y = round(ay - h + dy)
    frame.alpha_composite(f, (x, y)) if x >= 0 and y >= 0 else frame.paste(f, (x, y), f)
    return frame


def frames_for(action, keys):
    spec = ACTIONS[action]
    figs = [fit(k, spec) for k in keys]
    out = []
    for i in range(N):
        t = i / N
        if spec["motion"] == "breathe":
            sy = 1.0 + 0.010 * math.sin(2 * math.pi * t)
            out.append(place(figs[0], spec, sy=sy, sx=1.0 - 0.004 * math.sin(2 * math.pi * t)))
        elif spec["motion"] == "hop":
            k = figs[(i // 2) % len(figs)]
            lift = -10 * math.sin(math.pi * (i % 4) / 4) if len(figs) == 1 else -4 * math.sin(math.pi * (i % 4) / 4)
            squash = 0.975 if (i % 4) == 0 else 1.0
            out.append(place(k, spec, dy=lift, sy=squash, sx=2.0 - squash))
        elif spec["motion"] == "pedal":
            k = figs[(i // 2) % len(figs)]
            out.append(place(k, spec, dy=-2 if i % 2 else 0))
        elif spec["motion"] == "alternate":
            k = figs[(i // 2) % len(figs)]
            out.append(place(k, spec, sy=1.0 + 0.006 * math.sin(2 * math.pi * t)))
    return out


def head_box(frame):
    """Rough head box: the top 42% of the figure's height (chibi), full figure width there."""
    box = bbox(frame)
    if not box:
        return dict(x=0, y=0, w=0, h=0)
    x0, y0, x1, y1 = box
    hh = (y1 - y0) * 0.42
    a = np.asarray(frame.getchannel("A")) > 128
    band = a[y0:int(y0 + hh)]
    cols = np.nonzero(band.any(axis=0))[0]
    return dict(x=int(cols.min()) // 2, y=y0 // 2, w=int(cols.max() - cols.min()) // 2, h=int(hh) // 2)


def main():
    cid = sys.argv[1]
    root = os.path.join(OUT, cid)
    manifest = {"image": f"{cid}.webp", "frameW": LO, "frameH": LO, "scale": 6, "style": "anime",
                "character": cid, "actions": {}}
    rows = []
    for arg in sys.argv[2:]:
        action, paths = arg.split("=", 1)
        keys = [to_image(key_green(p)) for p in paths.split(",")]
        frames = frames_for(action, keys)
        hd = os.path.join(root, "hi", action)
        ld = os.path.join(root, "frames", action)
        os.makedirs(hd, exist_ok=True)
        os.makedirs(ld, exist_ok=True)
        small = []
        for i, f in enumerate(frames):
            f.save(os.path.join(hd, f"{i:02d}.png"))
            s = f.resize((LO, LO), Image.LANCZOS)
            s.save(os.path.join(ld, f"{i:02d}.png"))
            small.append(s)
        spec = ACTIONS[action]
        manifest["actions"][action] = {"row": len(rows), "frames": N, "fps": FPS, "view": spec["view"],
                                       "anchor": {"x": spec["anchor"][0], "y": spec["anchor"][1]},
                                       "head": head_box(frames[0]), "props": spec["props"]}
        rows.append(small)
        print(f"{cid} {action}: {len(keys)} key(s) -> {N} frames")
    sheet = Image.new("RGBA", (LO * N, LO * len(rows)), (0, 0, 0, 0))
    for r, fr in enumerate(rows):
        for c, f in enumerate(fr):
            sheet.alpha_composite(f, (c * LO, r * LO))
    sheet.save(os.path.join(root, f"{cid}.png"))
    sheet.save(os.path.join(root, f"{cid}.webp"), quality=90, method=6)
    json.dump(manifest, open(os.path.join(root, f"{cid}.json"), "w"), indent=1)
    print("sheet", sheet.size, os.path.join(root, f"{cid}.webp"))


if __name__ == "__main__":
    main()
