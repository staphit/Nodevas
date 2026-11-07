"""Rig kit: one rig spec per character base image -> cut-out layers -> every action.

A base is one rig-friendly drawing of a character (front_stand, side_stand, front_sit,
side_sit). Its spec (rig/specs/<character>_<base>.json) says where the joints are and
how SAM finds each part:

{
  "character": "deepseek", "base": "side_stand",
  "image": "mv/deepseek_mv_side.png",          # green background source
  "ground_y": 1162,                            # soles (stand) / seat contact (sit), view px
  "anchor_x": 448,                             # view x placed on the frame's anchor x
  "head_top": 28,                              # top of the hair (no ahoge) for the scale
  "joints": {"hip": [x, y], "neck": [x, y], "shoulder_n": .., "elbow_n": .., "wrist_n": ..,
             "shoulder_f": .., "elbow_f": .., "wrist_f": .., "knee_n": .., "ankle_n": ..,
             "knee_f": .., "ankle_f": .., "tail_root": .., "tail_mid": .., "wing_root": ..},
  "parts": {"arm_n": {"pos": [[x, y]], "neg": [[x, y]]}, "arm_f": .., "leg_n": .., "leg_f": ..,
            "head": .., "hair": .., "tail": .., "wing_n": .., "wing_f": ..},
  "hair_sway": {"y0": 420, "y1": 790}          # where long hair starts / ends (view px)
}

Only the parts a character has are listed. Arms and legs are split at the elbow / knee
by distance to the two bone segments, so each limb becomes two layers (upper + lower)
that bend. Layer names, back to front:
  tail, wing_f, arm_f(+fore_f), leg_f(+shin_f), leg_n(+shin_n)*, body(+hair sway), head,
  wing_n, arm_n(+fore_n)
(*side views: near leg in front of the far one but under the skirt/body.)
Poses are deltas from the drawing, in degrees (counter-clockwise on screen) and px.
"""
import json
import math
import os
import subprocess
import sys

import cv2
import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import rig2d as R  # noqa: E402
from sprites import key_green, to_image  # noqa: E402

COMFY_PY = r"H:/ComfyUI/venv/Scripts/python.exe"
SPECS = os.path.join(HERE, "rig", "specs")
WORK = os.path.join(HERE, "rig", "work")
FRAME_HI, FRAME_LO = 384, 192
ANCHOR_HI = (192, 348)          # = (96, 174) in the 192 frame: feet / wheels on the ground row
STAND_HEIGHT_HI = 300           # head top -> soles, standing (150 px in the app frame)
PAD = 320                       # working-canvas margin round the drawing (props and poses may leave it)


def load(path):
    spec = json.load(open(path, encoding="utf-8"))
    spec["_path"] = path
    return spec


def _key(spec):
    os.makedirs(WORK, exist_ok=True)
    out = os.path.join(WORK, f"{spec['character']}_{spec['base']}.png")
    src = os.path.join(HERE, spec["image"])
    if not os.path.exists(out) or os.path.getmtime(out) < os.path.getmtime(src):
        to_image(key_green(src)).save(out)
    return out


def masks(spec, force=False):
    """SAM masks for every part (cached; rerun when the spec changes)."""
    rgba = _key(spec)
    mdir = os.path.join(WORK, f"{spec['character']}_{spec['base']}_masks")
    stamp = os.path.join(mdir, "parts.json")
    parts = spec.get("parts", {})
    if force or not os.path.exists(stamp) or json.load(open(stamp)) != parts:
        os.makedirs(mdir, exist_ok=True)
        json.dump(parts, open(stamp, "w"))
        r = subprocess.run([COMFY_PY, os.path.join(HERE, "sam_masks.py"), rgba, stamp, mdir],
                           capture_output=True, text=True, encoding="utf-8", errors="replace")
        if r.returncode:
            raise RuntimeError(r.stderr[-1500:])
    return {n: np.asarray(Image.open(os.path.join(mdir, f"{n}.png"))) > 127 for n in parts}, mdir


def _seg_dist(px, py, a, b):
    ax, ay = a
    bx, by = b
    vx, vy = bx - ax, by - ay
    L = vx * vx + vy * vy or 1.0
    t = np.clip(((px - ax) * vx + (py - ay) * vy) / L, 0.0, 1.0)
    return np.hypot(px - (ax + t * vx), py - (ay + t * vy))


def split_limb(mask, root, mid, end):
    """Split a limb mask into (upper, lower) by which bone segment each pixel is nearer."""
    ys, xs = np.nonzero(mask)
    d1 = _seg_dist(xs, ys, root, mid)
    d2 = _seg_dist(xs, ys, mid, end)
    upper = np.zeros_like(mask)
    lower = np.zeros_like(mask)
    up = d1 <= d2
    upper[ys[up], xs[up]] = True
    lower[ys[~up], xs[~up]] = True
    return upper, lower


