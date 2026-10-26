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
go vet ./...
go test ./...

npm --prefix web exec -- tsc -b
npm --prefix web run check:api-imports
npm --prefix web exec -- vitest run
npm run build --prefix web
```

For end-to-end tests, install Playwright Chromium first:

```bash
npm --prefix web exec -- playwright install --with-deps chromium
npm run e2e --prefix web
```

## Demo videos

`npm run demo:videos --prefix web` records the README's feature videos (dark theme, zoomed on each step, captioned) into `docs/videos/*.mp4`; it needs ffmpeg. GitHub plays a video in Markdown only from an attachment, so after re-recording, drag each MP4 into a GitHub comment box (don't post it), copy the `https://github.com/user-attachments/assets/…` URL it inserts, and put that URL on its own line where the video goes in `README*.md` or `docs/workstation*.md`.

## Main code locations

- `cmd/nodevas/`: CLI and server entry points.
- `internal/engine/`: node state, lifecycle journal, conditions, and validation.
- `internal/mcp/`: MCP server, tools, resources, and prompts.
- `internal/realtime/`: WebSocket presence and CRDT document sessions.
- `internal/server/`: HTTP API, WebSocket routing, and file management.
- `web/src/`: React and TypeScript interface.

When changing the plan/timeline UI, preserve the distinction between planned milestones in `graph.yaml` and actual lifecycle events in `run/journal.jsonl`.
