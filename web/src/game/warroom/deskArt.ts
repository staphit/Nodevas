// Which sprites draw a desk in each pose, and where. Pure: the drawing
// (PixelSprite) only paints what this returns, so the state → picture mapping
// is testable on its own. Every pose has a still picture that says what it
// is without colour or motion (a glyph on the screen, a bubble, a raised arm
// or a head down); the animated ones add frames on top. At work, what the
// agent is doing (its activity) replaces the pose's screen and arms; out of
// the office on a usage limit, the agent is drawn on its outing instead
// (outdoorScene). The agent itself comes from its character's skin
// (pixel/skins.ts): the scenes draw everything round it, and put the props
// and the bubbles at the anchors its figure gives. For a viewer who draws
// characters as anime, a character whose sheet has the state's action is
// drawn from it instead (anime/figure.ts), its frame's anchor on the scene's
// point for the state (the points below), the props its render has left
// out; anything else stays the skin's. At a desk the render is the whole
// desk unit, seen from the front: no monitor, so a badge off the head says
// what the screen would (statusBadge).

import type { Activity } from "../activity";
import { animeAction } from "../anime/actions";
import { drawFigure, type Box } from "../anime/figure";
import type { AnimeSheet } from "../anime/manifest";
import type { CharacterId } from "../character";
import { OUTING_STEPS, type Outing, type OutingKind } from "../outing";
import * as A from "../pixel/activitySprites";
import type { Sprite } from "../pixel/compile";
import * as O from "../pixel/outdoorSprites";
import { at, framed, type Placed, type Point } from "../pixel/placed";
import { SIT_BOX, skinOf, STAND_BOX, type DeskGesture, type Figure } from "../pixel/skins";
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

// Where an anime figure's anchor goes (anime/manifest.ts), state by state.
// Art pixels here are edges, row r spanning r … r + 1: under shoes drawn on
// row 29 is y 30, where a render's soles go to stand where the person's do.
// Seated, a render's seat contact goes on the seat's top, a top row r at
// y r, its legs hanging below it in front of the seat; a sheet's from
// before, whose seated anchor is still its soles, goes under the shoes.

/** Standing (the door, the outings on foot): under the feet, the bottom
 * centre of STAND_BOX at `at`, its shoes on row at.y. */
const standingPoint = (at: Point): Point => ({ x: at.x + STAND_BOX.w / 2, y: at.y + 1 });

/** Seated from the front on a rock or a log, for a sheet's from before:
 * under the shoes, the bottom centre of SIT_BOX at `at`. */
const sittingPoint = (at: Point): Point => ({ x: at.x + SIT_BOX.w / 2, y: at.y + 1 });

/** Seated from the front on a rock or a log: the seat's top, its top row
 * `top`, under the middle of SIT_BOX at `at`, where the person sits. */
const seatPoint = (at: Point, top: number): Point => ({ x: at.x + SIT_BOX.w / 2, y: top });

/** At a desk, a desk action's render (its whole desk unit: the character
 * behind a desk of its own, from the front): its soles' line on the floor
 * under the desk top's legs (rows 15–28), centred, so the frame (192 × 192
 * at 6) fills the cell. */
const DESK_POINT: Point = { x: S.CELL / 2, y: S.DESK_TOP_Y + 14 };

/** At a meeting-table seat, the same render two rows up: its laptop stands
 * on the table top (row 15) and its own desk is behind the table, which is
 * drawn in front of it. Nothing of it shows under the top's front edge
 * (TABLE_FRONT), where its desk would. */
const TABLE_POINT: Point = { x: DESK_POINT.x, y: DESK_POINT.y - 2 };
const TABLE_FRONT = S.DESK_TOP_Y + 6;

/** Where the person stands in the doorway: STAND_BOX at (10, 29). */
const DOOR_AT: Point = { x: 10, y: 29 };

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

/** Where a held message's envelope lies over a desk or the doorway. */
const MAIL_AT: Point = { x: 0, y: 10 };

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

// ---------- an anime figure's status badge ----------

/** What a badge says, as screen() would: the screen's name for the state
 * (badge.<name>, its glyph badge.<name>.glyph), the screen's colour for its
 * face, and the glyph on the face, from its top-left. */
