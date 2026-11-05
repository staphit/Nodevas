// How a character looks in every place the war room draws it: its skin. A
// scene (warroom/deskArt.ts) draws the furniture, the screen, the backdrop
// and the props; it asks the agent's skin for the figure in the state it is
// in and places the props (a mouse, a backpack, a pole, a rod, tongs, an
// apron, the water) and the bubbles (?, …, zzz, a thought) at the anchors
// the figure gives, so a character of any shape holds them where its hands,
// back and head are. Which character an agent is: game/character.ts.
//
// THE CONTRACT, for whoever draws a skin (pixel/skinClaude.ts, skinWhale.ts,
// skinStar.ts; the person is skinHuman.ts):
//
// - Original pixel art in the war room's style: text grids, one character per
//   art pixel, "." transparent, every other character a palette token through
//   sprites.ts LEGEND (sprite() from sprites.ts compiles a grid). A colour of
//   the skin's own needs a PIXEL_TOKENS entry (palette.ts, appended at the
//   end), a LEGEND char (sprites.ts: claude 5 6 7, whale 8 9 0 +, star I X Z %,
//   so parallel work never takes the same one) and a value in warroom.css for
//   the light theme (.px-office) and the dark one ([data-theme="dark"]
//   .px-office); office styles never redefine it. "agent" and "agent-dark" are
//   the agent kind's colour (agent--<kind>), "outline", "skin", "eye", "pants"…
//   are shared; use them where they fit.
// - Every method returns parts already placed in the scene's frame (absolute
//   coordinates, whole art pixels), and anchors in the same frame. Every part
//   stays inside the frame named for its state; skins.test.ts checks every
//   state, step and frame of every skin.
// - `kind` is the agent kind (agentColorKey: claude, codex, gemini, opencode,
//   antigravity, other), for headwear or anything else per kind; a skin may
//   ignore it. Part names are free ("whale.tail"…), but a skin's own prefix
//   keeps them readable; the person's names are what deskArt.test.ts checks.
// - Frames: a part with `frame: { index, of }` is one of `of` frames taking
//   turns; frame 0 is what a still (reduced-motion) picture shows, so frame 0
//   alone must read as the state. Only the `of` values FRAMES_OF lists for a
//   state may be used (the scene's loop); FRAMES_NEEDED says which states must
//   move (typing hands, walking legs, pedalling legs).
// - A state must read without colour or motion: each desk gesture differs
//   from the others in its still pixels (the test compares them), and the
//   screen's glyph (x 11–19, y 3–9 of the desk frame: ?, ×, ✓, an hourglass,
//   a clock) stays two thirds uncovered at least; keep the head below row 10
//   there, as the person's is.

import type { CharacterId } from "../character";
import type { Sprite } from "./compile";
import type { Placed, Point } from "./placed";
import { claudeSkin } from "./skinClaude";
import { humanSkin } from "./skinHuman";
import { starSkin } from "./skinStar";
import { whaleSkin } from "./skinWhale";

/** What the agent does with its arms at a desk (or the meeting table),
 * seated with its back to the room:
 * - rest: hands on the desk (idle, waiting on CI, reviewing, offline);
 * - type: typing, the hands taking turns (working; editing, a shell);
 * - chin: a hand on the chin (reading, thinking);
 * - mouse: a hand on the mouse, right of the keyboard (searching, the web);
 * - ask: a hand up, the other on the desk (it asked you something);
 * - cheer: both arms up (finished);
 * - slump: slumped, the head sunk and leaning (failed);
 * - sleep: asleep on folded arms, the head down (a usage limit). */
export type DeskGesture = "rest" | "type" | "chin" | "mouse" | "ask" | "cheer" | "slump" | "sleep";

export const DESK_GESTURES: readonly DeskGesture[] = ["rest", "type", "chin", "mouse", "ask", "cheer", "slump", "sleep"];

