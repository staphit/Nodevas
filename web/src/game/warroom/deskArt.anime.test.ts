// The scenes with a character drawn from its anime sheet (game/anime): the
// sheet's figure where the skin's would be, at the scene's point for the
// state (seated, its seat contact on the seat's top; a sheet's from before,
// its soles under the shoes), kept to the scene's frame; the pixel props the
// render has left out;
// bubbles off the sheet's head box. At a desk the render is the whole desk
// unit (the character behind a desk of its own, from the front), so the
// monitor, the desk top and the chair go and a badge off the head says what
// the screen would. Anything the sheet has no action for is the pixel figure,
// unchanged.

import { describe, expect, it } from "vitest";

import type { Activity } from "../activity";
import fixture from "../anime/fixture.json";
import { parseManifest, toSheet, type AnimeSheet } from "../anime/manifest";
import { OFFICE_STYLES } from "../officeStyle";
import { OUTING_KINDS, OUTING_STEPS } from "../outing";
import * as A from "../pixel/activitySprites";
import type { Sprite } from "../pixel/compile";
import { FISH_BACKDROP, HIKE_PATH, OUTDOOR_H, OUTDOOR_W, ROD } from "../pixel/outdoorSprites";
import type { PixelToken } from "../pixel/palette";
import { at } from "../pixel/placed";
import { DESK_GESTURES } from "../pixel/skins";
import * as S from "../pixel/sprites";
import { BUBBLE_ASK, BUBBLE_WAIT, CELL, HEADWEAR_KINDS } from "../pixel/sprites";
import { POSES, type Pose } from "../world";
import { deskScene, doorScene, meetingScene, miniDesk, outdoorScene, SEAT_W, sofaScene, type Placed } from "./deskArt";

const SHEET: AnimeSheet = toSheet("gpt", parseManifest(fixture)!, "/sheets/fixture.webp");

/** The fixture with more actions, for states it does not draw. */
function sheetWith(actions: Record<string, unknown>): AnimeSheet {
  const raw = { ...fixture, actions: { ...fixture.actions, ...actions } };
  return toSheet("gpt", parseManifest(raw)!, "/sheets/fixture.webp");
}

/** A desk action as the art pipeline writes it: the desk unit, from the
 * front, its soles' line at the frame's (96, 174). */
const UNIT = {
  row: 4,
  frames: 8,
  fps: 10,
  view: "front",
  anchor: { x: 96, y: 174 },
  head: { x: 62, y: 16, w: 68, h: 62 },
  props: ["desk"],
};
/** A sheet with every desk action. */
const DESKS = sheetWith(Object.fromEntries(DESK_GESTURES.map((gesture, i) => [`desk-${gesture}`, { ...UNIT, row: 4 + i }])));
/** A seated action as the art pipeline writes it: from the front, its
 * anchor the seat contact, the bottom of the hips (96, 135), the legs
 * hanging below it down to the soles (about y 174). */
const SEATED = { ...UNIT, anchor: { x: 96, y: 135 }, props: [] };
/** A seated action of a sheet from before: its anchor the soles' line. */
const SEATED_BEFORE = { ...UNIT, props: [] };
/** UNIT's head box in the desk frame, the frame at the cell's corner. */
const HEAD = { left: Math.round(62 / 6), right: Math.round(130 / 6), top: Math.round(16 / 6) };

const names = (parts: Placed[]) => parts.map((part) => part.name);
const animeOf = (parts: Placed[]) => parts.filter((part) => part.anime);
const find = (parts: Placed[], name: string) => parts.find((part) => part.name === name);

/** Where a frame whose top-left is at (left, top) in the scene's px (art
 * px × 6, so a whole anchor is whole there) shows in a w × h scene: the
 * part's place and size (art px), and the window of the frame (frame px) it
 * shows. */
function expectFramePx(part: Placed, left: number, top: number, w: number, h: number) {
  const x0 = Math.max(0, left);
  const y0 = Math.max(0, top);
  const x1 = Math.min(w * 6, left + 192);
  const y1 = Math.min(h * 6, top + 192);
  expect({ x: part.x, y: part.y, w: part.sprite.w, h: part.sprite.h }, part.name).toEqual({
    x: x0 / 6,
    y: y0 / 6,
    w: (x1 - x0) / 6,
    h: (y1 - y0) / 6,
  });
  expect(part.anime!.window, part.name).toEqual({ x: x0 - left, y: y0 - top, w: x1 - x0, h: y1 - y0 });
}

/** The same, the frame's top-left at (fx, fy) art px. */
const expectFrameAt = (part: Placed, fx: number, fy: number, w: number, h: number) => expectFramePx(part, fx * 6, fy * 6, w, h);

/** A sprite's opaque pixels as "x,y" → token, at its place. */
function paint(part: Placed): Map<string, string> {
  const drawn = new Map<string, string>();
  for (const layer of part.sprite.layers)
    for (const [, x, y, n] of layer.d.matchAll(/M(\d+) (\d+)h(\d+)/g))
      for (let i = 0; i < Number(n); i++) drawn.set(`${part.x + Number(x) + i},${part.y + Number(y)}`, layer.token);
  return drawn;
}

/** Whether two parts' boxes share a pixel. */
const overlap = (a: Placed, b: Placed) =>
  a.x < b.x + b.sprite.w && b.x < a.x + a.sprite.w && a.y < b.y + b.sprite.h && b.y < a.y + a.sprite.h;

/** The parts that draw the desk the sheet's render stands for, and the
 * person's own figure. */
const PIXEL_DESK = /^(monitor|desk|chair|desk\.mouse|screen\..*|arm\..*|arms\..*|head\..*|body\..*|headwear\..*)$/;

