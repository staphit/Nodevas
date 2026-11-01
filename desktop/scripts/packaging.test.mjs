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

// An all-users install made before the folder page was dropped may sit in
// such a folder, and every upgrade installs into the folder recorded for it
// (the NSIS template reads InstallLocation back), as does a /D= install. The
// installer's own script locks an all-users folder outside Program Files
// down to what Program Files allows: administrators and SYSTEM change it,
// users only read and run it.
test("the Windows installer locks down an all-users folder it did not pick", () => {
  assert.equal(typeof build.nsis?.include, "string", "the installer has no script of its own");
  const script = readFileSync(resolve(desktop, build.nsis.include), "utf8");
  const macro = /!macro customInstall\b([\s\S]*?)!macroend/.exec(script)?.[1] ?? "";
  assert.match(macro, /\$installMode == "all"/, "only an all-users install is locked down");
  assert.match(macro, /\$SYSDIR\\icacls\.exe/, "icacls runs from the system folder, not the download folder");
  const lock = macro.split("\n").find((line) => line.includes("/inheritance:r")) ?? "";
  assert.match(lock, /\*S-1-5-18:\(OI\)\(CI\)F/, "SYSTEM keeps full control");
  assert.match(lock, /\*S-1-5-32-544:\(OI\)\(CI\)F/, "administrators keep full control");
  assert.match(lock, /\*S-1-5-32-545:\(OI\)\(CI\)RX/, "users may only read and run");
  assert.doesNotMatch(lock, /S-1-5-11|S-1-1-0|\(M\)|:M\b|\(W\)/, "no one else may write");
  assert.match(macro, /\/reset \/T/, "files already there lose ACEs of their own");
  assert.match(macro, /\/setowner \*S-1-5-32-544/, "files someone else made no longer belong to them");
});