interface BadgeArt {
  name: string;
  face: S.BadgeFace;
  marks: Placed[];
}

/** A glyph in the middle of the face (left and up by an odd pixel). */
const centred = (name: string, sprite: Sprite): Placed =>
  at(name, sprite, Math.floor((S.BADGE_FACE.w - sprite.w) / 2), Math.max(0, Math.floor((S.BADGE_FACE.h - sprite.h) / 2)));

/** Code being typed: the small desks' code. */
const CODE_BADGE: BadgeArt = { name: "code", face: "n", marks: [centred("badge.code.glyph", S.MINI_CODE)] };

/** An activity's badge: its screen's picture, small (a page, the shell's
 * prompt, the search box, the globe), each a shape of its own as the
 * screens are; nothing on the screen's colour for a thought, which is in
 * its bubble. */
function activityBadge(activity: Activity): BadgeArt {
  switch (activity) {
    case "read":
      return { name: "read", face: "n", marks: [centred("badge.read.glyph", A.BADGE_PAGE)] };
    case "edit":
      return CODE_BADGE;
    case "shell":
      return { name: "shell", face: "n", marks: [at("badge.shell.glyph", A.PROMPT, 2, 2), at("badge.shell.cursor", A.CURSOR, 5, 4)] };
    case "search":
      return { name: "search", face: "n", marks: [centred("badge.search.glyph", A.BADGE_SEARCH)] };
    case "web":
      return { name: "web", face: "n", marks: [at("badge.web.glyph", A.GLOBE, 0, 0), at("badge.web.land", A.GLOBE_LAND_A, 0, 0)] };
    case "think":
      return { name: "think", face: "n", marks: [] };
  }
}

/** The badge for a pose (or, at work, an activity), as its screen. */
function badgeArt(pose: Pose, activity?: Activity): BadgeArt {
  const shown = shownActivity(pose, activity);
  if (shown) return activityBadge(shown);
  switch (pose) {
    case "working":
      return CODE_BADGE;
    case "reviewing":
      return { name: "review", face: "n", marks: [centred("badge.review.glyph", S.MAGNIFIER)] };
    case "asking-user":
      return { name: "ask", face: "n", marks: [centred("badge.ask.glyph", S.QUESTION)] };
    case "failed":
      return { name: "fail", face: "F", marks: [centred("badge.fail.glyph", S.CROSS)] };
    case "finished":
      return { name: "ok", face: "K", marks: [centred("badge.ok.glyph", S.CHECK)] };
    case "waiting-ci":
      return { name: "ci", face: "n", marks: [centred("badge.ci.glyph", S.HOURGLASS)] };
    case "limit":
      return { name: "limit", face: "x", marks: [centred("badge.limit.glyph", S.CLOCK)] };
    default:
      return { name: "off", face: "x", marks: [at("badge.off.glare", S.GLARE, 1, 1)] };
  }
}

/** What the screen says, for a figure drawn from its anime sheet, whose desk
 * unit has a laptop and no monitor: the screen's glyph on a small plate in
 * the screen's colour, hung off the head box's top-right (its middle row on
 * the head's top), kept in the cell; off its top-left instead where that
 * would cover what is over the desk already (`over`: a bubble, the mail, a
 * run's role tag, the held messages' count), which keeps its place; under
 * the envelope, on the floor by the desk, where both would. Still, whatever
 * the pose: the screen's frames are the monitor's. */
function statusBadge(pose: Pose, activity: Activity | undefined, head: Box, over: Placed[]): Placed[] {
  const { name, face, marks } = badgeArt(pose, activity);
  const plate = S.badgePlate(
    face,
    Math.max(S.BADGE_FACE.w, ...marks.map((mark) => mark.x + mark.sprite.w)),
    Math.max(S.BADGE_FACE.h, ...marks.map((mark) => mark.y + mark.sprite.h)),
  );
  const spot = (x: number, y: number) =>
    at(`badge.${name}`, plate, Math.min(Math.max(x, 0), S.CELL - plate.w), Math.min(Math.max(y, 0), S.CELL - plate.h));
  const top = head.y - Math.floor(plate.h / 2);
  const left = head.x - plate.w;
  const badge = clearest([spot(head.x + head.w, top), spot(left, top), spot(left, MAIL_AT.y + S.ENVELOPE.h)], over);
  return [badge, ...marks.map((mark) => ({ ...mark, x: badge.x + 1 + mark.x, y: badge.y + 1 + mark.y }))];
}