describe("GPT in pixel art", () => {
  it("is the person, in every scene, pose and kind", () => {
    const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);
    for (const kind of [...HEADWEAR_KINDS, "other"]) {
      expect(doorScene(kind, { character: "gpt", mail: true })).toEqual(doorScene(kind, { character: "human", mail: true }));
      for (const pose of POSES) {
        expect(deskScene(pose, kind, { character: "gpt" })).toEqual(deskScene(pose, kind, { character: "human" }));
        expect(meetingScene(pose, kind, { character: "gpt" })).toEqual(meetingScene(pose, kind, { character: "human" }));
        expect(sofaScene(pose, kind, { character: "gpt" })).toEqual(sofaScene(pose, kind, { character: "human" }));
        expect(miniDesk(pose, { character: "gpt" })).toEqual(miniDesk(pose, { character: "human" }));
      }
      for (const outing of OUTING_KINDS) {
        for (const step of steps) {
          expect(outdoorScene({ kind: outing, step }, kind, { character: "gpt" })).toEqual(
            outdoorScene({ kind: outing, step }, kind, { character: "human" }),
          );
        }
      }
    }
  });
});

describe("an anime figure", () => {
  it("stands in the doorway, its feet where the person's are, the … bubble off its head", () => {
    const parts = doorScene("codex", { character: "gpt", anime: SHEET });
    const [figure] = animeOf(parts);
    expect(figure.name).toBe("anime.door-idle");
    // Feet (96, 174) on the bottom of the doorway's row 29: frame at (0, 1).
    expectFrameAt(figure, 0, 1, CELL, CELL);
    expect(figure.anime).toMatchObject({ href: "/sheets/fixture.webp", row: 0, frames: 8, loopMs: 800, still: false });
    expect(names(parts)).toEqual(["door", "anime.door-idle", "bubble.wait"]);
    // The head box's right edge at 22, its top at 3: the bubble as off the
    // person's head (right edge 21, top 7), kept in the frame.
    const bubble = find(parts, "bubble.wait")!;
    expect({ x: bubble.x, y: bubble.y }).toEqual({ x: Math.min(22, CELL - BUBBLE_WAIT.w), y: 0 });
  });

  it("walks up the trail with the backpack its render has on, the pixel one left out", () => {
    for (let step = 0; step < OUTING_STEPS - 1; step++) {
      const parts = outdoorScene({ kind: "hike", step }, "codex", { character: "gpt", anime: SHEET });
      const [figure] = animeOf(parts);
      expect(figure.name, `${step}`).toBe("anime.hike-walk");
      // Feet (96, 174) on the bottom of the trail row the person stands on.
      const { x, feet } = HIKE_PATH[step];
      expectFrameAt(figure, x + 6 - 16, feet + 1 - 29, OUTDOOR_W, OUTDOOR_H);
      expect(names(parts), `${step}`).not.toContain("hike.pack");
      expect(names(parts), `${step}`).toEqual(expect.arrayContaining(["hike.backdrop", "hike.flag.pole", "hike.flag.a"]));
      expect(names(parts).filter((name) => name.startsWith("legs.")), `${step}`).toEqual([]);
    }
  });

  it("stays pixel art at the summit, which its sheet does not draw yet, backpack and all", () => {
    const top = { kind: "hike" as const, step: OUTING_STEPS - 1 };
    const parts = outdoorScene(top, "codex", { character: "gpt", anime: SHEET });
    expect(animeOf(parts)).toEqual([]);
    expect(parts).toEqual(outdoorScene(top, "codex", { character: "gpt" }));
    expect(names(parts)).toEqual(expect.arrayContaining(["hike.pack", "hike.summit"]));
  });

  it("rides its own bike, the pixel bike and wheels left out, along the road to the flag", () => {
    for (let step = 0; step < OUTING_STEPS; step++) {
      const parts = outdoorScene({ kind: "cycle", step }, "codex", { character: "gpt", anime: SHEET });
      const [figure] = animeOf(parts);
      expect(figure.name, `${step}`).toBe("anime.cycle-ride");
      // The wheels' ground (96, 174) under the pixel bike's centre: x + 11,
      // the bottom of its wheels' row 38.
      expectFrameAt(figure, 4 * step + 11 - 16, 39 - 29, OUTDOOR_W, OUTDOOR_H);
      expect(figure.anime!.row).toBe(3);
      const drawn = names(parts);
      expect(drawn.filter((name) => name === "cycle.bike" || name.startsWith("cycle.wheel.")), `${step}`).toEqual([]);
      expect(drawn.filter((name) => name.startsWith("cycle.legs.")), `${step}`).toEqual([]);
      expect(drawn, `${step}`).toEqual(expect.arrayContaining(["cycle.backdrop", "cycle.flag.pole", "cycle.flag"]));
      expect(drawn.includes("cycle.finish"), `${step}`).toBe(step === OUTING_STEPS - 1);
    }
  });

  it("keeps the pixel figure for every state its sheet has no action for", () => {
    for (const pose of POSES) {
      if (pose === "working") continue;
      expect(deskScene(pose, "codex", { character: "gpt", anime: SHEET }), pose).toEqual(deskScene(pose, "codex", { character: "gpt" }));
      expect(sofaScene(pose, "codex", { character: "gpt", anime: SHEET }), pose).toEqual(sofaScene(pose, "codex", { character: "gpt" }));
    }
    for (const kind of ["stream", "fish", "camp", "bbq"] as const) {
      const outing = { kind, step: 3 };
      expect(outdoorScene(outing, "codex", { character: "gpt", anime: SHEET }), kind).toEqual(
        outdoorScene(outing, "codex", { character: "gpt" }),
      );
    }
    // A small desk in another office is never drawn from a sheet.
    expect(miniDesk("working", { character: "gpt" })).toEqual(miniDesk("working", { character: "human" }));
  });

  it("sits on a sofa seat's cushion, its seat contact on the cushion's top, its legs down the sofa's front, kept to the seat's frame", () => {
    // The lounge lays the sofa 11 rows down a seat's frame (warroom.css: the
    // sofa from row 12, a seat's frame from row 1): under the left seat's
    // middle (the sofa's x 13), the crease where the back meets the seat
    // (rows 9 and 10), the cushion's lit top from row 11, the frame's 22,
    // and its front edge (row 14).
    const sofa = paint(at("sofa", S.SOFA, 0, 0));
    expect([9, 10, 11, 12, 13, 14].map((row) => sofa.get(`13,${row}`))).toEqual(["sofa-dark", "sofa-dark", "sofa", "sofa", "sofa", "outline"]);
    const sheet = sheetWith({ "sofa-sit": { ...SEATED, row: 5 }, "sofa-sleep": { ...SEATED, row: 6 } });
    for (const [pose, action, row] of [["idle", "sofa-sit", 5], ["offline", "sofa-sit", 5], ["limit", "sofa-sleep", 6]] as const) {
      const parts = sofaScene(pose, "codex", { character: "gpt", anime: sheet });
      const [figure, ...more] = animeOf(parts);
      expect(more, pose).toEqual([]);
      expect(figure.name, pose).toBe(`anime.${action}`);
      expect(figure.anime!.row, pose).toBe(row);
      // (96, 135) on the cushion's top (12, 22): the frame's top-left at
      // (12 × 6 - 96, 22 × 6 - 135) = (-24, -3), half an art pixel up, its
      // sides and top cut to the seat's frame; its soles (174) at 28.5, down
      // the sofa's front.
      expectFramePx(figure, (SEAT_W / 2) * 6 - 96, 22 * 6 - 135, SEAT_W, CELL);
      expect(names(parts).filter((name) => /^(legs|body|head|arm|arms|headwear)\./.test(name)), pose).toEqual([]);
    }
    // Asleep, zzz hangs off its head, inside the seat.
    const zzz = find(sofaScene("limit", "codex", { character: "gpt", anime: sheet }), "bubble.sleep")!;
    expect(zzz.x >= 0 && zzz.y >= 0 && zzz.x + zzz.sprite.w <= SEAT_W).toBe(true);
  });

  it("sits a sheet's from before, its seated anchor still the soles' line, on the sofa as it did", () => {
    for (const anchor of [SEATED_BEFORE.anchor, { x: 96, y: 170 }]) {
      const sheet = sheetWith({ "sofa-sit": { ...SEATED_BEFORE, anchor } });
      const [figure] = animeOf(sofaScene("idle", "codex", { character: "gpt", anime: sheet }));
      expect(figure.name).toBe("anime.sofa-sit");
      // Its soles under the person's shoes (on row 26), centred on the seat.
      expectFramePx(figure, (SEAT_W / 2) * 6 - 96, 27 * 6 - anchor.y, SEAT_W, CELL);
    }
  });
});