def build(spec):
    """Layers (back to front) for a spec, plus the rest geometry."""
    rgba_path = _key(spec)
    raw = Image.open(rgba_path).convert("RGBA")
    P = PAD
    view = Image.new("RGBA", (raw.width + 2 * P, raw.height + 2 * P), (0, 0, 0, 0))
    view.paste(raw, (P, P))
    a8 = np.asarray(view.getchannel("A"))
    alpha = a8 > 127          # what SAM and the joints see
    alpha_any = a8 > 0        # plus the anti-aliased edge, which must not be lost
    M, _ = masks(spec)
    M = {k: np.pad(v, P) for k, v in M.items()}
    J = {k: (v[0] + P, v[1] + P) for k, v in spec["joints"].items()}
    spec["_pad"] = P
    drops = [M.pop(k) for k in spec.get("drop", []) if k in M]
    if drops:  # props in the drawing that the scene draws itself (a seat box), with their outline
        keep = np.zeros_like(alpha)
        for k, m in M.items():
            if k != "hair":
                keep |= m
        for m in drops:
            grown = cv2.dilate(m.astype(np.uint8), np.ones((15, 15), np.uint8)) > 0
            alpha &= ~(grown & ~keep)
            alpha_any &= ~(grown & ~keep)
    for k in list(M):
        M[k] = R.largest(M[k], spec.get("keep", {}).get(k, 1)) & alpha
    # SAM stops halfway through a part's outline: the outer half (and the soft edge) would stay on
    # the body as a ghost line. Grow each part over the silhouette's rim next to it.
    rim = alpha_any & (cv2.dilate((~alpha).astype(np.uint8), np.ones((5, 5), np.uint8)) > 0)
    for k in list(M):
        if k != "hair":
            M[k] = M[k] | ((cv2.dilate(M[k].astype(np.uint8), np.ones((5, 5), np.uint8)) > 0) & rim)
    claimed = np.zeros_like(alpha)
    layers = []
    holes = []   # what an arm (or a bike's near leg) covered on the body: painted from the no-arm drawing
    crank_holes = []
    copied = spec.get("far_leg_from_near")

    def add(name, m, pivot, parent, over_body):
        nonlocal claimed
        if not (name in ("leg_f", "shin_f") and copied):  # the far-leg copy is not in the drawing
            m = m & ~claimed  # may be empty (a thigh hidden in a skirt): the joint still exists
            claimed |= m
        layers.append((name, m, pivot, parent, over_body))
        # what a moving part drawn over the body covered: painted from the drawing without it
        # (<image>_noarm.png: the arms removed, or for a bike the near leg)
        if over_body and name.startswith(("arm_", "fore_", "leg_", "shin_")):
            holes.append(m)
        elif over_body and name == "crank":  # the drawing without the leg has its own crank: inpaint here
            crank_holes.append(m)

    # the order parts claim pixels: front-most first (so overlaps belong to the nearer part)
    if "arm_n" in M:
        up, lo = split_limb(M["arm_n"], J["shoulder_n"], J["elbow_n"], J["wrist_n"])
        add("fore_n", lo, J["elbow_n"], "arm_n", True)
        add("arm_n", up, J["shoulder_n"], "body", True)
    wings_over = spec.get("wings_over_body", False)  # wings grow from the back: behind the body by default
    if "wing_n" in M:
        add("wing_n", M["wing_n"], J.get("wing_root", J["neck"]), "body", wings_over)
    if "head" in M:
        add("head", M["head"], J["neck"], "body", True)
    if "leg_n" in M:
        up, lo = split_limb(M["leg_n"], J["hip"], J["knee_n"], J["ankle_n"])
        add("shin_n", lo, J["knee_n"], "leg_n", spec.get("legs_over_body", False))
        add("leg_n", up, J["hip"], "body", spec.get("legs_over_body", False))
    if copied and "leg_n" in M:
        # the classic cut-out trick: the far leg is the near one, shifted to its own place
        dx = int(round(J["ankle_f"][0] - J["ankle_n"][0]))
        dy = int(round(J["ankle_f"][1] - J["ankle_n"][1]))
        M["leg_f"] = np.roll(np.roll(M["leg_n"], dx, axis=1), dy, axis=0)
        spec["_far_from_near"] = (dx, dy)
    if "leg_f" in M:
        up, lo = split_limb(M["leg_f"], J.get("hip_f", J["hip"]), J["knee_f"], J["ankle_f"])
        add("shin_f", lo, J["knee_f"], "leg_f", False)
        add("leg_f", up, J.get("hip_f", J["hip"]), "body", False)
    front_view = spec["base"].startswith("front")
    if "crank" in M:  # a bike drawn with the rider: the chainring, crank arm and pedal turn
        add("crank", M["crank"], J["crank"], "body", True)
    if "arm_f" in M:
        up, lo = split_limb(M["arm_f"], J["shoulder_f"], J["elbow_f"], J["wrist_f"])
        add("fore_f", lo, J["elbow_f"], "arm_f", front_view)
        add("arm_f", up, J["shoulder_f"], "body", front_view)
    if "wing_f" in M:
        add("wing_f", M["wing_f"], J.get("wing_root_f", J.get("wing_root", J["neck"])), "body", False)
    if "tail" in M:
        add("tail", M["tail"], J["tail_root"], "body", False)
    body_m = alpha_any & ~claimed
    body = R.cut(view, body_m)
    hole = np.zeros_like(alpha)
    for m in holes:
        hole |= m
    if hole.any():
        noarm = _variant(spec, "noarm", view.size, P)
        if noarm is not None:  # what was behind the arm, drawn; where it shows background, stay open
            a = np.asarray(body).copy()
            v = np.asarray(noarm)
            take = hole & (v[..., 3] > 127)
            if "crank" in J:  # the drawing without the leg draws its own crank there: never show a second one
                cx, cy = J["crank"]
                yy, xx = np.mgrid[0:alpha.shape[0], 0:alpha.shape[1]]
                ring = (xx - cx) ** 2 + (yy - cy) ** 2 <= (spec.get("pedal_r", 60) + 25) ** 2
                crank_holes.append(take & ring)
                take &= ~ring
            a[take] = v[take]
            body = Image.fromarray(a, "RGBA")
        else:
            body = R.fill_behind(body, hole)
    if crank_holes:
        ch = np.zeros_like(alpha)
        for m in crank_holes:
            ch |= m
        body = R.fill_behind(body, ch)
    head_m = next((m for n, m, *_ in layers if n == "head"), None)
    if head_m is not None and head_m.any():
        # a head that tilts opens a seam against the hair: back it with a thin band of fill
        main_body = R.largest(body_m, 1, frac=0.2)  # stray specks near the head would grow into blobs
        band = head_m & (cv2.dilate(main_body.astype(np.uint8), np.ones((17, 17), np.uint8)) > 0)
        body = R.fill_band(body, band)
    sway = None
    if "hair" in M and spec.get("hair_sway"):
        hs = spec["hair_sway"]
        hm = M["hair"].astype(np.float32)
        sway = {"mask": cv2.GaussianBlur(hm, (0, 0), 6), "y0": hs["y0"] + P, "y1": hs["y1"] + P, "power": hs.get("power", 1.5)}
    order_back = ["tail", "wing_f", "arm_f", "fore_f", "leg_f", "shin_f", "leg_n", "shin_n"]
    order_front = ["head", "wing_n", "arm_n", "fore_n"]
    if front_view:  # both arms in front of the body (and of the head: a hand can reach the chin)
        order_back = [n for n in order_back if n not in ("arm_f", "fore_f")]
        order_front = ["head", "wing_n", "arm_f", "fore_f", "arm_n", "fore_n"]
    if not wings_over:
        order_front = [n for n in order_front if n != "wing_n"]
        order_back = ["tail", "wing_f", "wing_n"] + [n for n in order_back if n not in ("tail", "wing_f")]
    if spec.get("legs_over_body"):
        order_back = [n for n in order_back if n not in ("leg_n", "shin_n")]
        order_front = ["leg_n", "shin_n"] + order_front
    order_front = ["crank"] + order_front
    spec["_head_mask"] = head_m
    by = {n: (m, p, par) for n, m, p, par, _ in layers}
    far_shade = spec.get("far_shade", 0.86 if copied else 1.0)
    out = []
    for n in order_back:
        if n in by:
            m, p, par = by[n]
            if n in ("leg_f", "shin_f") and spec.get("_far_from_near"):
                dx, dy = spec["_far_from_near"]
                src = np.roll(np.roll(m, -dx, axis=1), -dy, axis=0)
                arr = np.asarray(R.cut(view, src))
                img = Image.fromarray(np.roll(np.roll(arr, dx, axis=1), dy, axis=0), "RGBA")
            else:
                img = R.cut(view, m)
            if n.endswith("_f") and not front_view and far_shade != 1.0:
                img = R.shade(img, far_shade)
            out.append(R.Layer(n, img, p, parent=par))
    out.append(R.Layer("body", body, J["hip"], sway=sway))
    for n in order_front:
        if n in by:
            m, p, par = by[n]
            out.append(R.Layer(n, R.cut(view, m), p, parent=par))
    return view, out, J


