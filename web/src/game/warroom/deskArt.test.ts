import { describe, expect, it } from "vitest";

import { CELL } from "../pixel/sprites";
import { POSES, type Pose } from "../world";
import { deskScene, doorScene, poseAnimated, type Placed } from "./deskArt";

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
    expect(free).toEqual(["desk", "screen.off", "screen.off.glare", "chair"]);
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
