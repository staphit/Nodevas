// Which sprites draw a desk in each pose, and where. Pure: the drawing
// (PixelSprite) only paints what this returns, so the state → picture mapping
// is testable on its own. Every pose has a still picture that says what it
// is without colour or motion (a glyph on the screen, a bubble, a raised arm
// or a head down); the animated ones add frames on top. At work, what the
// agent is doing (its activity) replaces the pose's screen and arms; out of
// the office on a usage limit, the agent is drawn on its outing instead
// (outdoorScene). The agent itself comes from its character's skin
// (pixel/skins.ts): the scenes draw everything round it, and put the props
// and the bubbles at the anchors its figure gives.

import type { Activity } from "../activity";
import type { CharacterId } from "../character";
import { OUTING_STEPS, type Outing, type OutingKind } from "../outing";
import * as A from "../pixel/activitySprites";
import type { Sprite } from "../pixel/compile";
import * as O from "../pixel/outdoorSprites";
import { at, framed, type Placed, type Point } from "../pixel/placed";
import { skinOf, type DeskGesture, type Figure } from "../pixel/skins";
import * as S from "../pixel/sprites";
import { chairOf, deskTopOf, tableSegOf } from "../pixel/styleSprites";
import type { OfficeStyle, Pose } from "../world";

export type { Placed } from "../pixel/placed";

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

const CHAIR_AT = { x: 9, y: 23 };

const onScreen = (name: string, sprite: Sprite, x: number, y: number) => at(name, sprite, S.SCREEN.x + x, S.SCREEN.y + y);

/** A part kept inside a w × h frame, moved as little as it takes. */
function inside(part: Placed, w: number, h: number): Placed {
  const x = Math.min(Math.max(part.x, 0), w - part.sprite.w);
  const y = Math.min(Math.max(part.y, 0), h - part.sprite.h);
  return x === part.x && y === part.y ? part : { ...part, x, y };
}

/** A prop at a figure's anchor (its top-left corner), kept inside the frame. */
const prop = (name: string, sprite: Sprite, anchor: Point, w: number, h: number) =>
  inside(at(name, sprite, anchor.x, anchor.y), w, h);

/** A group drawn from (0, 0) (a bubble and what is written in it) moved so
 * its first part's corner is at `to`, all alike, the first kept inside the
 * frame. */
function group(parts: Placed[], to: Point, w: number, h: number): Placed[] {
  const first = inside({ ...parts[0], x: to.x, y: to.y }, w, h);
  return parts.map((part) => ({ ...part, x: part.x + first.x - parts[0].x, y: part.y + first.y - parts[0].y }));
}

/** The point (dx, dy) away from an anchor. */
const from = (anchor: Point, dx: number, dy: number): Point => ({ x: anchor.x + dx, y: anchor.y + dy });

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

/** What the arms do at a desk: at work, the activity's (typing for edit and
 * shell, a hand on the chin to read or think, on the mouse to search or
 * browse); the pose's otherwise. */
export function deskGesture(pose: Pose, activity?: Activity): DeskGesture {
  switch (shownActivity(pose, activity)) {
    case "edit":
    case "shell":
      return "type";
    case "read":
    case "think":
      return "chin";
    case "search":
    case "web":
      return "mouse";
  }
  switch (pose) {
    case "working":
      return "type";
    case "asking-user":
      return "ask";
    case "finished":
      return "cheer";
    case "failed":
      return "slump";
    case "limit":
      return "sleep";
    default:
      return "rest";
  }
}

/** The seated agent, back to the room, its figure for the gesture, and the
 * desk's mouse under the hand on it. */
function agent(figure: Figure<"mouse">, gesture: DeskGesture): Placed[] {
  if (gesture !== "mouse") return figure.parts;
  return [...figure.parts, prop("desk.mouse", A.MOUSE, figure.anchors.mouse, S.CELL, S.CELL)];
}

/** A sleep bubble with zzz written into it, from (0, 0): zzz, then z, zz
 * and round again; frame 0, what a still picture shows, has all three
 * letters. */
function zzz(): Placed[] {
  const z2 = at("bubble.z2", S.Z, 6, 2);
  return [
    at("bubble.sleep", S.BUBBLE_SLEEP, 0, 0),
    at("bubble.z1", S.Z, 2, 3),
    framed(z2, 0, 3),
    framed(at("bubble.z3", S.Z, 10, 1), 0, 3),
    framed({ ...z2, name: "bubble.z2.again" }, 2, 3),
  ];
}

/** Over the desk: a bubble for a question, sleep or a thought, over the
 * figure's head; a held message. */
