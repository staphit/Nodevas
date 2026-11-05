// Which sprites draw a desk in each pose, and where. Pure: the drawing
// (PixelSprite) only paints what this returns, so the state → picture mapping
// is testable on its own. Every pose has a still picture that says what it
// is without colour or motion (a glyph on the screen, a bubble, a raised arm
// or a head down); the animated ones add frames on top. At work, what the
// agent is doing (its activity) replaces the pose's screen and arms; out of
// the office on a usage limit, the agent is drawn on its outing instead
// (outdoorScene).

import type { Activity } from "../activity";
import { OUTING_STEPS, type Outing, type OutingKind } from "../outing";
import * as A from "../pixel/activitySprites";
import type { Sprite } from "../pixel/compile";
import * as O from "../pixel/outdoorSprites";
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

/** How long one loop of an activity's picture takes, in ms: each moves at
 * its own pace (typing fast, a page scrolling slowly). */
export const ACTIVITY_LOOP_MS: Record<Activity, number> = {
  read: 1600,
  edit: 500,
  shell: 1000,
  search: 1200,
  web: 1500,
  think: 1500,
};

/** The activity a desk shows: only at work (working or reviewing); any
 * other pose keeps its own picture. */
export function shownActivity(pose: Pose | "empty", activity: Activity | undefined): Activity | undefined {
  return pose === "working" || pose === "reviewing" ? activity : undefined;
}

/** A desk's loop: its activity's when it shows one, its pose's otherwise. */
export function deskLoopMs(pose: Pose, activity: Activity | undefined): number | undefined {
  const shown = shownActivity(pose, activity);
  return shown ? ACTIVITY_LOOP_MS[shown] : POSE_LOOP_MS[pose];
}

const HEAD_AT = { x: 11, y: 10 };
const BODY_AT = { x: 8, y: 19 };
const CHAIR_AT = { x: 9, y: 23 };
const LEFT_ARM_X = 6;
const RIGHT_ARM_X = 23;

const at = (name: string, sprite: Sprite, x: number, y: number): Placed => ({ name, sprite, x, y });
const framed = (placed: Placed, index: number, of: number): Placed => ({ ...placed, frame: { index, of } });
const onScreen = (name: string, sprite: Sprite, x: number, y: number) => at(name, sprite, S.SCREEN.x + x, S.SCREEN.y + y);

/** Code, and the line being typed. */
const typingScreen = (): Placed[] => [
  onScreen("screen.code", S.SCREEN_ON, 0, 0),
  onScreen("screen.code.lines", S.CODE, 0, 0),
  framed(onScreen("screen.typing.a", S.TYPING_A, 0, 4), 0, 2),
  framed(onScreen("screen.typing.b", S.TYPING_B, 0, 4), 1, 2),
];

/** The screen's picture per activity: each a shape of its own. */
function activityScreen(activity: Activity): Placed[] {
  switch (activity) {
    case "read":
      // A page, its lines a row up, the scroll bar's thumb further down.
      return [
        onScreen("screen.read", S.SCREEN_ON, 0, 0),
        onScreen("screen.read.page", A.PAGE, 0, 0),
        framed(onScreen("screen.read.lines.a", A.PAGE_LINES_A, 0, 0), 0, 2),
        framed(onScreen("screen.read.thumb.a", A.SCROLL_THUMB, 14, 1), 0, 2),
        framed(onScreen("screen.read.lines.b", A.PAGE_LINES_B, 0, 0), 1, 2),
        framed(onScreen("screen.read.thumb.b", A.SCROLL_THUMB, 14, 5), 1, 2),
      ];
    case "edit":
      return typingScreen();
    case "shell":
      // Output, then ">" and the cursor blinking after it.
      return [
        onScreen("screen.shell", S.SCREEN_ON, 0, 0),
        onScreen("screen.shell.lines", A.TERMINAL_LINES, 0, 0),
        onScreen("screen.shell.prompt", A.PROMPT, 1, 5),
        framed(onScreen("screen.shell.cursor", A.CURSOR, 4, 7), 0, 2),
      ];
    case "search":
      // A search box over results, the magnifier going along them.
      return [
        onScreen("screen.search", S.SCREEN_ON, 0, 0),
        onScreen("screen.search.box", A.SEARCH_BOX, 0, 0),
        onScreen("screen.search.results", A.SEARCH_RESULTS, 0, 4),
        framed(onScreen("screen.search.lens.a", S.MAGNIFIER, 2, 4), 0, 2),
        framed(onScreen("screen.search.lens.b", S.MAGNIFIER, 9, 4), 1, 2),
      ];
    case "web": {
      // A globe turning (its land in three places), an address bar over it.
      const land = [A.GLOBE_LAND_A, A.GLOBE_LAND_B, A.GLOBE_LAND_C];
      return [
        onScreen("screen.web", S.SCREEN_ON, 0, 0),
        onScreen("screen.web.globe", A.GLOBE, 4, 1),
        ...land.map((sprite, i) => framed(onScreen(`screen.web.land.${"abc"[i]}`, sprite, 4, 1), i, 3)),
        onScreen("screen.web.bar", A.ADDRESS_BAR, 0, 0),
      ];
    }
    case "think":
      // Nothing on the screen: the thought is in the bubble over the head.
      return [onScreen("screen.think", S.SCREEN_ON, 0, 0)];
  }
}

