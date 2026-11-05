// Claude's own art: its block in every state (none of the person's parts
// left), eyes from the front only, and the props in its nubs. The contract
// every skin keeps is skins.test.ts'.

import { describe, expect, it } from "vitest";

import { OUTING_STEPS } from "../outing";
import { outdoorPlace } from "../warroom/deskArt";
import { BIKE_BARS, ROD } from "./outdoorSprites";
import type { Placed, Point } from "./placed";
import { claudeSkin } from "./skinClaude";
import { DESK_GESTURES, type Figure } from "./skins";

const KINDS = ["claude", "codex", "other"];
const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);

/** The pixels drawn in a frame: "x,y" → token, later parts over earlier. */
function paint(parts: Placed[], frame = 0): Map<string, string> {
  const drawn = new Map<string, string>();
  for (const part of parts) {
    if (part.frame && part.frame.index !== frame) continue;
    for (const layer of part.sprite.layers)
      for (const [, x, y, n] of layer.d.matchAll(/M(\d+) (\d+)h(\d+)/g))
        for (let i = 0; i < Number(n); i++) drawn.set(`${part.x + Number(x) + i},${part.y + Number(y)}`, layer.token);
  }
  return drawn;
}
const tokens = (parts: Placed[], frame = 0) => new Set(paint(parts, frame).values());
const tokenAt = (parts: Placed[], { x, y }: Point, frame = 0) => paint(parts, frame).get(`${x},${y}`);
const near = (parts: Placed[], point: Point, reach = 1) => {
  const drawn = paint(parts);
  for (let dy = -reach; dy <= reach; dy++)
    for (let dx = -reach; dx <= reach; dx++) {
      const token = drawn.get(`${point.x + dx},${point.y + dy}`);
      if (token === "clawd" || token === "clawd-dark" || token === "clawd-light") return true;
    }
  return false;
};

/** Every figure the skin draws, by state. */
function everyFigure(kind: string): [string, Figure<never>][] {
  const figures: [string, Figure<never>][] = DESK_GESTURES.map((gesture) => [`desk ${gesture}`, claudeSkin.desk(gesture, kind)]);
  figures.push(["door", claudeSkin.door(kind)], ["sofa", claudeSkin.sofa(kind, false)], ["sofa asleep", claudeSkin.sofa(kind, true)]);
  for (const step of steps) {
    figures.push(
      [`hike ${step}`, claudeSkin.hike(kind, outdoorPlace("hike", step), step < OUTING_STEPS - 1)],
      [`stream ${step}`, claudeSkin.stream(kind, outdoorPlace("stream", step))],
      [`fish ${step}`, claudeSkin.fish(kind, outdoorPlace("fish", step))],
      [`camp ${step}`, claudeSkin.camp(kind, outdoorPlace("camp", step))],
      [`bbq ${step}`, claudeSkin.bbq(kind, outdoorPlace("bbq", step))],
      [`cycle ${step}`, claudeSkin.cycle(kind, outdoorPlace("cycle", step))],
    );
  }
  return figures;
}

