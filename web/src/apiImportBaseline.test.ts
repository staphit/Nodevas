// @vitest-environment node
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// scripts/check-api-imports.mjs holds the line on components that call the api
// directly instead of going through a state/ slice. It only ran by hand, so two
// pop-out components slipped past it; the suite now runs it.
describe("api import baseline", () => {
  it("no component outside the recorded baseline imports the api module", () => {
    const run = spawnSync(process.execPath, [resolve(process.cwd(), "scripts/check-api-imports.mjs")], {
      cwd: process.cwd(),
      encoding: "utf8",
    });
    expect(run.stderr).toBe("");
    expect(run.status).toBe(0);
  });
});
