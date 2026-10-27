import { describe, expect, it } from "vitest";

import { OFFICE_STYLES } from "../officeStyle";
import { CELL } from "../pixel/sprites";
import { POSES, type Pose } from "../world";
import {
  deskScene,
  doorScene,
  itemScene,
  meetingScene,
  miniDesk,
  poseAnimated,
  rackScene,
  sofaScene,
  type Placed,
} from "./deskArt";

const names = (parts: Placed[]) => parts.map((part) => part.name);
/** What a still desk shows: everything but the frames after the first. */
const still = (parts: Placed[]) => names(parts.filter((part) => !part.frame || part.frame.index === 0));

describe("deskScene", () => {
  it("gives each pose a still picture of its own", () => {
    const seen = new Map<string, Pose>();
    for (const pose of POSES) {
      if (pose === "awaiting-accept" || pose === "offline") continue; // at the door; greyed by the stylesheet
      const key = still(deskScene(pose, "claude")).join(",");
      expect(seen.get(key), `${pose} looks like ${seen.get(key)}`).toBeUndefined();
      seen.set(key, pose);
    }
  });

  it("says each state with a glyph or a gesture, not a colour alone", () => {
    const scene = (pose: Pose) => names(deskScene(pose, "codex"));
    expect(scene("working")).toEqual(expect.arrayContaining(["screen.code.lines", "arm.left.up", "arm.right.up"]));
    expect(scene("reviewing")).toEqual(expect.arrayContaining(["screen.review.doc", "screen.review.lens.a"]));
    expect(scene("asking-user")).toEqual(expect.arrayContaining(["bubble.ask", "arm.right.raised", "screen.ask.glyph"]));
    expect(scene("limit")).toEqual(expect.arrayContaining(["bubble.sleep", "bubble.z1", "arms.folded", "screen.limit.glyph"]));
    expect(scene("failed")).toEqual(expect.arrayContaining(["screen.fail", "screen.fail.glyph"]));
    expect(scene("finished")).toEqual(expect.arrayContaining(["screen.ok.glyph", "arm.left.raised", "arm.right.raised"]));
    expect(scene("waiting-ci")).toContain("screen.ci.glyph");
    expect(scene("idle")).toContain("screen.off");
    expect(scene("idle")).not.toContain("bubble.ask");
  });

  it("animates only working, reviewing, asking and the limit, and all of them still show zzz or a bubble", () => {
    expect(POSES.filter(poseAnimated)).toEqual(["asking-user", "limit", "reviewing", "working"]);
    for (const pose of POSES) {
      const framed = deskScene(pose, "gemini").filter((part) => part.frame);
      expect(framed.length > 0, pose).toBe(poseAnimated(pose));
    }
    // Still, the sleeping desk has all three letters.
    expect(still(deskScene("limit", "claude"))).toEqual(expect.arrayContaining(["bubble.z1", "bubble.z2", "bubble.z3"]));
  });

  it("dresses each agent kind its own way", () => {
    const wear = (kind: string) => names(deskScene("idle", kind)).filter((name) => name.startsWith("headwear."));
    expect(wear("claude")).toEqual(["headwear.claude"]);
    expect(wear("codex")).toEqual(["headwear.codex"]);
    expect(wear("gemini")).toEqual(["headwear.gemini"]);
    expect(wear("opencode")).toEqual(["headwear.opencode"]);
    expect(wear("antigravity")).toEqual(["headwear.antigravity"]);
    expect(wear("other")).toEqual([]);
  });

  it("draws a free desk without anyone at it, and a held message as an envelope", () => {
    const free = names(deskScene("empty", "other"));
    expect(free).toEqual(["monitor", "desk", "screen.off", "screen.off.glare", "chair"]);
    expect(names(deskScene("working", "claude", { mail: true }))).toContain("mail");
  });

  it("keeps every part on the desk's grid", () => {
    for (const pose of [...POSES, "empty" as const]) {
      for (const part of deskScene(pose, "codex", { mail: true })) {
        expect(Number.isInteger(part.x) && Number.isInteger(part.y), part.name).toBe(true);
        expect(part.x + part.sprite.w, `${pose} ${part.name}`).toBeLessThanOrEqual(CELL);
        expect(part.y + part.sprite.h, `${pose} ${part.name}`).toBeLessThanOrEqual(CELL);
        expect(part.x, part.name).toBeGreaterThanOrEqual(0);
      }
    }
  });
});

describe("doorScene", () => {
  it("stands the agent in the doorway, facing the room, with a … bubble", () => {
    const parts = names(doorScene("opencode"));
    expect(parts).toEqual(expect.arrayContaining(["door", "head.front", "body.standing", "bubble.wait", "headwear.opencode"]));
  });
});

/** What a style may change: the furniture, never the agent or its state. */
const FURNITURE = new Set(["desk", "chair", "table", "table.cap.l", "table.cap.r"]);
const placedKey = (parts: Placed[]) =>
  parts.filter((part) => !FURNITURE.has(part.name)).map((part) => `${part.name}@${part.x},${part.y}:${part.sprite.w}x${part.sprite.h}`);

