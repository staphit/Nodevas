// The sheets the art pipeline drops into sheets/ (<id>.json and <id>.webp,
// web/scripts/gemini), checked against the frame contract the war room
// places them by (manifest.ts), and run through the scenes that draw them.
// With no sheets there yet, there is nothing to check.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { ACTIVITIES } from "../activity";
import { CHARACTERS, type CharacterId } from "../character";
import { OUTING_KINDS, OUTING_STEPS } from "../outing";
import { OUTDOOR_H, OUTDOOR_W, RING_A } from "../pixel/outdoorSprites";
import type { Placed } from "../pixel/placed";
import { CELL } from "../pixel/sprites";
import { deskScene, meetingScene, outdoorScene, sofaScene } from "../warroom/deskArt";
import { POSES } from "../world";
import { ANIME_ACTIONS, type AnimeActionName } from "./actions";
import { parseManifest, SHEET_IDS, sheetOf, SOLES_Y, toSheet, type SheetId } from "./manifest";

const MANIFESTS = import.meta.glob<unknown>("./sheets/*.json", { eager: true, import: "default" });
/** The folder beside this file, wherever the tests run from. */
const SHEETS_DIR = join(dirname(fileURLToPath(import.meta.url)), "sheets");

/** A WebP file's width and height, from its first chunk. */
function webpSize(bytes: Buffer): { width: number; height: number } | undefined {
  if (bytes.toString("ascii", 0, 4) !== "RIFF" || bytes.toString("ascii", 8, 12) !== "WEBP") return undefined;
  const chunk = bytes.toString("ascii", 12, 16);
  if (chunk === "VP8X") return { width: 1 + bytes.readUIntLE(24, 3), height: 1 + bytes.readUIntLE(27, 3) };
  if (chunk === "VP8L") {
    const bits = bytes.readUInt32LE(21);
    return { width: 1 + (bits & 0x3fff), height: 1 + ((bits >> 14) & 0x3fff) };
  }
  if (chunk === "VP8 ") return { width: bytes.readUInt16LE(26) & 0x3fff, height: bytes.readUInt16LE(28) & 0x3fff };
  return undefined;
}

/** Every action's anchor but the seated ones' (frame px): the soles' line
 * (a desk action's desk unit stands on it too), or the wheels' ground, at
 * the frame's centre. */
const ANCHOR = { x: 96, y: 174 };

/** The seated actions, from the front: their anchor is the seat contact,
 * the bottom of the hips on the seat, (96, y) with y from 100 up to the
 * soles' (manifest.ts SOLES_Y, 170, not included), the legs hanging below
 * it in front of the seat. */
const SEATED: readonly AnimeActionName[] = ["sofa-sit", "sofa-sleep", "camp-sit", "fish-sit"];
const SEAT_Y = { min: 100, max: SOLES_Y };

/** Where the scenes put a seated anchor (art px): the seat's top, for the
 * seat contact; under the person's shoes, for a sheet's from before, its
 * soles' line. */
const SEATS = {
  sofa: { seat: { x: 12, y: 22 }, shoes: { x: 12, y: 27 } },
  camp: { seat: { x: 33, y: 35 }, shoes: { x: 33, y: 39 } },
  fish: { seat: { x: 11, y: 33 }, shoes: { x: 11, y: 36 } },
};

/** Sheets not rendered again since the seated actions' anchor became the
 * seat contact: theirs is still the soles' line (96, 174), which the scenes
 * put under the person's shoes, as before. A sheet comes off the list once
 * it has the seat contact (this test then says so). */
const SEATED_ON_SOLES: Partial<Record<SheetId, string>> = {};

/** The props each render has (figure.ts PROP_PARTS), which the scene then
 * leaves out; a desk action's desk unit goes whatever its manifest says. */
const PROPS: Partial<Record<AnimeActionName, string[]>> = {
  "hike-walk": ["pack"],
  "cycle-ride": ["bike"],
  "stream-wade": ["pole"],
  "bbq-grill": ["tongs"],
  "fish-sit": ["rod"],
};