/** The props a scene places at a figure's anchors, each anchor the prop's
 * top-left corner (its size in brackets, its sprite in outdoorSprites.ts or
 * activitySprites.ts):
 * - mouse: the desk's mouse [3×4], under the hand of the "mouse" gesture;
 * - pack: the hiker's backpack [14×10], drawn BEHIND the figure (its
 *   shoulders and sides show past the body); absent, no backpack;
 * - pole: the walking pole in the stream [1×16], drawn over the figure, its
 *   foot in the water;
 * - wade: the water round the wader [14×3], drawn over the figure at its
 *   waist; below it the figure is under water (draw nothing under row
 *   wade.y + 2);
 * - rod: the fishing rod [23×21], over the figure, its grip (bottom-left
 *   pixel, rod + (0, 20)) in the hand, its tip (rod + (22, 0)) where the line
 *   hangs from to the bobber on the water;
 * - tongs: the barbecue tongs' box [9×4], frame 0 at the anchor and frame 1
 *   three rows up [9×5], drawn over the figure and the grill; the scene draws
 *   the tongs (columns 5–8), the skin the forearm that holds them (in
 *   Figure.front: columns 0–4, its hand at column 4 on the handle's row,
 *   anchor + (4, 1) in frame 0 and anchor + (4, -1) in frame 1, frames of
 *   2); the jaws reach the skewers on the grill, so keep it near the
 *   person's (x + 10, feet - 13);
 * - apron: the cook's apron [6×6] over the figure's front; absent, none. */
export type PropAnchor = "mouse" | "pack" | "pole" | "wade" | "rod" | "tongs" | "apron";

/** A figure in a state: its parts, and where the scene puts what goes with
 * it. */
export interface Figure<Need extends PropAnchor = never> {
  /** Drawn before the scene's middle layer: only the bike ride has one (the
   * far leg, behind the bike). */
  behind?: Placed[];
  /** The figure itself, drawn in order (later over earlier). */
  parts: Placed[];
  /** Drawn after the scene's middle layer: only the barbecue has one (the
   * forearm on the tongs, over the grill and under the tongs), in the skin's
   * own colours. */
  front?: Placed[];
  /** `head`: the top-left corner of the head (headwear not counted); for a
   * figure with no head of its own, of its face or top. Bubbles hang above
   * and to its right (deskArt: ask at head + (12, -10), a thought at
   * head + (8, -10), zzz at head + (7, -14) at a desk, head + (5, -9) on a
   * sofa, "…" at the door at head + (10, -7)), always kept inside the frame.
   * The rest are the props' corners (PropAnchor); every anchor is inside the
   * frame. */
  anchors: { head: Point } & Record<Need, Point> & Partial<Record<PropAnchor, Point>>;
}

/** The person's standing box, the place of every standing state: `at` is
 * its bottom-left corner (x its left column, y the row the feet stand on).
 * A skin stands on row `at.y`, centred on column at.x + STAND_BOX.w / 2. */
export const STAND_BOX = { w: 12, h: 23 } as const;

/** The person's seated box, from the front (legs, body, arms, head): `at` is
 * its bottom-left corner, y the row the shoes rest on. Centre on
 * at.x + SIT_BOX.w / 2; the seat (a rock, a log) is under rows at.y - 2 …
 * at.y. */
export const SIT_BOX = { w: 20, h: 20 } as const;

/** Every state a character is drawn in. Frames, in art pixels; the human's
 * extents are a guide to how big a figure may be.
 *
 * Seated at a desk, back to the room (desk and meeting scenes, 32×32; the
 * monitor rows 0–14 behind, the desk top from row 15 and the chair rows
 * 23–30, x 9–22, drawn OVER the figure). The person: head at (11, 10), 10×9;
 * shoulders (8, 19) 16×8; arms from x 6 to 28, raised ones from row 5. */
export interface Skin {
  id: CharacterId;

  /** At a desk, in a gesture (DeskGesture). Frame 32×32. Frames: "type"
   * must have frames 0 and 1 of 2 (the hands take turns); "ask" may use
   * of 2, "sleep" of 3; the others hold still (none). anchors.mouse: where
   * the mouse goes for "mouse" (the person's (29, 16)); give it for every
   * gesture. Used by deskScene and meetingScene. */
  desk(gesture: DeskGesture, kind: string): Figure<"mouse">;

  /** Standing in the doorway, facing the room, waiting to be let in. Frame
   * 32×32, the doorway open at x 10–21, y 4–28; the person stands in
   * STAND_BOX at (10, 29). No frames. Used by doorScene. */
  door(kind: string): Figure;