describe("an anime figure at a desk", () => {
  it("is its sheet's desk unit, the frame filling the cell, in every gesture", () => {
    const cases: [Pose, Activity | undefined, string][] = [
      ["idle", undefined, "desk-rest"],
      ["working", "edit", "desk-type"],
      ["working", "read", "desk-chin"],
      ["working", "search", "desk-mouse"],
      ["asking-user", undefined, "desk-ask"],
      ["finished", undefined, "desk-cheer"],
      ["failed", undefined, "desk-slump"],
      ["limit", undefined, "desk-sleep"],
    ];
    for (const [pose, activity, action] of cases) {
      const parts = deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity });
      const [figure, ...more] = animeOf(parts);
      expect(more, pose).toEqual([]);
      expect(figure.name, pose).toBe(`anime.${action}`);
      // Its soles' line (96, 174) on the floor under the desk's legs (16, 29):
      // the 32 × 32 frame is the cell's, all of it.
      expectFrameAt(figure, 0, 0, CELL, CELL);
      expect(figure.anime!.row, pose).toBe(4 + DESK_GESTURES.indexOf(action.slice("desk-".length) as never));
      // No monitor, desk top, chair, mouse or screen: the render has its own
      // desk and a laptop; nor the person, of course.
      expect(names(parts).filter((name) => PIXEL_DESK.test(name)), pose).toEqual([]);
    }
  });

  it("drops the desk all the same when an older manifest does not say its render has one", () => {
    const sheet = sheetWith({ "desk-rest": { ...UNIT, props: [] }, "desk-mouse": { ...UNIT, props: ["mouse"] } });
    for (const [pose, activity] of [["idle", undefined], ["working", "web"]] as const) {
      const parts = deskScene(pose, "codex", { character: "gpt", anime: sheet, activity });
      expect(animeOf(parts), pose).toHaveLength(1);
      expect(names(parts).filter((name) => PIXEL_DESK.test(name)), pose).toEqual([]);
    }
  });

  it("is the same in every office style: the style's desk and chair are not drawn", () => {
    for (const style of OFFICE_STYLES) {
      for (const pose of POSES) {
        expect(deskScene(pose, "codex", { character: "gpt", anime: DESKS, style }), `${style} ${pose}`).toEqual(
          deskScene(pose, "codex", { character: "gpt", anime: DESKS }),
        );
      }
    }
  });

  /** What each state's badge says: the screen's colour for its plate, the
   * screen's glyph on it, small (none for thinking: the thought is in its
   * bubble). */
  const BADGES: [string, Pose, Activity | undefined, PixelToken, [string, Sprite][]][] = [
    ["working", "working", undefined, "screen-on", [["badge.code.glyph", S.MINI_CODE]]],
    ["reviewing", "reviewing", undefined, "screen-on", [["badge.review.glyph", S.MAGNIFIER]]],
    ["asking", "asking-user", undefined, "screen-on", [["badge.ask.glyph", S.QUESTION]]],
    ["failed", "failed", undefined, "fail", [["badge.fail.glyph", S.CROSS]]],
    ["finished", "finished", undefined, "ok", [["badge.ok.glyph", S.CHECK]]],
    ["waiting on CI", "waiting-ci", undefined, "screen-on", [["badge.ci.glyph", S.HOURGLASS]]],
    ["on a usage limit", "limit", undefined, "screen-off", [["badge.limit.glyph", S.CLOCK]]],
    ["idle", "idle", undefined, "screen-off", [["badge.off.glare", S.GLARE]]],
    ["offline", "offline", undefined, "screen-off", [["badge.off.glare", S.GLARE]]],
    ["reading", "working", "read", "screen-on", [["badge.read.glyph", A.BADGE_PAGE]]],
    ["editing", "working", "edit", "screen-on", [["badge.code.glyph", S.MINI_CODE]]],
    ["in a shell", "working", "shell", "screen-on", [["badge.shell.glyph", A.PROMPT], ["badge.shell.cursor", A.CURSOR]]],
    ["searching", "reviewing", "search", "screen-on", [["badge.search.glyph", A.BADGE_SEARCH]]],
    ["searching at work", "working", "search", "screen-on", [["badge.search.glyph", A.BADGE_SEARCH]]],
    ["on the web", "working", "web", "screen-on", [["badge.web.glyph", A.GLOBE], ["badge.web.land", A.GLOBE_LAND_A]]],
    ["thinking", "working", "think", "screen-on", []],
  ];
  const plateOf = (parts: Placed[]) => parts.filter((part) => /^badge\.[a-z]+$/.test(part.name));

  it.each(BADGES)("says %s with a badge in the screen's colour and the screen's glyph", (_, pose, activity, token, glyphs) => {
    const parts = deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity });
    const [plate, ...more] = plateOf(parts);
    expect(more).toEqual([]);
    // A rounded plate: the outline round a face of the screen's colour, its
    // corners cut; the face 9 × 7 (8 rows for the globe).
    expect(plate.sprite.layers.map((layer) => layer.token).sort()).toEqual(["outline", token].sort());
    const face = { w: plate.sprite.w - 2, h: plate.sprite.h - 2 };
    expect(face.w).toBe(9);
    expect(face.h).toBe(Math.max(7, ...glyphs.map(([, sprite]) => sprite.h)));
    const drawn = paint(plate);
    for (const [x, y] of [[0, 0], [plate.sprite.w - 1, 0], [0, plate.sprite.h - 1], [plate.sprite.w - 1, plate.sprite.h - 1]]) {
      expect(drawn.has(`${plate.x + x},${plate.y + y}`), `corner ${x},${y}`).toBe(false);
    }
    expect(drawn.get(`${plate.x + 1},${plate.y + 1}`)).toBe(token);
    // The glyph on the face, over the plate, inside it.
    const marks = parts.filter((part) => part.name.startsWith(`${plate.name}.`));
    expect(marks.map((part) => [part.name, part.sprite])).toEqual(glyphs);
    const order = names(parts);
    for (const mark of marks) {
      expect(order.indexOf(mark.name)).toBeGreaterThan(order.indexOf(plate.name));
      expect(mark.x, mark.name).toBeGreaterThanOrEqual(plate.x + 1);
      expect(mark.y, mark.name).toBeGreaterThanOrEqual(plate.y + 1);
      expect(mark.x + mark.sprite.w, mark.name).toBeLessThanOrEqual(plate.x + plate.sprite.w - 1);
      expect(mark.y + mark.sprite.h, mark.name).toBeLessThanOrEqual(plate.y + plate.sprite.h - 1);
    }
    // Over the render, still: frame 0 of nothing.
    expect(order.indexOf(plate.name)).toBeGreaterThan(order.indexOf(animeOf(parts)[0].name));
    expect([plate, ...marks].filter((part) => part.frame)).toEqual([]);
  });

  it("gives every state a badge of its own, told apart by its glyph's pixels without its colours", () => {
    /** The badge's glyph as the pixels it paints over the plate, from the
     * plate's corner, whatever their colours; and with them. */
    const glyph = (pose: Pose, activity: Activity | undefined) => {
      const badge = deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity }).filter((part) => part.name.startsWith("badge."));
      const [plate, ...marks] = badge;
      const drawn = new Map<string, string>();
      for (const mark of marks) for (const [at, token] of paint(mark)) drawn.set(at, token);
      const from = (at: string) => at.split(",").map(Number).map((n, i) => n - (i ? plate.y : plate.x)).join(",");
      const pixels = [...drawn.keys()].map(from).sort();
      return { shape: `${plate.sprite.w}x${plate.sprite.h}:${pixels.join(";")}`, colours: [...drawn].map(([at, token]) => `${from(at)}=${token}`).sort().join(";") };
    };
    // What pixel art shows alike, the badge does too: editing is typing, as
    // working is; searching is searching at work or in a review; offline is
    // idle, greyed by the stylesheet.
    const alike: Record<string, string> = { editing: "working", "searching at work": "searching", offline: "idle" };
    const seen = new Map<string, string>();
    for (const [label, pose, activity] of BADGES) {
      const { shape, colours } = glyph(pose, activity);
      if (alike[label]) {
        expect(shape, `${label} as ${alike[label]}`).toBe(seen.get(alike[label]));
        continue;
      }
      expect([...seen].find(([, other]) => other === shape)?.[0], label).toBeUndefined();
      seen.set(label, shape);
      // Every glyph but the thought's draws something.
      expect(colours === "", label).toBe(label === "thinking");
    }
    // A glyph for every state but the thought, which has its bubble.
    expect(seen.size).toBe(BADGES.length - Object.keys(alike).length);
  });

  it.each(BADGES)("hangs the badge %s off the head's top-right, inside the cell, clear of the bubbles and the mail", (_, pose, activity) => {
    const parts = deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity, mail: true });
    const [plate] = plateOf(parts);
    const bubbles = parts.filter((part) => part.name.startsWith("bubble."));
    const mail = find(parts, "mail")!;
    // The mail envelope stays where it is.
    expect({ x: mail.x, y: mail.y }).toEqual({ x: 0, y: 10 });
    expect(overlap(plate, mail)).toBe(false);
    for (const bubble of bubbles) expect(overlap(plate, bubble), bubble.name).toBe(false);
    // Its middle row on the head's top; right of the head, or left of it
    // when a bubble hangs right of it.
    expect(plate.y).toBe(Math.max(0, HEAD.top - Math.floor(plate.sprite.h / 2)));
    if (bubbles.length) expect(plate.x).toBe(Math.max(0, HEAD.left - plate.sprite.w));
    else expect(plate.x).toBe(Math.min(HEAD.right, CELL - plate.sprite.w));
    // On the grid, inside the cell, as every part but the render.
    for (const part of parts) {
      if (part.anime) continue;
      expect(Number.isInteger(part.x) && Number.isInteger(part.y), part.name).toBe(true);
      expect(part.x, part.name).toBeGreaterThanOrEqual(0);
      expect(part.y, part.name).toBeGreaterThanOrEqual(0);
      expect(part.x + part.sprite.w, part.name).toBeLessThanOrEqual(CELL);
      expect(part.y + part.sprite.h, part.name).toBeLessThanOrEqual(CELL);
    }
  });

  it("hangs the badge off the head's top-left when a run's role tag or test lamp takes the cell's top-right", () => {
    const badgeless = (parts: Placed[]) => parts.filter((part) => !part.name.startsWith("badge."));
    for (const [pose, activity] of [["working", undefined], ["failed", undefined], ["working", "shell"], ["limit", undefined]] as const) {
      const parts = deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity, tagged: true });
      const [plate] = plateOf(parts);
      expect(plate.x, pose).toBe(Math.max(0, HEAD.left - plate.sprite.w));
      // The badge alone moves.
      expect(badgeless(parts), pose).toEqual(badgeless(deskScene(pose, "codex", { character: "gpt", anime: DESKS, activity })));
    }
    // Nothing moves for a pixel figure; at the table, the badge moves as at a desk.
    expect(deskScene("working", "codex", { character: "gpt", tagged: true })).toEqual(deskScene("working", "codex", { character: "gpt" }));
    const [seat] = plateOf(meetingScene("working", "codex", { character: "gpt", anime: DESKS, edge: "m", tagged: true }));
    expect(seat.x).toBe(0);
  });

  /** Where PixelDesk lays a run's role tag and test lamp, and the held
   * messages' count, over the drawing (warroom.css), in art px. */
  const TAG = { x: CELL - 11, y: 2, w: 11, h: 8 };
  const COUNT = { x: 5, y: 5, w: 8, h: 6 };
  const covers = (part: Placed, box: { x: number; y: number; w: number; h: number }) =>
    part.x < box.x + box.w && box.x < part.x + part.sprite.w && part.y < box.y + box.h && box.y < part.y + part.sprite.h;

  it.each(BADGES)("hangs the badge %s clear of the held messages' count, a role tag and the bubbles together", (_, pose, activity) => {
    for (const tagged of [false, true]) {
      for (const scene of ["desk", "table"] as const) {
        const options = { character: "gpt" as const, anime: DESKS, activity, mail: true, mailCount: true, tagged };
        const parts = scene === "desk" ? deskScene(pose, "codex", options) : meetingScene(pose, "codex", { ...options, edge: "m" });
        const [plate] = plateOf(parts);
        const where = `${scene}${tagged ? " tagged" : ""}`;
        expect(covers(plate, COUNT), where).toBe(false);
        if (tagged) expect(covers(plate, TAG), where).toBe(false);
        for (const part of parts.filter((other) => other.name.startsWith("bubble.") || other.name === "mail")) {
          expect(overlap(plate, part), `${where} ${part.name}`).toBe(false);
        }
        expect(plate.x + plate.sprite.w <= CELL && plate.y + plate.sprite.h <= CELL, where).toBe(true);
        // Off the head's top-right while that is clear, under the envelope
        // when the role tag or a bubble takes it and the count the top-left.
        const right = !tagged && !parts.some((part) => part.name.startsWith("bubble."));
        expect({ x: plate.x, y: plate.y }, where).toEqual(
          right ? { x: Math.min(HEAD.right, CELL - plate.sprite.w), y: Math.max(0, HEAD.top - Math.floor(plate.sprite.h / 2)) } : { x: 0, y: 15 },
        );
      }
    }
  });

  it("keeps the badge where it was for one held message, which has no count", () => {
    const one = deskScene("working", "codex", { character: "gpt", anime: DESKS, mail: true, tagged: true });
    const [plate] = plateOf(one);
    expect({ x: plate.x, y: plate.y }).toEqual({ x: 0, y: 0 });
    expect(one).toEqual(deskScene("working", "codex", { character: "gpt", anime: DESKS, mail: true, tagged: true, mailCount: false }));
    // Nothing moves for a pixel figure.
    expect(deskScene("working", "codex", { character: "gpt", mail: true, mailCount: true })).toEqual(
      deskScene("working", "codex", { character: "gpt", mail: true }),
    );
  });

  it("keeps the bubbles where they hang off its head", () => {
    const sheet = sheetWith({ "desk-ask": { ...UNIT, head: { x: 54, y: 66, w: 72, h: 60 } } });
    const parts = deskScene("asking-user", "codex", { character: "gpt", anime: sheet });
    expect(animeOf(parts).map((part) => part.name)).toEqual(["anime.desk-ask"]);
    // The head's right edge at (54 + 72) / 6 = 21, its top at 11: the "!" at
    // head + (12, -10), as off the person's 10-wide head at (11, 11).
    const ask = find(parts, "bubble.ask")!;
    expect({ x: ask.x, y: ask.y }).toEqual({ x: Math.min(11 + 12, CELL - BUBBLE_ASK.w), y: 1 });
    // zzz and the thought bubble are the pixel desk's, off the same head.
    const sleep = names(deskScene("limit", "codex", { character: "gpt", anime: DESKS }));
    expect(sleep).toEqual(expect.arrayContaining(["bubble.sleep", "bubble.z1", "bubble.z2", "bubble.z3"]));
    expect(names(deskScene("working", "codex", { character: "gpt", anime: DESKS, activity: "think" }))).toContain("bubble.think");
  });

  it("holds still while its agent is offline", () => {
    const [idle] = animeOf(deskScene("idle", "codex", { character: "gpt", anime: DESKS }));
    const [offline] = animeOf(deskScene("offline", "codex", { character: "gpt", anime: DESKS }));
    expect(idle.anime!.still).toBe(false);
    expect(offline.anime!.still).toBe(true);
  });

  it("sits behind the meeting table, its laptop on the table top, the table whole and in front", () => {
    for (const edge of ["l", "m", "r", "s"] as const) {
      const parts = meetingScene("working", "codex", { character: "gpt", anime: DESKS, edge, activity: "edit", mail: true });
      const [figure] = animeOf(parts);
      expect(figure.name, edge).toBe("anime.desk-type");
      // Two rows up, so the laptop stands on the table top (row 15), cut at
      // the top's front (row 21): nothing of its own desk under the table.
      expectFrameAt(figure, 0, -2, CELL, S.DESK_TOP_Y + 6);
      const order = names(parts);
      // The table, its legs where the seat has them, in front of the render.
      const table = names(meetingScene("working", "codex", { edge })).filter((name) => name.startsWith("table"));
      expect(order.filter((name) => name.startsWith("table")), edge).toEqual(table);
      for (const name of table) expect(order.indexOf(name), `${edge} ${name}`).toBeGreaterThan(order.indexOf(figure.name));
      // No monitor, screen or chair; the badge and the mail as at a desk.
      expect(order.filter((name) => PIXEL_DESK.test(name)), edge).toEqual([]);
      expect(order, edge).toEqual(expect.arrayContaining(["badge.code", "badge.code.glyph", "mail"]));
      // The badge over the table, inside the seat.
      const [plate] = plateOf(parts);
      expect(order.indexOf(plate.name)).toBeGreaterThan(order.indexOf("table"));
      expect(plate.x + plate.sprite.w).toBeLessThanOrEqual(CELL);
    }
    // An empty seat is the table and a chair, as ever.
    expect(names(meetingScene("empty", "other", { edge: "m" }))).toEqual(["table", "chair"]);
  });
});

