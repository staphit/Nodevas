// drawFigure on its own: which of the scene's pixel parts an anime render
// stands for, the head's box in the scene, and a prop's anchor from the tip
// a sheet gives for it. The scenes that use it: warroom/deskArt.anime.test.ts.

import { describe, expect, it } from "vitest";

import { ROD } from "../pixel/outdoorSprites";
import { at, type Placed } from "../pixel/placed";
import { skinOf } from "../pixel/skins";
import { solid } from "../pixel/sprites";
import { ANIME_ACTIONS } from "./actions";
import { animeProps, drawFigure, PROP_PARTS, type AnimeSpot } from "./figure";
import fixture from "./fixture.json";
import { parseManifest, SOLES_Y, toSheet, type AnimeSheet } from "./manifest";

/** The fixture with more actions. */
function sheetWith(actions: Record<string, unknown>): AnimeSheet {
  return toSheet("deepseek", parseManifest({ ...fixture, actions: { ...fixture.actions, ...actions } })!, "/sheets/fixture.webp");
}
const FRONT = { row: 4, frames: 8, fps: 10, view: "front", anchor: { x: 96, y: 174 }, head: { x: 62, y: 16, w: 68, h: 62 }, props: [] };

/** Parts of a scene by name, a pixel each. */
const scene = (...names: string[]): Placed[] => names.map((name) => at(name, solid(1, 1, "ink"), 0, 0));
const names = (parts: Placed[]) => parts.map((part) => part.name);

/** At a desk: the soles' line on the desk frame's floor. */
const DESK = { point: { x: 16, y: 29 }, w: 32, h: 32 };
/** On the fishing rock: under the angler's shoes, and the rock's top. */
const ROCK: AnimeSpot = { point: { x: 11, y: 36 }, seat: { x: 11, y: 33 }, w: 56, h: 44 };

describe("animeProps", () => {
  it("has every desk action stand for its whole desk unit, whatever its manifest says", () => {
    for (const action of ANIME_ACTIONS.filter((name) => name.startsWith("desk-"))) {
      expect(animeProps(action, []), action).toEqual(["desk"]);
      expect(animeProps(action, ["desk"]), action).toEqual(["desk"]);
      expect(animeProps(action, ["mouse"]), action).toEqual(["mouse", "desk"]);
    }
  });

  it("never puts the pixel apron on an anime cook", () => {
    expect(animeProps("bbq-grill", ["tongs"])).toEqual(["tongs", "apron"]);
    expect(animeProps("bbq-grill", [])).toEqual(["apron"]);
  });

  it("never puts the pixel backpack on an anime hiker, at the summit either", () => {
    expect(animeProps("hike-walk", ["pack"])).toEqual(["pack"]);
    expect(animeProps("hike-walk", [])).toEqual(["pack"]);
    expect(animeProps("hike-summit", [])).toEqual(["pack"]);
  });

  it("takes the manifest's word for every other action", () => {
    expect(animeProps("cycle-ride", ["bike"])).toEqual(["bike"]);
    expect(animeProps("fish-sit", ["rod"])).toEqual(["rod"]);
    expect(animeProps("stream-wade", ["pole"])).toEqual(["pole"]);
    expect(animeProps("door-idle", [])).toEqual([]);
    expect(animeProps("sofa-sit", [])).toEqual([]);
  });
});

