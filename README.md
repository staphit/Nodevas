# Nodevas

Traditional Chinese documentation: [README.zh-TW.md](./README.zh-TW.md)

> A visual workspace for connecting documents, dependencies, workflows, schedules, and AI agents.

Nodevas turns scattered notes and tasks into a workspace you can see, edit, and move forward. Each node keeps its Markdown content and working context—metadata, assignees, priorities, tags, links, status, and history—in one place. Relationships are visible on the canvas, and progress can be planned and reviewed on a timeline.

It is also a personal agent workstation: the board is the control room, and each node is a task you can hand to a coding agent on one of your own machines. You watch the run live, and the change comes back as a pull request on the node.

[Quick start](./docs/getting-started.md) · [Concepts](./docs/concepts.md) · [MCP guide](./docs/mcp.md) · [Install a runner](./docs/install.md) · [Demo videos](#demo-videos)

## Features

| Feature | What it gives you |
| --- | --- |
| [Graph workspace](./docs/concepts.md#nodes-and-relationships) | See documents, tasks, and relationships as a graph, board, or timeline. |
| [Dependency gates](./docs/concepts.md#dependency-gates) | Model `MUST`, `AND`, `OR`, `XOR`, `NAND`, and `NOR` conditions and find work that is ready to start. |
| [Planning and progress](./docs/timeline.md) | Compare planned milestones with actual status transitions without mixing plan and history. |
| [MCP integration](./docs/mcp.md) | Let AI agents discover ready tasks, claim work, update nodes, manage dependencies, and report status. |
| [Agent runs](./docs/install.md) | Dispatch a node to Claude Code, Codex, Gemini CLI, OpenCode, or Antigravity CLI on your own machine. Each run gets its own git worktree and branch, and ends in a pull request. |
| [Autopilot](./docs/nodevas-workstation-plan.md) | Break a goal into a task graph, then implement, review, revise, and merge each task as its dependencies finish. |
| [Voice copilot](./COPILOT.md) | Read and edit the graph by voice or text, with Claude Code–style permission modes. |
| [Token usage](./docs/usage.md) | See what runs and agent sessions spent, by model, as API-equivalent estimates. |
| [Local or shared workspace](./docs/collaboration.md) | Run privately on a local machine or deploy one shared server for team collaboration. |

## Agent workstation

The server (the **hub**) shows the board, the runs, and their changes, but never starts a program. A **runner** (`nodevas runner`) on your own PC connects out to the hub, takes the runs you accept, and runs the agent CLIs with your own logins. Your code, your agent logins, and your `gh` login stay on the runner.

- **Runs.** Each run is either headless, streaming its events to the node, or interactive, with the agent's TUI in your browser. The runner commits the work, pushes, and opens a PR with `gh`, then reports the diff and CI status.
- **Models.** Choose the agent and model per run, or let routing rules and fallback chains choose. Rate limits, used-up quota, and missing CLIs fall through to the next model in the chain. OpenCode can also be driven over the [Agent Client Protocol](https://agentclientprotocol.com) (`nodevas runner set opencodeProtocol acp`, experimental), where each tool call streams as it happens and any permission it asks for is refused, since nobody is at a headless run to grant it.
- **Review pipelines.** An implement run can be followed by a review from other agents or models, a person, or both. Requested changes go back for a revise run.
- **Agent network.** Agents see who else is working, message each other, and can ask you a question (`ask_user`) through the hub.
- **Safe defaults.**
  - Runs need your acceptance at the runner's terminal. Remote dispatch needs a step-up passcode and an auto-accept allowlist you set on the runner.
  - Remote merge is off until you turn it on.
  - Reviews and plans run read only, without the repository's own agent config.
  - Put the hub behind Cloudflare Access; the runner connects with a service token.
  - Turn on two-factor authentication with an authenticator app (**Project settings → Account security**). Sign-in then needs the app's code too, and step-up asks the app instead of sending email.

Set it up in two steps:

1. Deploy the hub, for example [on TrueNAS behind Cloudflare Tunnel](./deploy/truenas/README.md).
2. On the hub, open **Runners → Link a new device** and copy the one-time link. On your PC, run [`scripts/install.sh --hub <url> --link`](./docs/install.md#set-up-a-new-pc-with-a-device-link) with the repositories that runs may use, and paste the link when asked. The link works once, for ten minutes, and carries the hub's Access service token if an administrator stored one, so nothing is copied from Cloudflare by hand.

## Beta

The following capabilities are available today but still in beta, and may change between releases:

- [Agent workstation](./docs/install.md): runners, agent runs, review pipelines, and autopilot.
- [MCP integration](./docs/mcp.md): AI agent tools, resources, and prompts.
- [Real-time co-editing](./docs/collaboration.md): shared servers, WebSocket presence, and CRDT document sessions.
- Backup: scheduled snapshots to a local folder, and restore.
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

## Demo videos

Recorded from the real app by `npm run demo:videos` in `web/`; the runner, agent and hub data in them are staged. Click a clip for the full-size MP4.

### Dispatch a node to an agent

Pick a runner, agent and model; accept it at the runner; watch OpenCode work over ACP; read the diff; merge the PR.

[![Dispatch a node to OpenCode, watch the run, read the diff and merge](./docs/videos/agent-run.gif)](./docs/videos/agent-run.mp4)

### Autopilot

A goal broken into tasks. Each one is implemented, checked by CI, reviewed by other models and revised, then waits for you.

[![Autopilot moving tasks through implement, review and human review](./docs/videos/autopilot.gif)](./docs/videos/autopilot.mp4)

### Two-factor authentication and device links

Turn on an authenticator app, save the recovery codes, then link a new PC: step-up asks the app, and the hub hands back a one-time link.

[![Setting up two-factor authentication, then creating a device link](./docs/videos/two-factor-and-device-link.gif)](./docs/videos/two-factor-and-device-link.mp4)

### Token usage

Tokens and API-equivalent cost by day, agent, model and session.

[![The usage dialog: daily chart, sessions and prices](./docs/videos/usage.gif)](./docs/videos/usage.mp4)

### Agent network

Who is working on what, messages between agents, and a held message waiting for your approval.

[![The agent network panel approving a held message](./docs/videos/agent-network.gif)](./docs/videos/agent-network.mp4)

## Also included

- Multiple projects and subprojects in one workspace tree.
- Markdown, plain text, HTML, and Word document pages.
- Imports from Markdown, JSON Canvas, and `.veproj`; exports to Word, plain text, HTML, Markdown, and PDF.
- Filters and saved views for status, assignee, label, priority, and project.
- Drafts, history, trash, conflict detection, and filesystem change notifications.
- Inspectable project files that remain useful with Git and ordinary text tools.

## Quick start

Requirements: Go 1.26.10, Node.js 22 with npm, and Git.

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
- [Install a runner](./docs/install.md): `scripts/install.sh` sets up the machine your agents run on in one command.
- [Concepts](./docs/concepts.md): nodes, edges, dependency gates, readiness, and node metadata.
- [Timeline](./docs/timeline.md): planned milestones, actual events, and date assessment.
- [MCP integration](./docs/mcp.md): connect Claude Code, Codex, or another MCP client.
- [Token usage](./docs/usage.md): what runs and agent sessions spent, and how costs are estimated.
- [Voice copilot](./COPILOT.md): the in-app AI copilot, its tools, and its permission modes.
- [Workstation plan](./docs/nodevas-workstation-plan.md) (Traditional Chinese): hub and runner design, agent adapters, security, and autopilot.
- [Storage and collaboration](./docs/collaboration.md): project files, shared servers, WebSocket, and CRDT behavior.
- [OCI deployment](./deploy/oci/README.md): provision and operate a shared cloud deployment.
- [TrueNAS deployment](./deploy/truenas/README.md): run Nodevas behind Cloudflare Tunnel without publishing an origin port.
- [Contributing](./docs/contributing.md): development checks and repository locations.

## Code signing

Windows releases are planned to be signed with free code signing provided by [SignPath.io](https://signpath.io), certificate by the [SignPath Foundation](https://signpath.org). See the [code signing policy](./docs/code-signing-policy.md).

## License

Nodevas is licensed under the [GNU General Public License v3.0](./LICENSE).
