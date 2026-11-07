// The whale's own promises, past skins.test.ts' contract: her headband in
// place of any kind's headwear, her tail in every state that shows her
// back or side, her eyes shut asleep, and the props' handles in her hands.

import { describe, expect, it } from "vitest";

import { OUTING_STEPS } from "../outing";
import { outdoorPlace } from "../warroom/deskArt";
import type { Placed, Point } from "./placed";
import { DESK_GESTURES } from "./skins";
import { whaleSkin as skin } from "./skinWhale";
import { HEADWEAR_KINDS } from "./sprites";

const KINDS = [...HEADWEAR_KINDS, "other"];
const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);

/** The parts as drawn in frame 0: "x,y" → token, later parts over earlier. */
function paint(parts: Placed[]): Map<string, string> {
  const drawn = new Map<string, string>();
  for (const part of parts) {
    if (part.frame && part.frame.index !== 0) continue;
    for (const layer of part.sprite.layers)
      for (const [, x, y, n] of layer.d.matchAll(/M(\d+) (\d+)h(\d+)/g))
        for (let i = 0; i < Number(n); i++) drawn.set(`${part.x + Number(x) + i},${part.y + Number(y)}`, layer.token);
  }
  return drawn;
}
const tokens = (parts: Placed[]) => new Set(paint(parts).values());

/** Whether a pixel of `token` is drawn at `point` or right next to it. */
function touches(drawn: Map<string, string>, point: Point, token?: string): boolean {
  for (let dy = -1; dy <= 1; dy++)
    for (let dx = -1; dx <= 1; dx++) {
      const got = drawn.get(`${point.x + dx},${point.y + dy}`);
      if (got && (!token || got === token)) return true;
    }
  return false;
}

describe("the whale skin", () => {
  it("wears her own headband whatever the kind, the kind's colour only in her ribbon", () => {
    for (const gesture of DESK_GESTURES) {
      const pictures = new Set(KINDS.map((kind) => [...paint(skin.desk(gesture, kind).parts)].sort().join(";")));
      expect(pictures.size, gesture).toBe(1);
    }
    const all = [skin.door("claude"), skin.sofa("gemini", false), skin.cycle("codex", outdoorPlace("cycle", 0))];
    for (const figure of all) {
      expect(figure.parts.some((part) => part.name.startsWith("headwear.")), "headwear").toBe(false);
      expect(tokens(figure.parts)).toContain("agent");
    }
  });

  it("is drawn in her own colours in every state: hair, its light ends, the dress, the fins' white", () => {
    const figures = [
      ...DESK_GESTURES.map((gesture) => skin.desk(gesture, "other")),
      skin.door("other"),
      skin.sofa("other", false),
      skin.sofa("other", true),
      skin.hike("other", outdoorPlace("hike", 0), true),
      skin.stream("other", outdoorPlace("stream", 0)),
      skin.fish("other", outdoorPlace("fish", 0)),
      skin.camp("other", outdoorPlace("camp", 0)),
      skin.bbq("other", outdoorPlace("bbq", 0)),
      skin.cycle("other", outdoorPlace("cycle", 0)),
    ];
    for (const figure of figures) {
      const drawn = tokens([...(figure.behind ?? []), ...figure.parts]);
      for (const token of ["whale-hair", "whale-dress", "bubble"]) expect(drawn, token).toContain(token);
    }
    expect(skin.mini.layers.map((layer) => layer.token)).toEqual(expect.arrayContaining(["whale-hair", "agent", "bubble"]));
  });

  it("shows her tail at the desk, the door, the sofa, every outing and on the bike", () => {
    const hasTail = (parts: Placed[]) => parts.some((part) => part.name.startsWith("whale.tail"));
    for (const gesture of DESK_GESTURES) expect(hasTail(skin.desk(gesture, "other").parts), gesture).toBe(true);
    expect(hasTail(skin.door("other").parts)).toBe(true);
    expect(hasTail(skin.sofa("other", true).parts)).toBe(true);
    for (const step of steps) {
      expect(hasTail(skin.hike("other", outdoorPlace("hike", step), true).parts)).toBe(true);
      expect(hasTail(skin.stream("other", outdoorPlace("stream", step)).parts)).toBe(true);
      expect(hasTail(skin.cycle("other", outdoorPlace("cycle", step)).parts)).toBe(true);
    }
    expect(paint(skin.door("other").parts).size).toBeGreaterThan(0);
    expect(tokens(skin.door("other").parts)).toContain("whale-fin");
  });

  it("shuts her eyes asleep on the sofa: no blue in her face", () => {
    const face = (asleep: boolean) => skin.sofa("other", asleep).parts.filter((part) => part.name.startsWith("whale.head"));
    expect(tokens(face(false))).toContain("whale-fin");
    expect(tokens(face(true))).not.toContain("whale-fin");
  });

  it("holds the rod's grip, the pole and the tongs in her hands", () => {
    for (const step of steps) {
      const fish = skin.fish("other", outdoorPlace("fish", step));
      expect(touches(paint(fish.parts), { x: fish.anchors.rod.x, y: fish.anchors.rod.y + 20 }, "skin"), `rod ${step}`).toBe(true);
      const stream = skin.stream("other", outdoorPlace("stream", step));
      const top = stream.anchors.pole;
      expect(touches(paint(stream.parts), { x: top.x, y: top.y + 1 }, "skin"), `pole ${step}`).toBe(true);
      // The hand is above the water.
      expect(top.y + 1, `pole ${step}`).toBeLessThan(stream.anchors.wade.y);
    }
    const bbq = skin.bbq("other", outdoorPlace("bbq", 0));
    // Her sleeve on the tongs is her dress, from her shoulder; her hand on
    // their handle.
    const { tongs } = bbq.anchors;
    const arm = paint(bbq.front ?? []);
    expect(arm.get(`${tongs.x},${tongs.y + 1}`)).toBe("whale-dress");
    expect(arm.get(`${tongs.x + 4},${tongs.y + 1}`)).toBe("skin");
    expect(touches(paint(bbq.parts), { x: tongs.x, y: tongs.y + 1 }, "whale-dress")).toBe(true);
    expect(bbq.anchors.apron, "her own apron is on").toBeUndefined();
  });
});
