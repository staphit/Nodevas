import { describe, expect, it } from "vitest";
import { safeCssColor } from "./cssColor";

describe("safeCssColor", () => {
  it("keeps colours", () => {
    for (const v of ["#31566a", "#fff", "red", "rgb(1, 2, 3)", "hsl(200 50% 40%)"]) {
      expect(safeCssColor(v)).toBe(v);
    }
  });
  it("drops anything that can fetch", () => {
    for (const v of ["url(https://a.co/x)", "url(x)", 'image-set("x")', String.raw`\75rl(x)`, "red;x:y"]) {
      expect(safeCssColor(v)).toBeUndefined();
    }
  });
});