/** The screen's picture per pose (or, at work, per activity). */
function screen(pose: Pose | "empty", activity?: Activity): Placed[] {
  const shown = shownActivity(pose, activity);
  if (shown) return activityScreen(shown);
  switch (pose) {
    case "working":
      return typingScreen();
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
/** Typing: the hands take turns on the keyboard. */
const typingArms = () => [
  framed(at("arm.left.up", S.ARM_UP, LEFT_ARM_X, 15), 0, 2),
  framed(at("arm.right", S.ARM, RIGHT_ARM_X, 16), 0, 2),
  framed(at("arm.left", S.ARM, LEFT_ARM_X, 16), 1, 2),
  framed(at("arm.right.up", S.ARM_UP, RIGHT_ARM_X, 15), 1, 2),
];

/** The arms for an activity: typing (edit, shell), a hand on the chin
 * (read, think), a hand on the mouse (search, web). */
function activityArms(activity: Activity): Placed[] {
  switch (activity) {
    case "edit":
    case "shell":
      return typingArms();
    case "read":
    case "think":
      return [at("arm.left", S.ARM, LEFT_ARM_X, 16), at("arm.right.chin", A.ARM_CHIN, 19, 14)];
    default:
      return [at("arm.left", S.ARM, LEFT_ARM_X, 16), at("desk.mouse", A.MOUSE, 29, 16), at("arm.right", S.ARM, 26, 16)];
  }
}

/** The seated agent, back to the room, in a pose (or, at work, at its
 * activity). */
function agent(pose: Pose, kind: string, activity?: Activity): Placed[] {
  const shown = shownActivity(pose, activity);
  if (shown) return [body(), ...head(kind, HEAD_AT.x, HEAD_AT.y), ...activityArms(shown)];
  switch (pose) {
    case "working":
      return [body(), ...head(kind, HEAD_AT.x, HEAD_AT.y), ...typingArms()];
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

/** Over the desk: a bubble for a question, sleep or a thought, a held
 * message. */
function extras(pose: Pose, mail: boolean, activity?: Activity): Placed[] {
  const parts: Placed[] = [];
  if (mail) parts.push(at("mail", S.ENVELOPE, 0, 10));
  if (shownActivity(pose, activity) === "think") {
    // "...", then ".", ".." and round again: still, all three dots (as zzz).
    const dot2 = at("bubble.think.dot2", A.THOUGHT_DOT, 24, 2);
    parts.push(
      at("bubble.think", A.THOUGHT, 19, 0),
      at("bubble.think.dot1", A.THOUGHT_DOT, 21, 2),
      framed(dot2, 0, 3),
      framed(at("bubble.think.dot3", A.THOUGHT_DOT, 27, 2), 0, 3),
      framed({ ...dot2, name: "bubble.think.dot2.again" }, 2, 3),
    );
  } else if (pose === "asking-user") {
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
  /** What the agent is doing, from its last tool: drawn while it works or
   * reviews (shownActivity), the pose's own picture otherwise. */
  activity?: Activity;
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
    : [at("monitor", S.MONITOR, 0, 0), at("desk", deskTopOf(style), 0, S.DESK_TOP_Y), ...screen(pose, options.activity)];
  if (pose !== "empty") parts.push(...agent(pose, kind, options.activity));
  parts.push(chair(style));
  if (pose !== "empty") parts.push(...extras(pose, Boolean(options.mail), options.activity));
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
  const { activity } = options;
  parts.push(
    ...screen(pose, activity),
    ...agent(pose, kind, activity),
    chair(style),
    ...extras(pose, Boolean(options.mail), activity),
  );
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

// ---------- the outdoors ----------

/** How long one loop of an outing's picture takes, in ms. */
export const OUTDOOR_LOOP_MS: Record<OutingKind, number> = {
  hike: 800,
  stream: 1200,
  fish: 1600,
  camp: 900,
  bbq: 1500,
  cycle: 400,
};

/** The agent standing, from the front, the body's left edge at x and its
 * feet on row `feet` (legs drawn by the caller): head and headwear over the
 * body, as at the door. */
function standing(kind: string, x: number, feet: number, body: Sprite): Placed[] {
  const headY = feet - 22;
  const parts = [at("body.standing", body, x, feet - 13), at("head.front", S.HEAD_FRONT, x + 1, headY)];
  const wear = S.headwearOf(kind, "front");
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, x + 1 + wear.dx, headY + wear.dy));
  return parts;
}

/** The agent sitting, from the front, as on a sofa (sofaScene), its head
 * top-left corner at (x, y). */
function sitting(kind: string, x: number, y: number): Placed[] {
  const parts = [
    at("legs.seated", S.LEGS_SEATED, x - 1, y + 17),
    at("body.seated", S.BODY_SEATED, x - 3, y + 9),
    at("head.front", S.HEAD_FRONT, x, y),
  ];
  const wear = S.headwearOf(kind, "front");
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, x + wear.dx, y + wear.dy));
  parts.push(at("arm.left", S.ARM, x - 5, y + 11), at("arm.right", S.ARM, x + 12, y + 11));
  return parts;
}

/** Up the trail a step at a time, legs striding, a backpack on; the flag
 * at the top, and a spark once it is reached. */
function hike(kind: string, step: number): Placed[] {
  const { x, feet } = O.HIKE_PATH[step];
  const top = step === OUTING_STEPS - 1;
  const flag = O.FLAG_POLE_AT;
  const parts: Placed[] = [
    at("hike.backdrop", O.HIKE_BACKDROP, 0, 0),
    at("hike.flag.pole", O.FLAG_POLE, flag.x, flag.y),
    framed(at("hike.flag.a", O.FLAG_A, flag.x + 1, flag.y + 1), 0, 2),
    framed(at("hike.flag.b", O.FLAG_B, flag.x + 1, flag.y + 1), 1, 2),
    at("hike.pack", O.BACKPACK, x - 1, feet - 15),
  ];
  if (top) parts.push(at("legs", S.LEGS, x, feet - 4));
  else {
    parts.push(
      framed(at("legs.walk.a", O.LEGS_WALK_A, x, feet - 4), 0, 2),
      framed(at("legs.walk.b", O.LEGS_WALK_B, x, feet - 4), 1, 2),
    );
  }
  parts.push(...standing(kind, x, feet, O.BODY_HIKER));
  if (top) parts.push(at("hike.summit", O.SPARK, flag.x - 2, flag.y - 8));
  return parts;
}

/** Wading up the stream towards its falls, waist deep, a pole in hand; the
 * water ripples past, downstream. */
function stream(kind: string, step: number): Placed[] {
  const x = 1 + 5 * step;
  const feet = 33;
  const ripples = [O.STREAM_RIPPLE_A, O.STREAM_RIPPLE_B, O.STREAM_RIPPLE_C];
  return [
    at("stream.backdrop", O.STREAM_BACKDROP, 0, 0),
    ...ripples.map((sprite, i) => framed(at(`stream.ripple.${"abc"[i]}`, sprite, 0, O.STREAM_RIPPLE_Y), i, 3)),
    ...standing(kind, x, feet, S.BODY_STANDING),
    at("stream.pole", O.POLE, x + 12, feet - 15),
    at("stream.wade", O.WADE, x - 1, feet - 6),
  ];
}

/** Fish caught by a step: none at first, then one, then two. */
const CATCH_AT = [3, 6];

/** Sitting on a rock with a rod, the bobber bobbing; late on a fish comes
 * to bite, and the bucket fills. */
function fish(kind: string, step: number): Placed[] {
  const head = { x: 6, y: 16 };
  const parts: Placed[] = [at("fish.backdrop", O.FISH_BACKDROP, 0, 0), at("fish.bucket", O.BUCKET, 19, 37)];
  CATCH_AT.forEach((from, i) => {
    if (step >= from) parts.push(at(`fish.catch.${i + 1}`, O.TAIL, 20 + 3 * i, 34));
  });
  if (step >= 4) parts.push(at("fish.bite", O.FISH, 31, 28));
  parts.push(
    ...sitting(kind, head.x, head.y),
    at("fish.rod", O.ROD, head.x + 13, head.y - 8),
    at("fish.line", O.LINE, 41, 10),
    framed(at("fish.bobber.a", O.BOBBER, 40, 21), 0, 2),
    framed(at("fish.ring.a", O.RING_A, 37, 26), 0, 2),
    framed(at("fish.bobber.b", O.BOBBER, 40, 22), 1, 2),
    framed(at("fish.ring.b", O.RING_B, 37, 26), 1, 2),
  );
  return parts;
}

/** On a log by a tent and a fire; the sun (the moon, in the dark theme)
 * crosses the sky with the wait. */
function camp(kind: string, step: number): Placed[] {
  const sun = O.SUN_PATH[step];
  const flames = [O.FLAME_A, O.FLAME_B, O.FLAME_C];
  return [
    at("camp.backdrop", O.CAMP_BACKDROP, 0, 0),
    at("camp.sun", O.SUN, sun.x, sun.y),
    at("camp.tent", O.TENT, 0, 18),
    at("camp.log", O.LOG, 24, 35),
    ...sitting(kind, 28, 19),
    at("camp.fire.logs", O.FIRE_LOGS, 43, 36),
    ...flames.map((sprite, i) => framed(at(`camp.fire.${"abc"[i]}`, sprite, 44, 28), i, 3)),
  ];
}

/** How done the barbecue's skewers are by a step: raw at first, seared,
 * then done. */
const SEARED_FROM = 3;
const DONE_FROM = 5;
/** Done skewers on the plate by a step: none until the grill's are done,
 * then one, two, three. */
const PLATED_AT = [DONE_FROM, 6, 7];

/** At the grill, an apron on, turning two skewers with the tongs, smoke
 * rising; the skewers cook through with the wait (raw, seared, done, each
 * marked by its grill lines) and the plate on the table fills. */
function bbq(kind: string, step: number): Placed[] {
  const feet = 41;
  const x = 17;
  const doneness = step >= DONE_FROM ? "done" : step >= SEARED_FROM ? "seared" : "raw";
  const skewer = { raw: O.SKEWER_RAW, seared: O.SKEWER_SEARED, done: O.SKEWER_DONE }[doneness];
  const smoke = [O.SMOKE_A, O.SMOKE_B, O.SMOKE_C];
  const parts: Placed[] = [
    at("bbq.backdrop", O.BBQ_BACKDROP, 0, 0),
    at("bbq.table", O.CAMP_TABLE, 1, 32),
    at("bbq.cooler", O.COOLER, 3, 36),
    at("bbq.plate", O.PLATE, 2, 29),
  ];
  PLATED_AT.forEach((from, i) => {
    if (step >= from) parts.push(at(`bbq.plate.skewer.${i + 1}`, O.PLATED, 3, 27 - 2 * i));
  });
  parts.push(
    at("legs", S.LEGS, x, feet - 4),
    ...standing(kind, x, feet, S.BODY_STANDING),
    at("bbq.apron", O.APRON, x + 3, feet - 11),
    at("bbq.grill", O.GRILL, 30, 30),
    at(`bbq.food.1.${doneness}`, skewer, 31, 25),
    at(`bbq.food.2.${doneness}`, skewer, 42, 25),
    framed(at("bbq.tongs.a", O.TONGS_A, x + 10, 28), 0, 2),
    framed(at("bbq.tongs.b", O.TONGS_B, x + 10, 25), 1, 2),
    ...smoke.map((sprite, i) => framed(at(`bbq.smoke.${"abc"[i]}`, sprite, 40, 8), i, 3)),
  );
  return parts;
}

/** The rider from the side, over a bike whose top-left corner is at
 * (x, y): torso leaning to the bars, the near arm on them, the head (and
 * headwear) in profile, facing the way it rides. The legs are the caller's,
 * as they pedal. */
function riding(kind: string, x: number, y: number): Placed[] {
  const head = { x: x + 10, y: y - 17 };
  const parts = [
    at("body.rider", O.BODY_RIDER, x + 3, y - 9),
    at("arm.reach", O.ARM_REACH, x + 10, y - 8),
    at("head.side", S.HEAD_SIDE, head.x, head.y),
  ];
  const wear = S.headwearOf(kind, "side");
  if (wear) parts.push(at(`headwear.${kind}`, wear.sprite, head.x + wear.dx, head.y + wear.dy));
  return parts;
}

/** Along the road on a bike, from the side, a step at a time, wheels and
 * legs pedalling, towards the chequered flag; a spark once it is reached. */
function cycle(kind: string, step: number): Placed[] {
  // The last step's bike (and front wheel) ends right at the pole, short of it.
  const x = 4 * step;
  const bikeY = 26;
  const wheelY = bikeY + O.BIKE.h - 1 - Math.floor(O.WHEEL_A.h / 2);
  const wheelX = (hub: number) => x + hub - Math.floor(O.WHEEL_A.w / 2);
  const pole = { x: 50, y: 20 };
  const parts: Placed[] = [
    at("cycle.backdrop", O.CYCLE_BACKDROP, 0, 0),
    at("cycle.flag.pole", O.FLAG_POLE, pole.x, pole.y),
    at("cycle.flag", O.CHEQUER, pole.x + 1, pole.y + 1),
    framed(at("cycle.wheel.back.a", O.WHEEL_A, wheelX(O.BIKE_HUBS.back), wheelY), 0, 2),
    framed(at("cycle.wheel.back.b", O.WHEEL_B, wheelX(O.BIKE_HUBS.back), wheelY), 1, 2),
    framed(at("cycle.wheel.front.a", O.WHEEL_A, wheelX(O.BIKE_HUBS.front), wheelY), 0, 2),
    framed(at("cycle.wheel.front.b", O.WHEEL_B, wheelX(O.BIKE_HUBS.front), wheelY), 1, 2),
    framed(at("cycle.legs.far.a", O.LEGS_RIDE_FAR_A, x + 3, bikeY - 3), 0, 2),
    framed(at("cycle.legs.far.b", O.LEGS_RIDE_FAR_B, x + 3, bikeY - 3), 1, 2),
    at("cycle.bike", O.BIKE, x, bikeY),
    framed(at("cycle.legs.a", O.LEGS_RIDE_A, x + 3, bikeY - 3), 0, 2),
    framed(at("cycle.legs.b", O.LEGS_RIDE_B, x + 3, bikeY - 3), 1, 2),
    ...riding(kind, x, bikeY),
  ];
  // The spark over the flag, clear of the rider's face.
  if (step === OUTING_STEPS - 1) parts.push(at("cycle.finish", O.SPARK, pole.x + 1, pole.y - 6));
  return parts;
}

/** An agent on its outing, a tile of OUTDOOR_W × OUTDOOR_H (agent kind as
 * agentColorKey gives it). How far along it is, is its step (0 …
 * OUTING_STEPS - 1, clamped), each step placed rather than moved; the
 * frames only move legs, water, a bobber, flames, smoke, tongs, wheels. */
export function outdoorScene(outing: Pick<Outing, "kind" | "step">, kind: string): Placed[] {
  const step = Math.min(OUTING_STEPS - 1, Math.max(0, Math.floor(outing.step) || 0));
  switch (outing.kind) {
    case "hike":
      return hike(kind, step);
    case "stream":
      return stream(kind, step);
    case "fish":
      return fish(kind, step);
    case "bbq":
      return bbq(kind, step);
    case "cycle":
      return cycle(kind, step);
    default:
      return camp(kind, step);
  }
}