/** How many pixels two parts' boxes share. */
function shared(a: Placed, b: Placed): number {
  const w = Math.min(a.x + a.sprite.w, b.x + b.sprite.w) - Math.max(a.x, b.x);
  const h = Math.min(a.y + a.sprite.h, b.y + b.sprite.h) - Math.max(a.y, b.y);
  return w > 0 && h > 0 ? w * h : 0;
}

/** The first of the spots clear of every part, else the one that covers the
 * least of them. */
function clearest(spots: Placed[], parts: Placed[]): Placed {
  const covered = (spot: Placed) => parts.reduce((sum, part) => sum + shared(spot, part), 0);
  return spots.reduce((best, spot) => (covered(spot) < covered(best) ? spot : best));
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
  if (mail) parts.push(at("mail", S.ENVELOPE, MAIL_AT.x, MAIL_AT.y));
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
  /** The character's anime sheet, given while the viewer draws characters
   * as anime and it has loaded: a state it has an action for is drawn from
   * it, any other from the skin. */
  anime?: AnimeSheet;
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
  /** A run's role tag or test lamp hangs over the cell's top-right corner
   * (PixelDesk lays them over the drawing): an anime figure's badge keeps
   * clear of it. */
  tagged?: boolean;
  /** More than one message is held: PixelDesk lays their count over the
   * envelope's corner, which an anime figure's badge keeps clear of too. */
  mailCount?: boolean;
}

/** Where a run's role tag and test lamp hang over a desk (warroom.css
 * .px-badge.is-role and .is-test: right-aligned, from row 2), at their
 * widest, in art px. */
const TAG_CORNER: Placed = { name: "tag", sprite: { w: 11, h: 8, layers: [] }, x: S.CELL - 11, y: 2 };

/** Where the held messages' count hangs over the envelope's corner
 * (warroom.css .px-badge.is-mail: from (5, 5), 16 px tall, two digits at
 * its widest), in art px. */
const COUNT_TAG: Placed = { name: "count", sprite: { w: 8, h: 6, layers: [] }, x: 5, y: 5 };

/** The chair, its bottom where the default chair's is whatever its height. */
function chair(style: OfficeStyle): Placed {
  const sprite = chairOf(style);
  return at("chair", sprite, CHAIR_AT.x, CHAIR_AT.y + S.CHAIR.h - sprite.h);
}

/** The agent at a desk or a table seat (`furniture`: the desk top, or the
 * table and its legs), under the monitor and its screen, the chair in front
 * of it and what is over it. Drawn from its anime sheet, the render is the
 * desk unit: no monitor, screen, desk top, chair or mouse (its "desk"
 * prop); at the table, the table in front of it; and a badge for the
 * screen. */
function seated(pose: Pose, kind: string, style: OfficeStyle, furniture: Placed[], options: DeskOptions, table = false): Placed[] {
  const gesture = deskGesture(pose, options.activity);
  const { figure, without, head } = drawFigure(
    skinOf(options.character).desk(gesture, kind),
    options.anime,
    animeAction({ at: "desk", gesture }),
    { point: table ? TABLE_POINT : DESK_POINT, w: S.CELL, h: table ? TABLE_FRONT : S.CELL, still: pose === "offline" },
  );
  const monitor = at("monitor", S.MONITOR, 0, 0);
  const shown = screen(pose, options.activity);
  const over = extras(pose, Boolean(options.mail), figure.anchors.head, options.activity);
  if (!head) return [monitor, ...furniture, ...shown, ...agent(figure, gesture), chair(style), ...over];
  // What the badge keeps clear of: the bubbles and the mail, and what
  // PixelDesk lays over the drawing.
  const taken = [...over, ...(options.tagged ? [TAG_CORNER] : []), ...(options.mailCount ? [COUNT_TAG] : [])];
  return without([
    monitor,
    ...shown,
    ...agent(figure, gesture),
    ...furniture,
    chair(style),
    ...statusBadge(pose, options.activity, head, taken),
    ...over,
  ]);
}

