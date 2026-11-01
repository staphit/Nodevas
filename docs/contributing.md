# Contributing

Create a branch, make the change, run the checks, and open a Pull Request. Feature work, bug fixes, demos, screenshots, and documentation changes can start with an Issue or Pull Request.

## Setup

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas
npm ci --prefix web
```

## Checks

```bash
gofmt -l ./cmd ./internal   # must print nothing
go vet ./...
go test ./...
govulncheck ./...           # go install golang.org/x/vuln/cmd/govulncheck@v1.8.0

npm --prefix web exec -- tsc -b
npm --prefix web run check:api-imports
npm --prefix web exec -- vitest run
npm run build --prefix web

bash scripts/install_test.sh
```

Database tests run on PostgreSQL. With nothing set, each test binary starts the embedded server (`internal/testpg`, a template initdb'd once per machine under the user cache directory) and gives every test a fresh migrated database; the first run downloads the pinned binaries. To use a server you already run instead:

```bash
NODEVAS_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:5432/postgres?sslmode=disable go test ./...
```

`NODEVAS_PG_REAL=1` makes the embedded-server tests that need the real binaries fail instead of skipping when they are missing (CI's `embedded-pg` job).

For end-to-end tests, install Playwright Chromium first. The run shares one PostgreSQL (`NODEVAS_E2E_DATABASE_URL`, or the embedded one `global-setup` starts) with a database per worker:

```bash
npm --prefix web exec -- playwright install --with-deps chromium
npm run e2e --prefix web
```

## Demo videos

`npm run demo:videos --prefix web` records the README's feature videos (dark theme, zoomed on each step, captioned) into `docs/videos/*.mp4`; it needs ffmpeg. GitHub plays a video in Markdown only from an attachment, so after re-recording, drag each MP4 into a GitHub comment box (don't post it), copy the `https://github.com/user-attachments/assets/…` URL it inserts, and put that URL on its own line where the video goes in `README*.md` or `docs/workstation*.md`.

## Main code locations

- `cmd/nodevas/`: CLI and server entry points.
- `internal/engine/`: node state, lifecycle journal, conditions, and validation.
- `internal/store/`: project files, version history, and trash.
- `internal/mcp/`: MCP server, tools, resources, and prompts.
- `internal/realtime/`: WebSocket presence and CRDT document sessions.
- `internal/server/`: middleware and the route list (`routes.go`); the handlers are in `internal/httpapi/`.
- `internal/runner/`: the runner; `internal/runs/`: the hub's run records and dispatch.
- `internal/agents/`: agent sessions, Agent chat and `ask_user`; `internal/copilot/`: the voice copilot.
- `internal/db/` and `internal/embeddedpg/`: PostgreSQL and the embedded server.
- `web/src/`: React and TypeScript interface.
- `desktop/`: the Electron desktop app.

When changing the plan/timeline UI, preserve the distinction between planned milestones in `graph.yaml` and actual lifecycle events in `run/journal.jsonl`.
