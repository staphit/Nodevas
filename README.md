# Nodevas

Traditional Chinese documentation: [README.zh-TW.md](./README.zh-TW.md)

> A visual workspace for connecting documents, dependencies, workflows, schedules, and AI agents.

Nodevas turns scattered notes and tasks into a workspace you can see, edit, and move forward. Each node keeps its Markdown content and working context—metadata, assignees, priorities, tags, links, status, and history—in one place. Relationships are visible on the canvas, and progress can be planned and reviewed on a timeline.

[Quick start](./docs/getting-started.md) · [Concepts](./docs/concepts.md) · [MCP guide](./docs/mcp.md)

## Features

| Feature | What it gives you |
| --- | --- |
| [Graph workspace](./docs/concepts.md#nodes-and-relationships) | See documents, tasks, and relationships as a graph, board, or timeline. |
| [Dependency gates](./docs/concepts.md#dependency-gates) | Model `MUST`, `AND`, `OR`, `XOR`, `NAND`, and `NOR` conditions and find work that is ready to start. |
| [Planning and progress](./docs/timeline.md) | Compare planned milestones with actual status transitions without mixing plan and history. |
| [MCP integration](./docs/mcp.md) | Let AI agents discover ready tasks, claim work, update nodes, manage dependencies, and report status. |
| [Local or shared workspace](./docs/collaboration.md) | Run privately on a local machine or deploy one shared server for team collaboration. |

## Beta

The following capabilities are available today but still in beta, and may change between releases:

- [MCP integration](./docs/mcp.md): AI agent tools, resources, and prompts.
- [Real-time co-editing](./docs/collaboration.md): shared servers, WebSocket presence, and CRDT document sessions.
- Cloud backup: Google Drive sync, backup, and restore.
- Version history: document drafts, past revisions, and restore.

## Use cases

- [AI harness engineering](./demo/ai-harness-engineering): dependency-aware queues that agents can safely work through with MCP.
- [Daily work schedule](./demo/daily-work-schedule): priorities, ownership, planned milestones, and timeline assessment.
- [Novel writing](./demo/novel-writing): scenes, choices, characters, gates, and revision history as one story graph.

## See it in action

The feature tour keeps the browser viewport at its normal scale and enlarges only the relevant local UI area in a cyan focus window.

![Nodevas feature tour with local focus windows](./docs/screenshots/nodevas-feature-tour.gif)

Readable focused captures and the full screenshot index are available in [`docs/screenshots/`](./docs/screenshots/README.md).

The interface defaults to English and can switch to Traditional Chinese (`zh-TW`). The language and dark-mode capture is available at [nodevas-language-zh-tw.png](./docs/screenshots/nodevas-language-zh-tw.png).

## Also included

- Multiple projects and subprojects in one workspace tree.
- Markdown, plain text, HTML, and Word document pages.
- Imports from Markdown, JSON Canvas, and `.veproj`; exports to Word, plain text, HTML, Markdown, and PDF.
- Filters and saved views for status, assignee, label, priority, and project.
- Drafts, history, trash, conflict detection, and filesystem change notifications.
- Inspectable project files that remain useful with Git and ordinary text tools.

## Quick start

Requirements: Go 1.25.12, Node.js 22 with npm, and Git.

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas
npm ci --prefix web
npm run build --prefix web
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

Open <http://127.0.0.1:5666>. For production builds, development mode, configuration, and network access, see [Getting started](./docs/getting-started.md).

## Documentation

- [Getting started](./docs/getting-started.md): build, run, configure, and expose a server safely.
- [Concepts](./docs/concepts.md): nodes, edges, dependency gates, readiness, and node metadata.
- [Timeline](./docs/timeline.md): planned milestones, actual events, and date assessment.
- [MCP integration](./docs/mcp.md): connect Claude Code, Codex, or another MCP client.
- [Storage and collaboration](./docs/collaboration.md): project files, shared servers, WebSocket, and CRDT behavior.
- [OCI deployment](./deploy/oci/README.md): provision and operate a shared cloud deployment.
- [Contributing](./docs/contributing.md): development checks and repository locations.

## License

Nodevas is licensed under the [GNU General Public License v3.0](./LICENSE).
