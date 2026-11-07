import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { OFFICE_STYLES } from "../officeStyle";
import * as Activity from "./activitySprites";
import { compileSprite, type Sprite } from "./compile";
import * as Outdoor from "./outdoorSprites";
import { PIXEL_TOKENS } from "./palette";
import * as SkinClaude from "./skinClaude";
import * as SkinStar from "./skinStar";
import * as SkinWhale from "./skinWhale";
import * as S from "./sprites";
import * as Styles from "./styleSprites";

/** Every sprite module of the war room (the characters' skins too). */
const MODULES = [S, Styles, Activity, Outdoor, SkinClaude, SkinWhale, SkinStar];
const allSprites = () => MODULES.flatMap((module) => Object.entries(module));

function isSprite(value: unknown): value is Sprite {
  return typeof value === "object" && value !== null && "layers" in value && "w" in value;
}

describe("compileSprite", () => {
  it("measures the grid and merges each row's runs per token", () => {
    const sprite = compileSprite(
      `
        .oo.
        okko
      `,
      { o: "outline", k: "skin" },
    );
    expect(sprite).toMatchObject({ w: 4, h: 2 });
    const byToken = Object.fromEntries(sprite.layers.map((layer) => [layer.token, layer.d]));
    expect(byToken.outline).toBe("M1 0h2v1h-2zM0 1h1v1h-1zM3 1h1v1h-1z");
    expect(byToken.skin).toBe("M1 1h2v1h-2z");
  });

  it("refuses a pixel the legend does not name, and a ragged row", () => {
    expect(() => compileSprite("ox", { o: "outline" })).toThrow(/"x"/);
    expect(() => compileSprite("oo\no", { o: "outline" })).toThrow(/row 1/);
    expect(() => compileSprite("\n", { o: "outline" })).toThrow(/empty/);
  });

  it("compiles every sprite of the war room at load, on whole pixels", () => {
    for (const [name, value] of allSprites()) {
      if (!isSprite(value)) continue;
      expect(value.w, name).toBeGreaterThan(0);
      for (const layer of value.layers) expect(layer.d, name).toMatch(/^(M\d+ \d+h\d+v1h-\d+z)+$/);
    }
    expect(S.MONITOR.w).toBe(S.CELL);
    expect(S.MONITOR.h).toBe(S.DESK_TOP_Y);
  });

  it("gives every office style a desk top of 32×14 and a chair 14 wide", () => {
    for (const style of OFFICE_STYLES) {
      const desk = Styles.deskTopOf(style);
      expect([desk.w, desk.h], style).toEqual([S.CELL, 14]);
      expect(Styles.chairOf(style).w, style).toBe(S.CHAIR.w);
      const table = Styles.tableSegOf(style);
      expect([table.w, table.h], style).toEqual([S.CELL, 14]);
      expect(Styles.wallDecorOf(style).length, style).toBeGreaterThan(0);
      expect(Styles.floorDecorOf(style).h, style).toBeLessThanOrEqual(S.CELL);
    }
    // The table's ends match its segments' height.
    expect([S.TABLE_CAP_L.h, S.TABLE_CAP_R.h]).toEqual([14, 14]);
  });

  it("names only palette tokens in its legend", () => {
    const tokens = new Set<string>(PIXEL_TOKENS);
    for (const [char, token] of Object.entries(S.LEGEND)) expect(tokens.has(token), `${char} → ${token}`).toBe(true);
    for (const [name, value] of allSprites()) {
      if (!isSprite(value)) continue;
      for (const layer of value.layers) expect(tokens.has(layer.token), `${name}: ${layer.token}`).toBe(true);
    }
  });

  it("draws the outdoors' backdrops a whole tile each, and every activity screen inside the screen", () => {
    for (const name of ["HIKE_BACKDROP", "STREAM_BACKDROP", "FISH_BACKDROP", "CAMP_BACKDROP", "BBQ_BACKDROP", "CYCLE_BACKDROP"] as const) {
      expect([Outdoor[name].w, Outdoor[name].h], name).toEqual([Outdoor.OUTDOOR_W, Outdoor.OUTDOOR_H]);
    }
    for (const [name, value] of Object.entries(Activity)) {
      if (!isSprite(value) || name === "ARM_CHIN" || name === "MOUSE" || name.startsWith("THOUGHT")) continue;
      expect(value.w, name).toBeLessThanOrEqual(S.SCREEN.w);
      expect(value.h, name).toBeLessThanOrEqual(S.SCREEN.h);
    }
  });
});

describe("the palette in the stylesheet", () => {
  const css = readFileSync(resolve(process.cwd(), "src/game/warroom/warroom.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  /** The custom properties a rule sets, by its exact selector. */
  const declared = (selector: string) => {
    const at = css.indexOf(`\n${selector} {`);
    expect(at, selector).toBeGreaterThanOrEqual(0);
    const body = css.slice(at, css.indexOf("}", at));
    return new Set([...body.matchAll(/(--px-[a-z0-9-]+)\s*:/g)].map((match) => match[1]));
  };

  it("gives every token a colour in the light theme and in the dark one", () => {
    const light = declared(".px-office");
    const dark = declared('[data-theme="dark"] .px-office');
    for (const token of PIXEL_TOKENS) {
      if (token === "agent" || token === "agent-dark") continue; // the agent--<kind> classes
      expect(light.has(`--px-${token}`), `light --px-${token}`).toBe(true);
      expect(dark.has(`--px-${token}`), `dark --px-${token}`).toBe(true);
    }
  });

  it("leaves the outdoors' colours to the theme: no office style redefines them", () => {
    const OUTDOOR_TOKENS = PIXEL_TOKENS.slice(PIXEL_TOKENS.indexOf("sky"));
    for (const style of OFFICE_STYLES) {
      const own = declared(`.px-style--${style}`);
      for (const token of OUTDOOR_TOKENS) expect(own.has(`--px-${token}`), `${style} --px-${token}`).toBe(false);
    }
  });
});
