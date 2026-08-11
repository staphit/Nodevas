#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS DMG must be built on macOS (hdiutil and codesign are required)." >&2
  exit 1
fi

tag="${1:-$(tr -d '\r\n' < VERSION)}"

# GH_OWNER/ELECTRON_REPO_NAME end up in the generated app-update.yml. A
# placeholder there would hand the update channel of any shipped artifact to
# whoever registers that GitHub namespace, so a signed/publishable build must
# name the real repository; a dev build gets an obviously unusable sentinel.
if [[ "${NODEVAS_SIGNED_RELEASE:-}" == "1" ]]; then
  if [[ -z "${GH_OWNER:-}" || -z "${ELECTRON_REPO_NAME:-}" ]]; then
    echo "NODEVAS_SIGNED_RELEASE=1 requires GH_OWNER and ELECTRON_REPO_NAME to be set" >&2
    echo "so the update feed points at the repository we actually own." >&2
    exit 1
  fi
fi
export GH_OWNER="${GH_OWNER:-INVALID-LOCAL-BUILD}"
export ELECTRON_REPO_NAME="${ELECTRON_REPO_NAME:-INVALID-LOCAL-BUILD}"
node desktop/scripts/set-release-version.mjs "${tag}" "${BUILD_NUMBER:-1}"

npm ci --prefix web
npm run build --prefix web

mkdir -p desktop/build desktop/resources/bin
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
  -tags nomsgpack -trimpath -ldflags="-s -w" \
  -o desktop/build/nodevas-arm64 ./cmd/nodevas
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build \
  -tags nomsgpack -trimpath -ldflags="-s -w" \
  -o desktop/build/nodevas-amd64 ./cmd/nodevas
lipo -create \
  desktop/build/nodevas-arm64 \
  desktop/build/nodevas-amd64 \
  -output desktop/resources/bin/nodevas
chmod +x desktop/resources/bin/nodevas

npm ci --prefix desktop
npm run pack:mac --prefix desktop

echo
echo "macOS release created in: ${repo_root}/desktop/dist"
