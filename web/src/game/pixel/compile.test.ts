import { describe, expect, it } from "vitest";

import { OFFICE_STYLES } from "../officeStyle";
import { compileSprite, type Sprite } from "./compile";
import { PIXEL_TOKENS } from "./palette";
import * as S from "./sprites";
import * as Styles from "./styleSprites";

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
    for (const [name, value] of [...Object.entries(S), ...Object.entries(Styles)]) {
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
    for (const [name, value] of [...Object.entries(S), ...Object.entries(Styles)]) {
      if (!isSprite(value)) continue;
      for (const layer of value.layers) expect(tokens.has(layer.token), `${name}: ${layer.token}`).toBe(true);
    }
  });
});
