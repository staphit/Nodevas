#!/usr/bin/env python3
"""Check the example view packs against the rules in docs/view-packs.md.

Standard library only. Run from anywhere:

    python3 -I examples/view-packs/check.py            # every pack here
    python3 -I examples/view-packs/check.py neon-station

Exits 0 when every pack passes, 1 otherwise. The hub checks the same rules
again when a pack is installed; this is a quick local check before zipping.
"""

import json
import os
import re
import sys
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))

# Box sizes from docs/view-packs.md, as (width, height) in px.
ROOMS = {
    "agent": (80, 96),
    "agent.working": (80, 96),
    "agent.reviewing": (80, 96),
    "agent.asking-user": (80, 96),
    "agent.awaiting-accept": (80, 96),
    "agent.waiting-ci": (80, 96),
    "agent.failed": (80, 96),
    "agent.idle": (80, 96),
    "agent.offline": (80, 96),
    "agent.standing": (40, 64),
    "desk.empty": (80, 96),
    "table": (96, 56),
}
PATH = {
    "monster": (48, 48),
    "monster.attackable": (48, 48),
    "monster.locked": (48, 48),
    "monster.defeated": (48, 48),
    "boss": (140, 130),
    "boss.defeated": (140, 130),
    "hero": (30, 42),
    "tile.corridor": (80, 44),
    "tile.arena": (80, 47),
    "gate.closed": (51, 74),
    "gate.open": (51, 74),
    "chest.closed": (27, 30),
    "chest.open": (27, 30),
    "column": (20, 59),
    "barrel": (25, 26),
}
LAYOUTS = {"rooms": ROOMS, "path": PATH}

TOP_KEYS = {"format", "id", "name", "layout", "palette", "sprites"}
REQUIRED_KEYS = {"format", "id", "name", "layout"}
PALETTE_KEYS = {"ground", "text"}
ID_RE = re.compile(r"^[a-z0-9-]{1,40}$")
COLOUR_RE = re.compile(r"^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$")

MAX_FILES = 200
MAX_PACK_BYTES = 20 * 1024 * 1024
MAX_IMAGE_BYTES = 2 * 1024 * 1024  # the spec's limit
MAX_SVG_BYTES = 10 * 1024  # this folder's own limit for hand-written SVGs

# The SVG namespace declaration is the one URL-looking string an SVG needs:
# without it an SVG loaded through <img> renders blank. It names the format
# and is never fetched, so it is removed before scanning for URLs.
SVG_XMLNS = 'xmlns="http://www.w3.org/2000/svg"'
SVG_FORBIDDEN = [
    "<script",
    "foreignobject",
    "http",
    "//",
    "href",
    "url(",
    "@import",
    "data:",
    "<style",
    "<image",
    "<!doctype",
    "<!entity",
]
JSON_URLISH = re.compile(r"(?i)(://|^//|https?:|www\.|data:|javascript:|file:)")
SVG_NS = "{http://www.w3.org/2000/svg}"


def json_strings(value):
    """Every string in a parsed JSON value, keys included."""
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for k, v in value.items():
            yield k
            yield from json_strings(v)
    elif isinstance(value, list):
        for v in value:
            yield from json_strings(v)


def check_svg(path, rel, box, errors):
    data = open(path, "rb").read()
    if len(data) > MAX_SVG_BYTES:
        errors.append(f"{rel}: {len(data)} bytes, over the {MAX_SVG_BYTES}-byte limit for these examples")
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        errors.append(f"{rel}: not UTF-8 text")
        return
    if text.count(SVG_XMLNS) != 1:
        errors.append(f"{rel}: needs exactly one {SVG_XMLNS}")
    scan = text.replace(SVG_XMLNS, "").lower()
    for bad in SVG_FORBIDDEN:
        if bad in scan:
            errors.append(f"{rel}: contains {bad!r}")
    try:
        root = ET.fromstring(text)
    except ET.ParseError as e:
        errors.append(f"{rel}: not well-formed XML ({e})")
        return
    if root.tag != SVG_NS + "svg":
        errors.append(f"{rel}: the root element is not <svg>")
        return
    for el in root.iter():
        for attr in el.attrib:
            if attr.lower().startswith("on"):
                errors.append(f"{rel}: event attribute {attr!r} on <{el.tag.replace(SVG_NS, '')}>")
    if box is None:
        return
    vb = root.get("viewBox", "").replace(",", " ").split()
    if len(vb) != 4:
        errors.append(f"{rel}: needs a viewBox")
        return
    vw, vh = float(vb[2]), float(vb[3])
    bw, bh = box
    if vw <= 0 or vh <= 0 or abs((vw / vh) / (bw / bh) - 1) > 0.02:
        errors.append(f"{rel}: viewBox {vw:g}x{vh:g} does not match the {bw}x{bh} box's proportions")
    elif not 1.5 <= vw / bw <= 3:
        errors.append(f"{rel}: viewBox {vw:g}x{vh:g} is not about 2x the {bw}x{bh} box")


def sniff(path):
    head = open(path, "rb").read(512)
    if head.startswith(b"\x89PNG\r\n\x1a\n"):
        return "png"
    if head[:4] == b"RIFF" and head[8:12] == b"WEBP":
        return "webp"
    if b"<svg" in head.lstrip()[:512]:
        return "svg"
    return None


