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

## Main code locations

- `cmd/nodevas/`: CLI and server entry points.
- `internal/engine/`: node state, lifecycle journal, conditions, and validation.
- `internal/mcp/`: MCP server, tools, resources, and prompts.
- `internal/realtime/`: WebSocket presence and CRDT document sessions.
- `internal/server/`: HTTP API, WebSocket routing, and file management.
- `web/src/`: React and TypeScript interface.
- `deploy/oci/`: OCI Terraform, bootstrap, deployment, backup, and restore scripts.

When changing the plan/timeline UI, preserve the distinction between planned milestones in `graph.yaml` and actual lifecycle events in `run/journal.jsonl`.
