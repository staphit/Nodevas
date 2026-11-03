import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { defaultOfficeStyle, isOfficeStyle, OFFICE_STYLES, resolveOfficeStyle } from "./officeStyle";

const fixturePath = [
  resolve(process.cwd(), "testdata/office_styles.json"),
  resolve(process.cwd(), "../testdata/office_styles.json"),
].find(existsSync);
if (!fixturePath) throw new Error("cannot locate testdata/office_styles.json");
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as {
  styles: string[];
  defaults: Record<string, string>;
  invalid: string[];
};

describe("office styles", () => {
  it("lists the same styles as the hub, in its order", () => {
    expect([...OFFICE_STYLES]).toEqual(fixture.styles);
    for (const style of fixture.styles) expect(isOfficeStyle(style)).toBe(true);
  });

  it("rejects what the hub rejects; an empty value means unset", () => {
    for (const value of fixture.invalid) expect(isOfficeStyle(value)).toBe(false);
  });

  it("defaults by layout as the hub does", () => {
    for (const [layout, style] of Object.entries(fixture.defaults)) {
      expect(defaultOfficeStyle(layout)).toBe(style);
      expect(resolveOfficeStyle({ layout })).toBe(style);
      expect(resolveOfficeStyle({ layout, officeStyle: "" })).toBe(style);
    }
  });

  it("a valid choice wins over the layout; an unknown one does not", () => {
    for (const style of fixture.styles) expect(resolveOfficeStyle({ layout: "story", officeStyle: style })).toBe(style);
    for (const value of fixture.invalid) {
      expect(resolveOfficeStyle({ layout: "story", officeStyle: value })).toBe("study");
    }
  });
});
