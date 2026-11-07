"""The action library: 8-frame poses for the cut-out rigs (rigkit), shared by every character.

Each action names its base (which drawing it animates) and returns 8 pose dicts
{layer: {"a": deg CCW, "dx": px, "dy": px, "sway": px}} in view px. Distances scale with
the character's size: u = view px per 1/1000 of the standing height. Limbs that must reach a
point (hands on a keyboard or grips, feet on pedals) use rigkit.ik_pose.

Front views: "_n" is the limb on the viewer's left (her right), "_f" the viewer's right.
Side views face right: "_n" is the near limb, "_f" the far one.
"""
import math

from rigkit import ik_pose

N = 8
BASE = {
    "door-idle": "front_stand", "hike-summit": "front_stand",
    "hike-walk": "side_stand", "stream-wade": "side_stand", "bbq-grill": "side_stand",
    "desk-rest": "front_sit", "desk-type": "front_sit", "desk-chin": "front_sit", "desk-mouse": "front_sit",
    "desk-ask": "front_sit", "desk-cheer": "front_sit", "desk-slump": "front_sit", "desk-sleep": "front_sit",
    "sofa-sit": "front_sit", "sofa-sleep": "front_sit", "camp-sit": "front_sit", "fish-sit": "front_sit",
    "cycle-ride": "side_bike",
}


def _ph(i, shift=0.0):
    return 2 * math.pi * i / N + shift


def unit(spec):
    return (spec.get("stand_height") or (spec["ground_y"] - spec["head_top"])) / 1000.0


def idle_bits(i, u, breath=6.0, sway=7.0, tail=5.0):
    """Breathing, hair, tail and wing motion every action layers on top."""
    ph = _ph(i)
    return {
        "body": {"dy": -breath * u * (0.5 + 0.5 * math.sin(ph)), "sway": sway * u * math.sin(ph - 1.2)},
        "head": {"a": 1.5 * math.sin(ph - 0.6)},
        "tail": {"a": tail * math.sin(ph - 0.8)},
        "wing_n": {"a": 3 * math.sin(ph)}, "wing_f": {"a": -3 * math.sin(ph)},
    }


def merge(*ds):
    out = {}
    for d in ds:
        for k, v in d.items():
            cur = out.setdefault(k, {})
            for kk, vv in v.items():
                cur[kk] = cur.get(kk, 0.0) + vv
    return out


def reach(J, side, target, bend):
    """Arm deltas so the wrist of arm `side` ("n"/"f") reaches target."""
    if f"wrist_{side}" not in J:
        return {}
    up, lo = ik_pose(J, f"shoulder_{side}", f"elbow_{side}", f"wrist_{side}", target, bend)
    return {f"arm_{side}": {"a": up}, f"fore_{side}": {"a": lo}}


def plant(J, side, target, bend):
    if f"ankle_{side}" not in J:
        return {}
    up, lo = ik_pose(J, "hip" if side == "n" or "hip_f" not in J else "hip_f", f"knee_{side}", f"ankle_{side}", target, bend)
    return {f"leg_{side}": {"a": up}, f"shin_{side}": {"a": lo}}