/** A desk with its agent (agent kind as agentColorKey gives it, drawn as its
 * character), or a free desk. An agent waiting to be let in stands at the
 * door instead (doorScene). The style changes the desk top and the chair,
 * nothing the agent does. */
export function deskScene(pose: Pose | "empty", kind: string, options: DeskOptions = {}): Placed[] {
  const style = options.style ?? "default";
  const desk = at("desk", deskTopOf(style), 0, S.DESK_TOP_Y);
  if (pose !== "empty") return seated(pose, kind, style, [desk], options);
  if (options.spare) return [desk, chair(style)];
  return [at("monitor", S.MONITOR, 0, 0), desk, ...screen(pose, options.activity), chair(style)];
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
 * empty seat is the table and a chair. An anime figure sits behind the
 * table, which stays whole along the seats. */
export function meetingScene(pose: Pose | "empty", kind: string, options: MeetingOptions = {}): Placed[] {
  const style = options.style ?? "default";
  const table = tableParts(style, options.edge);
  if (pose === "empty") return [...table, chair(style)];
  return seated(pose, kind, style, table, options, true);
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

/** On a sofa seat, for a sheet's from before: under the shoes (the
 * person's on row 26), centred on the seat. */
const SOFA_POINT: Point = { x: SEAT_W / 2, y: 27 };

/** On a sofa seat: the cushion's top, centred on the seat. The lounge lays
 * the sofa 11 rows down a seat's frame (warroom.css: the sofa from row 12
 * of its pair of seats, a seat's frame from row 1, under its button's
 * padding), so the cushion's lit top, the sofa's row 11 under the crease
 * (rows 9 and 10) where the back meets it, is the frame's row 22; its
 * front edge is row 25. */
const SOFA_SEAT: Point = { x: SEAT_W / 2, y: 22 };

/** An agent on a sofa seat, facing the room: sitting, asleep on a usage limit
 * (head down over folded arms, zzz) or offline (greyed by the stylesheet).
 * The sofa itself is drawn once under each pair of seats (Lounge). */
export function sofaScene(pose: Pose, kind: string, options: FigureOptions = {}): Placed[] {
  const asleep = pose === "limit";
  const { figure, without } = drawFigure(
    skinOf(options.character).sofa(kind, asleep),
    options.anime,
    animeAction({ at: "sofa", asleep }),
    { point: SOFA_POINT, seat: SOFA_SEAT, w: SEAT_W, h: S.CELL, still: pose === "offline" },
  );
  if (!asleep) return without(figure.parts);
  // zzz as at a desk: all three letters while still, one at a time moving.
  return without([...figure.parts, ...group(zzz(), from(figure.anchors.head, 5, -9), SEAT_W, S.CELL)]);
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
  const { figure, without } = drawFigure(skinOf(options.character).door(kind), options.anime, animeAction({ at: "door" }), {
    point: standingPoint(DOOR_AT),
    w: S.CELL,
    h: S.CELL,
  });
  const parts: Placed[] = [
    at("door", S.DOOR, 7, 2),
    ...figure.parts,
    prop("bubble.wait", S.BUBBLE_WAIT, from(figure.anchors.head, 10, -7), S.CELL, S.CELL),
  ];
  if (options.mail) parts.push(at("mail", S.ENVELOPE, MAIL_AT.x, MAIL_AT.y));
  return without(parts);
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
function hike(kind: string, options: FigureOptions, step: number): Placed[] {
  const top = step === OUTING_STEPS - 1;
  const flag = O.FLAG_POLE_AT;
  const place = outdoorPlace("hike", step);
  const { figure, without } = drawFigure(
    skinOf(options.character).hike(kind, place, !top),
    options.anime,
    animeAction({ at: "outing", kind: "hike", walking: !top }),
    { point: standingPoint(place), w: W, h: H },
  );
  const parts: Placed[] = [
    at("hike.backdrop", O.HIKE_BACKDROP, 0, 0),
    at("hike.flag.pole", O.FLAG_POLE, flag.x, flag.y),
    framed(at("hike.flag.a", O.FLAG_A, flag.x + 1, flag.y + 1), 0, 2),
    framed(at("hike.flag.b", O.FLAG_B, flag.x + 1, flag.y + 1), 1, 2),
  ];
  if (figure.anchors.pack) parts.push(prop("hike.pack", O.BACKPACK, figure.anchors.pack, W, H));
  parts.push(...figure.parts);
  if (top) parts.push(at("hike.summit", O.SPARK, flag.x - 2, flag.y - 8));
  return without(parts);
}

/** Wading up the stream towards its falls, waist deep, a pole in hand; the
 * water ripples past, downstream. */
function stream(kind: string, options: FigureOptions, step: number): Placed[] {
  const place = outdoorPlace("stream", step);
  const pixel = skinOf(options.character).stream(kind, place);
  // An anime wader is cut where a skin's figure ends (pixel/skins.ts: nothing
  // from the water's splash row, wade.y + 2, down): under the water round its
  // waist, in the stream, nothing of its legs shows.
  const { figure, without } = drawFigure(pixel, options.anime, animeAction({ at: "outing", kind: "stream" }), {
    point: standingPoint(place),
    w: W,
    h: pixel.anchors.wade.y + O.WADE.h - 1,
  });
  const ripples = [O.STREAM_RIPPLE_A, O.STREAM_RIPPLE_B, O.STREAM_RIPPLE_C];
  return without([
    at("stream.backdrop", O.STREAM_BACKDROP, 0, 0),
    ...ripples.map((sprite, i) => framed(at(`stream.ripple.${"abc"[i]}`, sprite, 0, O.STREAM_RIPPLE_Y), i, 3)),
    ...figure.parts,
    prop("stream.pole", O.POLE, figure.anchors.pole, W, H),
    prop("stream.wade", O.WADE, figure.anchors.wade, W, H),
  ]);
}

/** Fish caught by a step: none at first, then one, then two. */
const CATCH_AT = [3, 6];
/** The row the bobber floats on (and dips a row from), and its rings'. */
const BOBBER_Y = 21;
const RING_Y = 26;
/** The water's first column on the rings' row (FISH_BACKDROP). */
const WATER_X = 21;
/** The least line a rod's tip hangs, in rows, down to the bobber. */
const LINE_MIN = 2;
/** The rock's top row (FISH_BACKDROP: its outline, x 4–15, over the rock's
 * rows 34–37), the seat's top for the angler in SIT_BOX at (1, 35): at.y - 2,
 * where skins.ts has the seat begin. */
const ROCK_TOP = 33;

/** Where the line hangs from: a rod's tip, rod + (ROD.w - 1, 0) from its
 * top-left. */
const rodTip = (rod: Point): Point => ({ x: rod.x + O.ROD.w - 1, y: rod.y });

/** Whether a line from a rod's tip hangs down to the bobber, the bobber and
 * its rings all on the water: the tip in the tile, out over the water and
 * up off it (anime/manifest.ts says so to the art pipeline). */
function rodTipHangs(tip: Point): boolean {
  const line = BOBBER_Y + 1 - (tip.y + 2);
  return tip.y >= 0 && line >= LINE_MIN && tip.x - 4 >= WATER_X && tip.x - 4 + O.RING_A.w <= W;
}

/** Sitting on a rock with a rod, the bobber bobbing; late on a fish comes
 * to bite, and the bucket fills. */
function fish(kind: string, options: FigureOptions, step: number): Placed[] {
  const place = outdoorPlace("fish", step);
  const pixel = skinOf(options.character).fish(kind, place);
  const drawn = drawFigure(pixel, options.anime, animeAction({ at: "outing", kind: "fish" }), {
    point: sittingPoint(place),
    seat: seatPoint(place, ROCK_TOP),
    w: W,
    h: H,
  });
  // An anime render's rod is its own: the line hangs from the tip its sheet
  // gives. Without one, or with one the line cannot hang from to the bobber
  // on the water (by the shore, too low), the skin's figure fishes instead,
  // rod and all.
  const own = drawn.head ? drawn.tips?.rod : undefined;
  const hangs = own !== undefined && rodTipHangs(rodTip(own));
  const { figure, without } = drawn.head && !hangs ? { figure: pixel, without: (parts: Placed[]) => parts } : drawn;
  const rod = prop("fish.rod", O.ROD, figure.anchors.rod, W, H);
  // The line hangs from the rod's tip down to the bobber on the water.
  const tip = rodTip(hangs && own ? own : rod);
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
  return without(parts);
}

/** The log by the fire (LOG, rows 35–38). Its top row, its outline's (x
 * 25–38), is the seat's top: a row over the seat skins.ts has under SIT_BOX
 * at (23, 38), rows 36–38, which the person's body comes down over. */
const LOG_AT: Point = { x: 24, y: 35 };

/** On a log by a tent and a fire; the sun (the moon, in the dark theme)
 * crosses the sky with the wait. */
function camp(kind: string, options: FigureOptions, step: number): Placed[] {
  const sun = O.SUN_PATH[step];
  const flames = [O.FLAME_A, O.FLAME_B, O.FLAME_C];
  const place = outdoorPlace("camp", step);
  const { figure, without } = drawFigure(
    skinOf(options.character).camp(kind, place),
    options.anime,
    animeAction({ at: "outing", kind: "camp" }),
    { point: sittingPoint(place), seat: seatPoint(place, LOG_AT.y), w: W, h: H },
  );
  return without([
    at("camp.backdrop", O.CAMP_BACKDROP, 0, 0),
    at("camp.sun", O.SUN, sun.x, sun.y),
    at("camp.tent", O.TENT, 0, 18),
    at("camp.log", O.LOG, LOG_AT.x, LOG_AT.y),
    ...figure.parts,
    at("camp.fire.logs", O.FIRE_LOGS, 43, 36),
    ...flames.map((sprite, i) => framed(at(`camp.fire.${"abc"[i]}`, sprite, 44, 28), i, 3)),
  ]);
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
function bbq(kind: string, options: FigureOptions, step: number): Placed[] {
  const place = outdoorPlace("bbq", step);
  const { figure, without } = drawFigure(
    skinOf(options.character).bbq(kind, place),
    options.anime,
    animeAction({ at: "outing", kind: "bbq" }),
    { point: standingPoint(place), w: W, h: H },
  );
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
  return without(parts);
}

/** Along the road on a bike, from the side, a step at a time, wheels and
 * legs pedalling, towards the chequered flag; a spark once it is reached.
 * What of the rider is behind the bike (the far leg) goes under it. */
function cycle(kind: string, options: FigureOptions, step: number): Placed[] {
  const bike = outdoorPlace("cycle", step);
  const wheelY = bike.y + O.BIKE.h - 1 - Math.floor(O.WHEEL_A.h / 2);
  // An anime rider's wheels on the pixel wheels' ground, under the bike's
  // centre: the bottom of their bottom row.
  const { figure, without } = drawFigure(
    skinOf(options.character).cycle(kind, bike),
    options.anime,
    animeAction({ at: "outing", kind: "cycle" }),
    { point: { x: bike.x + O.BIKE.w / 2, y: wheelY + O.WHEEL_A.h }, w: W, h: H },
  );
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
  return without(parts);
}

/** An agent on its outing, a tile of OUTDOOR_W × OUTDOOR_H (agent kind as
 * agentColorKey gives it, drawn as its character). How far along it is, is
 * its step (0 … OUTING_STEPS - 1, clamped), each step placed rather than
 * moved; the frames only move legs, water, a bobber, flames, smoke, tongs,
 * wheels. */
export function outdoorScene(outing: Pick<Outing, "kind" | "step">, kind: string, options: FigureOptions = {}): Placed[] {
  const step = Math.min(OUTING_STEPS - 1, Math.max(0, Math.floor(outing.step) || 0));
  switch (outing.kind) {
    case "hike":
      return hike(kind, options, step);
    case "stream":
      return stream(kind, options, step);
    case "fish":
      return fish(kind, options, step);
    case "bbq":
      return bbq(kind, options, step);
    case "cycle":
      return cycle(kind, options, step);
    default:
      return camp(kind, options, step);
  }
}
