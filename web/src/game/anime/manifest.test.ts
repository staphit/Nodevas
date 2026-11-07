import { describe, expect, it } from "vitest";

import { CHARACTERS } from "../character";
import fixture from "./fixture.json";
import { parseManifest, SHEET_IDS, sheetOf, toSheet } from "./manifest";

/** The fixture with some of it changed. */
const changed = (over: Record<string, unknown>) => ({ ...structuredClone(fixture), ...over });
const withAction = (name: string, action: unknown) =>
  changed({ actions: { ...structuredClone(fixture.actions), [name]: action } });

describe("parseManifest", () => {
  it("reads a manifest as the art pipeline writes it", () => {
    const manifest = parseManifest(fixture)!;
    expect(manifest).toMatchObject({ image: "fixture.webp", frameW: 192, frameH: 192, scale: 6, style: "anime" });
    expect(Object.keys(manifest.actions)).toEqual(["door-idle", "desk-type", "hike-walk", "cycle-ride"]);
    expect(manifest.actions["cycle-ride"]).toEqual({
      row: 3,
      frames: 8,
      fps: 10,
      view: "side",
      anchor: { x: 96, y: 174 },
      head: { x: 102, y: 30, w: 60, h: 54 },
      props: ["bike"],
    });
  });

  it.each([
    ["nothing", undefined],
    ["a string", "door-idle"],
    ["another style", changed({ style: "pixel" })],
    ["no image", changed({ image: "" })],
    ["an image elsewhere", changed({ image: "../gpt.webp" })],
    ["an image in a folder", changed({ image: "sheets/gpt.webp" })],
    ["a frame of no size", changed({ frameW: 0 })],
    ["a frame of part pixels", changed({ frameH: 191.5 })],
    ["no scale", changed({ scale: 0 })],
    ["no actions", changed({ actions: [] })],
    ["no action that reads", changed({ actions: { "door-idle": { row: 0 } } })],
  ])("refuses %s", (_, raw) => {
    expect(parseManifest(raw)).toBeUndefined();
  });

  it.each([
    ["no frames", { frames: 0 }],
    ["part of a frame", { frames: 2.5 }],
    ["no speed", { fps: 0 }],
    ["a row above the sheet", { row: -1 }],
    ["no anchor", { anchor: undefined }],
    ["an anchor that is not a number", { anchor: { x: "96", y: 174 } }],
    ["a head of no size", { head: { x: 60, y: 36, w: 0, h: 60 } }],
    ["props that are not names", { props: [3] }],
    ["a view of its own", { view: "top" }],
  ])("drops an action with %s and keeps the rest", (_, over) => {
    const manifest = parseManifest(withAction("door-idle", { ...fixture.actions["door-idle"], ...over }))!;
    expect(manifest.actions["door-idle"]).toBeUndefined();
    expect(Object.keys(manifest.actions)).toEqual(["desk-type", "hike-walk", "cycle-ride"]);
  });

  it("takes an action without props as one that draws none", () => {
    const bare = Object.fromEntries(Object.entries(fixture.actions["door-idle"]).filter(([key]) => key !== "props"));
    expect(parseManifest(withAction("door-idle", bare))!.actions["door-idle"].props).toEqual([]);
  });

  /** Fishing as the art pipeline writes it: the rod in the render, its tip. */
  const FISH = { ...fixture.actions["door-idle"], row: 4, props: ["rod"], tips: { rod: { x: 160, y: 58 } } };

  it("reads where a prop the render has ends: the rod's tip", () => {
    const manifest = parseManifest(withAction("fish-sit", FISH))!;
    expect(manifest.actions["fish-sit"].tips).toEqual({ rod: { x: 160, y: 58 } });
    expect(manifest.actions["fish-sit"].props).toEqual(["rod"]);
  });

  it("gives an action without tips none", () => {
    expect(parseManifest(fixture)!.actions["door-idle"]).not.toHaveProperty("tips");
    expect(parseManifest(withAction("fish-sit", { ...FISH, tips: {} }))!.actions["fish-sit"]).not.toHaveProperty("tips");
  });

  it.each([
    ["a list", [{ x: 160, y: 58 }]],
    ["a name alone", "rod"],
    ["a tip that is not a point", { rod: 160 }],
    ["a tip that is not a number", { rod: { x: "160", y: 58 } }],
    ["no tip at all", { rod: null }],
  ])("drops an action whose tips are %s and keeps the rest", (_, tips) => {
    const manifest = parseManifest(withAction("fish-sit", { ...FISH, tips }))!;
    expect(manifest.actions["fish-sit"]).toBeUndefined();
    expect(Object.keys(manifest.actions)).toEqual(["door-idle", "desk-type", "hike-walk", "cycle-ride"]);
  });
});

describe("toSheet", () => {
  it("sizes the sheet from its rows and the longest action", () => {
    const sheet = toSheet("gpt", parseManifest(fixture)!, "/sheets/fixture.webp");
    expect(sheet).toMatchObject({ id: "gpt", href: "/sheets/fixture.webp", width: 8 * 192, height: 4 * 192 });
  });
});

describe("sheetOf", () => {
  it("draws GPT, Claude, DeepSeek and Gemini from their own sheets, the person from none", () => {
    expect(SHEET_IDS).toEqual(["gpt", "claude", "deepseek", "gemini"]);
    expect(sheetOf("gpt")).toBe("gpt");
    expect(sheetOf("claude")).toBe("claude");
    expect(sheetOf("whale")).toBe("deepseek");
    expect(sheetOf("star")).toBe("gemini");
    expect(sheetOf("human")).toBeUndefined();
    for (const character of CHARACTERS) if (character !== "human") expect(SHEET_IDS).toContain(sheetOf(character));
  });
});
