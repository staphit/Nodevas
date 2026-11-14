// What a theme pack's theme.json holds, and the client's own check of it. The
// server validates a zip in full before it stores one; this is the second
// look, so a stale or odd answer costs the viewer a pack and never the page:
// whatever is wrong here means the built-in people show.

import type { ThemePack } from "../api";

/** A scene whose image is a strip: `count` frames side by side, each the
 * scene's w by h. Frame 0 is the still; every other holds only what it
 * changes, and is drawn over the still. */
/** A text of the pack in the interface's languages. */
export interface ThemeText {
  en: string;
  "zh-TW"?: string;
}

export interface ThemeFrames {
  count: number;
  /** Played now and then on its own. */
  blink?: number;
  /** Played in order on a click. */
  click: number[];
}

export interface ThemeScene {
  /** The room drawn under the image; none when the image is the whole picture. */
  room?: string;
  image: string;
  x: number;
  y: number;
  w: number;
  h: number;
  /** left, top, right, bottom on the canvas. */
  box: [number, number, number, number];
  frames?: ThemeFrames;
  /** A clip played silent and looping in place of the image, which stays the
   * still: its poster, and what is drawn wherever nothing moves. Never with
   * frames. */
  video?: string;
  /** A clip played once, silent, when the picture is clicked; then the loop
   * (or the still) comes back. It starts and ends on the image. Never with
   * frames. */
  clickVideo?: string;
  /** Where the character's sign hangs: its centre and its width, on the canvas. */
  sign?: [number, number, number];
}

export interface ThemeCharacter {
  match: { agents: string[]; models: string[] };
  /** Stands in for agents no character matches. A pack has at most one. */
  fallback?: true;
  /** An image that floats over her head, where a scene says. */
  sign?: string;
  scenes: Record<string, ThemeScene>;
  /** What she is called on the story's name plate, before the agent's name. */
  title?: ThemeText;
  /** What she says, by state: hers win over the pack's. */
  lines?: ThemeLines;
}

/** The app's colours a pack may set: each is the token `--bg`, `--surface-2`, … */
export const THEME_COLORS = [
  "bg",
  "surface",
  "surface-2",
  "border",
  "border-strong",
  "hover",
  "text",
  "text-2",
  "accent",
  "accent-text",
] as const;
export type ThemeColor = (typeof THEME_COLORS)[number];

/** How the whole app looks while the pack is on. */
export interface ThemeSkin {
  /** Which of the app's two palettes the colours are laid over. */
  scheme: "light" | "dark";
  font?: "sans" | "serif";
  /** Hex only: `#rgb`, `#rrggbb` or `#rrggbbaa`. */
  colors: Partial<Record<ThemeColor, string>>;
}

/** The places of the war room a pack may give its own names. */
export type ThemePlaceId = "work" | "meeting" | "entrance" | "lounge" | "away" | "runners";
export const THEME_PLACES: readonly ThemePlaceId[] = ["work", "meeting", "entrance", "lounge", "away", "runners"];

/** The story's own words a pack may put in its world's terms. */
export const THEME_WORDS = [
  "narrator",
  "unnamed",
  "task",
  "people",
  "notices",
  "others",
  "nobody",
  "count",
  "runnerCount",
  "failed",
  "empty",
  "noticesLine",
  "runnersLine",
  "heldLine",
  "more",
  "status",
  "role",
  "node",
  "runner",
  "tokens",
  "project",
  "held",
  "tests",
] as const;
export type ThemeWord = (typeof THEME_WORDS)[number];

/** The states an agent has a line for in the story. */
export const THEME_LINE_STATES = [
  "offline",
  "asking-user",
  "failed",
  "limit",
  "awaiting-accept",
  "waiting-ci",
  "reviewing",
  "working",
  "finished",
  "idle",
] as const;
export type ThemeLineState = (typeof THEME_LINE_STATES)[number];
/** A line may hold {node}: the node the agent is on, or the word for a task. */
export type ThemeLines = Partial<Record<ThemeLineState, ThemeText>>;

