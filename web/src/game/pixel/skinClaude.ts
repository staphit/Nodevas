// Claude, as a blocky orange creature (game/character.ts says who): a wide
// block with a flat top, two tall eyes and no mouth, a square nub out of
// each side for arms and four stubby legs, two pairs. From behind (at a
// desk, the small desk) a plain block and its nubs; from the side (the trail,
// the bike) a narrower block, one eye at its front edge, one nub, two legs.
// Its colours are its own (clawd, clawd-dark, clawd-light), whatever the
// agent kind; it wears nothing per kind but for the small figure in another
// office, where nothing else says which agent it is (a band at its base). Every state's contract (frames,
// sizes, anchors) is skins.ts'; skins.test.ts and skinClaude.test.ts check it.

import { BIKE_BARS } from "./outdoorSprites";
import { at, framed, type Placed, type Point } from "./placed";
import type { DeskGesture, Skin } from "./skins";
import { sprite } from "./sprites";

/** The block's width and height, from the front or behind (outline in). */
const BLOCK_W = 14;
const BLOCK_H = 11;

/** The block from the front, as a grid, its eyes `eyes` (rows 3–5). */
const front = (eyes: [string, string, string]) => `
  oooooooooooooo
  o777777777777o
  o555555555555o
  ${eyes[0]}
  ${eyes[1]}
  ${eyes[2]}
  o555555555555o
  o555555555555o
  o555555555555o
  o666666666666o
  oooooooooooooo
`;
const OPEN = "o55e555555e55o";
const LOOK = "o555e555555e5o";
const PLAIN = "o555555555555o";
/** Eyes open, looking out of the picture. */
const FRONT_GRID = front([OPEN, OPEN, OPEN]);
/** Eyes open, looking right (at the bobber, the fire, the grill). */
const FRONT_LOOK_GRID = front([LOOK, LOOK, LOOK]);

export const CLAWD_FRONT = sprite(FRONT_GRID);
export const CLAWD_FRONT_LOOK = sprite(FRONT_LOOK_GRID);
/** Asleep: the eyes shut, flat lines. */
export const CLAWD_FRONT_SLEEP = sprite(front([PLAIN, PLAIN, "o5ee555555ee5o"]));
/** Waist deep: the block's first rows only (the rest is under the water). */
export const CLAWD_WADER = sprite(FRONT_LOOK_GRID.trim().split("\n").slice(0, 9).join("\n"));
/** From behind: no eyes. */
export const CLAWD_BACK = sprite(front([PLAIN, PLAIN, PLAIN]));

/** From the side, facing right: narrower, one eye at the front edge, high
 * (clear of the nub under it). */
export const CLAWD_SIDE = sprite(`
  oooooooooo
  o77777777o
  o555555e5o
  o555555e5o
  o555555e5o
  o55555555o
  o55555555o
  o55555555o
  o55555555o
  o66666666o
  oooooooooo
`);

/** The nubs, out of the block's sides at its middle: the column over the
 * block's outline (the left nub's last, the right's first) opens into it. */
export const CLAWD_NUB_L = sprite(`
  oooo
  o555
  o555
  oooo
`);
export const CLAWD_NUB_R = sprite(`
  oooo
  555o
  555o
  oooo
`);
/** The right nub stretched out (to the mouse). */
export const CLAWD_NUB_LONG_R = sprite(`
  ooooooo
  555555o
  555555o
  ooooooo
`);
/** The right nub on the barbecue tongs, out of the block's top corner to
 * their handle (skins.ts' tongs box, its hand column): level, down on a
 * skewer (over the corner's outline column); then lifted, rising off the
 * corner (a column left of that). */
export const CLAWD_NUB_TONGS_A = sprite(`
  oooooo.
  555555.
  555555o
  ooooooo
`);
export const CLAWD_NUB_TONGS_B = sprite(`
  .oooooo.
  .o55555.
  o555555o
  .5oooooo
`);
/** A nub raised from the block's top corner (a hand up), its last row on the
 * block's top; and a row lower, for a wave. */
export const CLAWD_NUB_UP = sprite(`
  oooo
  o55o
  o55o
  o55o
  o55o
`);
export const CLAWD_NUB_UP_LOW = sprite(`
  oooo
  o55o
  o55o
  o55o
`);

