import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { describe, expect, it } from "vitest";
import { SOURCE_SCAN_TIMEOUT } from "./sourceScan";

// A test that walks the whole of web/src reads hundreds of files
// synchronously. Alone that is well under a second; under a full parallel
// run beside the Go suite it has taken over 5 s, vitest's default, and
// failed a correct tree (listFormat, noHardcodedCjk). Each such test gives
// itself the shared, longer budget.
const SRC = join(__dirname);

/** Walks a directory at collection time, outside any test's timeout. */
const WALKS_OUTSIDE_A_TEST = new Set(["components/settings/TextSize.test.tsx"]);

function testFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return testFiles(path);
    return /\.test\.tsx?$/.test(name) ? [path] : [];
  });
}

describe("tests that walk the source tree", () => {
  it("allow the shared source-scan timeout", () => {
    const offenders = testFiles(SRC)
      .map((path) => relative(SRC, path).split(sep).join("/"))
      .filter((file) => !WALKS_OUTSIDE_A_TEST.has(file))
      .filter((file) => {
        const text = readFileSync(join(SRC, file), "utf8");
        return /isDirectory\(\)/.test(text) && !/\bSOURCE_SCAN_TIMEOUT\)/.test(text);
      });
    expect(offenders).toEqual([]);
  }, SOURCE_SCAN_TIMEOUT);
});
