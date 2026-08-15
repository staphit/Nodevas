#!/usr/bin/env bash
# Rebuilds the app and, optionally, restarts it.
#
# The binary embeds web/dist at compile time, so a frontend change is only
# visible after both builds run in this order:
#
#     npm run build   (tsc + vite → web/dist)
#     go build        (embeds web/dist into the binary)
#
# Forgetting the second step is why a fixed UI can still look broken: the
# running server serves the bundle from whenever it was last compiled.
#
# Usage:
#   scripts/dev.sh                                      build web, no repo exe
#   scripts/dev.sh --run --project /path/to/workspace  build and run via go run
#   scripts/dev.sh --build-binary --run                explicitly build nodevas.exe
#   scripts/dev.sh --skip-web                          Go-only change
#   scripts/dev.sh --test                              also run the test suites

set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

skip_web=0
run=0
test=0
build_binary=0
project=""
port=5666

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-web) skip_web=1 ;;
    --run) run=1 ;;
    --build-binary) build_binary=1 ;;
    --test) test=1 ;;
    --project) project="$2"; shift ;;
    --port) port="$2"; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

step() { printf '\033[36m==> %s\033[0m\n' "$1"; }

binary="nodevas"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) binary="nodevas.exe" ;;
esac

# Stop any running Nodevas server instance.
stop_running() {
  if [ "$binary" = "nodevas.exe" ]; then
    for image in nodevas.exe; do
      if tasklist 2>/dev/null | grep -qi "^$image"; then
        step "stopping running $image"
        taskkill //F //IM "$image" >/dev/null 2>&1 || true
        sleep 0.4
      fi
    done
  else
    pkill -f "$repo/nodevas serve" 2>/dev/null || true
  fi
}

stop_port_owner() {
  if ! command -v lsof >/dev/null 2>&1; then
    stop_running
    return
  fi

  local pids pid name
  pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
  for pid in $pids; do
    name="$(ps -p "$pid" -o comm= | tr -d '[:space:]')"
    case "$name" in
      nodevas|nodevas.exe)
        step "stopping $name PID $pid on port $port"
        kill "$pid" 2>/dev/null || true
        ;;
      *)
        echo "port $port is already used by PID $pid ($name); stop it or choose --port" >&2
        return 1
        ;;
    esac
  done
  sleep 0.4
}

if [ "$test" -eq 1 ]; then
  step "go test"
  go test ./...
  step "vitest"
  npm --prefix web run test
fi

if [ "$skip_web" -eq 0 ]; then
  step "building web (tsc + vite)"
  npm --prefix web run build
fi

if [ "$run" -eq 1 ]; then
  stop_port_owner
elif [ "$build_binary" -eq 1 ]; then
  # A running server can hold the standalone binary lock.
  stop_running
fi

if [ "$build_binary" -eq 1 ]; then
  step "building $binary (embeds web/dist)"
  go build -tags nomsgpack -o "$binary" ./cmd/nodevas
  ls -l --time-style=+%H:%M:%S "$binary" | awk '{printf "    %s  %s\n", $5, $6}'

  if [ "$run" -eq 1 ]; then
    step "serving $binary on http://127.0.0.1:$port  (Ctrl+C to stop)"
    echo "    hard-reload the page (Ctrl+Shift+R) so the browser drops the old bundle"
    if [ -n "$project" ]; then
      exec "./$binary" serve --project "$project" --port "$port"
    else
      exec "./$binary" serve --port "$port"
    fi
  fi
  printf '\033[32m==> done. %s built; start it with: ./%s serve --port %s\033[0m\n' "$binary" "$binary" "$port"
elif [ "$run" -eq 1 ]; then
  step "serving via go run on http://127.0.0.1:$port  (Ctrl+C to stop)"
  echo "    no nodevas.exe is created in the repository"
  echo "    hard-reload the page (Ctrl+Shift+R) so the browser drops the old bundle"
  if [ -n "$project" ]; then
    exec go run ./cmd/nodevas serve --project "$project" --port "$port"
  else
    exec go run ./cmd/nodevas serve --port "$port"
  fi
else
  printf '\033[32m==> web build done. No nodevas.exe was created.\033[0m\n'
fi
