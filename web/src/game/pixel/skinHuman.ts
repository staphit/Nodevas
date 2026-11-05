// The person: every agent without a character of its own (game/character.ts),
// in its kind's headwear (sprites.ts headwearOf) and a shirt in its kind's
// colour. The sprites are sprites.ts' and outdoorSprites.ts' (the scenes'
// first figure); this only puts them together per state, as skins.ts says.

import * as A from "./activitySprites";
import * as O from "./outdoorSprites";
import { at, framed, type Placed, type Point } from "./placed";
import type { DeskGesture, Skin } from "./skins";
import * as S from "./sprites";
import type { Sprite } from "./compile";

/** The head and the kind's headwear over it, seen from behind, the front or
 * the side, its top-left corner at (x, y). */
function head(kind: string, view: "back" | "front" | "side", sprite: Sprite, name: string, x: number, y: number): Placed[] {
  const parts = [at(name, sprite, x, y)];
  const wear = S.headwearOf(kind, view);
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, x + wear.dx, y + wear.dy));
  return parts;
}

// ---------- at a desk, back to the room ----------

const HEAD_AT = { x: 11, y: 10 };
const BODY_AT = { x: 8, y: 19 };
const LEFT_ARM_X = 6;
const RIGHT_ARM_X = 23;
/** Where the mouse goes, right of the hand on it. */
const MOUSE_AT = { x: 29, y: 16 };

const body = () => at("body.seated", S.BODY_SEATED, BODY_AT.x, BODY_AT.y);
const backHead = (kind: string, dx = 0, dy = 0) => head(kind, "back", S.HEAD_BACK, "head.back", HEAD_AT.x + dx, HEAD_AT.y + dy);
const restingArms = () => [at("arm.left", S.ARM, LEFT_ARM_X, 16), at("arm.right", S.ARM, RIGHT_ARM_X, 16)];
/** Typing: the hands take turns on the keyboard. */
const typingArms = () => [
  framed(at("arm.left.up", S.ARM_UP, LEFT_ARM_X, 15), 0, 2),
  framed(at("arm.right", S.ARM, RIGHT_ARM_X, 16), 0, 2),
  framed(at("arm.left", S.ARM, LEFT_ARM_X, 16), 1, 2),
  framed(at("arm.right.up", S.ARM_UP, RIGHT_ARM_X, 15), 1, 2),
];

function deskParts(gesture: DeskGesture, kind: string): { parts: Placed[]; head: Point } {
  const upright = { x: HEAD_AT.x, y: HEAD_AT.y };
  switch (gesture) {
    case "type":
      return { parts: [body(), ...backHead(kind), ...typingArms()], head: upright };
    case "chin":
      return {
        parts: [body(), ...backHead(kind), at("arm.left", S.ARM, LEFT_ARM_X, 16), at("arm.right.chin", A.ARM_CHIN, 19, 14)],
        head: upright,
      };
    case "mouse":
      // The right hand off to the side, on the mouse (the scene's, at MOUSE_AT).
      return {
        parts: [body(), ...backHead(kind), at("arm.left", S.ARM, LEFT_ARM_X, 16), at("arm.right", S.ARM, 26, 16)],
        head: upright,
      };
    case "ask":
      // A hand up, the other on the desk.
      return {
        parts: [body(), ...backHead(kind), at("arm.left", S.ARM, LEFT_ARM_X, 16), at("arm.right.raised", S.ARM_RAISED, 21, 5)],
        head: upright,
      };
    case "cheer":
      // Both arms up: a cheer.
      return {
        parts: [
          body(),
          ...backHead(kind),
          at("arm.left.raised", S.ARM_RAISED, 8, 5),
          at("arm.right.raised", S.ARM_RAISED, 21, 5),
        ],
        head: upright,
      };
    case "slump":
      // Slumped: the head sinks and leans.
      return { parts: [body(), ...backHead(kind, 1, 2), ...restingArms()], head: { x: HEAD_AT.x + 1, y: HEAD_AT.y + 2 } };
    case "sleep":
      // Asleep on folded arms: the head goes down behind the shoulders.
      return {
        parts: [at("arms.folded", S.ARMS_FOLDED, 8, 17), ...backHead(kind, 0, 4), body()],
        head: { x: HEAD_AT.x, y: HEAD_AT.y + 4 },
      };
    default:
      return { parts: [body(), ...backHead(kind), ...restingArms()], head: upright };
  }
}

// ---------- facing the room ----------

/** Standing, from the front, the body's left edge at x and its feet on row
 * `feet` (legs drawn by the caller): head and headwear over the body. */
function standing(kind: string, x: number, feet: number, sprite: Sprite): Placed[] {
  return [at("body.standing", sprite, x, feet - 13), ...head(kind, "front", S.HEAD_FRONT, "head.front", x + 1, feet - 22)];
}
const standingHead = (x: number, feet: number) => ({ x: x + 1, y: feet - 22 });

/** Sitting, from the front, as on a sofa, its head's top-left corner at
 * (x, y). */
