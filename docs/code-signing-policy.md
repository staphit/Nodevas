# Code Signing Policy

This document describes how Nodevas release artifacts are built and signed.

## What gets signed

- **Windows**: the NSIS installer (`Nodevas-*-Windows-x64.exe`) and the
  portable ZIP produced by `electron-builder` from `desktop/`. These are
  currently published **unsigned**, so SmartScreen may warn; verify them with
  the checksums below.
- **macOS**: the universal DMG and ZIP produced by `electron-builder`
  (signed and notarized with Apple credentials when configured).

The bundled backend binary (`nodevas` / `nodevas.exe`) is embedded in these
artifacts and covered by the same signatures.

## How releases are built

All release artifacts are built by public GitHub Actions workflows from a
tagged commit on this repository:

- [`release-windows.yml`](../.github/workflows/release-windows.yml)
- [`release-macos.yml`](../.github/workflows/release-macos.yml)

Each workflow builds the web frontend, compiles the Go backend, packages the
Electron shell, generates `SHA256SUMS-*.txt` checksums, and uploads the
artifacts to the corresponding GitHub release. No release artifact is built on
a developer machine.

## Who can trigger signing

Only repository maintainers can push release tags or approve the release
workflows. Signing is performed exclusively inside the CI pipeline; private
keys are never present on developer machines or in the repository.

Local development builds (`npm run pack:win` / `pack:mac` in `desktop/`) are
intentionally unsigned: `desktop/scripts/pack.mjs` refuses to publish unless
`NODEVAS_SIGNED_RELEASE=1` is set together with valid signing credentials, and
substitutes unusable placeholder values for the update feed so a local build
can never impersonate a release.

## Key storage

Windows signing, once enabled, goes through [SignPath](https://signpath.io)
under the SignPath Foundation open-source program, so the certificate's
private key stays on SignPath's HSM. macOS signing credentials are
stored as GitHub Actions secrets and used only inside the release workflow.

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
(for example mail notifications).