/** Four stubby legs, two pairs, standing (the feet's outline on the last
 * row). */
export const CLAWD_LEGS = sprite(`
  .o5o5o..o5o5o.
  .o5o5o..o5o5o.
  .ooooo..ooooo.
`);
/** Seated: the legs tucked, a row of them showing. */
export const CLAWD_LEGS_TUCKED = sprite(`
  .o5o5o..o5o5o.
  .ooooo..ooooo.
`);

/** The nub from the side, a short stub out of the block's front or back,
 * low (high up under the eye, it reads as a beak; on the flank, as a
 * mouth). */
export const CLAWD_STUB_R = sprite(`
  ooo
  55o
  55o
  ooo
`);
export const CLAWD_STUB_L = sprite(`
  ooo
  o55
  o55
  ooo
`);
/** The near nub reaching forward from the flank to the bike's bars. */
export const CLAWD_NUB_REACH = sprite(`
  oooooo
  o5555o
  o5555o
  oooooo
`);

/** From the side, two legs: standing (under the middle, as they pass in a
 * stride), and striding apart. */
export const CLAWD_LEGS_SIDE = sprite(`
  .o55oo55o.
  .o55oo55o.
  .oooooooo.
`);
export const CLAWD_LEGS_STRIDE = sprite(`
  .o55oo55o.
  o55o..o55o
  oooo..oooo
`);

/** A leg on the bike, from the block down to its pedal: up (the pedal at
 * the top of its turn), down (at the bottom); the far one in shade. */
const leg = (rows: number, fill: string) => `${`o${fill}o\n`.repeat(rows)}ooo`;
export const CLAWD_PEDAL_UP = sprite(leg(1, "5"));
export const CLAWD_PEDAL_DOWN = sprite(leg(4, "5"));
export const CLAWD_PEDAL_FAR_UP = sprite(leg(1, "6"));
export const CLAWD_PEDAL_FAR_DOWN = sprite(leg(4, "6"));

/** From behind, small, at another office's desk: the block and its nubs,
 * its base a band in the agent kind's colour, the one place the block shows
 * it (skins.ts' mini). */
export const CLAWD_MINI = sprite(`
  .oooooo.
  .o7777o.
  oo5555oo
  o555555o
  oo5555oo
  .oaaaao.
  .oAAAAo.
`);

// ---------- placing it ----------

/** Rows from the block's top to its nubs' top outline (the nubs' middle is
 * the block's rows 5–6). */
const NUB_DY = 4;

/** From the side, rows from the block's top to the nub's top outline: low,
 * clear of the eye. */
const SIDE_NUB_DY = 6;

/** The two nubs at the block's sides, `left` and `right` rows under its top
 * (their top outline; NUB_DY both, at rest). */
function nubs(x: number, top: number, left = NUB_DY, right = left): Placed[] {
  return [at("clawd.nub.left", CLAWD_NUB_L, x - 3, top + left), at("clawd.nub.right", CLAWD_NUB_R, x + BLOCK_W - 1, top + right)];
}
const raisedLeft = (x: number, top: number, sprite = CLAWD_NUB_UP) =>
  at("clawd.nub.left.up", sprite, x - 1, top - sprite.h + 1);
const raisedRight = (x: number, top: number, sprite = CLAWD_NUB_UP) =>
  at("clawd.nub.right.up", sprite, x + BLOCK_W - 3, top - sprite.h + 1);

/** The head of a block at (x, top): where a person's would be over it,
 * centred, so the bubbles hang as they do over a person. */
const headOf = (x: number, top: number): Point => ({ x: x + 2, y: top });

/** Standing from the front in STAND_BOX at (x, feet): the block's left edge
 * and top. */
const standAt = (x: number, feet: number) => ({ x: x - 1, top: feet - BLOCK_H - 2 });

/** Seated from the front in SIT_BOX at `place` (the legs tucked). */
const sitAt = (place: Point) => ({ x: place.x + 3, top: place.y - BLOCK_H - 1 });

/** Seated on a rock or a log, from the front, looking right. */
function seated(place: Point): { parts: Placed[]; x: number; top: number } {
  const { x, top } = sitAt(place);
  return {
    x,
    top,
    parts: [
      at("clawd.legs.tucked", CLAWD_LEGS_TUCKED, x, top + BLOCK_H),
      at("clawd.front.look", CLAWD_FRONT_LOOK, x, top),
      ...nubs(x, top),
    ],
  };
}