def poses(action, spec, J, props=None):
    u = unit(spec)
    out = []
    for i in range(N):
        ph = _ph(i)
        s, c = math.sin(ph), math.cos(ph)
        base = idle_bits(i, u)
        if action == "door-idle":
            plant_feet = -base["body"]["dy"]  # the legs hang from the body: hold the feet on the floor
            p = merge(base, {"arm_n": {"a": -2 * math.sin(ph - 1)}, "arm_f": {"a": 2 * math.sin(ph - 1)},
                             "leg_n": {"dy": plant_feet}, "leg_f": {"dy": plant_feet}})
        elif action == "hike-summit":
            # both arms up in a little victory bounce
            lift = 30 * u * abs(math.sin(ph))
            # a V: chibi arms are short, so up-and-out reads as a cheer where straight up reads as hands on the head
            tn = (J["shoulder_n"][0] - 230 * u, J["shoulder_n"][1] - 230 * u - lift)
            tf = (J["shoulder_f"][0] + 230 * u, J["shoulder_f"][1] - 230 * u - lift)
            p = merge(base, reach(J, "n", tn, -1), reach(J, "f", tf, 1), {"body": {"dy": -10 * u * abs(math.sin(ph))}})
        elif action in ("hike-walk", "stream-wade"):
            k = 1.0 if action == "hike-walk" else 0.55
            base["body"]["sway"] = 0.0  # the walk's own trail replaces the idle sway (they cancelled out)
            p = merge(base, {
                "body": {"dy": -10 * u * k * (1 - abs(s)), "sway": -10 * u * k - 7 * u * math.sin(ph - 1.3)},
                "leg_n": {"a": 13 * k * s}, "leg_f": {"a": -13 * k * s},
                "shin_n": {"a": -14 * k * max(0.0, c)}, "shin_f": {"a": -14 * k * max(0.0, -c)},
                "arm_n": {"a": -14 * k * s if action == "hike-walk" else 25}, "arm_f": {"a": 14 * k * s},
                "fore_n": {"a": 0 if action == "hike-walk" else 35},
            })
        elif action == "bbq-grill":
            flip = 1 if (i // 2) % 2 else 0
            p = merge(base, {"arm_n": {"a": 55 + 6 * flip}, "fore_n": {"a": 30 + 18 * flip}, "arm_f": {"a": 20},
                             "fore_f": {"a": 20}, "body": {"dy": -3 * u * flip}})
        elif action.startswith("desk-") or action.startswith("sofa-") or action in ("camp-sit", "fish-sit"):
            p = merge(base, desk_like(action, i, J, u, props or {}))
        elif action == "cycle-ride":
            p = merge(base, cycle(i, J, u, props or {}))
        else:
            raise KeyError(action)
        out.append(p)
    return out


def desk_like(action, i, J, u, props):
    """Seated, from the front. props["desk_y"]: desk top (view px); hands rest there."""
    ph = _ph(i)
    desk_y = props.get("desk_y", J["wrist_n"][1])
    cx = (J["shoulder_n"][0] + J["shoulder_f"][0]) / 2
    half = abs(J["shoulder_f"][0] - J["shoulder_n"][0]) / 2
    rest_n = (cx - half * 0.85, desk_y)
    rest_f = (cx + half * 0.85, desk_y)
    chin = (J["neck"][0] - 25 * u, J["neck"][1] - 20 * u)
    if action == "desk-rest":
        return merge(reach(J, "n", rest_n, -1), reach(J, "f", rest_f, 1))
    if action == "desk-type":
        a = 10 * u * (1 if (i // 1) % 2 else -1)
        return merge(reach(J, "n", (rest_n[0] + 10 * u, desk_y - 8 * u + a), -1),
                     reach(J, "f", (rest_f[0] - 10 * u, desk_y - 8 * u - a), 1),
                     {"head": {"a": 2 * math.sin(ph * 2), "dy": 3 * u * abs(math.sin(ph))}})
    if action == "desk-chin":
        return merge(reach(J, "n", chin, -1), reach(J, "f", rest_f, 1), {"head": {"a": -4}})
    if action == "desk-mouse":
        return merge(reach(J, "n", rest_n, -1),
                     reach(J, "f", (rest_f[0] + 60 * u + 8 * u * math.sin(ph), desk_y + 4 * u * math.cos(ph)), 1))
    if action == "desk-ask":
        wave = 10 * math.sin(ph * 2)
        up = (J["shoulder_n"][0] - 40 * u, J["shoulder_n"][1] - 300 * u)
        return merge(reach(J, "n", up, -1), {"fore_n": {"a": wave}}, reach(J, "f", rest_f, 1), {"head": {"a": 5}})
    if action == "desk-cheer":
        hop = 18 * u * abs(math.sin(ph))
        return merge(reach(J, "n", (J["shoulder_n"][0] - 230 * u, J["shoulder_n"][1] - 230 * u - hop), -1),
                     reach(J, "f", (J["shoulder_f"][0] + 230 * u, J["shoulder_f"][1] - 230 * u - hop), 1),
                     {"body": {"dy": -hop}})
    if action == "desk-slump":
        return merge(reach(J, "n", (rest_n[0] - 20 * u, desk_y + 30 * u), -1),
                     reach(J, "f", (rest_f[0] + 20 * u, desk_y + 30 * u), 1),
                     {"head": {"a": 7, "dy": 14 * u}, "body": {"dy": 12 * u}})
    if action == "desk-sleep":
        breathe = 4 * u * math.sin(ph / 2)
        return merge(reach(J, "n", (cx - half * 0.3, desk_y - 10 * u), -1),
                     reach(J, "f", (cx + half * 0.3, desk_y - 10 * u), 1),
                     {"head": {"a": 10, "dy": 30 * u + breathe}, "body": {"dy": 18 * u + breathe}})
    lap_y = props.get("lap_y", J["wrist_n"][1])
    if action == "sofa-sit":
        return merge(reach(J, "n", (cx - half * 0.6, lap_y), -1), reach(J, "f", (cx + half * 0.6, lap_y), 1))
    if action == "sofa-sleep":
        return merge(reach(J, "n", (cx - half * 0.5, lap_y), -1), reach(J, "f", (cx + half * 0.5, lap_y), 1),
                     {"head": {"a": 9, "dy": 16 * u + 3 * u * math.sin(ph / 2)}, "body": {"dy": 8 * u}})
    if action == "camp-sit":
        w = 6 * u * math.sin(ph * 2)
        fire = (J["shoulder_f"][0] + 170 * u, J["neck"][1] + 150 * u)
        return merge(reach(J, "n", (fire[0] - 110 * u, fire[1] + w), -1), reach(J, "f", (fire[0] - 20 * u, fire[1] - w), 1),
                     {"head": {"a": -5}})
    if action == "fish-sit":
        bob = 4 * u * math.sin(ph)
        hold = (cx + half * 0.7, lap_y - 60 * u + bob)
        return merge(reach(J, "n", (hold[0] - 30 * u, hold[1] + 15 * u), -1), reach(J, "f", hold, 1), {"head": {"a": -3}})
    raise KeyError(action)


def cycle(i, J, u, props):
    """Side view on the bike, one crank turn per loop (clockwise on screen = pedalling forward).
    props: crank (x, y), pedal_r, crank_rest (degrees, image angle of the drawn near pedal);
    grip (x, y) only when the bike is a separate prop (the hands then reach for it)."""
    turn = 360.0 * i / N
    cx, cy = props["crank"]
    r = props["pedal_r"]
    a_n = math.radians(props.get("crank_rest", 90.0) + turn)
    pn = (cx + r * math.cos(a_n), cy + r * math.sin(a_n))
    pf = (cx - r * math.cos(a_n), cy - r * math.sin(a_n))
    bob = -2 * u * abs(math.sin(math.radians(turn)))
    p = merge(plant(J, "n", (pn[0], pn[1] - bob), props.get("knee_bend", -1)),
              plant(J, "f", (pf[0], pf[1] - bob), props.get("knee_bend", -1)),
              {"body": {"dy": bob, "sway": -10 * u - 4 * u * math.sin(math.radians(turn))},
               "tail": {"a": props.get("tail_a", 0.0) + 4 * math.sin(math.radians(turn))},
               "crank": {"a": -turn}})
    if "grip" in props:
        p = merge(p, reach(J, "n", props["grip"], 1), reach(J, "f", (props["grip"][0] - 8 * u, props["grip"][1]), 1))
    return p
