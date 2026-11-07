"""2D cut-out rig: layers cut from one character view, each turning about its joint.

A rig is a list of layers (back to front). Each layer is an RGBA image the size of the
view, a pivot (its joint, view px) and a parent; a pose gives every layer an angle
(degrees, counter-clockwise on screen) and an offset, composed down the parent chain.
A layer may also carry a sway warp (hair): pixels in its mask shift sideways by an
amount that grows toward the tips, so long hair trails behind the motion.

    frames = render(layers, poses)        # poses: one {layer: {...}} per frame
"""
import math

import cv2
import numpy as np
from PIL import Image


class Layer:
    def __init__(self, name, rgba, pivot, parent=None, sway=None):
        self.name, self.pivot, self.parent, self.sway = name, pivot, parent, sway
        a = np.asarray(rgba, dtype=np.float32) / 255.0
        a[..., :3] *= a[..., 3:4]  # premultiplied, so edges stay clean under resampling
        self.img = a


def _affine(angle, dx, dy, pivot):
    """2x3 matrix: rotate about pivot, then translate."""
    m = cv2.getRotationMatrix2D((float(pivot[0]), float(pivot[1])), angle, 1.0)
    m[0, 2] += dx
    m[1, 2] += dy
    return np.vstack([m, [0, 0, 1]])


def _sway(img, sway, amount):
    """Shift the masked pixels sideways by amount * weight(y): weight grows from y0 to y1."""
    if not amount:
        return img
    h, w = img.shape[:2]
    y0, y1 = sway["y0"], sway["y1"]
    ys = np.arange(h, dtype=np.float32)
    wy = np.clip((ys - y0) / max(1.0, y1 - y0), 0.0, 1.0) ** sway.get("power", 1.6)
    mask = sway["mask"]  # float 0..1, view size
    dx = (amount * wy)[:, None] * mask
    mx, my = np.meshgrid(np.arange(w, dtype=np.float32), ys)
    return cv2.remap(img, mx - dx, my, interpolation=cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT, borderValue=0)


def render(layers, poses, size):
    """poses[i][layer] = {"a": deg, "dx": px, "dy": px, "sway": px}; returns RGBA PIL frames."""
    by_name = {l.name: l for l in layers}
    out = []
    for pose in poses:
        mats = {}

        def world(name):
            if name in mats:
                return mats[name]
            l = by_name[name]
            p = pose.get(name, {})
            local = _affine(p.get("a", 0.0), p.get("dx", 0.0), p.get("dy", 0.0), l.pivot)
            m = world(l.parent) @ local if l.parent else local
            mats[name] = m
            return m

        acc = np.zeros((size[1], size[0], 4), dtype=np.float32)
        for l in layers:
            img = _sway(l.img, l.sway, pose.get(l.name, {}).get("sway", 0.0)) if l.sway else l.img
            m = world(l.name)[:2]
            warped = cv2.warpAffine(img, m, size, flags=cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT, borderValue=0)
            acc = warped + acc * (1.0 - warped[..., 3:4])
        rgb = np.where(acc[..., 3:4] > 1e-4, acc[..., :3] / np.maximum(acc[..., 3:4], 1e-4), 0)
        arr = np.dstack([rgb, acc[..., 3]])
        out.append(Image.fromarray((np.clip(arr, 0, 1) * 255 + 0.5).astype(np.uint8), "RGBA"))
    return out


def cut(rgba, mask):
    """The part of rgba under mask (L image / bool array)."""
    a = np.asarray(rgba).copy()
    m = np.asarray(mask) > 127 if not isinstance(mask, np.ndarray) or mask.dtype != bool else mask
    a[..., 3] = np.where(m, a[..., 3], 0)
    return Image.fromarray(a, "RGBA")


def fill_behind(rgba, hole, radius=9):
    """Fill hole (bool) in rgba by inpainting colour from the surroundings; alpha there = 255
    where the surroundings are opaque (what was behind a cut-out limb)."""
    a = np.asarray(rgba).copy()
    rgb = np.ascontiguousarray(a[..., :3][..., ::-1])
    m = (hole.astype(np.uint8) * 255)
    filled = cv2.inpaint(rgb, m, radius, cv2.INPAINT_TELEA)[..., ::-1]
    a[..., :3] = np.where(hole[..., None], filled, a[..., :3])
    # alpha: opaque where the hole sits inside the silhouette (closed alpha), clear outside
    # opaque only where the hole lies inside the body's own (closed) silhouette: a limb held out
    # against the background leaves background behind it
    sil = cv2.morphologyEx((a[..., 3] > 127).astype(np.uint8), cv2.MORPH_CLOSE, np.ones((15, 15), np.uint8))
    a[..., 3] = np.where(hole, sil * 255, a[..., 3])
    return Image.fromarray(a, "RGBA")


def fill_band(rgba, band, radius=7):
    """Paint `band` (bool) opaque with colour inpainted from its surroundings: the backing behind a
    seam that opens when a part turns (a head against its hair)."""
    if not band.any():
        return rgba
    a = np.asarray(rgba).copy()
    rgb = np.ascontiguousarray(a[..., :3][..., ::-1])
    filled = cv2.inpaint(rgb, band.astype(np.uint8) * 255, radius, cv2.INPAINT_TELEA)[..., ::-1]
    a[..., :3] = np.where(band[..., None], filled, a[..., :3])
    a[..., 3] = np.where(band, 255, a[..., 3])
    return Image.fromarray(a, "RGBA")


def largest(mask, keep=1, frac=0.04):
    """Drop SAM's stray specks: keep the `keep` largest connected parts plus any part at least
    `frac` of the largest (a hand cut off its sleeve by an outline is still the arm)."""
    n, lab, stats, _ = cv2.connectedComponentsWithStats(mask.astype(np.uint8), 8)
    if n <= 1:
        return mask
    areas = stats[1:, cv2.CC_STAT_AREA]
    order = list(np.argsort(-areas)[:keep] + 1)
    order += [i + 1 for i in range(len(areas)) if areas[i] >= frac * areas.max() and i + 1 not in order]
    return np.isin(lab, order)


def shade(rgba, k):
    a = np.asarray(rgba).astype(np.float32)
    a[..., :3] *= k
    return Image.fromarray(np.clip(a, 0, 255).astype(np.uint8), "RGBA")


def cycle(i, n=8, phase=0.0):
    return math.sin(2 * math.pi * i / n + phase)
