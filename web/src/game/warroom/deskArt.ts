// Which sprites draw a desk in each pose, and where. Pure: the drawing
// (PixelSprite) only paints what this returns, so the state → picture mapping
// is testable on its own. Every pose has a still picture that says what it
// is without colour or motion (a glyph on the screen, a bubble, a raised arm
// or a head down); the animated ones add frames on top.

import type { Sprite } from "../pixel/compile";
import * as S from "../pixel/sprites";
import { chairOf, deskTopOf, tableSegOf } from "../pixel/styleSprites";
import type { OfficeStyle, Pose } from "../world";

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
  /** The office's style: its desk top, chair and table. */
  style?: OfficeStyle;
  /** An empty desk with no runner slot behind it: no monitor, no screen. */
  spare?: boolean;
}

/** The chair, its bottom where the default chair's is whatever its height. */
function chair(style: OfficeStyle): Placed {
  const sprite = chairOf(style);
  return at("chair", sprite, CHAIR_AT.x, CHAIR_AT.y + S.CHAIR.h - sprite.h);
}

/** A desk with its agent (agent kind as agentColorKey gives it), or a free
 * desk. An agent waiting to be let in stands at the door instead (doorScene).
 * The style changes the desk top and the chair, nothing the agent does. */
export function deskScene(pose: Pose | "empty", kind: string, options: DeskOptions = {}): Placed[] {
  const style = options.style ?? "default";
  const spare = pose === "empty" && Boolean(options.spare);
  const parts: Placed[] = spare
    ? [at("desk", deskTopOf(style), 0, S.DESK_TOP_Y)]
    : [at("monitor", S.MONITOR, 0, 0), at("desk", deskTopOf(style), 0, S.DESK_TOP_Y), ...screen(pose)];
  if (pose !== "empty") parts.push(...agent(pose, kind));
  parts.push(chair(style));
  if (pose !== "empty") parts.push(...extras(pose, Boolean(options.mail)));
  return parts;
}

export interface MeetingOptions extends DeskOptions {
  /** Where the seat is along the table: its left end, the middle, the right
   * end (or "s", a table of one seat). The ends get a leg. */
  edge?: "l" | "m" | "r" | "s";
}

function tableParts(style: OfficeStyle, edge: MeetingOptions["edge"]): Placed[] {
  const parts = [at("table", tableSegOf(style), 0, S.DESK_TOP_Y)];
  if (edge === "l" || edge === "s") parts.push(at("table.cap.l", S.TABLE_CAP_L, 0, S.DESK_TOP_Y));
  if (edge === "r" || edge === "s") {
    parts.push(at("table.cap.r", S.TABLE_CAP_R, S.CELL - S.TABLE_CAP_R.w, S.DESK_TOP_Y));
  }
  return parts;
}

/** A seat at the meeting-room table: the seats side by side make one table.
 * The agent and its monitor (the screen it presents) are as at a desk; an
 * empty seat is the table and a chair. */
export function meetingScene(pose: Pose | "empty", kind: string, options: MeetingOptions = {}): Placed[] {
  const style = options.style ?? "default";
  const parts: Placed[] = [];
  if (pose !== "empty") parts.push(at("monitor", S.MONITOR, 0, 0));
  parts.push(...tableParts(style, options.edge));
  if (pose === "empty") return [...parts, chair(style)];
  parts.push(...screen(pose), ...agent(pose, kind), chair(style), ...extras(pose, Boolean(options.mail)));
  return parts;
}

export type ItemKind = "merge" | "review" | "held";

/** Where an item lying on the table ends, in the desk frame. */
const ITEM_BOTTOM = 22;

/** Something on the meeting-room table that waits on the viewer, no agent at
 * it: a PR folder (ready to merge), a clipboard (a review), an envelope (a
 * held message). Its screen says the same with a glyph. */
export function itemScene(
  kind: ItemKind,
  style: OfficeStyle = "default",
  edge: MeetingOptions["edge"] = "m",
): Placed[] {
  const item = kind === "merge" ? S.PR_FOLDER : kind === "review" ? S.CLIPBOARD : S.ENVELOPE;
  const glyph =
    kind === "merge"
      ? [onScreen("screen.ok", S.SCREEN_OK, 0, 0), onScreen("screen.ok.glyph", S.CHECK, 3, 1)]
      : kind === "review"
        ? [onScreen("screen.review", S.SCREEN_ON, 0, 0), onScreen("screen.review.glyph", S.QUESTION, 5, 1)]
        : [onScreen("screen.mail", S.SCREEN_ON, 0, 0), onScreen("screen.mail.glyph", S.ENVELOPE, 4, 2)];
  return [
    at("monitor", S.MONITOR, 0, 0),
    ...tableParts(style, edge),
    ...glyph,
    at(`item.${kind}`, item, Math.floor((S.CELL - item.w) / 2), ITEM_BOTTOM - item.h),
  ];
}