function sitting(kind: string, x: number, y: number): Placed[] {
  return [
    at("legs.seated", S.LEGS_SEATED, x - 1, y + 17),
    at("body.seated", S.BODY_SEATED, x - 3, y + 9),
    ...head(kind, "front", S.HEAD_FRONT, "head.front", x, y),
    at("arm.left", S.ARM, x - 5, y + 11),
    at("arm.right", S.ARM, x + 12, y + 11),
  ];
}
/** The head of someone sitting in SIT_BOX at `place`. */
const sittingHead = (place: Point) => ({ x: place.x + 5, y: place.y - 19 });

export const humanSkin: Skin = {
  id: "human",

  desk(gesture, kind) {
    const { parts, head: headAt } = deskParts(gesture, kind);
    return { parts, anchors: { head: headAt, mouse: MOUSE_AT } };
  },

  door(kind) {
    const x = 10;
    const feet = 29;
    return {
      parts: [
        at("body.standing", S.BODY_STANDING, x, feet - 13),
        at("legs", S.LEGS, x, feet - 4),
        ...head(kind, "front", S.HEAD_FRONT, "head.front", x + 1, feet - 22),
      ],
      anchors: { head: standingHead(x, feet) },
    };
  },

  sofa(kind, asleep) {
    const headAt = asleep ? { x: 5, y: 9 } : { x: 7, y: 6 };
    const face = asleep
      ? head(kind, "front", S.HEAD_FRONT_SLEEP, "head.sleep", headAt.x, headAt.y)
      : head(kind, "front", S.HEAD_FRONT, "head.front", headAt.x, headAt.y);
    const parts: Placed[] = [at("legs.seated", S.LEGS_SEATED, 6, 24), at("body.seated", S.BODY_SEATED, 4, 16)];
    if (asleep) parts.push(at("arms.folded", S.ARMS_FOLDED, 4, 18), ...face);
    else parts.push(...face, at("arm.left", S.ARM, 2, 18), at("arm.right", S.ARM, 19, 18));
    return { parts, anchors: { head: headAt } };
  },

  hike(kind, { x, y: feet }, walking) {
    const legs = walking
      ? [
          framed(at("legs.walk.a", O.LEGS_WALK_A, x, feet - 4), 0, 2),
          framed(at("legs.walk.b", O.LEGS_WALK_B, x, feet - 4), 1, 2),
        ]
      : [at("legs", S.LEGS, x, feet - 4)];
    return {
      parts: [...legs, ...standing(kind, x, feet, O.BODY_HIKER)],
      anchors: { head: standingHead(x, feet), pack: { x: x - 1, y: feet - 15 } },
    };
  },

  stream(kind, { x, y: feet }) {
    // No legs: they are under the water.
    return {
      parts: standing(kind, x, feet, S.BODY_STANDING),
      anchors: { head: standingHead(x, feet), pole: { x: x + 12, y: feet - 15 }, wade: { x: x - 1, y: feet - 6 } },
    };
  },

  fish(kind, place) {
    const headAt = sittingHead(place);
    return { parts: sitting(kind, headAt.x, headAt.y), anchors: { head: headAt, rod: { x: headAt.x + 13, y: headAt.y - 8 } } };
  },

  camp(kind, place) {
    const headAt = sittingHead(place);
    return { parts: sitting(kind, headAt.x, headAt.y), anchors: { head: headAt } };
  },

  bbq(kind, { x, y: feet }) {
    // The forearm on the tongs: the shirt's sleeve, the hand on the handle.
    const tongs = { x: x + 10, y: feet - 13 };
    return {
      parts: [at("legs", S.LEGS, x, feet - 4), ...standing(kind, x, feet, S.BODY_STANDING)],
      front: [
        framed(at("arm.tongs.a", O.ARM_TONGS_A, tongs.x, tongs.y), 0, 2),
        framed(at("arm.tongs.b", O.ARM_TONGS_B, tongs.x, tongs.y - 1), 1, 2),
      ],
      anchors: { head: standingHead(x, feet), apron: { x: x + 3, y: feet - 11 }, tongs },
    };
  },

  cycle(kind, { x, y }) {
    // The torso leaning to the bars, the near arm on them, the head in
    // profile; the legs pedalling, the far one behind the bike.
    const headAt = { x: x + 10, y: y - 17 };
    return {
      behind: [
        framed(at("cycle.legs.far.a", O.LEGS_RIDE_FAR_A, x + 3, y - 3), 0, 2),
        framed(at("cycle.legs.far.b", O.LEGS_RIDE_FAR_B, x + 3, y - 3), 1, 2),
      ],
      parts: [
        framed(at("cycle.legs.a", O.LEGS_RIDE_A, x + 3, y - 3), 0, 2),
        framed(at("cycle.legs.b", O.LEGS_RIDE_B, x + 3, y - 3), 1, 2),
        at("body.rider", O.BODY_RIDER, x + 3, y - 9),
        at("arm.reach", O.ARM_REACH, x + 10, y - 8),
        ...head(kind, "side", S.HEAD_SIDE, "head.side", headAt.x, headAt.y),
      ],
      anchors: { head: headAt },
    };
  },

  mini: S.MINI_AGENT,
};