// ---------- at a desk, back to the room ----------

/** The block at a desk: centred on the chair, its top under the screen's
 * glyph, its last rows behind the chair. */
const DESK = { x: 9, top: 13 };
/** Where the mouse goes: under the end of the stretched nub. */
const MOUSE_AT = { x: 28, y: DESK.top + NUB_DY };

function deskParts(gesture: DeskGesture): { parts: Placed[]; head: Point } {
  const { x, top } = DESK;
  const back = (dx = 0, dy = 0) => at("clawd.back", CLAWD_BACK, x + dx, top + dy);
  const upright = headOf(x, top);
  switch (gesture) {
    case "type":
      // The nubs take turns on the keyboard: up two rows, then down.
      return {
        parts: [
          back(),
          ...nubs(x, top, NUB_DY - 2, NUB_DY).map((part) => framed(part, 0, 2)),
          ...nubs(x, top, NUB_DY, NUB_DY - 2).map((part) => framed(part, 1, 2)),
        ],
        head: upright,
      };
    case "chin":
      // Thinking: the right nub up by the top.
      return { parts: [back(), ...nubs(x, top, NUB_DY, 1)], head: upright };
    case "mouse":
      // The right nub stretched out to the mouse (the scene's, at MOUSE_AT).
      return {
        parts: [back(), nubs(x, top)[0], at("clawd.nub.right.long", CLAWD_NUB_LONG_R, x + BLOCK_W - 1, top + NUB_DY)],
        head: upright,
      };
    case "ask":
      // The left nub up and waving (the question's bubble is on the right).
      return {
        parts: [
          back(),
          nubs(x, top)[1],
          framed(raisedLeft(x, top), 0, 2),
          framed({ ...raisedLeft(x, top, CLAWD_NUB_UP_LOW), name: "clawd.nub.left.wave" }, 1, 2),
        ],
        head: upright,
      };
    case "cheer":
      return { parts: [back(), raisedLeft(x, top), raisedRight(x, top)], head: upright };
    case "slump":
      // Slumped: sunk two rows and leaning, the nubs hanging low.
      return { parts: [back(1, 2), ...nubs(x + 1, top + 2, NUB_DY + 2)], head: headOf(x + 1, top + 2) };
    case "sleep":
      // Asleep: sunk low over the desk, the nubs flat on it.
      return { parts: [back(0, 4), ...nubs(x, top + 4, 1)], head: headOf(x, top + 4) };
    default:
      return { parts: [back(), ...nubs(x, top)], head: upright };
  }
}

