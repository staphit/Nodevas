// The star's own art: a sparkle in every state (none of the person's parts
// or colours), its eye from the front only (shut asleep, none from behind),
// standing and sitting on the rows the boxes give, and the props in its
// hands. The contract every skin keeps is skins.test.ts'.

import { describe, expect, it } from "vitest";

import { OUTING_STEPS } from "../outing";
import { deskScene, outdoorPlace } from "../warroom/deskArt";
import { BIKE_BARS, ROD } from "./outdoorSprites";
import type { Placed, Point } from "./placed";
import { starSkin } from "./skinStar";
import { DESK_GESTURES, type Figure } from "./skins";

const KINDS = ["gemini", "antigravity", "other"];
const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);
/** The star's hands: yellow mitts at the end of its stick arms. */
const HAND = "gem-yellow";

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
/** A hand within `reach` of a point. */
const handNear = (parts: Placed[], { x, y }: Point, reach = 1) => {
  const drawn = paint(parts);
  for (let dy = -reach; dy <= reach; dy++)
    for (let dx = -reach; dx <= reach; dx++) if (drawn.get(`${x + dx},${y + dy}`) === HAND) return true;
  return false;
};
/** The lowest row anything is drawn on. */
const bottom = (parts: Placed[]) => Math.max(...[...paint(parts).keys()].map((key) => Number(key.split(",")[1])));
const named = (parts: Placed[], name: string) => parts.some((part) => part.name === name);

/** Every figure the skin draws, by state. */
function everyFigure(kind: string): [string, Figure<never>][] {
  const figures: [string, Figure<never>][] = DESK_GESTURES.map((gesture) => [`desk ${gesture}`, starSkin.desk(gesture, kind)]);
  figures.push(["door", starSkin.door(kind)], ["sofa", starSkin.sofa(kind, false)], ["sofa asleep", starSkin.sofa(kind, true)]);
  for (const step of steps) {
    figures.push(
      [`hike ${step}`, starSkin.hike(kind, outdoorPlace("hike", step), step < OUTING_STEPS - 1)],
      [`stream ${step}`, starSkin.stream(kind, outdoorPlace("stream", step))],
      [`fish ${step}`, starSkin.fish(kind, outdoorPlace("fish", step))],
      [`camp ${step}`, starSkin.camp(kind, outdoorPlace("camp", step))],
      [`bbq ${step}`, starSkin.bbq(kind, outdoorPlace("bbq", step))],
      [`cycle ${step}`, starSkin.cycle(kind, outdoorPlace("cycle", step))],
    );
  }
  return figures;
}