export interface ThemeManifest {
  schema: 1;
  id: string;
  name: { en: string; "zh-TW"?: string };
  version: string;
  author: string;
  canvas: [number, number];
  rooms: Record<string, string>;
  characters: Record<string, ThemeCharacter>;
  skin?: ThemeSkin;
  places?: Partial<Record<ThemePlaceId, ThemeText>>;
  words?: Partial<Record<ThemeWord, ThemeText>>;
  /** What anyone says who has no line of her own. */
  lines?: ThemeLines;
}

const PACK_ID = /^[a-z0-9][a-z0-9-]{0,39}$/;
const PART_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const IMAGE = /\.(webp|png|jpe?g)$/i;
const CLIP = /\.(mp4|webm)$/i;
const MAX_CANVAS = 8192;
const MAX_PATH_BYTES = 200;
const MAX_FRAMES = 16;
const MAX_CLICKS = 4;
const HEX_COLOR = /^#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i;
const CONTROL = /\p{Cc}/u;
const MAX_PLACE_NAME = 40;
const MAX_TITLE = 40;
const MAX_WORD = 120;
const MAX_LINE = 200;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isInt(value: unknown, min: number, max: number): value is number {
  return Number.isInteger(value) && (value as number) >= min && (value as number) <= max;
}

function isStringList(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

/**
 * A relative, forward-slashed path that stays inside its pack. The dots matter
 * most: encoding leaves them as they are, and a browser resolves "a/../.." in
 * a URL before it asks for anything.
 */
export function isSafeThemePath(path: unknown): path is string {
  if (typeof path !== "string" || path === "") return false;
  if (path.includes("\\") || path.includes("\0") || /^[A-Za-z]:/.test(path)) return false;
  if (new TextEncoder().encode(path).length > MAX_PATH_BYTES) return false;
  return path.split("/").every((segment) => segment !== "" && segment !== "." && segment !== "..");
}

function isImagePath(path: unknown): path is string {
  return isSafeThemePath(path) && IMAGE.test(path);
}

function isClipPath(path: unknown): path is string {
  return isSafeThemePath(path) && CLIP.test(path);
}

/**
 * A colour as a skin may give it. The value ends up in a CSS custom property,
 * so nothing but hex passes: no names, no functions, nothing with a url in it.
 */
export function isThemeHexColor(value: unknown): value is string {
  return typeof value === "string" && HEX_COLOR.test(value);
}

// An inclusion, not a lookup: "constructor" is no colour.
function isThemeColor(key: string): key is ThemeColor {
  return (THEME_COLORS as readonly string[]).includes(key);
}

/** Counted in characters, as the server counts them, not in UTF-16 units. */
function isShown(value: unknown, max: number): value is string {
  return typeof value === "string" && [...value].length <= max && !CONTROL.test(value);
}

/** A text with its English, each language no longer than max. */
function parseText(raw: unknown, max: number): ThemeText | undefined {
  if (!isRecord(raw)) return undefined;
  const zh = raw["zh-TW"];
  if (!isShown(raw.en, max) || raw.en === "") return undefined;
  if (zh !== undefined && !isShown(zh, max)) return undefined;
  return zh === undefined ? { en: raw.en } : { en: raw.en, "zh-TW": zh };
}

/** Texts by key, every key one of those known. */
function parseTexts<K extends string>(
  raw: unknown,
  keys: readonly K[],
  max: number,
): Partial<Record<K, ThemeText>> | undefined {
  if (!isRecord(raw)) return undefined;
  const texts: Partial<Record<K, ThemeText>> = {};
  for (const [key, value] of Object.entries(raw)) {
    const text = parseText(value, max);
    // An inclusion, not a lookup: "constructor" is no word.
    if (!(keys as readonly string[]).includes(key) || !text) return undefined;
    texts[key as K] = text;
  }
  return texts;
}

/** The fields of a listed pack the page indexes into. */
export function isThemePack(value: unknown): value is ThemePack {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    PACK_ID.test(value.id) &&
    isRecord(value.name) &&
    typeof value.name.en === "string"
  );
}