/** Sheets whose fishing the war room draws in pixel art for now, and why:
 * their rod's tip is not one the line can hang from to the bobber on the
 * water (manifest.ts, `tips`), so the scene keeps the skin's figure, rod and
 * all. The art pipeline's to fix; a sheet comes off the list once its
 * render fishes (this test then says so). */
const FISHING_HELD: Partial<Record<SheetId, string>> = {};

/** Where PixelDesk lays a run's role tag and test lamp, and the held
 * messages' count, over a desk's drawing (warroom.css), in art px. */
const TAG = { x: CELL - 11, y: 2, w: 11, h: 8 };
const COUNT = { x: 5, y: 5, w: 8, h: 6 };

/** The rings' row, the water's first column on it, and the bobber's row. */
const RING_Y = 26;
const WATER_X = 21;
const BOBBER_Y = 21;

const sheets = Object.entries(MANIFESTS)
  .map(([path, raw]) => ({ id: path.slice("./sheets/".length, -".json".length) as SheetId, raw }))
  // Only the characters' own: the pipeline's mannequin is not drawn.
  .filter(({ id }) => SHEET_IDS.includes(id));

/** Who a sheet draws. */
const characterOf = (id: SheetId): CharacterId => CHARACTERS.find((character) => sheetOf(character) === id)!;

const inFrame = (x: number, y: number) => x >= 0 && y >= 0 && x <= 192 && y <= 192;

const covers = (part: Placed, box: { x: number; y: number; w: number; h: number }) =>
  part.x < box.x + box.w && box.x < part.x + part.sprite.w && part.y < box.y + box.h && box.y < part.y + part.sprite.h;
const overlap = (a: Placed, b: Placed) => covers(a, { x: b.x, y: b.y, w: b.sprite.w, h: b.sprite.h });

