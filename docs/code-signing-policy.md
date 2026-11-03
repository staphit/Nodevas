# Code Signing Policy

This document describes how Nodevas release artifacts are built and signed.

## What gets signed

- **Windows**: the NSIS installer (`Nodevas-*-Windows-x64.exe`) and the
  portable ZIP produced by `electron-builder` from `desktop/`. These are
  currently published **unsigned**, so SmartScreen may warn; verify them with
  the checksums below. The installer has no folder picker: it installs to
  Program Files (all users) or `%LOCALAPPDATA%\Programs` (just me), and an
  upgrade keeps the folder of the previous install. An all-users folder outside
  Program Files (from an older installer or `/D=`) is given Program Files'
  permissions, so only administrators can change the files in it. A just-me
  folder outside `%LOCALAPPDATA%\Programs` is limited to SYSTEM,
  administrators and the installing user (files another account already
  owns there keep their owner).
- **macOS**: the universal DMG and ZIP produced by `electron-builder`, built
  unsigned and then signed and notarized with Apple credentials, when
  configured, by a separate job (below).

The bundled backend binary (`nodevas` / `nodevas.exe`) is embedded in these
artifacts and covered by the same signatures. The artifacts also bundle the
pinned PostgreSQL binaries: the release workflow refuses the download unless
its SHA-256 matches the manifest compiled into `internal/embeddedpg`, and on
macOS they are signed with the app.

## How releases are built

All release artifacts are built by public GitHub Actions workflows from a
tagged commit on this repository:

- [`release-windows.yml`](../.github/workflows/release-windows.yml)
- [`release-macos.yml`](../.github/workflows/release-macos.yml)

Each workflow builds the web frontend, compiles the Go backend, packages the
Electron shell and generates checksums (`SHA256SUMS-windows.txt`; on macOS the
signing job below writes `SHA256SUMS.txt`) in a build job whose
GitHub token can only read; a separate publish job, which runs no build or
dependency code, takes the artifacts (kept for one day) and uploads them to the
corresponding GitHub release. No release artifact is built on a developer
machine.

On macOS the build job holds no secrets and always builds unsigned. A
separate `sign-macos` job in the `release` environment, which checks nothing
out and runs no project or dependency code, signs the app with `codesign`
(hardened runtime) in a keychain of its own, remakes the ZIP, signs,
notarizes and staples the DMG, rewrites the update feed and writes
`SHA256SUMS.txt`; the publish job uploads what it made. `.blockmap` files are
not published on macOS.

## Who can trigger signing

Only repository maintainers can push release tags or approve the release
workflows. On macOS the approval in the `release` environment comes before
signing, on `sign-macos`, not before the build. Signing is performed
exclusively inside the CI pipeline; private keys are never present on developer machines or in the repository.

Local development builds (`npm run pack:win` / `pack:mac` in `desktop/`) are
intentionally unsigned: `desktop/scripts/pack.mjs` refuses to publish unless
`NODEVAS_SIGNED_RELEASE=1` is set together with valid signing credentials, and
substitutes unusable placeholder values for the update feed so a local build
can never impersonate a release.

## Key storage

Windows signing, once enabled, goes through [SignPath](https://signpath.io)
under the SignPath Foundation open-source program, so the certificate's
private key stays on SignPath's HSM. macOS signing credentials
(`MAC_CSC_LINK`, the base64 of the .p12, `MAC_CSC_PASSWORD` and the three
`APPLE_*` secrets) are environment secrets of `release` and are used only by
the `sign-macos` job; without `MAC_CSC_LINK` the unsigned build is published
with a warning.

## Verifying a release

Each Windows release includes `SHA256SUMS-windows.txt`, and each macOS release
`SHA256SUMS.txt`.
Verify a download with:

```bash
sha256sum -c SHA256SUMS-windows.txt
```

Signed Windows binaries can additionally be verified with
`Get-AuthenticodeSignature` in PowerShell; the certificate subject is
"SignPath Foundation".

## Privacy

Nodevas does not collect telemetry. The desktop app checks GitHub Releases for
updates; no other network calls are made without explicit user configuration
(for example mail notifications). The desktop app ships PostgreSQL; the
command-line `serve` downloads the pinned PostgreSQL from GitHub on its first
start (this project's own release mirror, then theseus-rs), unless it uses your
own PostgreSQL server or `NODEVAS_PG_OFFLINE=1` is set.
