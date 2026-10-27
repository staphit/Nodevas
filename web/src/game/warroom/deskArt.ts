// Which sprites draw a desk in each pose, and where. Pure: the drawing
// (PixelSprite) only paints what this returns, so the state → picture mapping
// is testable on its own. Every pose has a still picture that says what it
// is without colour or motion (a glyph on the screen, a bubble, a raised arm
// or a head down); the animated ones add frames on top.

import type { Sprite } from "../pixel/compile";
import * as S from "../pixel/sprites";
import type { Pose } from "../world";

/** A sprite at a place in the 32×32 desk frame. With `frame`, it is one of
 * `of` frames that take turns (CSS steps), frame 0 the one shown still. */
export interface Placed {
  /** What it is, for tests and debugging: "screen.fail", "arm.raised"… */
  name: string;
  sprite: Sprite;
  x: number;
  y: number;
  frame?: { index: number; of: number };
}

/** How long one loop of a pose's animation takes, in ms; absent when it
 * holds still. */
export const POSE_LOOP_MS: Partial<Record<Pose, number>> = {
  working: 500,
  reviewing: 1400,
  "asking-user": 1000,
  limit: 1800,
};

export function poseAnimated(pose: Pose): boolean {
  return POSE_LOOP_MS[pose] !== undefined;
}

const HEAD_AT = { x: 11, y: 10 };
const BODY_AT = { x: 8, y: 19 };
const CHAIR_AT = { x: 9, y: 23 };
const LEFT_ARM_X = 6;
const RIGHT_ARM_X = 23;

const at = (name: string, sprite: Sprite, x: number, y: number): Placed => ({ name, sprite, x, y });
const framed = (placed: Placed, index: number, of: number): Placed => ({ ...placed, frame: { index, of } });
const onScreen = (name: string, sprite: Sprite, x: number, y: number) => at(name, sprite, S.SCREEN.x + x, S.SCREEN.y + y);

/** The screen's picture per pose. */
function screen(pose: Pose | "empty"): Placed[] {
  switch (pose) {
    case "working":
      return [
        onScreen("screen.code", S.SCREEN_ON, 0, 0),
        onScreen("screen.code.lines", S.CODE, 0, 0),
        framed(onScreen("screen.typing.a", S.TYPING_A, 0, 4), 0, 2),
        framed(onScreen("screen.typing.b", S.TYPING_B, 0, 4), 1, 2),
      ];
    case "reviewing":
      return [
        onScreen("screen.review", S.SCREEN_ON, 0, 0),
        onScreen("screen.review.doc", S.DOC, 0, 0),
        framed(onScreen("screen.review.lens.a", S.MAGNIFIER, 9, 1), 0, 2),
        framed(onScreen("screen.review.lens.b", S.MAGNIFIER, 9, 3), 1, 2),
      ];
    case "asking-user":
      return [onScreen("screen.ask", S.SCREEN_ON, 0, 0), onScreen("screen.ask.glyph", S.QUESTION, 5, 1)];
    case "failed":
      return [onScreen("screen.fail", S.SCREEN_FAIL, 0, 0), onScreen("screen.fail.glyph", S.CROSS, 4, 1)];
    case "finished":
      return [onScreen("screen.ok", S.SCREEN_OK, 0, 0), onScreen("screen.ok.glyph", S.CHECK, 3, 1)];
    case "waiting-ci":
      return [onScreen("screen.ci", S.SCREEN_ON, 0, 0), onScreen("screen.ci.glyph", S.HOURGLASS, 4, 1)];
    case "limit":
      return [onScreen("screen.limit", S.SCREEN_OFF, 0, 0), onScreen("screen.limit.glyph", S.CLOCK, 4, 1)];
    default:
      // idle, offline, a free desk (and the door queue, never at a desk)
      return [onScreen("screen.off", S.SCREEN_OFF, 0, 0), onScreen("screen.off.glare", S.GLARE, 1, 1)];
  }
}

function head(kind: string, x: number, y: number): Placed[] {
  const parts = [at("head.back", S.HEAD_BACK, x, y)];
  const wear = S.headwearOf(kind, "back");
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, x + wear.dx, y + wear.dy));
  return parts;
}

const body = () => at("body.seated", S.BODY_SEATED, BODY_AT.x, BODY_AT.y);
const restingArms = () => [
  at("arm.left", S.ARM, LEFT_ARM_X, 16),
  at("arm.right", S.ARM, RIGHT_ARM_X, 16),
];

