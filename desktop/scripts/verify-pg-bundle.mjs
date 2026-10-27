// Verifies the PostgreSQL binaries bundled into a desktop release
// (desktop/resources/pg/<platform>-<arch>) before electron-builder packs them:
// the stamp `nodevas db install-postgres` wrote must name the pinned version,
// the target and the archive SHA-256 compiled into internal/embeddedpg, and
// the server binaries must be there. Used by release-windows.yml and
// release-macos.yml.
//
//   node scripts/verify-pg-bundle.mjs resources/pg/win32-x64 windows-amd64
//
// RED PHASE: stubs.

/**
 * Reads the pinned version and per-target SHA-256 from the Go manifest source.
 * @param {string} goSource contents of internal/embeddedpg/manifest.go
 * @returns {{ version: string, targets: Record<string, string> }}
 */
export function readManifest(goSource) {
  throw new Error("verify-pg-bundle: readManifest not implemented");
}

/**
 * Throws unless dir holds the pinned binaries for target.
 * @param {string} dir
 * @param {{ version: string, target: string, sha256: string }} expected
 */
export function verifyPgBundle(dir, expected) {
  throw new Error("verify-pg-bundle: verifyPgBundle not implemented");
}
