/**
 * Runs electron-builder with the publish placeholders always filled in.
 *
 * `build.publish` points at GitHub through `${env.GH_OWNER}` /
 * `${env.ELECTRON_REPO_NAME}`; electron-builder expands them even for
 * `--publish never`, so a plain local pack used to die with
 * "cannot expand pattern". CI sets both (see .github/workflows/release-*.yml);
 * locally we substitute a deliberately unusable sentinel so a stray dev build
 * can never point its update feed at a namespace we do not own.
 */

import { spawn } from "node:child_process";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
if (args.length === 0) {
  throw new Error("usage: node pack.mjs <electron-builder args…>");
}

// Nothing is uploaded unless the caller asks for it explicitly.
const publishRequested = args.some(
  (arg) => arg === "--publish" || arg.startsWith("--publish="),
);
const signedRelease = process.env.NODEVAS_SIGNED_RELEASE === "1";
if (publishRequested && !signedRelease) {
  throw new Error(
    "refusing to publish an unsigned auto-update; set NODEVAS_SIGNED_RELEASE=1 and configure platform signing credentials",
  );
}

// A placeholder owner would be baked into app-update.yml, handing the update
// channel of every shipped artifact to whoever registers that GitHub account.
// Any build that could be consumed as a release must name the real repository.
const releaseBuild = publishRequested || signedRelease;
if (releaseBuild) {
  const missing = ["GH_OWNER", "ELECTRON_REPO_NAME"].filter(
    (name) => !(process.env[name] || "").trim(),
  );
  if (missing.length > 0) {
    console.error(
      `refusing to build a release: ${missing.join(" and ")} must be set so the ` +
        "generated app-update.yml points at the repository we actually own",
    );
    process.exit(1);
  }
}

const env = {
  ...process.env,
  // Dev packs still have to satisfy electron-builder's pattern expansion, but
  // the value must be obviously unusable rather than a squattable name.
  GH_OWNER: process.env.GH_OWNER || "INVALID-LOCAL-BUILD",
  ELECTRON_REPO_NAME: process.env.ELECTRON_REPO_NAME || "INVALID-LOCAL-BUILD",
};
const signingArgs = signedRelease
  ? ["--config.forceCodeSigning=true", "--config.extraMetadata.signedUpdates=true"]
  : [];
const finalArgs = [
  ...args,
  ...signingArgs,
  ...(publishRequested ? [] : ["--publish", "never"]),
];

const desktopDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const builder = path.join(
  desktopDir,
  "node_modules",
  ".bin",
  process.platform === "win32" ? "electron-builder.cmd" : "electron-builder",
);

const child = spawn(builder, finalArgs, {
  cwd: desktopDir,
  env,
  stdio: "inherit",
  shell: process.platform === "win32",
});
child.on("exit", (code, signal) => {
  process.exit(signal ? 1 : (code ?? 1));
});
child.on("error", (error) => {
  console.error(`electron-builder failed to start: ${error.message}`);
  process.exit(1);
});