/** The seated agent, back to the room, in a pose. */
function agent(pose: Pose, kind: string): Placed[] {
  switch (pose) {
    case "working":
      // Typing: the hands take turns on the keyboard.
      return [
        body(),
        ...head(kind, HEAD_AT.x, HEAD_AT.y),
        framed(at("arm.left.up", S.ARM_UP, LEFT_ARM_X, 15), 0, 2),
        framed(at("arm.right", S.ARM, RIGHT_ARM_X, 16), 0, 2),
        framed(at("arm.left", S.ARM, LEFT_ARM_X, 16), 1, 2),
        framed(at("arm.right.up", S.ARM_UP, RIGHT_ARM_X, 15), 1, 2),
      ];
    case "asking-user":
      // A hand up, the other on the desk.
      return [
        body(),
        ...head(kind, HEAD_AT.x, HEAD_AT.y),
        at("arm.left", S.ARM, LEFT_ARM_X, 16),
        at("arm.right.raised", S.ARM_RAISED, 21, 5),
      ];
    case "finished":
      // Both arms up: a cheer.
      return [
        body(),
        ...head(kind, HEAD_AT.x, HEAD_AT.y),
        at("arm.left.raised", S.ARM_RAISED, 8, 5),
        at("arm.right.raised", S.ARM_RAISED, 21, 5),
      ];
    case "failed":
      // Slumped: the head sinks and leans.
      return [body(), ...head(kind, HEAD_AT.x + 1, HEAD_AT.y + 2), ...restingArms()];
    case "limit":
      // Asleep on folded arms: the head goes down behind the shoulders.
      return [at("arms.folded", S.ARMS_FOLDED, 8, 17), ...head(kind, HEAD_AT.x, HEAD_AT.y + 4), body()];
    default:
      return [body(), ...head(kind, HEAD_AT.x, HEAD_AT.y), ...restingArms()];
  }
}

/** Over the desk: a bubble for a question or sleep, a held message. */
function extras(pose: Pose, mail: boolean): Placed[] {
  const parts: Placed[] = [];
  if (mail) parts.push(at("mail", S.ENVELOPE, 0, 10));
  if (pose === "asking-user") {
    parts.push(framed(at("bubble.ask", S.BUBBLE_ASK, 23, 0), 0, 2), framed(at("bubble.ask.up", S.BUBBLE_ASK, 23, 1), 1, 2));
  } else if (pose === "limit") {
    parts.push(at("bubble.sleep", S.BUBBLE_SLEEP, 18, 0));
    // zzz, then z, zz and round again: frame 0, what a still desk shows,
    // has all three letters.
    const z2 = at("bubble.z2", S.Z, 24, 2);
    parts.push(
      at("bubble.z1", S.Z, 20, 3),
      framed(z2, 0, 3),
      framed(at("bubble.z3", S.Z, 28, 1), 0, 3),
      framed({ ...z2, name: "bubble.z2.again" }, 2, 3),
    );
  }
  return parts;
}

export interface DeskOptions {
  /** A held message waits for approval. */
  mail?: boolean;
}

/** A desk with its agent (agent kind as agentColorKey gives it), or a free
 * desk. An agent waiting to be let in stands at the door instead (doorScene). */
export function deskScene(pose: Pose | "empty", kind: string, options: DeskOptions = {}): Placed[] {
  const parts: Placed[] = [at("desk", S.DESK, 0, 0), ...screen(pose)];
  if (pose !== "empty") parts.push(...agent(pose, kind));
  parts.push(at("chair", S.CHAIR, CHAIR_AT.x, CHAIR_AT.y));
  if (pose !== "empty") parts.push(...extras(pose, Boolean(options.mail)));
  return parts;
}

/** An agent at the door, waiting to be accepted: standing, facing the room,
 * a "…" bubble over it. */
export function doorScene(kind: string, options: DeskOptions = {}): Placed[] {
  const parts: Placed[] = [
    at("door", S.DOOR, 7, 2),
    at("body.standing", S.BODY_STANDING, 10, 16),
    at("legs", S.LEGS, 10, 25),
    at("head.front", S.HEAD_FRONT, 11, 7),
  ];
  const wear = S.headwearOf(kind, "front");
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, 11 + wear.dx, 7 + wear.dy));
  parts.push(at("bubble.wait", S.BUBBLE_WAIT, 21, 0));
  if (options.mail) parts.push(at("mail", S.ENVELOPE, 0, 10));
  return parts;
}
