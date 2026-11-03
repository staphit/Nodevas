import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test } from "node:test";
import { readManifest, verifyPgBundle } from "./verify-pg-bundle.mjs";

const repo = resolve(import.meta.dirname, "..", "..");
const pinned = {
  "windows-amd64": "4dee656719d4182055219765c29c9e692993d14ba0d1b4344dd24a8ff7296782",
  "darwin-amd64": "edb44b25cf8bffec60fa300fbe100fa4238bf2cab4f4bd98d4b7255c61780df3",
  "darwin-arm64": "f4b2ff0587d80b6f22bf09ec2d133a3c2949b6423318d3fed0f4ddddddf2f004",
};

function bundle({ version = "17.11.1", target = "windows-amd64", sha = pinned[target], binary = true } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "pg-bundle-"));
  mkdirSync(join(dir, "bin"), { recursive: true });
  if (binary) {
    writeFileSync(join(dir, "bin", target.startsWith("windows") ? "postgres.exe" : "postgres"), "pg");
  }
  writeFileSync(
    join(dir, "nodevas-pg.json"),
    JSON.stringify({ version, target, archiveSha256: sha }),
  );
  return dir;
}

test("reads the pinned manifest from the Go source", () => {
  const manifest = readManifest(readFileSync(join(repo, "internal", "embeddedpg", "manifest.go"), "utf8"));
  assert.equal(manifest.version, "17.11.1");
  for (const [target, sha] of Object.entries(pinned)) {
    assert.equal(manifest.targets[target], sha, target);
  }
});

test("accepts a bundle whose stamp matches the manifest", () => {
  const dir = bundle();
  try {
    verifyPgBundle(dir, { version: "17.11.1", target: "windows-amd64", sha256: pinned["windows-amd64"] });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("refuses a stamp from another archive, version or target, and a missing server", () => {
  const expected = { version: "17.11.1", target: "windows-amd64", sha256: pinned["windows-amd64"] };
  for (const [name, options, pattern] of [
    ["sha", { sha: "0".repeat(64) }, /sha/i],
    ["version", { version: "17.11.0" }, /version/i],
    ["target", { target: "darwin-arm64" }, /target/i],
    ["binary", { binary: false }, /postgres/i],
  ]) {
    const dir = bundle(options);
    try {
      assert.throws(() => verifyPgBundle(dir, expected), pattern, name);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }
});
