import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const tag = process.argv[2]?.trim();
const runNumber = process.argv[3]?.trim() || "1";
if (!tag) throw new Error("usage: node set-release-version.mjs <tag> [run-number]");

const match = /^(testflight-)?v(\d+)\.(\d+)(?:\.(\d+))?$/.exec(tag);
if (!match) {
  throw new Error(`unsupported release tag: ${tag}`);
}

const [, testflightPrefix, major, minor, patch = "0"] = match;
const version = `${major}.${minor}.${patch}`;
const releaseLabel = testflightPrefix
  ? `testflight v${major}.${minor}${match[4] ? `.${patch}` : ""}`
  : `v${version}`;
const artifactVersion = releaseLabel.replace(" ", "-");
const channel = testflightPrefix ? "testflight" : "latest";
const releaseType = testflightPrefix ? "prerelease" : "release";
const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const packagePath = path.resolve(scriptDir, "..", "package.json");
const packageJSON = JSON.parse(fs.readFileSync(packagePath, "utf8"));

packageJSON.version = version;
packageJSON.releaseLabel = releaseLabel;
packageJSON.build.buildVersion = `${major}.${minor}.${runNumber}`;
packageJSON.build.artifactName =
  `Nodevas-${artifactVersion}-macOS-\${arch}.\${ext}`;
if (packageJSON.build.win) {
  packageJSON.build.win.artifactName =
    `Nodevas-${artifactVersion}-Windows-\${arch}.\${ext}`;
}
packageJSON.build.dmg.title = `Nodevas ${releaseLabel}`;
packageJSON.build.publish[0].channel = channel;
packageJSON.build.publish[0].releaseType = releaseType;
fs.writeFileSync(packagePath, `${JSON.stringify(packageJSON, null, 2)}\n`);

if (process.env.GITHUB_OUTPUT) {
  fs.appendFileSync(
    process.env.GITHUB_OUTPUT,
    [
      `version=${version}`,
      `release_label=${releaseLabel}`,
      `channel=${channel}`,
      `prerelease=${testflightPrefix ? "true" : "false"}`,
      "",
    ].join("\n"),
  );
}

console.log(`${tag} -> ${releaseLabel} (${version}, ${channel})`);
