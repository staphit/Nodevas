// What a theme pack's theme.json holds, and the client's own check of it. The
// server validates a zip in full before it stores one; this is the second
// look, so a stale or odd answer costs the viewer a pack and never the page:
// whatever is wrong here means the built-in people show.

import type { ThemePack } from "../api";

export interface ThemeScene {
  room: string;
  image: string;
  x: number;
  y: number;
  w: number;
  h: number;
  /** left, top, right, bottom on the canvas. */
  box: [number, number, number, number];
}

export interface ThemeCharacter {
  match: { agents: string[]; models: string[] };
  scenes: Record<string, ThemeScene>;
}

export interface ThemeManifest {
  schema: 1;
  id: string;
  name: { en: string; "zh-TW"?: string };
  version: string;
  author: string;
  canvas: [number, number];
  rooms: Record<string, string>;
  characters: Record<string, ThemeCharacter>;
}

const PACK_ID = /^[a-z0-9][a-z0-9-]{0,39}$/;
const PART_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const IMAGE = /\.(webp|png|jpe?g)$/i;
const MAX_CANVAS = 8192;
const MAX_PATH_BYTES = 200;

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

function parseScene(raw: unknown, canvas: [number, number], rooms: Set<string>): ThemeScene | undefined {
  if (!isRecord(raw)) return undefined;
  const { room, image, x, y, w, h, box } = raw;
  const [width, height] = canvas;
  if (typeof room !== "string" || !rooms.has(room) || !isImagePath(image)) return undefined;
  if (!isInt(x, 0, width) || !isInt(y, 0, height) || !isInt(w, 1, width - x) || !isInt(h, 1, height - y)) {
    return undefined;
  }
  if (!Array.isArray(box) || box.length !== 4 || !box.every(Number.isFinite)) return undefined;
  const [left, top, right, bottom] = box as [number, number, number, number];
  if (left < 0 || top < 0 || left >= right || top >= bottom || right > width || bottom > height) return undefined;
  return { room, image, x, y, w, h, box: [left, top, right, bottom] };
}

function parseCharacter(raw: unknown, canvas: [number, number], rooms: Set<string>): ThemeCharacter | undefined {
  if (!isRecord(raw)) return undefined;
  const rawMatch = raw.match ?? {};
  const rawScenes = raw.scenes ?? {};
  if (!isRecord(rawMatch) || !isRecord(rawScenes)) return undefined;
  const agents = rawMatch.agents ?? [];
  const models = rawMatch.models ?? [];
  if (!isStringList(agents) || !isStringList(models)) return undefined;
  const scenes: Record<string, ThemeScene> = {};
  for (const [name, value] of Object.entries(rawScenes)) {
    const scene = parseScene(value, canvas, rooms);
    if (!PART_ID.test(name) || !scene) return undefined;
    scenes[name] = scene;
  }
  return { match: { agents, models }, scenes };
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
  return {
    schema: 1,
    id,
    name: zh === undefined ? { en: name.en } : { en: name.en, "zh-TW": zh },
    version,
    author,
    canvas: [size[0], size[1]],
    rooms,
    characters,
  };
}
