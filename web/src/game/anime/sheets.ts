// The anime sheets the build has (anime/sheets/<id>.json and <id>.webp,
// manifest.ts), a character's loaded the first time it is drawn as anime
// (the default style): a browser that picked pixel, or never shows that
// character, fetches nothing of it. The images are the build's hashed assets
// (cached for good, like the rest of the build); a manifest is a chunk of
// its own. A sheet counts once its manifest reads and its image has loaded
// at the size the manifest says, so a character never shows an empty spot
// while it loads, nor a sheet out of step with its manifest; a missing,
// unreadable or mismatched one leaves the character in pixel art. One that
// could not be fetched (a network blip, a deploy under way) is tried again a
// while later, for as long as it is drawn.

import { useEffect, useSyncExternalStore } from "react";

import type { CharacterId } from "../character";
import { useCharacterStyle } from "../characterStyle";
import { parseManifest, SHEET_IDS, sheetOf, toSheet, type AnimeSheet, type SheetId } from "./manifest";

/** Where sheets come from: manifests to load and image URLs, by path
 * ("./sheets/<file>"). */
export interface SheetSource {
  manifests: Readonly<Record<string, () => Promise<unknown>>>;
  images: Readonly<Record<string, string>>;
}

/** Loads an image: its size in px once it has, none when it does not. */
export type Decode = (href: string) => Promise<{ width: number; height: number } | undefined>;

const BUILT_IN: SheetSource = {
  manifests: import.meta.glob<unknown>("./sheets/*.json", { import: "default" }),
  images: import.meta.glob<string>("./sheets/*.webp", { query: "?url", import: "default", eager: true }),
};

function decodeImage(href: string): Promise<{ width: number; height: number } | undefined> {
  if (typeof Image === "undefined") return Promise.resolve(undefined);
  return new Promise((resolve) => {
    const image = new Image();
    image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight });
    image.onerror = () => resolve(undefined);
    image.src = href;
  });
}

/** What loading a sheet came to: the sheet; "missing" when the build has
 * none for the character or its files do not make one (for good); "failed"
 * when its manifest or image could not be fetched or decoded, which may
 * pass. */
type Loaded = AnimeSheet | "missing" | "failed";

async function fetchSheet(id: SheetId, source: SheetSource, decode: Decode): Promise<Loaded> {
  const load = source.manifests[`./sheets/${id}.json`];
  if (!load) return "missing";
  let raw: unknown;
  try {
    raw = await load();
  } catch {
    return "failed";
  }
  const manifest = parseManifest(raw);
  const href = manifest && source.images[`./sheets/${manifest.image}`];
  if (!manifest || !href) return "missing";
  const size = await decode(href).catch(() => undefined);
  if (!size) return "failed";
  const sheet = toSheet(id, manifest, href);
  // An image of another size than its manifest's would be drawn stretched,
  // every row off.
  return size.width === sheet.width && size.height === sheet.height ? sheet : "missing";
}

/** A character's sheet from a source, ready to draw; none when anything of
 * it is missing, does not read or does not match. */
export async function loadSheet(id: SheetId, source: SheetSource, decode: Decode = decodeImage): Promise<AnimeSheet | undefined> {
  const loaded = await fetchSheet(id, source, decode);
  return typeof loaded === "string" ? undefined : loaded;
}

/** How long before a sheet that could not be fetched is tried again: twice
 * as long after each try, at most RETRY_MAX_MS. */
export const RETRY_FIRST_MS = 5_000;
export const RETRY_MAX_MS = 300_000;

// The loaded sheets, shared by every character drawn.
let source = BUILT_IN;
let decode: Decode = decodeImage;
let requested = new Set<SheetId>();
let sheets: ReadonlyMap<SheetId, AnimeSheet> = new Map();
/** Tries that failed in a row, by character; the tries waiting to be made. */
let failures = new Map<SheetId, number>();
let retries = new Set<ReturnType<typeof setTimeout>>();
/** Bumped when a sheet may be tried again: whoever draws it asks again. */
let retryTick = 0;
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

function notify(): void {
  for (const listener of [...listeners]) listener();
}

const current = () => sheets;
const currentTick = () => retryTick;

/** Loads a character's sheet, once; again a while after a try that failed. */
function requestSheet(id: SheetId): void {
  if (requested.has(id)) return;
  requested.add(id);
  const from = source;
  void fetchSheet(id, from, decode).then((loaded) => {
    if (from !== source) return;
    if (loaded === "missing") return;
    if (loaded === "failed") return retryLater(id);
    failures.delete(id);
    sheets = new Map(sheets).set(id, loaded);
    notify();
  });
}

function retryLater(id: SheetId): void {
  const tries = (failures.get(id) ?? 0) + 1;
  failures.set(id, tries);
  const retry = setTimeout(
    () => {
      retries.delete(retry);
      requested.delete(id);
      // Whoever draws the character now asks for it again (useAnimeSheet);
      // nobody, and it is asked for when it is drawn next.
      retryTick++;
      notify();
    },
    Math.min(RETRY_FIRST_MS * 2 ** (tries - 1), RETRY_MAX_MS),
  );
  retries.add(retry);
}

/** Test seam: forget what was loaded, load from `source` (the build's by
 * default) when asked, or start with `sheets` loaded and load nothing. */
export function resetAnimeSheets(next: { source?: SheetSource; decode?: Decode; sheets?: readonly AnimeSheet[] } = {}): void {
  for (const retry of retries) clearTimeout(retry);
  retries = new Set();
  failures = new Map();
  source = next.source ?? BUILT_IN;
  decode = next.decode ?? decodeImage;
  requested = new Set(next.sheets ? SHEET_IDS : []);
  sheets = new Map((next.sheets ?? []).map((sheet) => [sheet.id, sheet]));
  notify();
}

/** The sheet to draw a character from: while the viewer draws characters as
 * anime, once its sheet has loaded (asking for it the first time, and again
 * after a try that failed); none otherwise, and never for the person. */
export function useAnimeSheet(character: CharacterId): AnimeSheet | undefined {
  const [style] = useCharacterStyle();
  const anime = style === "anime";
  const id = sheetOf(character);
  const tick = useSyncExternalStore(subscribe, currentTick, currentTick);
  useEffect(() => {
    if (anime && id) requestSheet(id);
  }, [anime, id, tick]);
  const loaded = useSyncExternalStore(subscribe, current, current);
  return anime && id ? loaded.get(id) : undefined;
}
