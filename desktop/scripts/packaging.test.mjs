import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { test } from "node:test";

const desktop = resolve(import.meta.dirname, "..");
const build = JSON.parse(readFileSync(resolve(desktop, "package.json"), "utf8")).build;

// The NSIS installer puts an all-users install in Program Files and a
// just-me install in that user's own Programs folder: only administrators,
// or that user, may change either. A folder picked on an install-folder page
// keeps its own ACL instead: on a drive root or a data drive any signed-in
// user may then replace Nodevas.exe, resources\bin\nodevas.exe or
// postgres.exe, which run as whoever opens Nodevas next.
test("the Windows installer has no install-folder page", () => {
  assert.notEqual(
    build.nsis?.allowToChangeInstallationDirectory,
    true,
    "an install folder picked in the installer keeps an ACL other users may write",
  );
});
