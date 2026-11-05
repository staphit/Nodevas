// Gemini, as a living four-pointed star (game/character.ts says who): the
// whole body one sparkle with concave sides, long points up and down and
// short ones to the sides, banded like a gradient (blue, red to the top
// point, yellow to the left, green to the bottom); one big eye in the middle
// under a few lashes; thin stick arms out of the side points and stick legs,
// knees bent, out of the bottom, with yellow mitts and shoes (a stick alone
// is lost on a desk's seams or in a dark doorway). From behind, the star
// without its eye; from the side (on the bike), a thinner star, the eye at
// its front edge. Original art; every state's contract (frames, sizes, anchors) is
// skins.ts', and skins.test.ts checks it. The kind is not drawn: the star is
// the same whichever agent runs Gemini, but for the small figure in another
// office, where nothing else says which agent it is (its bottom point).

import { at, framed, type Placed, type Point } from "./placed";
import type { DeskGesture, Skin } from "./skins";
import { sprite } from "./sprites";

/** A grid read right to left: a limb on the other side. */
const mirror = (grid: string) =>
  grid
    .trim()
    .split("\n")
    .map((row) => [...row.trim()].reverse().join(""))
    .join("\n");
/** A limb in shadow, all outline: the far leg behind the bike. */
const shadow = (grid: string) => grid.replace(/[»Z]/g, "o");
/** A grid's first `n` rows. */
const topRows = (grid: string, n: number) => grid.trim().split("\n").slice(0, n).join("\n");

// ---------- the star ----------

/** The star, from the front or behind (17×21): its points' tips at
 * (0, 10), (16, 10), (8, 0) and (8, 20), red fading to blue down the top
 * point, yellow at the left one, blue to green down the bottom one. */
const BODY = `
  ........o........
  .......oXo.......
  .......oXo.......
  .......oXo.......
  ......oXXXo......
  ......oXXXo......
  .....oXXIXXo.....
  ....oIXIXIXIo....
  ...oIIIIIIIIIo...
  .ooZIIIIIIIIIIoo.
  oZZZIIIIIIIIIIIIo
  .ooZIIIIIIIIIIoo.
  ...oIIIIIIIIIo...
  ....oI%I%I%Io....
  .....o%I%I%o.....
  ......o%%%o......
  ......o%%%o......
  .......o%o.......
  .......o%o.......
  .......o%o.......
  ........o........
`;
/** Slumped: the top point wilts over to one side. */
const WILT = `
  .................
  .................
  .............ooo.
  ...........ooXXo.
  ..........oXXoo..
  .........oXXo....
  .....oooXXXIo....
  ....oIIXXIXIo....
  ...oIIIIIIIIIo...
  .ooZIIIIIIIIIIoo.
  oZZZIIIIIIIIIIIIo
  .ooZIIIIIIIIIIoo.
  ...oIIIIIIIIIo...
  ....oI%I%I%Io....
  .....o%I%I%o.....
  ......o%%%o......
  ......o%%%o......
  .......o%o.......
  .......o%o.......
  .......o%o.......
  ........o........
`;
export const STAR = sprite(BODY);
/** At a desk, sunk: the bottom point cut off at the chair's seat (row 28),
 * so none of it shows under the seat by the chair's stem. */
export const STAR_SUNK = sprite(topRows(BODY, 16));
export const STAR_WILT = sprite(topRows(WILT, 17));
/** Waist deep: the star down to the bottom point's root, the rest under
 * the water. */
export const STAR_WADING = sprite(topRows(BODY, 16));
/** The eye, open, at the star's (5, 6): lashes, the white, the pupil. */
export const STAR_EYE = sprite(`
  ...o...
  .o.o.o.
  .ooooo.
  owwwwwo
  owweewo
  owweewo
  owwwwwo
  .ooooo.
`);
/** The eye shut, asleep, at the star's (5, 10): a lid, the lashes down. */
export const STAR_EYE_SHUT = sprite(`
  o.....o
  .ooooo.
  .o.o.o.
`);
/** From the side, facing right (11×21): edge on, so thinner, the side
 * points short; the eye at the front edge, looking ahead. */
