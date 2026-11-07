"""Render every action of a character from its rig specs + the shared props.

    python make_rig.py <character> [action ...]        # default: every action whose base has a spec
    python make_rig.py <character> --check <base>      # rest pose vs drawing, joints and layers (debug)

Writes $SPRITE_OUT/<character>/hi/<action>/00..07.png (384), frames/<action>/ (192),
<character>.png/.webp sheet + <character>.json manifest (merged with what is there), and
rig/check/<character>_<action>.jpg contact strips.
"""
import json
import math
import os
import sys

import numpy as np
from PIL import Image, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import actions2d as A  # noqa: E402
import rig2d as R  # noqa: E402
import rigkit as K  # noqa: E402
from sprites import key_green, to_image  # noqa: E402

OUT = os.environ.get("SPRITE_OUT", r"C:/Users/g2772/AppData/Local/Temp/claude/H--visual-editor/3c658b1f-3dd5-412a-b8a9-facf7f2aa2a4/scratchpad/shots/gemini")
CHECK = os.path.join(HERE, "rig", "check")
VIEW = {"front_stand": "front", "side_stand": "side", "front_sit": "front", "side_sit": "side", "side_bike": "side"}
PROPS = {"hike-walk": ["pack"], "cycle-ride": ["bike"], "stream-wade": ["pole"], "bbq-grill": ["tongs"], "fish-sit": ["rod"],
         **{a: ["desk"] for a in A.BASE if a.startswith("desk-")}}
TIPS = {}
SEAT_ANCHOR = {}
SEATED = ("sofa-sit", "sofa-sleep", "camp-sit", "fish-sit")
EXPR = {"desk-sleep": "sleep", "sofa-sleep": "sleep", "desk-slump": "sad", "desk-cheer": "happy", "hike-summit": "happy"}


def spec_for(cid, base):
    p = os.path.join(K.SPECS, f"{cid}_{base}.json")
    return K.load(p) if os.path.exists(p) else None


def prop_spec():
    return json.load(open(os.path.join(K.SPECS, "props.json"), encoding="utf-8"))


def prop_image(name, ps):
    p = ps[name]
    if not os.path.exists(os.path.join(HERE, p["image"])):
        return None
    path = os.path.join(K.WORK, f"prop_{name}.png")
    src = os.path.join(HERE, p["image"])
    if not os.path.exists(path) or os.path.getmtime(path) < os.path.getmtime(src):
        to_image(key_green(src)).save(path)
    return Image.open(path).convert("RGBA")


def place_prop(img, src_pt, dst_pt, scale, canvas):
    """Scale a prop so its point src_pt lands on dst_pt of a canvas-size layer image."""
    w, h = img.size
    s = img.resize((max(1, round(w * scale)), max(1, round(h * scale))), Image.LANCZOS)
    layer = Image.new("RGBA", canvas, (0, 0, 0, 0))
    layer.paste(s, (round(dst_pt[0] - src_pt[0] * scale), round(dst_pt[1] - src_pt[1] * scale)), s)
    return layer