function parseFrames(raw: unknown): ThemeFrames | undefined {
  if (!isRecord(raw) || !isInt(raw.count, 2, MAX_FRAMES)) return undefined;
  const last = raw.count - 1;
  const blink = raw.blink ?? undefined;
  const click = raw.click ?? [];
  if (blink !== undefined && !isInt(blink, 1, last)) return undefined;
  if (!Array.isArray(click) || click.length > MAX_CLICKS || !click.every((frame) => isInt(frame, 1, last))) {
    return undefined;
  }
  const frames: ThemeFrames = { count: raw.count, click: [...click] };
  if (blink !== undefined) frames.blink = blink;
  return frames;
}

function parseSign(raw: unknown, canvas: [number, number]): [number, number, number] | undefined {
  if (!Array.isArray(raw) || raw.length !== 3 || !raw.every(Number.isFinite)) return undefined;
  const [x, y, width] = raw as [number, number, number];
  if (x < 0 || y < 0 || x > canvas[0] || y > canvas[1] || width <= 0 || width > canvas[0]) return undefined;
  return [x, y, width];
}

function parseScene(
  raw: unknown,
  canvas: [number, number],
  rooms: Set<string>,
  signed: boolean,
): ThemeScene | undefined {
  if (!isRecord(raw)) return undefined;
  const { image, x, y, w, h, box } = raw;
  const room = raw.room ?? "";
  const [width, height] = canvas;
  if (typeof room !== "string" || (room !== "" && !rooms.has(room)) || !isImagePath(image)) return undefined;
  if (!isInt(x, 0, width) || !isInt(y, 0, height) || !isInt(w, 1, width - x) || !isInt(h, 1, height - y)) {
    return undefined;
  }
  if (!Array.isArray(box) || box.length !== 4 || !box.every(Number.isFinite)) return undefined;
  const [left, top, right, bottom] = box as [number, number, number, number];
  if (left < 0 || top < 0 || left >= right || top >= bottom || right > width || bottom > height) return undefined;
  const scene: ThemeScene = { image, x, y, w, h, box: [left, top, right, bottom] };
  if (room !== "") scene.room = room;
  if (raw.frames !== undefined && raw.frames !== null) {
    const frames = parseFrames(raw.frames);
    if (!frames) return undefined;
    scene.frames = frames;
  }
  // An empty video is none, as the server reads it.
  const video = raw.video ?? "";
  if (video !== "") {
    // A clip is the movement: nothing would play a strip beside it.
    if (!isClipPath(video) || scene.frames) return undefined;
    scene.video = video;
  }
  const clickVideo = raw.clickVideo ?? "";
  if (clickVideo !== "") {
    // A strip has its own click frames: two answers to one click.
    if (!isClipPath(clickVideo) || scene.frames) return undefined;
    scene.clickVideo = clickVideo;
  }
  if (raw.sign !== undefined && raw.sign !== null) {
    const sign = parseSign(raw.sign, canvas);
    // A place to hang a sign the character does not have.
    if (!sign || !signed) return undefined;
    scene.sign = sign;
  }
  return scene;
}

function parseCharacter(raw: unknown, canvas: [number, number], rooms: Set<string>): ThemeCharacter | undefined {
  if (!isRecord(raw)) return undefined;
  const rawMatch = raw.match ?? {};
  const rawScenes = raw.scenes ?? {};
  if (!isRecord(rawMatch) || !isRecord(rawScenes)) return undefined;
  const agents = rawMatch.agents ?? [];
  const models = rawMatch.models ?? [];
  if (!isStringList(agents) || !isStringList(models)) return undefined;
  if (raw.fallback !== undefined && typeof raw.fallback !== "boolean") return undefined;
  const sign = raw.sign ?? "";
  if (sign !== "" && !isImagePath(sign)) return undefined;
  const scenes: Record<string, ThemeScene> = {};
  for (const [name, value] of Object.entries(rawScenes)) {
    const scene = parseScene(value, canvas, rooms, sign !== "");
    if (!PART_ID.test(name) || !scene) return undefined;
    scenes[name] = scene;
  }
  const character: ThemeCharacter = { match: { agents, models }, scenes };
  if (raw.fallback === true) character.fallback = true;
  if (sign !== "") character.sign = sign;
  if (raw.title !== undefined && raw.title !== null) {
    const title = parseText(raw.title, MAX_TITLE);
    if (!title) return undefined;
    character.title = title;
  }
  if (raw.lines !== undefined && raw.lines !== null) {
    const lines = parseTexts(raw.lines, THEME_LINE_STATES, MAX_LINE);
    if (!lines) return undefined;
    character.lines = lines;
  }
  return character;
}