  /** On a lounge sofa, facing the room: sitting, or asleep on a usage limit
   * (head down over folded arms). Frame 24×32 (SEAT_W × CELL), the cushion
   * at row 22; the person's head at (7, 6) (asleep (5, 9)), body (4, 16),
   * shoes on row 26, arms x 2–21. Frames: none awake, of 3 asleep. Used by
   * sofaScene. */
  sofa(kind: string, asleep: boolean): Figure;

  /** Hiking up the trail, standing in STAND_BOX at `at` (HIKE_PATH, from
   * (2, 42) up to (37, 30)). `walking`: legs striding in frames 0 and 1 of
   * 2 (needed); at the summit (not walking) standing still, no frames.
   * anchors.pack: the backpack, optional. Frame 56×44. Used by hike. */
  hike(kind: string, at: Point, walking: boolean): Figure;

  /** Wading up the stream, waist deep, standing in STAND_BOX at `at` (x 1 …
   * 36, y 33; the water from row 29). anchors.pole and anchors.wade
   * (needed). No frames of its own (of 3 allowed, with the ripples). Frame
   * 56×44. Used by stream. */
  stream(kind: string, at: Point): Figure<"pole" | "wade">;

  /** Fishing, seated on a rock, from the front, in SIT_BOX at `at` (1, 35);
   * anchors.rod (needed; the person's (19, 8), grip in the right hand at
   * (19, 28)). Frames of 2 allowed. Frame 56×44. Used by fish. */
  fish(kind: string, at: Point): Figure<"rod">;

  /** At the camp, seated on a log by the fire, from the front, in SIT_BOX at
   * `at` (23, 38); the fire at x 43–52 on its right. Frames of 3 allowed.
   * Frame 56×44. Used by camp. */
  camp(kind: string, at: Point): Figure;

  /** At the barbecue, standing at the grill (x 30–53, rows 30–41, on its
   * right) in STAND_BOX at `at` (17, 41). anchors.tongs and the forearm on
   * them in `front` (needed; PropAnchor), anchors.apron (optional). Frames
   * of 2 or 3 allowed. Frame 56×44. Used by bbq. */
  bbq(kind: string, at: Point): Figure<"tongs">;

  /** Riding a bike, from the side, facing right (the way it goes). `bike`
   * is the top-left of BIKE (22×9, outdoorSprites.ts; x 0 … 28, y 26): the
   * saddle's top at bike + BIKE_SADDLE, the bars' at bike + BIKE_BARS, the
   * wheels' hubs on its bottom row. The person: head (bike.x + 10,
   * bike.y - 17), torso from (bike.x + 3, bike.y - 9), legs from
   * (bike.x + 3, bike.y - 3) down to the pedals. `behind` holds what is
   * behind the bike (the far leg), `parts` what is over it. Legs pedalling
   * in frames 0 and 1 of 2 (needed), in step with the wheels. Everything
   * stays left of the finish pole (x 50) at every step. Frame 56×44. Used by
   * cycle. */
  cycle(kind: string, bike: Point): Figure;

  /** From behind, small, at a desk in another project's office (miniDesk,
   * 16×16): at most 8×8, drawn with its bottom on row 14, centred on x 8;
   * its shirt in "agent". Nothing else at that size says which agent it is,
   * so a skin that draws no kind anywhere else still shows it here (a band,
   * a stem). */
  mini: Sprite;
}

/** The `of` values a state's frames may use: the scene's own loop. */
export const FRAMES_OF = {
  desk: { rest: [], type: [2], chin: [], mouse: [], ask: [2], cheer: [], slump: [], sleep: [3] } as Record<
    DeskGesture,
    readonly number[]
  >,
  door: [] as readonly number[],
  sofa: [] as readonly number[],
  sofaAsleep: [3] as readonly number[],
  hike: [2] as readonly number[],
  stream: [3] as readonly number[],
  fish: [2] as readonly number[],
  camp: [3] as readonly number[],
  bbq: [2, 3] as readonly number[],
  cycle: [2] as readonly number[],
} as const;

/** The states that must move: frames 0 and 1 of 2 in their figure. */
export const FRAMES_NEEDED = ["desk.type", "hike.walking", "cycle"] as const;

const SKINS: Record<CharacterId, Skin> = {
  human: humanSkin,
  claude: claudeSkin,
  whale: whaleSkin,
  star: starSkin,
};

/** A character's skin; the person for anything unknown. */
export function skinOf(character: CharacterId | undefined): Skin {
  return SKINS[character ?? "human"] ?? humanSkin;
}
