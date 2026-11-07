// The anime figures: each character's actions rendered (generated base
// images and 2D cut-out rigs, web/scripts/gemini) into one sheet, an image
// and its manifest. The war room draws a character from its sheet while the
// viewer draws characters as anime (game/characterStyle.ts, the default) and
// the sheet has an action for the state it is in; anything else stays pixel
// art (pixel/skins.ts).
//
// THE CONTRACT, for whoever renders a sheet:
//
// - A sheet per character (SHEET_IDS), <id>.webp and <id>.json in
//   anime/sheets/ (sheets.ts loads them; a missing or unreadable one leaves
//   the character in pixel art). Rows are the actions in the manifest's
//   order, columns frames 0 … n-1; the image is exactly (the most frames of
//   any action) × frameW wide and (rows) × frameH tall, straight alpha.
// - A frame is frameW × frameH px of the image; `scale` px make one art
//   pixel of the scene (3 CSS px), so 192 × 192 at 6 is 32 × 32 art px, two
//   device pixels to the CSS pixel. The renders are drawn as they are, never
//   scaled pixelated.
// - Every action loops: `frames` frames at `fps`; frame 0 alone must read as
//   the state (reduced motion shows only it).
// - `anchor` (frame px, frame 0) goes on the scene's point for the state
//   (warroom/deskArt.ts says which), on the frame's centre line, x 96. For
//   every action but the seated ones it is (96, 174): the soles' line, or
//   the wheels' ground for cycle-ride. Art pixels there are edges: the
//   point under the person's shoes is the bottom of their row, so soles at
//   y 174 stand where the person's do.
// - The seated actions (sofa-sit, sofa-sleep, camp-sit, fish-sit), from the
//   front: their anchor is the seat contact, the bottom of the hips where
//   they rest on the seat, (96, y) with y between 100 and 170. It goes on
//   the seat's top (the sofa's cushion, the log's top, the rock's), and the
//   legs hang below it, in front of the seat, as sitting looks from the
//   front. A seated anchor at y 170 or more (SOLES_Y) is taken for the
//   soles' line, as a sheet rendered before had it, and goes under the
//   person's shoes instead, as a standing one's would.
// - A desk action (desk-*) is the whole desk unit: the character from the
//   front, sitting behind a desk of its own with a laptop on it. Its soles'
//   line goes on the desk frame's floor (16, 29), so the frame fills the
//   32 × 32 cell; the scene draws no monitor, screen, desk top, chair or
//   mouse for it (its "desk" prop, whatever the manifest says) and says what
//   the screen would with a badge off the head. At the meeting table the
//   table stays, in front of it, the laptop standing on its top.
// - `head` (frame px, frame 0) is the head's box: bubbles (?, zzz, …) hang
//   off its right edge and top as they do off the person's head, and a
//   desk's badge off its top-right.
// - `props` names the scene's pixel props the render already has
//   (figure.ts PROP_PARTS: the desk unit for desk-*, the backpack for
//   hike-walk, the whole bike for cycle-ride, the pole for stream-wade, the
//   tongs for bbq-grill, the rod for fish-sit), so the scene does not draw
//   them again; an anime hiker never carries the pixel backpack (at the
//   summit either) nor an anime cook the pixel apron. The wader is cut at
//   the water round its waist: nothing of stream-wade shows under it.
// - `tips` (optional, frame px, frame 0) says where a prop the render has
//   ends, by its name, on the render as posed: fish-sit's rod's tip, where
//   the scene's line hangs from down to the bobber. The scene needs it out
//   over the water and up off it, in art px 25 … and row 18 at most of the
//   tile: with the seat contact on the rock's top, (11, 33), that is x 180
//   or more and y under anchor.y - 84 (under 51 for a seat contact at
//   y 135; under 72 for a sheet's from before, its soles under the shoes
//   at (11, 36)). Without it, or anywhere else, the character fishes in
//   pixel art.

import type { CharacterId } from "../character";

/** The characters with a sheet. */
export const SHEET_IDS = ["gpt", "claude", "deepseek", "gemini"] as const;

export type SheetId = (typeof SHEET_IDS)[number];

/** A seated action's anchor this far down its frame (frame px, y) or
 * further is its soles' line, as a sheet rendered before the seat contact
 * had it, not the seat contact; a standing action's (174) always is. */
export const SOLES_Y = 170;

const SHEET_OF: Readonly<Record<CharacterId, SheetId | undefined>> = {
  gpt: "gpt",
  claude: "claude",
  whale: "deepseek",
  star: "gemini",
  human: undefined,
};

/** A character's sheet; none for the person. */
export function sheetOf(character: CharacterId): SheetId | undefined {
  return SHEET_OF[character];
}

export const ANIME_VIEWS = ["front", "back", "side"] as const;

