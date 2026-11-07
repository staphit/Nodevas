"""Part masks with Segment Anything (run with ComfyUI's venv python: it has torch + segment_anything).

    H:/ComfyUI/venv/Scripts/python.exe sam_masks.py <rgba.png> <parts.json> <out_dir>

parts.json: {"arm": {"pos": [[x, y], ...], "neg": [[x, y], ...]}, ...}
Writes <out_dir>/<part>.png (L mask, 255 = part, clipped to the image's alpha) and
<out_dir>/overlay.jpg (every part tinted on the image, for checking by eye).
"""
import json
import os
import sys

import numpy as np
import torch
from PIL import Image
from segment_anything import SamPredictor, sam_model_registry

CKPT = r"H:/ComfyUI/models/sams/sam_vit_b_01ec64.pth"
TINTS = [(255, 60, 60), (60, 200, 60), (60, 120, 255), (255, 200, 0), (255, 0, 255), (0, 220, 220), (255, 128, 0), (128, 0, 255)]


def main():
    src, parts_path, out = sys.argv[1], sys.argv[2], sys.argv[3]
    os.makedirs(out, exist_ok=True)
    rgba = Image.open(src).convert("RGBA")
    white = Image.new("RGBA", rgba.size, (255, 255, 255, 255))
    white.alpha_composite(rgba)
    rgb = np.asarray(white.convert("RGB"))
    alpha = np.asarray(rgba.getchannel("A")) > 127
    dev = os.environ.get("SAM_DEVICE", "cpu")  # the GPU is often busy with ComfyUI; vit_b is quick on CPU
    sam = sam_model_registry["vit_b"](checkpoint=CKPT).to(dev)
    pred = SamPredictor(sam)
    pred.set_image(rgb)
    parts = json.load(open(parts_path, encoding="utf-8"))
    overlay = rgb.astype(np.float32).copy()
    for i, (name, spec) in enumerate(parts.items()):
        pts = spec.get("pos", []) + spec.get("neg", [])
        labels = [1] * len(spec.get("pos", [])) + [0] * len(spec.get("neg", []))
        box = np.array(spec["box"]) if "box" in spec else None
        masks, scores, _ = pred.predict(point_coords=np.array(pts, dtype=np.float32) if pts else None,
                                        point_labels=np.array(labels) if pts else None,
                                        box=box, multimask_output=True)
        pick = int(np.argmax(scores)) if spec.get("pick") is None else spec["pick"]
        m = masks[pick] & alpha
        Image.fromarray((m * 255).astype(np.uint8)).save(os.path.join(out, f"{name}.png"))
        t = np.array(TINTS[i % len(TINTS)], dtype=np.float32)
        overlay[m] = overlay[m] * 0.45 + t * 0.55
        print(f"{name}: score {scores[pick]:.3f} area {int(m.sum())} (candidates {[round(float(s), 3) for s in scores]})")
    Image.fromarray(overlay.clip(0, 255).astype(np.uint8)).save(os.path.join(out, "overlay.jpg"), quality=90)


if __name__ == "__main__":
    main()