export const STAR_SIDE = sprite(`
  .....o.....
  ....oXo....
  ....oXo....
  ....oXo....
  ....oXo....
  ...oXXXo...
  ...oXIXo.o.
  ..oIXIoooo.
  .oZIIowwwo.
  .oZIIowewo.
  oZZIIowewoo
  .oZIIowwwo.
  .oIIIIoooo.
  ..oI%I%Io..
  ...o%I%o...
  ...o%%%o...
  ....o%o....
  ....o%o....
  ....o%o....
  ....o%o....
  .....o.....
`);
/** From behind, small, at another office's desk (7×8): its bottom point
 * in the agent kind's colour, the one place the star shows it (skins.ts'
 * mini). */
export const STAR_MINI = sprite(`
  ...o...
  ..oXo..
  .ooIoo.
  oZIIIIo
  .ooIoo.
  ..oao..
  ..oao..
  ...o...
`);

// ---------- limbs: sticks in gem-limb, the hands and feet in yellow; the left
// one (the right one is its mirror) ----------

/** A hand on the desk, the arm up from the side point. */
const ARM_DESK = `
  ZZ.
  ZZ.
  .».
  .».
  ..»
`;
/** The hand lifted off the keys. */
const ARM_DESK_UP = `
  ZZ.
  ZZ.
  .».
  .».
  .».
  ..»
`;
/** Both arms up from the side points: a cheer. */
const ARM_CHEER = `
  ZZ...
  ZZ...
  .»...
  .»...
  ..»..
  ..»..
  ..»..
  ..»..
  ...».
  ...».
  ...».
  ....»
  ....»
`;
/** Up, close to the star: at the top of the trail. */
const ARM_UP = `
  ZZ
  ZZ
  .»
  .»
  .»
  .»
`;
/** Hanging limp from a slumped star. */
const ARM_LIMP = `
  .»
  .»
  .»
  ZZ
`;
/** Down at the side, the hand a little out. */
const ARM_HANG = `
  ..»
  .».
  .».
  ZZ.
  ZZ.
`;
/** Down at the side, close in: on the trail. */
const ARM_SWING = `
  .»
  .»
  .»
  ZZ
  ZZ
`;
/** The right arm, bent up, the hand on the star's cheek (by its top
 * point). */
export const ARM_CHIN = sprite(`
  ZZ...
  ZZ»..
  ...».
  ....»
  ....»
  ....»
`);
/** The right arm out to the mouse. */
export const ARM_MOUSE = sprite(`
  ..ZZ
  ..ZZ
  .»..
  .»..
  »...
`);
/** The right arm up: a question (the bubble over the hand). */
export const ARM_RAISED = sprite(`
  .ZZ
  .ZZ
  .».
  .».
  .».
  .».
  .».
  .».
  .».
  .».
  »..
`);
/** Asleep at the desk: the arms folded flat on its top, behind the star,
 * a forearm over each upper arm, the elbows out at its sides and the mitts
 * tucked in by its top point (21×3, on the desk top's rows 16–18). */
export const ARMS_FOLDED = sprite(`
  .»»»»»»ZZ...ZZ»»»»»».
  »...................»
  .»»»»»»»»»»»»»»»»»»».
`);
/** Asleep on the sofa: the arms round the star's middle. */
export const ARMS_HUG = sprite(`
  ».................»
  ».................»
  .»»»»»»»ZZZ»»»»»»».
`);
/** The right hand on the walking pole (the pole passes between its
 * fingers). */
export const ARM_POLE = sprite(`
  Z.Z
  Z.Z
  »..
`);
/** The left arm down into the water. */
export const ARM_WADE = sprite(`
  »
  »
  »
`);
/** The right arm down to the rod's grip. */
export const ARM_ROD = sprite(`
  ».
  ».
  ZZ
`);
/** The right hand out to the fire. */
export const ARM_WARM = sprite(`
  »..
  .ZZ
  .ZZ
`);
/** The right arm out of the point to the barbecue tongs, the mitt on
 * their handle: level, down on a skewer; then up a row, lifting them. */
export const ARM_TONGS_A = sprite(`
  .ZZ
  »ZZ
`);
export const ARM_TONGS_B = sprite(`
  .ZZ
  .ZZ
  »..
`);
/** On the bike, the front arm down to the bars. */
export const ARM_BARS = sprite(`
  »...
  »...
  .»..
  .»..
  .»..
  .»..
  ..».
  ..ZZ
`);