describe("office styles", () => {
  it("change only the desk and the chair: the screen, the agent and its extras stay put", () => {
    for (const style of OFFICE_STYLES) {
      for (const pose of [...POSES, "empty" as const]) {
        const styled = deskScene(pose, "claude", { style, mail: true });
        expect(placedKey(styled), `${style} ${pose}`).toEqual(placedKey(deskScene(pose, "claude", { mail: true })));
        expect(styled.find((part) => part.name === "desk")?.sprite.h, style).toBe(14);
        // Every chair ends where the default one does.
        const chair = styled.find((part) => part.name === "chair")!;
        expect(chair.y + chair.sprite.h, `${style} chair`).toBe(31);
      }
    }
  });

  it("seat the meeting the same way in every style", () => {
    for (const style of OFFICE_STYLES) {
      expect(placedKey(meetingScene("asking-user", "codex", { style, edge: "l" }))).toEqual(
        placedKey(meetingScene("asking-user", "codex", { edge: "l" })),
      );
    }
  });
});

describe("meetingScene", () => {
  it("puts the agent at a table seat, its screen and bubble as at a desk", () => {
    const parts = names(meetingScene("asking-user", "claude", { edge: "m" }));
    expect(parts).toEqual(expect.arrayContaining(["monitor", "table", "screen.ask.glyph", "arm.right.raised", "bubble.ask"]));
    expect(parts).not.toContain("desk");
    expect(parts).not.toContain("table.cap.l");
  });

  it("gives the table's ends a leg, and an empty seat only the table and a chair", () => {
    expect(names(meetingScene("working", "claude", { edge: "l" }))).toContain("table.cap.l");
    expect(names(meetingScene("working", "claude", { edge: "r" }))).toContain("table.cap.r");
    expect(names(meetingScene("working", "claude", { edge: "s" }))).toEqual(expect.arrayContaining(["table.cap.l", "table.cap.r"]));
    expect(names(meetingScene("empty", "other", { edge: "m" }))).toEqual(["table", "chair"]);
  });
});

describe("itemScene", () => {
  it("lays a folder, a clipboard or an envelope on the table, its screen saying the same", () => {
    expect(names(itemScene("merge"))).toEqual(expect.arrayContaining(["item.merge", "screen.ok.glyph", "table"]));
    expect(names(itemScene("review"))).toEqual(expect.arrayContaining(["item.review", "screen.review.glyph"]));
    expect(names(itemScene("held"))).toEqual(expect.arrayContaining(["item.held", "screen.mail.glyph"]));
    for (const part of itemScene("review", "cafe", "r")) {
      expect(part.x + part.sprite.w, part.name).toBeLessThanOrEqual(CELL);
      expect(part.y + part.sprite.h, part.name).toBeLessThanOrEqual(CELL);
    }
  });
});

describe("sofaScene", () => {
  it("sits an agent on the sofa, and puts one asleep on a usage limit with zzz", () => {
    expect(names(sofaScene("idle", "codex"))).toEqual(expect.arrayContaining(["head.front", "body.seated", "headwear.codex"]));
    const asleep = sofaScene("limit", "claude");
    expect(names(asleep)).toEqual(expect.arrayContaining(["head.sleep", "arms.folded", "bubble.sleep", "bubble.z1"]));
    expect(still(asleep)).toEqual(expect.arrayContaining(["bubble.z1", "bubble.z2", "bubble.z3"]));
    expect(names(sofaScene("idle", "claude"))).not.toContain("bubble.sleep");
  });
});

describe("miniDesk", () => {
  it("says busy, a hand up, asleep or waiting in a small desk", () => {
    expect(names(miniDesk("working"))).toEqual(expect.arrayContaining(["mini.screen.on", "mini.code", "mini.agent"]));
    expect(names(miniDesk("asking-user"))).toContain("mini.hand");
    expect(names(miniDesk("limit"))).toContain("mini.z");
    expect(names(miniDesk("awaiting-accept"))).toContain("mini.wait");
    expect(names(miniDesk("idle"))).toContain("mini.screen.off");
  });
});

describe("rackScene", () => {
  const lit = (parts: Placed[]) => names(parts).filter((name) => name.endsWith(".busy")).length;
  it("lights a slot per run it holds, four lights at most, and none when offline", () => {
    const slots = (parts: Placed[]) => names(parts).filter((name) => name.startsWith("led.slot")).length;
    expect(slots(rackScene(true, 0, 2))).toBe(2);
    expect(slots(rackScene(true, 0, 9))).toBe(4);
    expect(slots(rackScene(true, 0, undefined))).toBe(1);
    expect(lit(rackScene(true, 1, 3))).toBe(1);
    expect(lit(rackScene(true, 7, 3))).toBe(3);
    expect(names(rackScene(true, 0, 1))).toContain("led.power");
    const off = rackScene(false, 2, 3);
    expect(names(off)).toContain("led.power.off");
    expect(lit(off)).toBe(0);
    // Unknown state is not off.
    expect(names(rackScene(null, 1, 2))).toContain("led.power");
  });
});