describe("an anime figure on an outing", () => {
  const SIDE = { ...UNIT, view: "side", head: { x: 61, y: 18, w: 57, h: 62 } };
  /** Fishing from the rock, the rod raised out over the water: its tip
   * (186, 44) in the frame, which goes at (-30, 63) in the tile, its seat
   * contact (96, 135) on the rock's top (11, 33), is in art pixel (26, 17). */
  const TIP = { x: 186, y: 44 };
  const FISH = { ...SEATED, props: ["rod"], tips: { rod: TIP } };
  /** A sheet's from before: its soles (96, 174) under the shoes (11, 36),
   * the frame at (-30, 42), its tip (186, 40) in art pixel (26, 13). */
  const FISH_BEFORE = { ...SEATED_BEFORE, props: ["rod"], tips: { rod: { x: 186, y: 40 } } };
  const OUT = sheetWith({
    "stream-wade": { ...SIDE, row: 5, props: ["pole"] },
    "bbq-grill": { ...SIDE, row: 6, props: ["tongs"] },
    "fish-sit": { ...FISH, row: 7 },
  });
  const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);

  it("leaves out the props its render has, and keeps them for a pixel figure", () => {
    for (const step of steps) {
      const anime = (kind: "stream" | "bbq" | "fish") => names(outdoorScene({ kind, step }, "codex", { character: "gpt", anime: OUT }));
      const pixel = (kind: "stream" | "bbq" | "fish") => names(outdoorScene({ kind, step }, "codex", { character: "gpt" }));
      // The barbecue: its own tongs (and the hand on them); never the pixel apron.
      const cook = /^(bbq\.tongs\..*|bbq\.apron|arm\.tongs\..*)$/;
      expect(anime("bbq").filter((name) => cook.test(name)), `bbq ${step}`).toEqual([]);
      expect(pixel("bbq"), `bbq ${step}`).toEqual(expect.arrayContaining(["bbq.tongs.a", "bbq.tongs.b", "bbq.apron", "arm.tongs.a"]));
      expect(anime("bbq"), `bbq ${step}`).toEqual(expect.arrayContaining(["anime.bbq-grill", "bbq.grill", "bbq.smoke.a", "bbq.plate"]));
      // The stream: its own pole; the water round its waist stays.
      expect(anime("stream"), `stream ${step}`).not.toContain("stream.pole");
      expect(anime("stream"), `stream ${step}`).toEqual(expect.arrayContaining(["anime.stream-wade", "stream.wade", "stream.ripple.a"]));
      expect(pixel("stream"), `stream ${step}`).toEqual(expect.arrayContaining(["stream.pole", "stream.wade"]));
      // Fishing: its own rod; the line, the bobber and the catch stay.
      expect(anime("fish"), `fish ${step}`).not.toContain("fish.rod");
      expect(anime("fish"), `fish ${step}`).toEqual(expect.arrayContaining(["anime.fish-sit", "fish.line", "fish.bobber.a", "fish.bucket"]));
      expect(pixel("fish"), `fish ${step}`).toEqual(expect.arrayContaining(["fish.rod", "fish.line"]));
    }
  });

  it("never puts the pixel apron on an anime cook, whatever its manifest says", () => {
    const sheet = sheetWith({ "bbq-grill": { ...SIDE, props: [] } });
    expect(names(outdoorScene({ kind: "bbq", step: 2 }, "codex", { character: "gpt", anime: sheet }))).not.toContain("bbq.apron");
  });

  it("sits on the rock, its seat contact on the rock's top, its legs down the rock's front", () => {
    // The rock's top: its outline's row 33 under the middle of the person's
    // seated box at (1, 35), the grass over it, the rock under it.
    expect(["11,32", "11,33", "11,34"].map((pixel) => BACKDROP.get(pixel))).toEqual(["grass", "outline", "rock"]);
    const parts = outdoorScene({ kind: "fish", step: 0 }, "codex", { character: "gpt", anime: OUT });
    const [figure] = animeOf(parts);
    expect(figure.name).toBe("anime.fish-sit");
    // (96, 135) on (11, 33): the frame at (11 × 6 - 96, 33 × 6 - 135) =
    // (-30, 63), its left cut at the tile's edge; its soles (174) at 39.5,
    // on the grass in front of the rock.
    expectFramePx(figure, 11 * 6 - 96, 33 * 6 - 135, OUTDOOR_W, OUTDOOR_H);
    expect(names(parts).filter((name) => /^(legs|body|head|arm|headwear)\./.test(name))).toEqual([]);
  });

  it.each([
    ["its seat contact on the rock's top", FISH, { left: 11 * 6 - 96, top: 33 * 6 - 135 }, { x: 26, y: 17 }],
    ["a sheet's from before, its soles under the shoes", FISH_BEFORE, { left: 11 * 6 - 96, top: 36 * 6 - 174 }, { x: 26, y: 13 }],
  ])("hangs the fishing line from its render's rod tip, %s", (_, action, frame, tip) => {
    const sheet = sheetWith({ "fish-sit": { ...action, row: 7 } });
    for (const step of steps) {
      const parts = outdoorScene({ kind: "fish", step }, "codex", { character: "gpt", anime: sheet });
      const [figure] = animeOf(parts);
      // The frame's top-left, scene px (art px × 6), as it is placed.
      const left = Math.round(figure.x * 6 - figure.anime!.window.x);
      const top = Math.round(figure.y * 6 - figure.anime!.window.y);
      expect({ left, top }, `${step}`).toEqual(frame);
      // The art pixel the tip is in, the line from two rows under it as from
      // the pixel rod's tip, the bobber under it on the water.
      expect({ x: Math.floor((left + action.tips.rod.x) / 6), y: Math.floor((top + action.tips.rod.y) / 6) }).toEqual(tip);
      const line = find(parts, "fish.line")!;
      expect({ x: line.x, y: line.y }, `${step}`).toEqual({ x: tip.x, y: tip.y + 2 });
      expect(line.y + line.sprite.h, `${step}`).toBe(22);
      expect(find(parts, "fish.bobber.a")!.x, `${step}`).toBe(tip.x - 1);
      expect(names(parts), `${step}`).not.toContain("fish.rod");
      for (const part of parts) {
        if (part.anime) continue;
        expect(part.x, part.name).toBeGreaterThanOrEqual(0);
        expect(part.x + part.sprite.w, part.name).toBeLessThanOrEqual(OUTDOOR_W);
      }
    }
  });

  it("sits on the log by the fire, its seat contact on the log's top, its legs in front of it", () => {
    const sheet = sheetWith({ "camp-sit": { ...SEATED, row: 5 } });
    for (const step of steps) {
      const parts = outdoorScene({ kind: "camp", step }, "codex", { character: "gpt", anime: sheet });
      const [figure, ...more] = animeOf(parts);
      expect(more, `${step}`).toEqual([]);
      expect(figure.name, `${step}`).toBe("anime.camp-sit");
      // The log's top row (35), under the middle of the person's seated box
      // at (23, 38): the seat (33, 35), the frame's top-left at
      // (33 × 6 - 96, 35 × 6 - 135) = (102, 75), its soles (174) at 41.5, on
      // the grass in front of the log.
      const log = find(parts, "camp.log")!;
      const wood = paint(log);
      expect({ x: log.x, y: log.y, top: wood.has("33,35"), over: wood.has("33,34") }, `${step}`).toEqual({ x: 24, y: 35, top: true, over: false });
      expectFramePx(figure, 33 * 6 - 96, 35 * 6 - 135, OUTDOOR_W, OUTDOOR_H);
      // Over the log, under the fire; none of the person's figure.
      const order = names(parts);
      expect(order.indexOf(figure.name), `${step}`).toBeGreaterThan(order.indexOf("camp.log"));
      expect(order.indexOf(figure.name), `${step}`).toBeLessThan(order.indexOf("camp.fire.logs"));
      expect(order.filter((name) => /^(legs|body|head|arm|headwear)\./.test(name)), `${step}`).toEqual([]);
    }
    // A sheet's from before: its soles under the person's shoes, (33, 39).
    const before = sheetWith({ "camp-sit": { ...SEATED_BEFORE, row: 5 } });
    const [old] = animeOf(outdoorScene({ kind: "camp", step: 2 }, "codex", { character: "gpt", anime: before }));
    expectFramePx(old, 33 * 6 - 96, 39 * 6 - 174, OUTDOOR_W, OUTDOOR_H);
  });

  it("hangs the line from the pixel rod's tip for a pixel figure, as before", () => {
    const parts = outdoorScene({ kind: "fish", step: 3 }, "codex", { character: "gpt" });
    const rod = find(parts, "fish.rod")!;
    const line = find(parts, "fish.line")!;
    expect({ x: line.x, y: line.y }).toEqual({ x: rod.x + ROD.w - 1, y: rod.y + 2 });
  });

  /** The backdrop's colour under a scene pixel. */
  const BACKDROP = paint(at("fish.backdrop", FISH_BACKDROP, 0, 0));
  const WATER = new Set(["water", "water-light"]);

  it.each([
    ["its seat contact on the rock's top", SEATED.anchor, 33 * 6 - 135],
    ["a sheet's from before, its soles under the shoes", SEATED_BEFORE.anchor, 36 * 6 - 174],
  ])("fishes from its render only where the line hangs from its rod's tip to the bobber on the water, in pixel art anywhere else: %s", (_, anchor, top) => {
    let drawn = 0;
    // Tips (art px, in the tile) all over it, given in the frame's px, the
    // frame's top-left at (-30, top).
    for (let x = 14; x <= 56; x++) {
      for (let y = -3; y <= 24; y++) {
        const sheet = sheetWith({ "fish-sit": { ...FISH, anchor, tips: { rod: { x: x * 6 + 30 + 3, y: y * 6 - top + 3 } } } });
        for (const character of ["gpt", "whale"] as const) {
          const parts = outdoorScene({ kind: "fish", step: 4 }, "codex", { character, anime: sheet });
          const where = `${character} tip (${x}, ${y})`;
          // Out over the water (its rings on it, whole), and up off it by two rows of line at least.
          const hangs = x - 4 >= 21 && x - 4 + 10 <= OUTDOOR_W && y >= 0 && y + 2 <= 20;
          if (!hangs) {
            expect(parts, where).toEqual(outdoorScene({ kind: "fish", step: 4 }, "codex", { character }));
            continue;
          }
          drawn++;
          expect(names(parts), where).toContain("anime.fish-sit");
          expect(names(parts), where).not.toContain("fish.rod");
          const line = find(parts, "fish.line")!;
          expect({ x: line.x, y: line.y, end: line.y + line.sprite.h }, where).toEqual({ x, y: y + 2, end: 22 });
          // The rings on the water, every pixel; the bobber afloat, its bottom row in it.
          for (const name of ["fish.ring.a", "fish.ring.b"]) {
            for (const pixel of paint(find(parts, name)!).keys()) expect(WATER.has(BACKDROP.get(pixel)!), `${where} ${name} ${pixel}`).toBe(true);
          }
          for (const name of ["fish.bobber.a", "fish.bobber.b"]) {
            const bobber = find(parts, name)!;
            const bottom = [...paint(bobber).keys()].filter((pixel) => pixel.endsWith(`,${bobber.y + bobber.sprite.h - 1}`));
            for (const pixel of bottom) expect(WATER.has(BACKDROP.get(pixel)!), `${where} ${name} ${pixel}`).toBe(true);
          }
        }
      }
    }
    expect(drawn).toBeGreaterThan(0);
  });

  it("fishes in pixel art from a render whose sheet does not say where its rod ends", () => {
    const bare = { ...FISH };
    delete (bare as { tips?: unknown }).tips;
    const sheet = sheetWith({ "fish-sit": bare });
    expect(outdoorScene({ kind: "fish", step: 2 }, "codex", { character: "gpt", anime: sheet })).toEqual(
      outdoorScene({ kind: "fish", step: 2 }, "codex", { character: "gpt" }),
    );
  });

  it("wades cut where the water round its waist ends, as the skins' figures are", () => {
    for (const character of ["gpt", "whale", "claude", "star"] as const) {
      for (const step of steps) {
        const parts = outdoorScene({ kind: "stream", step }, "codex", { character, anime: OUT });
        const [figure] = animeOf(parts);
        const wade = find(parts, "stream.wade")!;
        const where = `${character} ${step}`;
        expect(figure.name, where).toBe("anime.stream-wade");
        // Nothing from the band's splash row down: the band's first two rows
        // over its waist, its legs in the water.
        expect(figure.y + figure.sprite.h, where).toBe(wade.y + 2);
        expect(figure.anime!.window.h, where).toBe(figure.sprite.h * 6);
        const pixel = outdoorScene({ kind: "stream", step }, "codex", { character });
        const ends = Math.max(...pixel.filter((part) => !part.name.startsWith("stream.")).map((part) => part.y + part.sprite.h));
        expect(figure.y + figure.sprite.h, where).toBe(ends);
        // The band over it.
        expect(names(parts).indexOf("stream.wade"), where).toBeGreaterThan(names(parts).indexOf(figure.name));
      }
    }
  });

  it("never carries the pixel backpack, up the trail or at the summit, whoever's skin has one", () => {
    const sheet = sheetWith({ "hike-summit": { ...UNIT, props: [] } });
    for (const character of ["gpt", "claude"] as const) {
      const top = { kind: "hike" as const, step: OUTING_STEPS - 1 };
      // The skin has one at the summit.
      expect(names(outdoorScene(top, "codex", { character })), character).toContain("hike.pack");
      const parts = outdoorScene(top, "codex", { character, anime: sheet });
      expect(names(parts), character).toEqual(expect.arrayContaining(["anime.hike-summit", "hike.summit", "hike.flag.pole"]));
      expect(names(parts), character).not.toContain("hike.pack");
    }
  });
});
