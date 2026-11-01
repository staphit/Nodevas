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

// A just-me install goes to the user's own Programs folder, which only that
// user (and SYSTEM and administrators) may change. One in any other folder
// (an install made with the old folder page, a /D= install, and every
// upgrade of either) keeps that folder's ACL: on a drive root every
// signed-in user may then replace Nodevas.exe, which that user runs next.
// The installer gives such a folder the ACL the user's own profile has.
test("the Windows installer locks down a just-me folder outside the user's Programs folder", () => {
  const script = readFileSync(resolve(desktop, build.nsis.include), "utf8");
  const macro = /!macro customInstall\b([\s\S]*?)!macroend/.exec(script)?.[1] ?? "";
  const mine = /\$\{(?:if|elseif)\} \$installMode == "CurrentUser"([\s\S]*?)\$\{endif\}\s*$/.exec(macro)?.[1] ?? "";
  assert.notEqual(mine, "", "a just-me install is not locked down");
  assert.match(mine, /"\$LOCALAPPDATA\\Programs\\"/, "the user's own Programs folder is left as it is");
  assert.match(mine, /\$SYSDIR\\whoami\.exe" \/user/, "the user's SID comes from whoami in the system folder");
  const lock = mine.split("\n").find((line) => line.includes("/inheritance:r")) ?? "";
  assert.match(lock, /\*S-1-5-18:\(OI\)\(CI\)F/, "SYSTEM keeps full control");
  assert.match(lock, /\*S-1-5-32-544:\(OI\)\(CI\)F/, "administrators keep full control");
  assert.match(lock, /\*\$\d:\(OI\)\(CI\)F/, "the user keeps full control, so updates still install");
  assert.doesNotMatch(lock, /S-1-5-32-545|S-1-5-11|S-1-1-0/, "no other user may read or change it");
  assert.match(mine, /\/reset \/T/, "files already there lose ACEs of their own");
  // Only the SID whoami prints is granted: without one the folder stays as
  // it is rather than locking the user out of their own install.
  assert.match(mine, /',"S-1-'/, "the SID is read from whoami's CSV line");
  assert.match(mine, /\$\{if\} \$1 < 8/, "no grant without a SID");
});