describe("drawFigure", () => {
  it("leaves out the desk unit's pixel parts, the monitor's screen and all, and nothing else", () => {
    expect(PROP_PARTS.desk).toBeDefined();
    const { without } = drawFigure(skinOf("human").desk("type", "codex"), sheetWith({}), "desk-type", DESK);
    const parts = scene(
      "monitor",
      "desk",
      "screen.code",
      "screen.code.lines",
      "screen.typing.a",
      "anime.desk-type",
      "desk.mouse",
      "chair",
      "table",
      "table.cap.l",
      "mail",
      "bubble.ask",
    );
    expect(names(without(parts))).toEqual(["anime.desk-type", "table", "table.cap.l", "mail", "bubble.ask"]);
  });

  it("is the skin's figure, the scene whole, without the sheet or the action", () => {
    const pixel = skinOf("human").desk("rest", "codex");
    const parts = scene("monitor", "desk", "chair");
    for (const drawn of [drawFigure(pixel, undefined, "desk-rest", DESK), drawFigure(pixel, sheetWith({}), "desk-rest", DESK)]) {
      expect(drawn.figure).toBe(pixel);
      expect(drawn.without(parts)).toBe(parts);
      expect(drawn.head).toBeUndefined();
    }
    // Seated too: a seat moves nothing of the skin's figure.
    const angler = skinOf("human").fish("codex", { x: 1, y: 35 });
    for (const drawn of [drawFigure(angler, undefined, "fish-sit", ROCK), drawFigure(angler, sheetWith({}), "fish-sit", ROCK)]) {
      expect(drawn.figure).toBe(angler);
      expect(drawn.head).toBeUndefined();
    }
  });

  it("puts a seated action's seat contact on the seat's top, and a sheet's from before (its soles' line) under the shoes", () => {
    /** The frame's top-left, the scene's px (art px × 6), as it is placed. */
    const placed = (anchor: { x: number; y: number }, spot: AnimeSpot = ROCK) => {
      const sheet = sheetWith({ "fish-sit": { ...FRONT, anchor, props: ["rod"] } });
      const [part] = drawFigure(skinOf("human").fish("codex", { x: 1, y: 35 }), sheet, "fish-sit", spot).figure.parts;
      return { left: Math.round(part.x * 6 - part.anime!.window.x), top: Math.round(part.y * 6 - part.anime!.window.y) };
    };
    // The seat contact (96, 135) on the rock's top (11, 33): (11 × 6 - 96, 33 × 6 - 135).
    expect(placed({ x: 96, y: 135 })).toEqual({ left: -30, top: 63 });
    expect(placed({ x: 96, y: 100 })).toEqual({ left: -30, top: 98 });
    expect(placed({ x: 96, y: SOLES_Y - 1 })).toEqual({ left: -30, top: 198 - (SOLES_Y - 1) });
    // From SOLES_Y (170) down it is the soles' line, under the shoes (11, 36): (11 × 6 - 96, 36 × 6 - y).
    expect(SOLES_Y).toBe(170);
    expect(placed({ x: 96, y: SOLES_Y })).toEqual({ left: -30, top: 216 - SOLES_Y });
    expect(placed({ x: 96, y: 174 })).toEqual({ left: -30, top: 42 });
    // Where the scene has no seat (standing, at a desk), the anchor goes on its point whatever its y.
    expect(placed({ x: 96, y: 135 }, { point: ROCK.point, w: ROCK.w, h: ROCK.h })).toEqual({ left: -30, top: 216 - 135 });
  });

  it("gives the head's box in the scene, and hangs the bubbles' anchor off its right edge", () => {
    const { head, figure } = drawFigure(skinOf("human").desk("type", "codex"), sheetWith({}), "desk-type", DESK);
    // (62, 16) 68 × 62 frame px, the frame at the cell's corner: edges rounded.
    expect(head).toEqual({ x: 10, y: 3, w: 12, h: 10 });
    expect(figure.anchors.head).toEqual({ x: 22 - 10, y: 3 });
  });

  it("puts the rod's anchor where the line math (tip = rod + (ROD.w - 1, 0)) starts at the sheet's tip", () => {
    const sheet = sheetWith({ "fish-sit": { ...FRONT, props: ["rod"], tips: { rod: { x: 160, y: 58 } } } });
    const spot = { point: { x: 11, y: 36 }, w: 56, h: 44 };
    const { figure, tips } = drawFigure(skinOf("human").fish("codex", { x: 1, y: 35 }), sheet, "fish-sit", spot);
    // The frame's top-left: (11 × 6 - 96, 36 × 6 - 174) = (-30, 42) scene px;
    // the tip (160, 58) is in art pixel (21, 16).
    const rod = figure.anchors.rod;
    expect({ x: rod.x + ROD.w - 1, y: rod.y }).toEqual({ x: Math.floor((-30 + 160) / 6), y: Math.floor((42 + 58) / 6) });
    // Which anchors the sheet gave, apart.
    expect(tips).toEqual({ rod });
    // Without a tip, the skin's anchor stays, and the sheet gave none.
    const bare = drawFigure(skinOf("human").fish("codex", { x: 1, y: 35 }), sheetWith({ "fish-sit": { ...FRONT, props: ["rod"] } }), "fish-sit", spot);
    expect(bare.figure.anchors.rod).toEqual(skinOf("human").fish("codex", { x: 1, y: 35 }).anchors.rod);
    expect(bare.tips).toEqual({});
  });
});