export const claudeSkin: Skin = {
  id: "claude",

  desk(gesture) {
    const { parts, head } = deskParts(gesture);
    return { parts, anchors: { head, mouse: MOUSE_AT } };
  },

  door() {
    const { x, top } = standAt(10, 29);
    return {
      parts: [at("clawd.legs", CLAWD_LEGS, x, top + BLOCK_H), at("clawd.front", CLAWD_FRONT, x, top), ...nubs(x, top)],
      anchors: { head: headOf(x, top) },
    };
  },

  sofa(_kind, asleep) {
    // On the cushion, the tucked legs over its edge (row 26); asleep, sunk a
    // row, eyes shut and the nubs dropped.
    const x = 5;
    const top = asleep ? 15 : 14;
    const legs = at("clawd.legs.tucked", CLAWD_LEGS_TUCKED, x, 25);
    const parts = asleep
      ? [legs, at("clawd.front.sleep", CLAWD_FRONT_SLEEP, x, top), ...nubs(x, top, NUB_DY + 1)]
      : [legs, at("clawd.front", CLAWD_FRONT, x, top), ...nubs(x, top)];
    return { parts, anchors: { head: headOf(x, top) } };
  },

  hike(_kind, { x: boxX, y: feet }, walking) {
    // From the side, up the trail: the backpack on its back (behind it,
    // showing past its left edge), the legs striding and the nub swinging
    // with them, out in front then behind; at the top, standing still.
    const x = boxX + 2;
    const top = feet - BLOCK_H - 2;
    const legsAt = { x, y: top + BLOCK_H };
    const forward = at("clawd.nub.forward", CLAWD_STUB_R, x + CLAWD_SIDE.w - 1, top + SIDE_NUB_DY);
    const parts: Placed[] = walking
      ? [
          framed(at("clawd.legs.stride", CLAWD_LEGS_STRIDE, legsAt.x, legsAt.y), 0, 2),
          framed(at("clawd.legs.side", CLAWD_LEGS_SIDE, legsAt.x, legsAt.y), 1, 2),
          at("clawd.side", CLAWD_SIDE, x, top),
          framed(forward, 0, 2),
          framed(at("clawd.nub.back", CLAWD_STUB_L, x - 2, top + SIDE_NUB_DY), 1, 2),
        ]
      : [at("clawd.legs.side", CLAWD_LEGS_SIDE, legsAt.x, legsAt.y), at("clawd.side", CLAWD_SIDE, x, top), forward];
    return { parts, anchors: { head: { x: x + 1, y: top }, pack: { x: x - 4, y: top + 1 } } };
  },

  stream(_kind, { x: boxX, y: feet }) {
    // Waist deep, the pole in the right nub; two columns right of the
    // person, so the left nub stays in the tile at the first step.
    const x = boxX + 2;
    const top = feet - BLOCK_H - 2;
    return {
      parts: [at("clawd.wader", CLAWD_WADER, x, top), ...nubs(x, top)],
      anchors: {
        head: headOf(x, top),
        pole: { x: x + BLOCK_W + 1, y: top + NUB_DY },
        wade: { x, y: feet - 6 },
      },
    };
  },

  fish(_kind, place) {
    // The rod's grip in the right nub.
    const { parts, x, top } = seated(place);
    const grip = { x: x + BLOCK_W + 1, y: top + NUB_DY + 1 };
    return { parts, anchors: { head: headOf(x, top), rod: { x: grip.x, y: grip.y - 20 } } };
  },

  camp(_kind, place) {
    const { parts, x, top } = seated(place);
    return { parts, anchors: { head: headOf(x, top) } };
  },

  bbq(_kind, place) {
    // Looking at the grill, the right nub out from the block's top corner
    // with the tongs, over the grill. No apron: on the block it reads as a
    // mouth.
    const { x, top } = standAt(place.x, place.y);
    const corner = x + BLOCK_W - 1;
    return {
      parts: [
        at("clawd.legs", CLAWD_LEGS, x, top + BLOCK_H),
        at("clawd.front.look", CLAWD_FRONT_LOOK, x, top),
        nubs(x, top)[0],
      ],
      front: [
        framed(at("clawd.nub.tongs.a", CLAWD_NUB_TONGS_A, corner, top), 0, 2),
        framed(at("clawd.nub.tongs.b", CLAWD_NUB_TONGS_B, corner - 1, top - 2), 1, 2),
      ],
      anchors: { head: headOf(x, top), tongs: { x: x + BLOCK_W, y: top } },
    };
  },

  cycle(_kind, { x, y }) {
    // Sat low in the frame (the saddle and the top tube's back under it),
    // the nub reaching from its flank to the bars, the stubby legs
    // pedalling, the far one behind the bike.
    const body = { x: x + 4, y: y - CLAWD_SIDE.h + 4 };
    return {
      behind: [
        framed(at("clawd.pedal.far.down", CLAWD_PEDAL_FAR_DOWN, x + 7, y + 4), 0, 2),
        framed(at("clawd.pedal.far.up", CLAWD_PEDAL_FAR_UP, x + 8, y + 4), 1, 2),
      ],
      parts: [
        framed(at("clawd.pedal.up", CLAWD_PEDAL_UP, x + 10, y + 4), 0, 2),
        framed(at("clawd.pedal.down", CLAWD_PEDAL_DOWN, x + 9, y + 4), 1, 2),
        at("clawd.side", CLAWD_SIDE, body.x, body.y),
        at("clawd.nub.bars", CLAWD_NUB_REACH, x + BIKE_BARS.x + 2 - CLAWD_NUB_REACH.w, body.y + SIDE_NUB_DY),
      ],
      anchors: { head: { x: body.x + 1, y: body.y } },
    };
  },

  mini: CLAWD_MINI,
};