describe("the claude skin", () => {
  it("is its own orange block everywhere: none of the person's parts or colours", () => {
    for (const kind of KINDS) {
      for (const [where, figure] of everyFigure(kind)) {
        const parts = [...(figure.behind ?? []), ...figure.parts, ...(figure.front ?? [])];
        for (const part of parts) expect(part.name, where).toMatch(/^clawd\./);
        const used = tokens(parts);
        expect(used.has("clawd"), where).toBe(true);
        for (const token of ["skin", "hair", "agent", "agent-dark", "pants"]) expect(used.has(token), `${where} ${token}`).toBe(false);
      }
    }
    expect(claudeSkin.mini.layers.map((layer) => layer.token)).toContain("clawd");
  });

  it("shows its eyes from the front and the side, never from behind, and shuts them asleep", () => {
    for (const gesture of DESK_GESTURES) expect(tokens(claudeSkin.desk(gesture, "claude").parts).has("eye"), gesture).toBe(false);
    expect(tokens([{ name: "mini", sprite: claudeSkin.mini, x: 0, y: 0 }]).has("eye")).toBe(false);
    for (const [where, figure] of [
      ["door", claudeSkin.door("claude")],
      ["sofa", claudeSkin.sofa("claude", false)],
      ["cycle", claudeSkin.cycle("claude", outdoorPlace("cycle", 0))],
    ] as const)
      expect(tokens(figure.parts).has("eye"), where).toBe(true);
    // Open eyes are tall (a column of three); asleep, flat lines (no two eye
    // pixels one over the other).
    const columns = (parts: Placed[]) => {
      const drawn = paint(parts);
      return [...drawn].filter(([key, token]) => {
        const [x, y] = key.split(",").map(Number);
        return token === "eye" && drawn.get(`${x},${y + 1}`) === "eye";
      }).length;
    };
    expect(columns(claudeSkin.sofa("claude", false).parts)).toBeGreaterThan(0);
    expect(columns(claudeSkin.sofa("claude", true).parts)).toBe(0);
    expect(tokens(claudeSkin.sofa("claude", true).parts).has("eye")).toBe(true);
  });

  it("types with its nubs taking turns, and puts one on the mouse", () => {
    const type = claudeSkin.desk("type", "claude");
    const nubs = (frame: number) =>
      type.parts.filter((part) => part.name.includes("nub") && (!part.frame || part.frame.index === frame)).map((part) => part.y);
    // Frame 0: the left nub up, frame 1: the right one.
    expect(nubs(0)[0]).toBeLessThan(nubs(0)[1]);
    expect(nubs(1)[0]).toBeGreaterThan(nubs(1)[1]);
    const mouse = claudeSkin.desk("mouse", "claude");
    expect(near(mouse.parts, { x: mouse.anchors.mouse.x, y: mouse.anchors.mouse.y + 1 })).toBe(true);
  });

  it("holds the props in its nubs: the pole, the rod's grip, the tongs, the bars", () => {
    for (const step of steps) {
      const stream = claudeSkin.stream("claude", outdoorPlace("stream", step));
      expect(near(stream.parts, stream.anchors.pole), `stream ${step} pole`).toBe(true);
      // Under the water, nothing.
      for (const part of stream.parts) expect(part.y + part.sprite.h, `stream ${step} ${part.name}`).toBeLessThanOrEqual(stream.anchors.wade.y + 2);

      const fish = claudeSkin.fish("claude", outdoorPlace("fish", step));
      expect(near(fish.parts, { x: fish.anchors.rod.x, y: fish.anchors.rod.y + ROD.h - 1 }), `fish ${step} grip`).toBe(true);

      const bbq = claudeSkin.bbq("claude", outdoorPlace("bbq", step));
      // The right nub stretched out to the tongs' handle (the prop's (5, 1)),
      // and lifted with them; out of the block's top corner.
      const { tongs } = bbq.anchors;
      for (const [frame, dy] of [[0, 1], [1, -1]] as const) {
        expect(tokenAt(bbq.front ?? [], { x: tongs.x + 4, y: tongs.y + dy }, frame), `bbq ${step} hand ${frame}`).toBe("clawd");
      }
      expect(near(bbq.parts, { x: tongs.x - 1, y: tongs.y + 1 }), `bbq ${step} shoulder`).toBe(true);

      const bike = outdoorPlace("cycle", step);
      const ride = claudeSkin.cycle("claude", bike);
      const bars = { x: bike.x + BIKE_BARS.x, y: bike.y + BIKE_BARS.y };
      expect(near(ride.parts, bars), `cycle ${step} bars`).toBe(true);
      expect(tokenAt(ride.parts, bars), `cycle ${step} nub on the bars`).toBe("clawd");
    }
  });
});