/** A lounge seat's frame: 24 art wide, 32 tall, the sofa's cushion at y 22. */
export const SEAT_W = 24;

/** An agent on a sofa seat, facing the room: sitting, asleep on a usage limit
 * (head down over folded arms, zzz) or offline (greyed by the stylesheet).
 * The sofa itself is drawn once under each pair of seats (Lounge). */
export function sofaScene(pose: Pose, kind: string): Placed[] {
  const asleep = pose === "limit";
  const headAt = asleep ? { x: 5, y: 9 } : { x: 7, y: 6 };
  const head: Placed[] = [
    at(asleep ? "head.sleep" : "head.front", asleep ? S.HEAD_FRONT_SLEEP : S.HEAD_FRONT, headAt.x, headAt.y),
  ];
  const wear = S.headwearOf(kind, "front");
  if (wear) head.push(at(`headwear.${kind}`, wear.sprite, headAt.x + wear.dx, headAt.y + wear.dy));
  const parts: Placed[] = [at("legs.seated", S.LEGS_SEATED, 6, 24), at("body.seated", S.BODY_SEATED, 4, 16)];
  if (!asleep) {
    parts.push(...head, at("arm.left", S.ARM, 2, 18), at("arm.right", S.ARM, 19, 18));
    return parts;
  }
  parts.push(at("arms.folded", S.ARMS_FOLDED, 4, 18), ...head);
  // zzz as at a desk: all three letters while still, one at a time moving.
  const z2 = at("bubble.z2", S.Z, 16, 2);
  parts.push(
    at("bubble.sleep", S.BUBBLE_SLEEP, 10, 0),
    at("bubble.z1", S.Z, 12, 3),
    framed(z2, 0, 3),
    framed(at("bubble.z3", S.Z, 20, 1), 0, 3),
    framed({ ...z2, name: "bubble.z2.again" }, 2, 3),
  );
  return parts;
}

/** A desk in another project's office, small (16×16): a busy screen, or a
 * hand up, zz or … over the agent. Its colour comes from the agent--<kind>
 * class around it. */
export function miniDesk(pose: Pose): Placed[] {
  const busy = pose === "working" || pose === "reviewing";
  const screenAt = S.MINI_SCREEN_AT;
  const parts: Placed[] = [
    at("mini.desk", S.MINI_DESK, 0, 0),
    at(busy ? "mini.screen.on" : "mini.screen.off", busy ? S.MINI_SCREEN_ON : S.MINI_SCREEN_OFF, screenAt.x, screenAt.y),
  ];
  if (busy) parts.push(at("mini.code", S.MINI_CODE, screenAt.x, screenAt.y + 1));
  parts.push(at("mini.agent", S.MINI_AGENT, 5, 9));
  if (pose === "asking-user") parts.push(at("mini.hand", S.MINI_HAND, 11, 5));
  else if (pose === "limit") parts.push(at("mini.z", S.MINI_Z, 12, 6));
  else if (pose === "awaiting-accept") parts.push(at("mini.wait", S.MINI_WAIT, 9, 6));
  return parts;
}

/** Slot lights a rack shows at most. */
export const RACK_LIGHTS = 4;

/** A runner as a rack: a power light while it is online, a light per slot
 * (at most four), lit for each run it holds. Offline, every light is off. */
export function rackScene(online: boolean | null, busy: number, slots: number | undefined): Placed[] {
  const on = online !== false;
  const parts: Placed[] = [
    at("rack", S.RACK, 0, 0),
    at(on ? "led.power" : "led.power.off", on ? S.LED : S.LED_OFF, S.RACK_POWER_AT.x, S.RACK_POWER_AT.y),
  ];
  const lights = Math.min(Math.max(slots ?? 1, 1), RACK_LIGHTS);
  for (let unit = 0; unit < lights; unit++) {
    const lit = on && unit < busy;
    const spot = S.RACK_SLOT_AT(unit);
    parts.push(at(lit ? `led.slot.${unit}.busy` : `led.slot.${unit}`, lit ? S.LED_BUSY : S.LED_OFF, spot.x, spot.y));
  }
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