def ik2(root, l1, l2, target, bend=1):
    """Two-bone IK in screen space: angles (radians, atan2 in image coords) of bone 1 and 2."""
    dx, dy = target[0] - root[0], target[1] - root[1]
    d = max(1e-3, min(math.hypot(dx, dy), l1 + l2 - 1e-3))
    a = math.atan2(dy, dx)
    c = (l1 * l1 + d * d - l2 * l2) / (2 * l1 * d)
    b = math.acos(max(-1.0, min(1.0, c)))
    a1 = a - bend * b
    kx, ky = root[0] + l1 * math.cos(a1), root[1] + l1 * math.sin(a1)
    a2 = math.atan2(target[1] - ky, target[0] - kx)
    return a1, a2


def ik_pose(J, root, mid, end, target, bend=1):
    """Deltas (degrees, screen CCW) that move limb root->mid->end so `end` reaches target."""
    l1 = math.dist(J[root], J[mid])
    l2 = math.dist(J[mid], J[end])
    a1, a2 = ik2(J[root], l1, l2, target, bend)
    r1 = math.atan2(J[mid][1] - J[root][1], J[mid][0] - J[root][0])
    r2 = math.atan2(J[end][1] - J[mid][1], J[end][0] - J[mid][0])
    # image y points down, so a screen-CCW angle is the negative of the atan2 delta
    up = -math.degrees(a1 - r1)
    lo = -math.degrees(a2 - r2) - up
    return up, lo