export const ARM_DESK_L = sprite(ARM_DESK);
export const ARM_DESK_R = sprite(mirror(ARM_DESK));
export const ARM_DESK_UP_L = sprite(ARM_DESK_UP);
export const ARM_DESK_UP_R = sprite(mirror(ARM_DESK_UP));
export const ARM_CHEER_L = sprite(ARM_CHEER);
export const ARM_CHEER_R = sprite(mirror(ARM_CHEER));
export const ARM_UP_L = sprite(ARM_UP);
export const ARM_UP_R = sprite(mirror(ARM_UP));
export const ARM_LIMP_L = sprite(ARM_LIMP);
export const ARM_LIMP_R = sprite(mirror(ARM_LIMP));
export const ARM_HANG_L = sprite(ARM_HANG);
export const ARM_HANG_R = sprite(mirror(ARM_HANG));
export const ARM_SWING_L = sprite(ARM_SWING);
export const ARM_SWING_R = sprite(mirror(ARM_SWING));

/** Standing, knees bent out, feet out (13×7, under the star's (2, 16)). */
export const LEGS = sprite(`
  ...».....»...
  ..».......»..
  .».........».
  ..».......»..
  ..».......»..
  ..».......»..
  ZZ».......»ZZ
`);
/** Walking: one leg planted, the other's knee up and its foot off the
 * ground, then the other way round. */
const WALK = `
  ...».....»...
  ...»......»..
  ...».......».
  ...»......»..
  ...»......»ZZ
  ...».........
  .ZZ».........
`;
export const LEGS_WALK_A = sprite(WALK);
export const LEGS_WALK_B = sprite(mirror(WALK));
/** Seated, from the front: thighs out to the knees, shins down (13×7). */
export const LEGS_SEATED = sprite(`
  ...».....»...
  .»».......»».
  .».........».
  .».........».
  .».........».
  .».........».
  ZZ.........ZZ
`);
/** On the bike, from the side: the near leg's knee up and its foot on the
 * pedal at the front (a), then pushed down, the foot at the bottom (b). */
const RIDE_UP = `
  »»».
  ...»
  ...»
  ...»
  ..».
  ..».
  .»ZZ
`;
const RIDE_DOWN = `
  »..
  .».
  .».
  ..»
  ..»
  .».
  .».
  .».
  .».
  .».
  »ZZ
`;
export const LEG_RIDE_UP = sprite(RIDE_UP);
export const LEG_RIDE_DOWN = sprite(RIDE_DOWN);
/** The far leg, behind the bike, a step behind the near one. */
export const LEG_RIDE_UP_FAR = sprite(shadow(RIDE_UP));
export const LEG_RIDE_DOWN_FAR = sprite(shadow(RIDE_DOWN));

// ---------- putting it together ----------

/** The star's width, and the row its side points are on. */
const W = 17;
const TIP_Y = 10;
/** Where the legs start, under the star's corner. */
const LEGS_AT = { x: 2, y: 16 };

/** The star at (x, y), its top-left corner: from the front (its eye open),
 * asleep (shut) or from behind (none). */
function star(x: number, y: number, view: "front" | "asleep" | "back", body = STAR): Placed[] {
  const parts = [at("star.body", body, x, y)];
  if (view === "front") parts.push(at("star.eye", STAR_EYE, x + 5, y + 6));
  if (view === "asleep") parts.push(at("star.eye.shut", STAR_EYE_SHUT, x + 5, y + 10));
  return parts;
}

/** The arms down at the sides of the star at (x, y). */
const hanging = (x: number, y: number) => [
  at("star.arm.left", ARM_HANG_L, x - 3, y + TIP_Y + 1),
  at("star.arm.right", ARM_HANG_R, x + W, y + TIP_Y + 1),
];

/** Standing, from the front, in STAND_BOX at `place` (its feet on the box's
 * bottom row): the star's corner, centred `dx` right of the box. */
function standingAt({ x, y: feet }: Point, dx = 0): Point {
  return { x: x - 2 + dx, y: feet - 22 };
}
const legs = (x: number, y: number) => at("star.legs", LEGS, x + LEGS_AT.x, y + LEGS_AT.y);

/** Seated, from the front, in SIT_BOX at `place`: the star's corner, its
 * bottom point on the seat, the feet on the box's bottom row. */
function seatedAt({ x, y: shoes }: Point): Point {
  return { x: x + 2, y: shoes - 22 };
}
function seated(x: number, y: number): Placed[] {
  return [at("star.legs.seated", LEGS_SEATED, x + LEGS_AT.x, y + LEGS_AT.y), ...star(x, y, "front")];
}