describe("the anime sheets in sheets/", () => {
  it("each read whole, in the war room's frame, every action of the contract once", () => {
    for (const { id, raw } of sheets) {
      const manifest = parseManifest(raw);
      expect(manifest, `${id}.json reads`).toBeDefined();
      const actions = (raw as { actions: Record<string, unknown> }).actions;
      // No action dropped as unreadable, none missing.
      expect(Object.keys(manifest!.actions), id).toEqual(Object.keys(actions));
      expect(Object.keys(actions).sort(), id).toEqual([...ANIME_ACTIONS].sort());
      expect(manifest, id).toMatchObject({ image: `${id}.webp`, frameW: 192, frameH: 192, scale: 6, style: "anime", character: id });
      Object.entries(manifest!.actions).forEach(([name, action], row) => {
        const where = `${id} ${name}`;
        expect(action, where).toMatchObject({ row, frames: 8, fps: 10 });
        if (!SEATED.includes(name as AnimeActionName)) expect(action.anchor, `${where} anchor`).toEqual(ANCHOR);
        else if (SEATED_ON_SOLES[id]) expect(action.anchor, `${where}: its seat contact is there now, so take it off SEATED_ON_SOLES`).toEqual(ANCHOR);
        else {
          // The seat contact: on the frame's centre line, above the soles'.
          expect(action.anchor.x, `${where} anchor`).toBe(ANCHOR.x);
          expect(action.anchor.y >= SEAT_Y.min && action.anchor.y < SEAT_Y.max, `${where} anchor y ${action.anchor.y}, the seat contact's from ${SEAT_Y.min} to ${SEAT_Y.max}`).toBe(true);
        }
        const { head } = action;
        expect(inFrame(head.x, head.y) && inFrame(head.x + head.w, head.y + head.h), `${where} head`).toBe(true);
        expect(action.props, where).toEqual(expect.arrayContaining(PROPS[name as AnimeActionName] ?? []));
        for (const [prop, tip] of Object.entries(action.tips ?? {})) expect(inFrame(tip.x, tip.y), `${where} ${prop} tip`).toBe(true);
      });
      // Fishing gives the rod's tip, where the line hangs from.
      expect(manifest!.actions["fish-sit"].tips?.rod, `${id} fish-sit rod tip`).toBeDefined();
      // The image is there, as big as its rows of eight frames.
      const size = webpSize(readFileSync(join(SHEETS_DIR, `${id}.webp`)));
      const sheet = toSheet(id, manifest!, "");
      expect(size, `${id}.webp`).toEqual({ width: sheet.width, height: sheet.height });
    }
  });

  it("fills a desk's cell with each desk unit, its badge clear of the bubbles, the mail, a role tag and the count, at a desk and at the table", () => {
    for (const { id, raw } of sheets) {
      const anime = toSheet(id, parseManifest(raw)!, `/sheets/${id}.webp`);
      const character = characterOf(id);
      for (const pose of POSES) {
        for (const activity of [undefined, ...ACTIVITIES]) {
          for (const [tagged, mail, mailCount] of [
            [false, false, false],
            [false, true, false],
            [true, true, false],
            [false, true, true],
            [true, true, true],
          ]) {
            const options = { character, anime, activity, mail, tagged, mailCount };
            for (const [where, parts] of [
              ["desk", deskScene(pose, "codex", options)],
              ["table", meetingScene(pose, "codex", { ...options, edge: "m" })],
            ] as const) {
              const at = `${id} ${where} ${pose} ${activity ?? ""}${tagged ? " tagged" : ""}${mail ? " mail" : ""}${mailCount ? "s" : ""}`;
              const figure = parts.find((part) => part.anime);
              expect(figure, at).toBeDefined();
              expect({ x: figure!.x, y: figure!.y, w: figure!.sprite.w }, at).toEqual({ x: 0, y: 0, w: CELL });
              const badge = parts.find((part) => /^badge\.[a-z]+$/.test(part.name));
              expect(badge, at).toBeDefined();
              for (const part of parts.filter((other) => other.name.startsWith("bubble.") || other.name === "mail")) {
                expect(overlap(badge!, part), `${at} ${part.name}`).toBe(false);
              }
              if (tagged) expect(covers(badge!, TAG), `${at} role tag`).toBe(false);
              if (mailCount) expect(covers(badge!, COUNT), `${at} count`).toBe(false);
              for (const part of parts) {
                if (part.anime) continue;
                expect(part.x >= 0 && part.y >= 0 && part.x + part.sprite.w <= CELL && part.y + part.sprite.h <= CELL, `${at} ${part.name}`).toBe(true);
              }
            }
          }
        }
      }
    }
  });

  it("hangs each sheet's fishing line from its rod's tip down to the bobber, its rings on the water, or fishes in pixel art while it is held", () => {
    for (const { id, raw } of sheets) {
      const anime = toSheet(id, parseManifest(raw)!, `/sheets/${id}.webp`);
      const character = characterOf(id);
      for (let step = 0; step < OUTING_STEPS; step++) {
        const where = `${id} ${step}`;
        const parts = outdoorScene({ kind: "fish", step }, "codex", { character, anime });
        const drawn = parts.some((part) => part.name === "anime.fish-sit");
        if (FISHING_HELD[id]) {
          expect(drawn, `${where}: its render fishes now, so take it off FISHING_HELD`).toBe(false);
          expect(parts, where).toEqual(outdoorScene({ kind: "fish", step }, "codex", { character }));
          continue;
        }
        expect(drawn, `${where}: the rod's tip cannot hang the line to the bobber on the water (manifest.ts tips)`).toBe(true);
        const find = (name: string) => parts.find((part) => part.name === name)!;
        const line = find("fish.line");
        expect(line.sprite.h, `${where} line`).toBeGreaterThanOrEqual(2);
        expect(line.y + line.sprite.h, `${where} line`).toBe(BOBBER_Y + 1);
        const ring = find("fish.ring.a");
        expect(ring.y, where).toBe(RING_Y);
        expect(ring.x >= WATER_X && ring.x + RING_A.w <= OUTDOOR_W, `${where} rings on the water`).toBe(true);
        for (const part of parts) {
          if (part.anime) continue;
          expect(part.x >= 0 && part.y >= 0 && part.x + part.sprite.w <= OUTDOOR_W && part.y + part.sprite.h <= OUTDOOR_H, `${where} ${part.name}`).toBe(
            true,
          );
        }
      }
    }
  });

  it("holds back fishing, and the seat contact, only for sheets that are there", () => {
    for (const id of [...Object.keys(FISHING_HELD), ...Object.keys(SEATED_ON_SOLES)]) expect(sheets.map((sheet) => sheet.id), id).toContain(id);
  });

  it("seats each sheet's seated figures on the seat's top (the sofa's cushion, the log's, the rock's), or a sheet's from before on its soles under the shoes", () => {
    for (const { id, raw } of sheets) {
      const manifest = parseManifest(raw)!;
      const anime = toSheet(id, manifest, `/sheets/${id}.webp`);
      const character = characterOf(id);
      const scenes: [AnimeActionName, keyof typeof SEATS, Placed[]][] = [
        ["sofa-sit", "sofa", sofaScene("idle", "codex", { character, anime })],
        ["sofa-sleep", "sofa", sofaScene("limit", "codex", { character, anime })],
        ["camp-sit", "camp", outdoorScene({ kind: "camp", step: 3 }, "codex", { character, anime })],
        ["fish-sit", "fish", outdoorScene({ kind: "fish", step: 3 }, "codex", { character, anime })],
      ];
      for (const [action, seat, parts] of scenes) {
        const where = `${id} ${action}`;
        const figure = parts.find((part) => part.anime);
        if (action === "fish-sit" && FISHING_HELD[id]) {
          expect(figure, where).toBeUndefined();
          continue;
        }
        expect(figure?.name, where).toBe(`anime.${action}`);
        // The frame's top-left, the scene's px (art px × 6): the anchor on
        // the seat's top, or under the shoes.
        const point = SEATED_ON_SOLES[id] ? SEATS[seat].shoes : SEATS[seat].seat;
        const { anchor } = manifest.actions[action];
        const left = Math.round(figure!.x * 6 - figure!.anime!.window.x);
        const top = Math.round(figure!.y * 6 - figure!.anime!.window.y);
        expect({ left, top }, where).toEqual({ left: point.x * 6 - anchor.x, top: point.y * 6 - anchor.y });
      }
    }
  });

  it("draws every outing from each sheet with none of the pixel props its render has, the wader cut at the water", () => {
    for (const { id, raw } of sheets) {
      const anime = toSheet(id, parseManifest(raw)!, `/sheets/${id}.webp`);
      const character = characterOf(id);
      for (const kind of OUTING_KINDS) {
        for (let step = 0; step < OUTING_STEPS; step++) {
          const where = `${id} ${kind} ${step}`;
          const parts = outdoorScene({ kind, step }, "codex", { character, anime });
          const names = parts.map((part) => part.name);
          if (kind === "fish" && FISHING_HELD[id]) continue;
          expect(parts.filter((part) => part.anime), where).toHaveLength(1);
          for (const name of names) expect(/^(hike\.pack|cycle\.bike|cycle\.wheel\..*|stream\.pole|bbq\.tongs\..*|bbq\.apron|fish\.rod)$/.test(name), `${where} ${name}`).toBe(false);
          if (kind === "stream") {
            const figure = parts.find((part) => part.anime)!;
            const wade = parts.find((part) => part.name === "stream.wade")!;
            expect(figure.y + figure.sprite.h, where).toBe(wade.y + 2);
          }
        }
      }
    }
  });

  it("holds only the characters' manifests and their WebP sheets", () => {
    // The manifests the build finds are this folder's: it is there whenever they are.
    expect(existsSync(SHEETS_DIR) || sheets.length === 0).toBe(true);
    const files = existsSync(SHEETS_DIR) ? readdirSync(SHEETS_DIR) : [];
    expect(files.filter((file) => file.endsWith(".json")).sort()).toEqual(Object.keys(MANIFESTS).map((path) => path.slice("./sheets/".length)).sort());
    for (const file of files) {
      const [, id, kind] = /^([\w-]+)\.(json|webp)$/.exec(file) ?? [];
      expect(SHEET_IDS as readonly string[], file).toContain(id);
      expect(files, file).toContain(`${id}.${kind === "json" ? "webp" : "json"}`);
    }
  });
});
