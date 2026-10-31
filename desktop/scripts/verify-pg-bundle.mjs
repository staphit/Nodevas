// Verifies the PostgreSQL binaries bundled into a desktop release
// (desktop/resources/pg/<platform>-<arch>) before electron-builder packs them:
// the stamp `nodevas db install-postgres` wrote must name the pinned version,
// the target and the archive SHA-256 compiled into internal/embeddedpg, and
// the server binaries must be there. Used by release-windows.yml and
// release-macos.yml.
//
//   node scripts/verify-pg-bundle.mjs resources/pg/win32-x64 windows-amd64

import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

/**
 * Reads the pinned version and per-target SHA-256 from the Go manifest source.
 * @param {string} goSource contents of internal/embeddedpg/manifest.go
 * @returns {{ version: string, targets: Record<string, string> }}
 */
export function readManifest(goSource) {
  const version = /const\s+PinnedVersion\s*=\s*"([^"]+)"/.exec(goSource)?.[1];
  if (!version) {
    throw new Error("verify-pg-bundle: PinnedVersion not found in manifest.go");
  }
  /** @type {Record<string, string>} */
  const targets = {};
  for (const match of goSource.matchAll(/pinned\(\s*"([a-z0-9]+-[a-z0-9]+)"[^)]*?"([0-9a-f]{64})"/gs)) {
    targets[match[1]] = match[2];
  }
  if (Object.keys(targets).length === 0) {
    throw new Error("verify-pg-bundle: no pinned targets found in manifest.go");
  }
  return { version, targets };
}

/**
 * Throws unless dir holds the pinned binaries for target.
 * @param {string} dir
 * @param {{ version: string, target: string, sha256: string }} expected
 */
export function verifyPgBundle(dir, expected) {
  const stampPath = join(dir, "nodevas-pg.json");
  if (!existsSync(stampPath)) {
    throw new Error(`verify-pg-bundle: ${stampPath} is missing; run nodevas db install-postgres --out ${dir}`);
  }
  const stamp = JSON.parse(readFileSync(stampPath, "utf8"));
  if (stamp.version !== expected.version) {
    throw new Error(`verify-pg-bundle: version ${stamp.version}, want ${expected.version}`);
  }
  if (stamp.target !== expected.target) {
    throw new Error(`verify-pg-bundle: target ${stamp.target}, want ${expected.target}`);
  }
  if (String(stamp.archiveSha256).toLowerCase() !== expected.sha256.toLowerCase()) {
    throw new Error(`verify-pg-bundle: archive sha256 ${stamp.archiveSha256}, want ${expected.sha256}`);
  }
  const windows = expected.target.startsWith("windows");
  for (const name of ["postgres", "initdb", "pg_ctl", "pg_dump", "pg_restore"]) {
    const binary = join(dir, "bin", windows ? `${name}.exe` : name);
    if (name === "postgres" && !existsSync(binary)) {
      throw new Error(`verify-pg-bundle: ${binary} (the postgres server) is missing`);
    }
  }
}

// CLI: node scripts/verify-pg-bundle.mjs <dir> <target>
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [dir, target] = process.argv.slice(2);
  if (!dir || !target) {
    console.error("usage: node scripts/verify-pg-bundle.mjs <bundle dir> <target, e.g. windows-amd64>");
    process.exit(2);
  }
  const manifestPath = resolve(import.meta.dirname, "..", "..", "internal", "embeddedpg", "manifest.go");
  const manifest = readManifest(readFileSync(manifestPath, "utf8"));
  const sha256 = manifest.targets[target];
  if (!sha256) {
    console.error(`verify-pg-bundle: no pinned target ${target}`);
    process.exit(1);
  }
  try {
    verifyPgBundle(dir, { version: manifest.version, target, sha256 });
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exit(1);
  }
  console.log(`verify-pg-bundle: ${dir} holds PostgreSQL ${manifest.version} for ${target} (sha256 ${sha256})`);
}