// ---------- at a desk, back to the room ----------

/** The star at a desk: its top point clear of the screen's glyph, its
 * bottom one under the chair. */
const DESK_AT = { x: 8, y: 10 };
/** Where bubbles hang from: the person's head, so they sit as theirs do. */
const DESK_HEAD = { x: 11, y: 10 };
const MOUSE_AT = { x: 29, y: 16 };

function deskParts(gesture: DeskGesture): { parts: Placed[]; head: Point } {
  const { x, y } = DESK_AT;
  const back = star(x, y, "back");
  const left = at("star.arm.left", ARM_DESK_L, x - 3, y + 6);
  const right = at("star.arm.right", ARM_DESK_R, x + W, y + 6);
  switch (gesture) {
    case "type":
      // The hands take turns on the keys.
      return {
        parts: [
          ...back,
          framed(at("star.arm.left.up", ARM_DESK_UP_L, x - 3, y + 5), 0, 2),
          framed(right, 0, 2),
          framed(left, 1, 2),
          framed(at("star.arm.right.up", ARM_DESK_UP_R, x + W, y + 5), 1, 2),
        ],
        head: DESK_HEAD,
      };
    case "chin":
      return { parts: [...back, left, at("star.arm.right.chin", ARM_CHIN, x + 13, y + 4)], head: DESK_HEAD };
    case "mouse":
      return { parts: [...back, left, at("star.arm.right.mouse", ARM_MOUSE, x + W, y + 6)], head: DESK_HEAD };
    case "ask":
      return { parts: [...back, left, at("star.arm.right.raised", ARM_RAISED, x + W, y)], head: DESK_HEAD };
    case "cheer":
      return {
        parts: [
          ...back,
          at("star.arm.left.raised", ARM_CHEER_L, x - 5, y - 2),
          at("star.arm.right.raised", ARM_CHEER_R, x + W, y - 2),
        ],
        head: DESK_HEAD,
      };
    case "slump": {
      // Sunk two rows, the top point wilted, the arms limp.
      const sunk = y + 2;
      return {
        parts: [
          ...star(x, sunk, "back", STAR_WILT),
          at("star.arm.left.limp", ARM_LIMP_L, x - 2, sunk + TIP_Y + 1),
          at("star.arm.right.limp", ARM_LIMP_R, x + W, sunk + TIP_Y + 1),
        ],
        head: { x: DESK_HEAD.x + 1, y: DESK_HEAD.y + 2 },
      };
    }
    case "sleep": {
      // Down on its folded arms, sunk three rows, the arms on the desk top.
      const sunk = y + 3;
      return {
        parts: [at("star.arms.folded", ARMS_FOLDED, x - 2, sunk + 3), ...star(x, sunk, "back", STAR_SUNK)],
        head: { x: DESK_HEAD.x, y: DESK_HEAD.y + 4 },
      };
    }
    default:
      return { parts: [...back, left, right], head: DESK_HEAD };
  }
}