function extras(pose: Pose, mail: boolean, head: Point, activity?: Activity): Placed[] {
  const parts: Placed[] = [];
  if (mail) parts.push(at("mail", S.ENVELOPE, 0, 10));
  if (shownActivity(pose, activity) === "think") {
    // "...", then ".", ".." and round again: still, all three dots (as zzz).
    const dot2 = at("bubble.think.dot2", A.THOUGHT_DOT, 5, 2);
    const thought = [
      at("bubble.think", A.THOUGHT, 0, 0),
      at("bubble.think.dot1", A.THOUGHT_DOT, 2, 2),
      framed(dot2, 0, 3),
      framed(at("bubble.think.dot3", A.THOUGHT_DOT, 8, 2), 0, 3),
      framed({ ...dot2, name: "bubble.think.dot2.again" }, 2, 3),
    ];
    parts.push(...group(thought, from(head, 8, -10), S.CELL, S.CELL));
  } else if (pose === "asking-user") {
    const ask = [framed(at("bubble.ask", S.BUBBLE_ASK, 0, 0), 0, 2), framed(at("bubble.ask.up", S.BUBBLE_ASK, 0, 1), 1, 2)];
    parts.push(...group(ask, from(head, 12, -10), S.CELL, S.CELL));
  } else if (pose === "limit") {
    parts.push(...group(zzz(), from(head, 7, -14), S.CELL, S.CELL));
  }
  return parts;
}

/** Who is drawn, for a scene that has nothing else to set. */
export interface FigureOptions {
  /** Who the agent is drawn as (game/character.ts); the person when absent. */
  character?: CharacterId;
}