def check_pack(folder):
    errors, warnings = [], []
    name = os.path.basename(folder)
    pj_path = os.path.join(folder, "pack.json")
    if not os.path.isfile(pj_path):
        return [f"no pack.json"], warnings

    # Every file in the folder is what the zip will hold.
    files, total = [], 0
    for dirpath, dirnames, filenames in os.walk(folder):
        for d in dirnames:
            if os.path.islink(os.path.join(dirpath, d)):
                errors.append(f"{os.path.relpath(os.path.join(dirpath, d), folder)}: a link")
        for f in filenames:
            full = os.path.join(dirpath, f)
            rel = os.path.relpath(full, folder).replace(os.sep, "/")
            if f == ".DS_Store":
                warnings.append(f"{rel}: leave it out of the zip (zip -x '*.DS_Store')")
                continue
            if os.path.islink(full):
                errors.append(f"{rel}: a link")
                continue
            files.append(rel)
            total += os.path.getsize(full)
    if len(files) > MAX_FILES:
        errors.append(f"{len(files)} files, over {MAX_FILES}")
    if total > MAX_PACK_BYTES:
        errors.append(f"{total} bytes unpacked, over {MAX_PACK_BYTES}")

    raw = open(pj_path, "rb").read()
    try:
        pack = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as e:
        return errors + [f"pack.json does not parse: {e}"], warnings
    if not isinstance(pack, dict):
        return errors + ["pack.json is not an object"], warnings

    for s in json_strings(pack):
        if JSON_URLISH.search(s):
            errors.append(f"pack.json: looks like a URL: {s!r}")

    for k in sorted(set(pack) - TOP_KEYS):
        errors.append(f"pack.json: unknown key {k!r}")
    for k in sorted(REQUIRED_KEYS - set(pack)):
        errors.append(f"pack.json: missing {k!r}")

    fmt = pack.get("format")
    if type(fmt) is not int or fmt != 1:
        errors.append(f"pack.json: format must be 1, not {fmt!r}")

    pid = pack.get("id")
    if not isinstance(pid, str) or not ID_RE.match(pid):
        errors.append(f"pack.json: id {pid!r} does not match [a-z0-9-]{{1,40}}")
    elif pid != name:
        errors.append(f"pack.json: id {pid!r} differs from the folder name {name!r}")

    names = pack.get("name")
    if not isinstance(names, dict):
        errors.append("pack.json: name must be an object of language to text")
    else:
        for lang in ("en", "zh-TW"):
            if not isinstance(names.get(lang), str) or not names[lang].strip():
                errors.append(f"pack.json: name needs a non-empty {lang!r}")
        for lang, v in names.items():
            if not isinstance(v, str):
                errors.append(f"pack.json: name[{lang!r}] is not text")

    layout = pack.get("layout")
    allowed = LAYOUTS.get(layout)
    if allowed is None:
        errors.append(f"pack.json: layout must be 'rooms' or 'path', not {layout!r}")

    palette = pack.get("palette", {})
    if not isinstance(palette, dict):
        errors.append("pack.json: palette must be an object")
    else:
        for k, v in palette.items():
            if k not in PALETTE_KEYS:
                errors.append(f"pack.json: unknown palette key {k!r}")
            if not isinstance(v, str) or not COLOUR_RE.match(v):
                errors.append(f"pack.json: palette.{k} {v!r} is not #rgb or #rrggbb")

    sprites = pack.get("sprites", {})
    referenced = set()
    if not isinstance(sprites, dict):
        errors.append("pack.json: sprites must be an object")
        sprites = {}
    for key, rel in sprites.items():
        if allowed is not None and key not in allowed:
            errors.append(f"sprites: {key!r} is not a {layout} sprite key")
        if not isinstance(rel, str) or not rel:
            errors.append(f"sprites.{key}: the path must be text")
            continue
        if rel.startswith("/") or "\\" in rel or ".." in rel.split("/") or ":" in rel:
            errors.append(f"sprites.{key}: {rel!r} must be a plain relative path inside the pack")
            continue
        full = os.path.join(folder, *rel.split("/"))
        real = os.path.realpath(full)
        if not real.startswith(os.path.realpath(folder) + os.sep):
            errors.append(f"sprites.{key}: {rel!r} leaves the pack")
            continue
        if not os.path.isfile(full) or os.path.islink(full):
            errors.append(f"sprites.{key}: {rel!r} is missing or not a plain file")
            continue
        referenced.add(rel)
        if os.path.getsize(full) > MAX_IMAGE_BYTES:
            errors.append(f"{rel}: over {MAX_IMAGE_BYTES} bytes")
        if not rel.lower().endswith(".svg") or sniff(full) != "svg":
            errors.append(f"{rel}: the examples use SVG only")
            continue
        check_svg(full, rel, (allowed or {}).get(key), errors)

    for rel in files:
        if rel == "pack.json":
            continue
        full = os.path.join(folder, *rel.split("/"))
        kind = sniff(full)
        if kind is None:
            errors.append(f"{rel}: not a PNG, WebP or SVG")
        elif rel not in referenced:
            warnings.append(f"{rel}: not used by any sprite")
            if kind == "svg":
                check_svg(full, rel, None, errors)
    return errors, warnings


def main(argv):
    names = argv or sorted(
        d for d in os.listdir(HERE) if os.path.isfile(os.path.join(HERE, d, "pack.json"))
    )
    if not names:
        print("no packs found")
        return 1
    failed = False
    for n in names:
        errors, warnings = check_pack(os.path.join(HERE, n))
        for w in warnings:
            print(f"{n}: warning: {w}")
        for e in errors:
            print(f"{n}: error: {e}")
        print(f"{n}: {'FAIL' if errors else 'ok'}")
        failed = failed or bool(errors)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