def to_frame(img, spec, scale=None):
    """View image -> 384 frame: scale by the spec's height, anchor point on ANCHOR_HI."""
    s = scale or frame_scale(spec)
    w, h = img.size
    small = img.resize((max(1, round(w * s)), max(1, round(h * s))), Image.LANCZOS)
    f = Image.new("RGBA", (FRAME_HI, FRAME_HI), (0, 0, 0, 0))
    P = spec.get("_pad", 0)
    x = round(ANCHOR_HI[0] - (spec["anchor_x"] + P) * s)
    y = round(ANCHOR_HI[1] - (spec["ground_y"] + P) * s)
    f.paste(small, (x, y), small)
    return f


def frame_scale(spec):
    """One scale per character: its standing height maps to STAND_HEIGHT_HI. Sitting and other
    bases give the standing height they were drawn at (spec.stand_height, view px)."""
    h = spec.get("stand_height") or (spec["ground_y"] - spec["head_top"])
    return STAND_HEIGHT_HI / h


def variant_head(spec, expr):
    """The head layer cut from an expression variant of the drawing (<image>_<expr>.png),
    aligned to the drawing by phase correlation. None when there is no such variant."""
    src = os.path.join(HERE, spec["image"])
    root, ext = os.path.splitext(src)
    vpath = f"{root}_{expr}{ext}"
    mask = spec.get("_head_mask")
    if mask is None or not os.path.exists(vpath):
        return None
    base = np.asarray(Image.open(_key(spec)).convert("RGBA")).astype(np.float32)
    var = to_image(key_green(vpath))
    if var.size != (base.shape[1], base.shape[0]):
        var = var.resize((base.shape[1], base.shape[0]), Image.LANCZOS)
    v = np.asarray(var).astype(np.float32)

    def gray(a):
        return (a[..., :3].mean(axis=2) * (a[..., 3] / 255.0)).astype(np.float32)
    (dx, dy), _ = cv2.phaseCorrelate(gray(base), gray(v))
    P = spec.get("_pad", 0)
    m = np.float32([[1, 0, -dx + P], [0, 1, -dy + P]])
    v = cv2.warpAffine(v, m, (mask.shape[1], mask.shape[0]), flags=cv2.INTER_LINEAR, borderValue=0)
    v[..., 3] = np.where(mask, v[..., 3], 0)
    return Image.fromarray(np.clip(v, 0, 255).astype(np.uint8), "RGBA")


def _variant(spec, tag, size, P):
    """<image>_<tag>.png keyed, aligned to the drawing by phase correlation, on the padded canvas."""
    src = os.path.join(HERE, spec["image"])
    root, ext = os.path.splitext(src)
    vpath = f"{root}_{tag}{ext}"
    if not os.path.exists(vpath):
        return None
    base = np.asarray(Image.open(_key(spec)).convert("RGBA")).astype(np.float32)
    var = to_image(key_green(vpath))
    if var.size != (base.shape[1], base.shape[0]):
        var = var.resize((base.shape[1], base.shape[0]), Image.LANCZOS)
    v = np.asarray(var).astype(np.float32)

    def gray(a):
        return (a[..., :3].mean(axis=2) * (a[..., 3] / 255.0)).astype(np.float32)
    (dx, dy), _ = cv2.phaseCorrelate(gray(base), gray(v))
    m = np.float32([[1, 0, -dx + P], [0, 1, -dy + P]])
    out = cv2.warpAffine(v, m, size, flags=cv2.INTER_LINEAR, borderValue=0)
    return Image.fromarray(np.clip(out, 0, 255).astype(np.uint8), "RGBA")