export interface DeskOptions extends FigureOptions {
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

/** The agent at a desk or a table seat, the chair in front of it and what is
 * over it. */
function seated(pose: Pose, kind: string, style: OfficeStyle, options: DeskOptions): Placed[] {
  const gesture = deskGesture(pose, options.activity);
  const figure = skinOf(options.character).desk(gesture, kind);
  return [
    ...agent(figure, gesture),
    chair(style),
    ...extras(pose, Boolean(options.mail), figure.anchors.head, options.activity),
  ];
}

/** A desk with its agent (agent kind as agentColorKey gives it, drawn as its
 * character), or a free desk. An agent waiting to be let in stands at the
 * door instead (doorScene). The style changes the desk top and the chair,
 * nothing the agent does. */
export function deskScene(pose: Pose | "empty", kind: string, options: DeskOptions = {}): Placed[] {
  const style = options.style ?? "default";
  const spare = pose === "empty" && Boolean(options.spare);
  const parts: Placed[] = spare
    ? [at("desk", deskTopOf(style), 0, S.DESK_TOP_Y)]
    : [at("monitor", S.MONITOR, 0, 0), at("desk", deskTopOf(style), 0, S.DESK_TOP_Y), ...screen(pose, options.activity)];
  if (pose === "empty") return [...parts, chair(style)];
  return [...parts, ...seated(pose, kind, style, options)];
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
  return [...parts, ...screen(pose, options.activity), ...seated(pose, kind, style, options)];
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
export function sofaScene(pose: Pose, kind: string, options: FigureOptions = {}): Placed[] {
  const asleep = pose === "limit";
  const figure = skinOf(options.character).sofa(kind, asleep);
  if (!asleep) return figure.parts;
  // zzz as at a desk: all three letters while still, one at a time moving.
  return [...figure.parts, ...group(zzz(), from(figure.anchors.head, 5, -9), SEAT_W, S.CELL)];
}

/** A small desk's frame, square. */
const MINI = 16;

/** A desk in another project's office, small (16×16): a busy screen, or a
 * hand up, zz or … over the agent (its character's small figure, from
 * behind). Its colour comes from the agent--<kind> class around it. */
export function miniDesk(pose: Pose, options: FigureOptions = {}): Placed[] {
  const busy = pose === "working" || pose === "reviewing";
  const screenAt = S.MINI_SCREEN_AT;
  const parts: Placed[] = [
    at("mini.desk", S.MINI_DESK, 0, 0),
    at(busy ? "mini.screen.on" : "mini.screen.off", busy ? S.MINI_SCREEN_ON : S.MINI_SCREEN_OFF, screenAt.x, screenAt.y),
  ];
  if (busy) parts.push(at("mini.code", S.MINI_CODE, screenAt.x, screenAt.y + 1));
  // The agent, its bottom on row 14, centred; what is over it, from its
  // top-left corner.
  const small = skinOf(options.character).mini;
  const agentAt = { x: MINI / 2 - Math.floor(small.w / 2), y: 15 - small.h };
  parts.push(at("mini.agent", small, agentAt.x, agentAt.y));
  const over = (name: string, sprite: Sprite, dx: number, dy: number) =>
    prop(name, sprite, from(agentAt, dx, dy), MINI, MINI);
  if (pose === "asking-user") parts.push(over("mini.hand", S.MINI_HAND, 6, -4));
  else if (pose === "limit") parts.push(over("mini.z", S.MINI_Z, 7, -3));
  else if (pose === "awaiting-accept") parts.push(over("mini.wait", S.MINI_WAIT, 4, -3));
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
  const figure = skinOf(options.character).door(kind);
  const parts: Placed[] = [
    at("door", S.DOOR, 7, 2),
    ...figure.parts,
    prop("bubble.wait", S.BUBBLE_WAIT, from(figure.anchors.head, 10, -7), S.CELL, S.CELL),
  ];
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

const W = O.OUTDOOR_W;
const H = O.OUTDOOR_H;

/** Where the figure is on an outing at a step (step already clamped), as
 * the skin's state takes it (pixel/skins.ts): a standing or a seated box's
 * bottom-left corner, or, on the bike, the bike's top-left. */
export function outdoorPlace(kind: OutingKind, step: number): Point {
  switch (kind) {
    case "hike": {
      const { x, feet } = O.HIKE_PATH[step];
      return { x, y: feet };
    }
    case "stream":
      return { x: 1 + 5 * step, y: 33 };
    case "fish":
      return { x: 1, y: 35 };
    case "bbq":
      return { x: 17, y: 41 };
    case "cycle":
      // The last step's bike (and front wheel) ends right at the pole, short of it.
      return { x: 4 * step, y: 26 };
    default:
      return { x: 23, y: 38 };
  }
}

/** Up the trail a step at a time, legs striding, a backpack on; the flag
 * at the top, and a spark once it is reached. */
function hike(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const top = step === OUTING_STEPS - 1;
  const flag = O.FLAG_POLE_AT;
  const figure = skinOf(character).hike(kind, outdoorPlace("hike", step), !top);
  const parts: Placed[] = [
    at("hike.backdrop", O.HIKE_BACKDROP, 0, 0),
    at("hike.flag.pole", O.FLAG_POLE, flag.x, flag.y),
    framed(at("hike.flag.a", O.FLAG_A, flag.x + 1, flag.y + 1), 0, 2),
    framed(at("hike.flag.b", O.FLAG_B, flag.x + 1, flag.y + 1), 1, 2),
  ];
  if (figure.anchors.pack) parts.push(prop("hike.pack", O.BACKPACK, figure.anchors.pack, W, H));
  parts.push(...figure.parts);
  if (top) parts.push(at("hike.summit", O.SPARK, flag.x - 2, flag.y - 8));
  return parts;
}

/** Wading up the stream towards its falls, waist deep, a pole in hand; the
 * water ripples past, downstream. */
function stream(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const figure = skinOf(character).stream(kind, outdoorPlace("stream", step));
  const ripples = [O.STREAM_RIPPLE_A, O.STREAM_RIPPLE_B, O.STREAM_RIPPLE_C];
  return [
    at("stream.backdrop", O.STREAM_BACKDROP, 0, 0),
    ...ripples.map((sprite, i) => framed(at(`stream.ripple.${"abc"[i]}`, sprite, 0, O.STREAM_RIPPLE_Y), i, 3)),
    ...figure.parts,
    prop("stream.pole", O.POLE, figure.anchors.pole, W, H),
    prop("stream.wade", O.WADE, figure.anchors.wade, W, H),
  ];
}

/** Fish caught by a step: none at first, then one, then two. */
const CATCH_AT = [3, 6];
/** The row the bobber floats on (and dips a row from), and its rings'. */
const BOBBER_Y = 21;
const RING_Y = 26;

/** Sitting on a rock with a rod, the bobber bobbing; late on a fish comes
 * to bite, and the bucket fills. */
function fish(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const figure = skinOf(character).fish(kind, outdoorPlace("fish", step));
  const rod = prop("fish.rod", O.ROD, figure.anchors.rod, W, H);
  // The line hangs from the rod's tip down to the bobber on the water.
  const tip = { x: rod.x + O.ROD.w - 1, y: rod.y };
  const lineY = tip.y + 2;
  const line = lineY + O.LINE.h === BOBBER_Y + 1 ? O.LINE : S.solid(1, Math.max(1, BOBBER_Y + 1 - lineY), "ink");
  const parts: Placed[] = [at("fish.backdrop", O.FISH_BACKDROP, 0, 0), at("fish.bucket", O.BUCKET, 19, 37)];
  CATCH_AT.forEach((from, i) => {
    if (step >= from) parts.push(at(`fish.catch.${i + 1}`, O.TAIL, 20 + 3 * i, 34));
  });
  if (step >= 4) parts.push(at("fish.bite", O.FISH, 31, 28));
  parts.push(
    ...figure.parts,
    rod,
    at("fish.line", line, tip.x, lineY),
    framed(at("fish.bobber.a", O.BOBBER, tip.x - 1, BOBBER_Y), 0, 2),
    framed(at("fish.ring.a", O.RING_A, tip.x - 4, RING_Y), 0, 2),
    framed(at("fish.bobber.b", O.BOBBER, tip.x - 1, BOBBER_Y + 1), 1, 2),
    framed(at("fish.ring.b", O.RING_B, tip.x - 4, RING_Y), 1, 2),
  );
  return parts;
}

/** On a log by a tent and a fire; the sun (the moon, in the dark theme)
 * crosses the sky with the wait. */
function camp(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const sun = O.SUN_PATH[step];
  const flames = [O.FLAME_A, O.FLAME_B, O.FLAME_C];
  return [
    at("camp.backdrop", O.CAMP_BACKDROP, 0, 0),
    at("camp.sun", O.SUN, sun.x, sun.y),
    at("camp.tent", O.TENT, 0, 18),
    at("camp.log", O.LOG, 24, 35),
    ...skinOf(character).camp(kind, outdoorPlace("camp", step)).parts,
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
function bbq(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const figure = skinOf(character).bbq(kind, outdoorPlace("bbq", step));
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
  parts.push(...figure.parts);
  if (figure.anchors.apron) parts.push(prop("bbq.apron", O.APRON, figure.anchors.apron, W, H));
  const tongs = figure.anchors.tongs;
  parts.push(
    at("bbq.grill", O.GRILL, 30, 30),
    at(`bbq.food.1.${doneness}`, skewer, 31, 25),
    at(`bbq.food.2.${doneness}`, skewer, 42, 25),
    // The skin's forearm on the tongs, over the grill, under the tongs.
    ...(figure.front ?? []),
    framed(prop("bbq.tongs.a", O.TONGS_A, tongs, W, H), 0, 2),
    framed(prop("bbq.tongs.b", O.TONGS_B, from(tongs, 0, -3), W, H), 1, 2),
    ...smoke.map((sprite, i) => framed(at(`bbq.smoke.${"abc"[i]}`, sprite, 40, 8), i, 3)),
  );
  return parts;
}

/** Along the road on a bike, from the side, a step at a time, wheels and
 * legs pedalling, towards the chequered flag; a spark once it is reached.
 * What of the rider is behind the bike (the far leg) goes under it. */
function cycle(kind: string, character: CharacterId | undefined, step: number): Placed[] {
  const bike = outdoorPlace("cycle", step);
  const figure = skinOf(character).cycle(kind, bike);
  const wheelY = bike.y + O.BIKE.h - 1 - Math.floor(O.WHEEL_A.h / 2);
  const wheelX = (hub: number) => bike.x + hub - Math.floor(O.WHEEL_A.w / 2);
  const pole = { x: 50, y: 20 };
  const parts: Placed[] = [
    at("cycle.backdrop", O.CYCLE_BACKDROP, 0, 0),
    at("cycle.flag.pole", O.FLAG_POLE, pole.x, pole.y),
    at("cycle.flag", O.CHEQUER, pole.x + 1, pole.y + 1),
    framed(at("cycle.wheel.back.a", O.WHEEL_A, wheelX(O.BIKE_HUBS.back), wheelY), 0, 2),
    framed(at("cycle.wheel.back.b", O.WHEEL_B, wheelX(O.BIKE_HUBS.back), wheelY), 1, 2),
    framed(at("cycle.wheel.front.a", O.WHEEL_A, wheelX(O.BIKE_HUBS.front), wheelY), 0, 2),
    framed(at("cycle.wheel.front.b", O.WHEEL_B, wheelX(O.BIKE_HUBS.front), wheelY), 1, 2),
    ...(figure.behind ?? []),
    at("cycle.bike", O.BIKE, bike.x, bike.y),
    ...figure.parts,
  ];
  // The spark over the flag, clear of the rider's face.
  if (step === OUTING_STEPS - 1) parts.push(at("cycle.finish", O.SPARK, pole.x + 1, pole.y - 6));
  return parts;
}

/** An agent on its outing, a tile of OUTDOOR_W × OUTDOOR_H (agent kind as
 * agentColorKey gives it, drawn as its character). How far along it is, is
 * its step (0 … OUTING_STEPS - 1, clamped), each step placed rather than
 * moved; the frames only move legs, water, a bobber, flames, smoke, tongs,
 * wheels. */
export function outdoorScene(outing: Pick<Outing, "kind" | "step">, kind: string, options: FigureOptions = {}): Placed[] {
  const step = Math.min(OUTING_STEPS - 1, Math.max(0, Math.floor(outing.step) || 0));
  const { character } = options;
  switch (outing.kind) {
    case "hike":
      return hike(kind, character, step);
    case "stream":
      return stream(kind, character, step);
    case "fish":
      return fish(kind, character, step);
    case "bbq":
      return bbq(kind, character, step);
    case "cycle":
      return cycle(kind, character, step);
    default:
      return camp(kind, character, step);
  }
}