function parseSkin(raw: unknown): ThemeSkin | undefined {
  if (!isRecord(raw)) return undefined;
  const { scheme } = raw;
  const font = raw.font ?? "";
  const rawColors = raw.colors ?? {};
  if (scheme !== "light" && scheme !== "dark") return undefined;
  if (font !== "" && font !== "sans" && font !== "serif") return undefined;
  if (!isRecord(rawColors)) return undefined;
  const colors: ThemeSkin["colors"] = {};
  for (const [color, value] of Object.entries(rawColors)) {
    if (!isThemeColor(color) || !isThemeHexColor(value)) return undefined;
    colors[color] = value;
  }
  const skin: ThemeSkin = { scheme, colors };
  if (font !== "") skin.font = font;
  return skin;
}


/**
 * The manifest as the client will use it, holding only what the contract names,
 * or undefined if any of it is not what it should be. One bad scene refuses the
 * whole pack, as it does on the server: half a pack would look broken.
 */
export function parseThemeManifest(raw: unknown): ThemeManifest | undefined {
  if (!isRecord(raw) || raw.schema !== 1) return undefined;
  const { id, name, version, canvas } = raw;
  const author = raw.author ?? "";
  const rawRooms = raw.rooms ?? {};
  const rawCharacters = raw.characters ?? {};
  if (typeof id !== "string" || !PACK_ID.test(id)) return undefined;
  if (typeof version !== "string" || version === "" || typeof author !== "string") return undefined;
  if (!isRecord(name) || typeof name.en !== "string" || name.en === "") return undefined;
  const zh = name["zh-TW"];
  if (zh !== undefined && typeof zh !== "string") return undefined;
  if (!Array.isArray(canvas) || canvas.length !== 2 || !canvas.every((side) => isInt(side, 1, MAX_CANVAS))) {
    return undefined;
  }
  if (!isRecord(rawRooms) || !isRecord(rawCharacters)) return undefined;
  const size = canvas as [number, number];

  const rooms: Record<string, string> = {};
  for (const [room, path] of Object.entries(rawRooms)) {
    if (!PART_ID.test(room) || !isImagePath(path)) return undefined;
    rooms[room] = path;
  }
  // A set, not rooms[room]: "constructor" is a fine room id and every object
  // already answers to it.
  const roomIds = new Set(Object.keys(rooms));
  const characters: Record<string, ThemeCharacter> = {};
  for (const [character, value] of Object.entries(rawCharacters)) {
    const parsed = parseCharacter(value, size, roomIds);
    if (!PART_ID.test(character) || !parsed) return undefined;
    characters[character] = parsed;
  }
  const manifest: ThemeManifest = {
    schema: 1,
    id,
    name: zh === undefined ? { en: name.en } : { en: name.en, "zh-TW": zh },
    version,
    author,
    canvas: [size[0], size[1]],
    rooms,
    characters,
  };
  // A skin, a place name or a word that is wrong refuses the pack like a bad
  // scene does: an app half in the pack's colours would look broken too.
  if (raw.skin !== undefined && raw.skin !== null) {
    const skin = parseSkin(raw.skin);
    if (!skin) return undefined;
    manifest.skin = skin;
  }
  if (raw.places !== undefined && raw.places !== null) {
    const places = parseTexts(raw.places, THEME_PLACES, MAX_PLACE_NAME);
    if (!places) return undefined;
    manifest.places = places;
  }
  if (raw.words !== undefined && raw.words !== null) {
    const words = parseTexts(raw.words, THEME_WORDS, MAX_WORD);
    if (!words) return undefined;
    manifest.words = words;
  }
  if (raw.lines !== undefined && raw.lines !== null) {
    const lines = parseTexts(raw.lines, THEME_LINE_STATES, MAX_LINE);
    if (!lines) return undefined;
    manifest.lines = lines;
  }
  return manifest;
}
