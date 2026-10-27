import { describe, expect, it } from "vitest";

import { compileSprite } from "./compile";
import * as S from "./sprites";

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
    for (const [name, value] of Object.entries(S)) {
      if (typeof value !== "object" || !value || !("layers" in value)) continue;
      expect(value.w, name).toBeGreaterThan(0);
      for (const layer of value.layers) expect(layer.d, name).toMatch(/^(M\d+ \d+h\d+v1h-\d+z)+$/);
    }
    expect(S.DESK.w).toBe(S.CELL);
  });
});