describe("the star skin", () => {
  it("is its own banded star everywhere: none of the person's parts or colours, the same for every kind", () => {
    const all = (figure: Figure<never>) => [...(figure.behind ?? []), ...figure.parts, ...(figure.front ?? [])];
    const pictures = KINDS.map((kind) => everyFigure(kind).map(([, figure]) => [...paint(all(figure))].sort().join(";")));
    for (const kind of KINDS) {
      for (const [where, figure] of everyFigure(kind)) {
        const parts = all(figure);
        for (const part of parts) expect(part.name.startsWith("star."), `${where} ${part.name}`).toBe(true);
        const used = tokens(parts);
        for (const token of ["skin", "hair", "pants", "agent", "agent-dark"]) expect(used.has(token), `${where} ${token}`).toBe(false);
        expect(used.has("gem-blue") && used.has("gem-red") && used.has("gem-green"), where).toBe(true);
      }
    }
    expect(pictures[1]).toEqual(pictures[0]);
    expect(pictures[2]).toEqual(pictures[0]);
  });

  it("shows its eye from the front, shut asleep and none from behind or at the desk", () => {
    const eyes = (figure: Figure<never>) => figure.parts.filter((part) => part.name.startsWith("star.eye")).map((part) => part.name);
    for (const gesture of DESK_GESTURES) expect(eyes(starSkin.desk(gesture, "gemini")), gesture).toEqual([]);
    expect(eyes(starSkin.door("gemini"))).toEqual(["star.eye"]);
    expect(eyes(starSkin.sofa("gemini", false))).toEqual(["star.eye"]);
    expect(eyes(starSkin.sofa("gemini", true))).toEqual(["star.eye.shut"]);
    for (const step of steps) expect(eyes(starSkin.camp("gemini", outdoorPlace("camp", step))), `camp ${step}`).toEqual(["star.eye"]);
    // From the side the eye is drawn into the thinner star itself.
    expect(named(starSkin.cycle("gemini", outdoorPlace("cycle", 0)).parts, "star.body.side")).toBe(true);
  });

  it("stands on the box's bottom row and sits with its feet on the seated box's", () => {
    expect(bottom(starSkin.door("gemini").parts)).toBe(29);
    for (const step of steps) {
      for (const outing of ["hike", "bbq"] as const) {
        const place = outdoorPlace(outing, step);
        const figure = outing === "hike" ? starSkin.hike("gemini", place, false) : starSkin.bbq("gemini", place);
        expect(bottom(figure.parts), `${outing} ${step}`).toBe(place.y);
      }
      for (const outing of ["fish", "camp"] as const) {
        const place = outdoorPlace(outing, step);
        expect(bottom(starSkin[outing]("gemini", place).parts), `${outing} ${step}`).toBe(place.y);
      }
    }
    expect(bottom(starSkin.sofa("gemini", false).parts)).toBe(26);
  });

  it("holds its props: the mouse, the pole, the rod's grip and the bike's bars in a hand, the tongs on its right point", () => {
    const mouse = starSkin.desk("mouse", "gemini");
    expect(handNear(mouse.parts, mouse.anchors.mouse)).toBe(true);
    for (const step of steps) {
      const wading = starSkin.stream("gemini", outdoorPlace("stream", step));
      // The pole passes between the fingers, its top just over the hand.
      expect(handNear(wading.parts, { x: wading.anchors.pole.x, y: wading.anchors.pole.y + 1 }), `stream ${step}`).toBe(true);
      const fishing = starSkin.fish("gemini", outdoorPlace("fish", step));
      const grip = { x: fishing.anchors.rod.x, y: fishing.anchors.rod.y + ROD.h - 1 };
      expect(handNear(fishing.parts, grip), `fish ${step}`).toBe(true);
      const bike = outdoorPlace("cycle", step);
      const ride = starSkin.cycle("gemini", bike);
      expect(handNear(ride.parts, { x: bike.x + BIKE_BARS.x + 1, y: bike.y + BIKE_BARS.y })).toBe(true);
      // A stick arm out of the right point, a mitt on the tongs' handle (the
      // prop's (5, 1)), down on the skewer and lifted.
      const place = outdoorPlace("bbq", step);
      const cook = starSkin.bbq("gemini", place);
      const tip = { x: place.x + 14, y: place.y - 12 };
      const { tongs } = cook.anchors;
      const arm = cook.front ?? [];
      expect(paint(cook.parts).has(`${tip.x},${tip.y}`), `bbq ${step} tip`).toBe(true);
      // The stick out of the tip: level with it, then up a row.
      for (const [frame, dy, up] of [[0, 1, 0], [1, -1, 1]] as const) {
        expect(paint(arm, frame).get(`${tongs.x + 4},${tongs.y + dy}`), `bbq ${step} mitt ${frame}`).toBe(HAND);
        expect(paint(arm, frame).get(`${tip.x + 1},${tip.y - up}`), `bbq ${step} arm ${frame}`).toBe("gem-limb");
      }
    }
  });

  it("keeps the slumped and sleeping star behind the chair's seat, nothing of it below", () => {
    // The seat's last row is 28; under it only the chair's stem and foot.
    for (const pose of ["failed", "limit"] as const) {
      const scene = deskScene(pose, "gemini", { character: "star" });
      const star = scene.filter((part) => part.name.startsWith("star."));
      const rest = scene.filter((part) => !part.name.startsWith("star."));
      for (const frame of [0, 1, 2]) {
        const drawn = paint(scene, frame);
        const behind = paint(rest, frame);
        const shows = [...paint(star, frame)].filter(([key]) => Number(key.split(",")[1]) > 28 && drawn.get(key) !== behind.get(key));
        expect(shows.map(([key]) => key), `${pose} ${frame}`).toEqual([]);
      }
    }
  });

  it("sleeps on its arms folded on the desk top, not hanging under it", () => {
    // The desk top's band is rows 15–20 (its outline on 15 and 20).
    const folded = starSkin.desk("sleep", "gemini").parts.filter((part) => part.name === "star.arms.folded");
    expect(folded).toHaveLength(1);
    const rows = [...paint(folded).keys()].map((key) => Number(key.split(",")[1]));
    expect(Math.min(...rows)).toBeGreaterThanOrEqual(16);
    expect(Math.max(...rows)).toBeLessThanOrEqual(19);
    expect(tokens(folded)).toContain(HAND);
  });
});