export const starSkin: Skin = {
  id: "star",

  desk(gesture) {
    const { parts, head } = deskParts(gesture);
    return { parts, anchors: { head, mouse: MOUSE_AT } };
  },

  door() {
    const { x, y } = standingAt({ x: 10, y: 29 });
    return { parts: [legs(x, y), ...star(x, y, "front"), ...hanging(x, y)], anchors: { head: { x: 11, y: 7 } } };
  },

  sofa(_kind, asleep) {
    // Seated on the cushion; asleep, sunk three rows, the eye shut, the arms
    // round its middle.
    const { x, y } = seatedAt({ x: 1, y: 26 });
    if (!asleep) {
      return { parts: [...seated(x, y), ...hanging(x, y)], anchors: { head: { x: 7, y: 6 } } };
    }
    const sunk = y + 3;
    return {
      parts: [
        at("star.legs.seated", LEGS_SEATED, x + LEGS_AT.x, y + LEGS_AT.y),
        ...star(x, sunk, "asleep"),
        at("star.arms.hug", ARMS_HUG, x - 1, sunk + TIP_Y + 1),
      ],
      anchors: { head: { x: 5, y: 9 } },
    };
  },

  hike(_kind, place, walking) {
    // Two columns right of the box, the arms close in, so it keeps inside
    // the tile from the trail's foot to the top; there, arms up.
    const { x, y } = standingAt(place, 2);
    const parts: Placed[] = walking
      ? [
          framed(at("star.legs.walk.a", LEGS_WALK_A, x + LEGS_AT.x, y + LEGS_AT.y), 0, 2),
          framed(at("star.legs.walk.b", LEGS_WALK_B, x + LEGS_AT.x, y + LEGS_AT.y), 1, 2),
          ...star(x, y, "front"),
          at("star.arm.left", ARM_SWING_L, x - 2, y + TIP_Y + 1),
          at("star.arm.right", ARM_SWING_R, x + W, y + TIP_Y + 1),
        ]
      : [
          legs(x, y),
          ...star(x, y, "front"),
          at("star.arm.left.up", ARM_UP_L, x - 2, y + TIP_Y - 5),
          at("star.arm.right.up", ARM_UP_R, x + W, y + TIP_Y - 5),
        ];
    return { parts, anchors: { head: { x: x + 3, y } } };
  },

  stream(_kind, place) {
    // The water at the bottom point's root (wade); a hand on the pole, the
    // other arm down into the water.
    const { x } = standingAt(place, 2);
    const wade = { x, y: place.y - 6 };
    const y = wade.y - 14;
    return {
      parts: [
        ...star(x, y, "front", STAR_WADING),
        at("star.arm.left.wade", ARM_WADE, x - 1, y + TIP_Y + 1),
        at("star.arm.right.pole", ARM_POLE, x + W, y + TIP_Y - 3),
      ],
      anchors: { head: { x: x + 3, y }, pole: { x: x + W + 1, y: y + TIP_Y - 4 }, wade },
    };
  },

  fish(_kind, place) {
    // The rod's grip (rod + (0, 20)) under the right hand.
    const { x, y } = seatedAt(place);
    const hand = { x: x + W, y: y + TIP_Y + 1 };
    return {
      parts: [...seated(x, y), at("star.arm.left", ARM_HANG_L, x - 3, y + TIP_Y + 1), at("star.arm.right.rod", ARM_ROD, hand.x, hand.y)],
      anchors: { head: { x: x + 3, y }, rod: { x: hand.x, y: hand.y + 3 - 20 } },
    };
  },

  camp(_kind, place) {
    // A hand out to the fire on its right.
    const { x, y } = seatedAt(place);
    return {
      parts: [...seated(x, y), at("star.arm.left", ARM_HANG_L, x - 3, y + TIP_Y + 1), at("star.arm.right.warm", ARM_WARM, x + W, y + TIP_Y)],
      anchors: { head: { x: x + 3, y } },
    };
  },

  bbq(_kind, place) {
    // A stick arm out of the right point, the mitt on the tongs (their hand
    // column, skins.ts, two right of the point's tip).
    const { x, y } = standingAt(place);
    const tongs = { x: x + W - 2, y: y + TIP_Y - 1 };
    return {
      parts: [legs(x, y), ...star(x, y, "front"), at("star.arm.left", ARM_HANG_L, x - 3, y + TIP_Y + 1)],
      front: [
        framed(at("star.arm.right.tongs.a", ARM_TONGS_A, x + W, tongs.y), 0, 2),
        framed(at("star.arm.right.tongs.b", ARM_TONGS_B, x + W, tongs.y - 2), 1, 2),
      ],
      anchors: { head: { x: x + 3, y }, tongs },
    };
  },

  cycle(_kind, bike) {
    // Upright on the saddle, the bottom point on it; the front arm to the
    // bars, the legs to the pedals, the far one behind the bike.
    const { x, y } = bike;
    const body = { x: x + 3, y: y - 19 };
    return {
      behind: [
        framed(at("star.leg.far.a", LEG_RIDE_DOWN_FAR, x + 9, y - 1), 0, 2),
        framed(at("star.leg.far.b", LEG_RIDE_UP_FAR, x + 9, y - 1), 1, 2),
      ],
      parts: [
        framed(at("star.leg.a", LEG_RIDE_UP, x + 10, y - 1), 0, 2),
        framed(at("star.leg.b", LEG_RIDE_DOWN, x + 10, y - 1), 1, 2),
        at("star.body.side", STAR_SIDE, body.x, body.y),
        at("star.arm.bars", ARM_BARS, x + 13, y - 8),
      ],
      anchors: { head: { x: body.x + 2, y: body.y } },
    };
  },

  mini: STAR_MINI,
};