export interface AnimeAction {
  /** Its row of the sheet. */
  row: number;
  frames: number;
  fps: number;
  /** From where the figure is seen. */
  view?: (typeof ANIME_VIEWS)[number];
  /** The point (frame px, frame 0) that goes on the scene's point: the
   * soles' line, a seated action's seat contact. */
  anchor: { x: number; y: number };
  /** The head's box (frame px, frame 0). */
  head: { x: number; y: number; w: number; h: number };
  /** The scene's pixel props the render already has. */
  props: readonly string[];
  /** Where a prop the render has ends (frame px, frame 0), by its name: the
   * rod's tip. */
  tips?: Readonly<Record<string, { x: number; y: number }>>;
}

export interface AnimeManifest {
  /** The sheet's file, beside the manifest. */
  image: string;
  frameW: number;
  frameH: number;
  /** Frame px to an art pixel. */
  scale: number;
  style: "anime";
  character: string;
  actions: Readonly<Record<string, AnimeAction>>;
}

/** A sheet ready to draw from: its manifest and where its image is. */
export interface AnimeSheet {
  id: SheetId;
  manifest: AnimeManifest;
  /** The image's URL. */
  href: string;
  /** The image's size in px. */
  width: number;
  height: number;
}

type Raw = Record<string, unknown>;

const isObject = (value: unknown): value is Raw => typeof value === "object" && value !== null && !Array.isArray(value);
const isNumber = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value);
const isCount = (value: unknown): value is number => Number.isInteger(value) && (value as number) > 0;

function point(value: unknown): { x: number; y: number } | undefined {
  return isObject(value) && isNumber(value.x) && isNumber(value.y) ? { x: value.x, y: value.y } : undefined;
}

function box(value: unknown): AnimeAction["head"] | undefined {
  const at = point(value);
  if (!at || !isObject(value) || !isNumber(value.w) || !isNumber(value.h) || value.w <= 0 || value.h <= 0) return undefined;
  return { ...at, w: value.w, h: value.h };
}

const isView = (value: unknown): value is NonNullable<AnimeAction["view"]> =>
  (ANIME_VIEWS as readonly unknown[]).includes(value);
const isNames = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every((name) => typeof name === "string");

/** Tips by name, each a point; undefined when any of them does not read. */
function tipsOf(value: unknown): Record<string, { x: number; y: number }> | undefined {
  if (!isObject(value)) return undefined;
  const tips: Record<string, { x: number; y: number }> = {};
  for (const [name, at] of Object.entries(value)) {
    const read = point(at);
    if (!read) return undefined;
    tips[name] = read;
  }
  return tips;
}

function action(value: unknown): AnimeAction | undefined {
  if (!isObject(value)) return undefined;
  const { row, frames, fps, view, props = [] } = value;
  const anchor = point(value.anchor);
  const head = box(value.head);
  const tips = value.tips === undefined ? {} : tipsOf(value.tips);
  if (!isCount(frames) || !isNumber(fps) || fps <= 0 || !anchor || !head || !isNames(props) || !tips) return undefined;
  if (!Number.isInteger(row) || (row as number) < 0) return undefined;
  if (view !== undefined && !isView(view)) return undefined;
  return {
    row: row as number,
    frames,
    fps,
    ...(isView(view) ? { view } : {}),
    anchor,
    head,
    props: [...props],
    ...(Object.keys(tips).length ? { tips } : {}),
  };
}

/** A manifest as its JSON says, checked: undefined when it does not read as
 * a sheet (then the character stays pixel art); an action that does not
 * read is left out, the others kept. */
export function parseManifest(raw: unknown): AnimeManifest | undefined {
  if (!isObject(raw) || raw.style !== "anime") return undefined;
  const { image, frameW, frameH, scale, character } = raw;
  if (typeof image !== "string" || !/^[\w.-]+$/.test(image) || image.startsWith(".")) return undefined;
  if (!isCount(frameW) || !isCount(frameH) || !isNumber(scale) || scale <= 0 || !isObject(raw.actions)) return undefined;
  const actions: Record<string, AnimeAction> = {};
  for (const [name, value] of Object.entries(raw.actions)) {
    const read = action(value);
    if (read) actions[name] = read;
  }
  if (Object.keys(actions).length === 0) return undefined;
  return { image, frameW, frameH, scale, style: "anime", character: typeof character === "string" ? character : "", actions };
}

/** A manifest and its image's URL as a sheet: the image as wide as the
 * longest action's frames, as tall as its rows. */
export function toSheet(id: SheetId, manifest: AnimeManifest, href: string): AnimeSheet {
  const actions = Object.values(manifest.actions);
  const columns = Math.max(...actions.map((action) => action.frames));
  const rows = Math.max(...actions.map((action) => action.row)) + 1;
  return { id, manifest, href, width: columns * manifest.frameW, height: rows * manifest.frameH };
}