def desk_layer(canvas, cx, top_y, width, u, floor_y):
    """A small wooden desk seen from the front, drawn in the sprites' toon style: a light top
    surface (seen a little from above), a darker front panel with a drawer line, dark outline."""
    layer = Image.new("RGBA", canvas, (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    ow = max(3, round(5 * u))
    top_h = 70 * u
    front_h = max(120 * u, floor_y - top_y - top_h)
    x0, x1 = cx - width / 2, cx + width / 2
    inset = 24 * u
    top = [(x0 + inset, top_y), (x1 - inset, top_y), (x1, top_y + top_h), (x0, top_y + top_h)]
    front = [(x0, top_y + top_h), (x1, top_y + top_h), (x1, top_y + top_h + front_h), (x0, top_y + top_h + front_h)]
    outline = (74, 46, 32, 255)
    d.polygon(front, fill=(170, 112, 70, 255), outline=outline, width=ow)
    d.rectangle([x0 + 0.08 * width, top_y + top_h + 40 * u, x1 - 0.08 * width, top_y + top_h + 120 * u],
                outline=(120, 76, 48, 255), width=max(2, ow - 2))
    d.ellipse([cx - 14 * u, top_y + top_h + 72 * u, cx + 14 * u, top_y + top_h + 92 * u], fill=(220, 180, 110, 255), outline=outline)
    d.polygon(top, fill=(222, 168, 112, 255), outline=outline, width=ow)
    d.line([(x0 + inset + 10 * u, top_y + 14 * u), (x1 - inset - 10 * u, top_y + 14 * u)], fill=(240, 200, 150, 255), width=max(2, ow - 1))
    return layer


def stick(canvas, a, b, width, colour, outline=(40, 32, 48, 255)):
    """A procedural rod/pole/tongs stroke with a dark outline."""
    layer = Image.new("RGBA", canvas, (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    d.line([a, b], fill=outline, width=width + 6)
    d.line([a, b], fill=colour, width=width)
    return layer


def rig_for(cid, action, spec, view, layers, J):
    """Layers + props for an action, and the props geometry the action needs."""
    u = A.unit(spec)
    ps = prop_spec()
    extra = {}
    layers = list(layers)
    names = [l.name for l in layers]

    def insert(layer, before=None, after=None):
        idx = names.index(before) if before in names else (names.index(after) + 1 if after in names else len(names))
        layers.insert(idx, layer)
        names.insert(idx, layer.name)

    if action.startswith("desk-"):
        lp = ps["laptop"]
        span = abs(J["shoulder_f"][0] - J["shoulder_n"][0])
        cx = (J["shoulder_n"][0] + J["shoulder_f"][0]) / 2
        desk_y = J["elbow_n"][1] + 40 * u
        insert(R.Layer("desk", desk_layer(view.size, cx, desk_y, span * 3.3, u, spec["ground_y"] + spec.get("_pad", 0)), (cx, desk_y)), before="arm_f")
        lap_img = prop_image("laptop", ps)
        if lap_img is not None:
            ls = span * lp["width_shoulders"] / (lp["right"][0] - lp["left"][0])
            bottom_mid = ((lp["left"][0] + lp["right"][0]) / 2, lp["left"][1])
            insert(R.Layer("laptop", place_prop(lap_img, bottom_mid, (cx, desk_y + 14 * u), ls, view.size), (cx, desk_y)))
        extra["desk_y"] = desk_y
        if action == "desk-sleep":  # her head lies on her folded arms
            hi = names.index("head")
            head = layers.pop(hi)
            names.pop(hi)
            insert(head, before="laptop")
    elif action == "hike-walk":
        b = ps["pack"]
        img = prop_image("pack", ps)
        if img is None:
            return layers, extra
        s = (b["height_units"] * u) / img.size[1]
        at = (J["neck"][0] - b["back_offset"] * u, J["neck"][1] + b["drop"] * u)
        insert(R.Layer("pack", place_prop(img, b["strap"], at, s, view.size), at, parent="body"), before="arm_n")
    elif action == "cycle-ride" and spec["base"] == "side_bike":
        extra.update(crank=J["crank"], pedal_r=spec["pedal_r"], crank_rest=spec.get("crank_rest", 90.0),
                     tail_a=spec.get("tail_a", 0.0), knee_bend=spec.get("knee_bend", -1))
    elif action == "cycle-ride":
        b = ps["bike"]
        img = prop_image("bike", ps)
        s = (b["wheel_d_units"] * u) / b["wheel_d"]
        sad = J.get("seat", J["hip"])  # the saddle top meets her seat, a little under the hip joint

        def at(pt):
            return (sad[0] + (pt[0] - b["saddle"][0]) * s, sad[1] + (pt[1] - b["saddle"][1]) * s)
        # split the bike: the crank (chainring, arm, pedal) turns, the rest holds still
        arr = np.asarray(img).astype(np.int16)
        hh, ww = arr.shape[:2]
        yy, xx = np.mgrid[0:hh, 0:ww]
        r, g, bl = arr[..., 0], arr[..., 1], arr[..., 2]
        blue = (bl > 180) & (g > 150) & (r < 170)
        crank_m = ((xx - b["crank"][0]) ** 2 + (yy - b["crank"][1]) ** 2 <= b["crank_cut_r"] ** 2) & ~blue
        crank_img = R.cut(img, crank_m)
        frame_img = R.cut(img, ~crank_m)
        crank_at = at(b["crank"])
        insert(R.Layer("bike", place_prop(frame_img, b["saddle"], sad, s, view.size), sad), before="leg_n" if "leg_n" in names else "body")
        insert(R.Layer("crank", place_prop(crank_img, b["saddle"], sad, s, view.size), crank_at), before="leg_n" if "leg_n" in names else "body")
        extra.update(crank=crank_at, pedal_r=b["pedal_r"] * s, grip=at(b["grip"]))
        extra["ground_y"] = at(b["ground"])[1] - spec.get("_pad", 0)
    elif action in ("sofa-sit", "sofa-sleep", "camp-sit", "fish-sit"):
        extra["lap_y"] = J.get("lap", J["wrist_n"])[1]
    elif action == "bbq-grill":
        # tongs continue the forearm, so they point at the grill once the arm comes forward
        w, e = J["wrist_n"], J["elbow_n"]
        L = math.dist(w, e) or 1.0
        dx, dy = (w[0] - e[0]) / L, (w[1] - e[1]) / L
        insert(R.Layer("tongs", stick(view.size, (w[0] + dx * 15 * u, w[1] + dy * 15 * u), (w[0] + dx * 170 * u, w[1] + dy * 170 * u),
                                      max(3, round(8 * u)), (190, 190, 200, 255)), w, parent="fore_n"), before="arm_n")
    elif action == "stream-wade":
        # an upright walking pole planted in front, where the posed hand holds it
        hand = fk_hand(J, "n", 25, 35)
        insert(R.Layer("pole", stick(view.size, (hand[0] + 6 * u, hand[1] - 110 * u), (hand[0] + 22 * u, spec["ground_y"] + spec.get("_pad", 0)),
                                     max(3, round(8 * u)), (150, 110, 70, 255)), hand, parent="body"), before="arm_n")
    return layers, extra


def fk_hand(J, side, up, lo):
    """Where the wrist ends up when the arm turns `up` degrees at the shoulder and `lo` more at the elbow
    (screen counter-clockwise, as poses are)."""
    def rot(p, c, deg):
        a = -math.radians(deg)  # screen CCW is negative in image coordinates (y down)
        x, y = p[0] - c[0], p[1] - c[1]
        return (c[0] + x * math.cos(a) - y * math.sin(a), c[1] + x * math.sin(a) + y * math.cos(a))
    s, e, w = J[f"shoulder_{side}"], J[f"elbow_{side}"], J[f"wrist_{side}"]
    e2 = rot(e, s, up)
    w2 = rot(rot(w, s, up), e2, lo)
    return w2


def view_to_frame(pt, spec, s, ground_y=None):
    """A view point (padded canvas px) -> 192-frame px, as to_frame places it."""
    P = spec.get("_pad", 0)
    gy = (ground_y if ground_y is not None else spec["ground_y"]) + P
    x = K.ANCHOR_HI[0] + (pt[0] - (spec["anchor_x"] + P)) * s
    y = K.ANCHOR_HI[1] + (pt[1] - gy) * s
    return {"x": round(x / 2), "y": round(y / 2)}


ROD_TIP = (186, 36)   # the rod's tip in the 192 frame: out over the water, where the scene hangs the line


def frame_to_view(fpt, spec, s, ground_y=None):
    """192-frame px -> view (padded canvas) px; the inverse of view_to_frame."""
    P = spec.get("_pad", 0)
    gy = (ground_y if ground_y is not None else spec["ground_y"]) + P
    return ((fpt[0] * 2 - K.ANCHOR_HI[0]) / s + spec["anchor_x"] + P, (fpt[1] * 2 - K.ANCHOR_HI[1]) / s + gy)


def add_rod(layers, spec, J, pose0, extra):
    """A fishing rod from the hand that holds it (where the posed frame 0 puts that hand) out to
    ROD_TIP, held still with the body."""
    u = A.unit(spec)
    hand = fk_hand(J, "f", pose0.get("arm_f", {}).get("a", 0.0), pose0.get("fore_f", {}).get("a", 0.0))
    hand = (hand[0], hand[1] + pose0.get("body", {}).get("dy", 0.0))
    tip = frame_to_view(ROD_TIP, spec, K.frame_scale(spec))
    L = math.dist(hand, tip) or 1.0
    butt = (hand[0] - (tip[0] - hand[0]) / L * 40 * u, hand[1] - (tip[1] - hand[1]) / L * 40 * u)
    size = (layers[0].img.shape[1], layers[0].img.shape[0])
    rod = R.Layer("rod", stick(size, butt, tip, max(3, round(6 * u)), (150, 98, 60, 255)), hand, parent="body")
    names = [l.name for l in layers]
    at = names.index("arm_f") if "arm_f" in names else len(layers)
    TIPS[(spec["character"], "fish-sit")] = {"rod": {"x": ROD_TIP[0], "y": ROD_TIP[1]}}
    return layers[:at] + [rod] + layers[at:]


def save_action(cid, action, spec, frames, extra):
    s = K.frame_scale(spec)
    sp = dict(spec)
    if "ground_y" in extra:
        sp["ground_y"] = extra["ground_y"]

    hi = [K.to_frame(f, sp, s) for f in frames]
    hd, ld = os.path.join(OUT, cid, "hi", action), os.path.join(OUT, cid, "frames", action)
    os.makedirs(hd, exist_ok=True)
    os.makedirs(ld, exist_ok=True)
    lo = []
    for i, f in enumerate(hi):
        f.save(os.path.join(hd, f"{i:02d}.png"))
        sm = f.resize((K.FRAME_LO, K.FRAME_LO), Image.LANCZOS)
        sm.save(os.path.join(ld, f"{i:02d}.png"))
        lo.append(sm)
    os.makedirs(CHECK, exist_ok=True)
    strip = Image.new("RGBA", (K.FRAME_HI * 8, K.FRAME_HI), (236, 236, 240, 255))
    for i, f in enumerate(hi):
        strip.alpha_composite(f, (K.FRAME_HI * i, 0))
    strip.convert("RGB").save(os.path.join(CHECK, f"{cid}_{action}.jpg"), quality=88)
    return lo


def head_box(frame_lo):
    a = np.asarray(frame_lo.getchannel("A")) > 128
    ys, xs = np.nonzero(a)
    if not len(ys):
        return {"x": 0, "y": 0, "w": 0, "h": 0}
    y0 = ys.min()
    hh = int((ys.max() - y0) * 0.4)
    band = a[y0:y0 + hh]
    cols = np.nonzero(band.any(axis=0))[0]
    return {"x": int(cols.min()), "y": int(y0), "w": int(cols.max() - cols.min()), "h": hh}


def write_sheet(cid, done):
    root = os.path.join(OUT, cid)
    mpath = os.path.join(root, f"{cid}.json")
    man = json.load(open(mpath)) if os.path.exists(mpath) else {"actions": {}}
    man.update({"image": f"{cid}.webp", "frameW": 192, "frameH": 192, "scale": 6, "style": "anime", "character": cid})
    acts = man.setdefault("actions", {})
    for action, (lo, view) in done.items():
        acts[action] = {"row": 0, "frames": 8, "fps": 10, "view": view,
                        "anchor": SEAT_ANCHOR.get((cid, action), {"x": 96, "y": 174}), "head": head_box(lo[0]),
                        "props": PROPS.get(action, [])}
        if (cid, action) in TIPS:
            acts[action]["tips"] = TIPS[(cid, action)]
    if "fish-sit" in acts and "sofa-sit" in acts:  # the rod would widen the head box: same pose, same head
        acts["fish-sit"]["head"] = acts["sofa-sit"]["head"]
    names = list(acts)
    rows = []
    for r, a in enumerate(names):
        acts[a]["row"] = r
        fdir = os.path.join(root, "frames", a)
        rows.append([Image.open(os.path.join(fdir, f"{i:02d}.png")).convert("RGBA") for i in range(8)])
    sheet = Image.new("RGBA", (192 * 8, 192 * len(rows)), (0, 0, 0, 0))
    for r, fr in enumerate(rows):
        for c, f in enumerate(fr):
            sheet.alpha_composite(f, (c * 192, r * 192))
    sheet.save(os.path.join(root, f"{cid}.png"))
    sheet.save(os.path.join(root, f"{cid}.webp"), quality=90, method=6)
    json.dump(man, open(mpath, "w"), indent=1)
    return sheet.size


def check(cid, base):
    spec = spec_for(cid, base)
    view, layers, J = K.build(spec)
    rest = R.render(layers, [{}], view.size)[0]
    dbg = Image.new("RGBA", (view.size[0] * 3, view.size[1]), (255, 255, 255, 255))
    dbg.alpha_composite(view, (0, 0))
    dbg.alpha_composite(rest, (view.size[0], 0))
    # layers tinted, joints dotted
    tints = [(255, 80, 80), (80, 200, 80), (80, 120, 255), (255, 190, 0), (230, 0, 230), (0, 200, 200), (255, 128, 0), (130, 70, 255), (120, 120, 120)]
    canvas = Image.new("RGBA", view.size, (255, 255, 255, 255))
    for i, l in enumerate(layers):
        a = (np.clip(l.img[..., 3], 0, 1) * 255).astype(np.uint8)
        col = Image.new("RGBA", view.size, tints[i % len(tints)] + (255,))
        canvas.paste(col, (0, 0), Image.fromarray((a * 0.75).astype(np.uint8)))
    d = ImageDraw.Draw(canvas)
    for k, (x, y) in J.items():
        d.ellipse([x - 7, y - 7, x + 7, y + 7], fill=(0, 0, 0, 255))
        d.text((x + 9, y - 6), k, fill=(0, 0, 0, 255))
    gy = spec["ground_y"] + spec.get("_pad", 0)
    d.line([(0, gy), (view.size[0], gy)], fill=(255, 0, 0, 255), width=2)
    dbg.alpha_composite(canvas, (view.size[0] * 2, 0))
    os.makedirs(CHECK, exist_ok=True)
    out = os.path.join(CHECK, f"{cid}_{base}_check.jpg")
    dbg.convert("RGB").resize((dbg.width // 2, dbg.height // 2), Image.LANCZOS).save(out, quality=88)
    print(out, [l.name for l in layers])


def main():
    cid = sys.argv[1]
    if "--check" in sys.argv:
        check(cid, sys.argv[sys.argv.index("--check") + 1])
        return
    wanted = sys.argv[2:] or list(A.BASE)
    built, done = {}, {}
    for action in wanted:
        base = A.BASE[action]
        if base not in built:
            spec = spec_for(cid, base)
            if not spec:
                print(f"skip {action}: no {base} spec")
                continue
            built[base] = (spec, K.build(spec))  # build() annotates the spec (_pad, _head_mask): keep that one
        spec, (view, layers, J) = built[base]
        ls, extra = rig_for(cid, action, spec, view, layers, J)
        expr = EXPR.get(action)
        if expr:
            head = K.variant_head(spec, expr)
            if head is not None:
                ls = [R.Layer(l.name, head, l.pivot, parent=l.parent, sway=l.sway) if l.name == "head" else l for l in ls]
        poses = A.poses(action, spec, J, extra)
        if action == "fish-sit":
            ls = add_rod(ls, spec, J, poses[0], extra)
        if action in SEATED:
            # seated on the scene's sofa, log or rock: her seat (not her soles) goes on its top
            seat = J.get("seat") or (J["hip"][0], J["hip"][1] + 40 * A.unit(spec))
            # on the frame's centre line, as every anchor is; only its height is the seat's
            SEAT_ANCHOR[(cid, action)] = {"x": 96, "y": view_to_frame(seat, spec, K.frame_scale(spec))["y"]}
        frames = R.render(ls, poses, view.size)
        done[action] = (save_action(cid, action, spec, frames, extra), VIEW[base])
        print(f"{cid} {action}: ok")
    if done:
        print("sheet", write_sheet(cid, done))


if __name__ == "__main__":
    main()
