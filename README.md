# Nodevas

[繁體中文](./README.zh-TW.md)

> A visual workspace for your notes, tasks, and AI agents.

Put your work on a canvas as nodes, connect them, and see what is ready to start. When a task is ready, hand it to a coding agent on your own machine and get a pull request back.

![Nodevas feature tour](./docs/screenshots/nodevas-feature-tour.gif)

## What you can do

- **Plan visually.** Each node is a Markdown page with a type, status, assignee, priority, and tags. View them on a dependency graph and a timeline.
- **Model dependencies.** Link nodes with conditions like `AND` or `OR`; Nodevas shows which work is ready.
- **Hand work to agents.** Send a node to Claude Code, Codex, Gemini CLI, OpenCode, or Antigravity CLI. Watch it run live, review the diff, merge the PR.
- **Answer your agents.** Agents can ask for your approval, ask you a question, or offer a choice; a runner can also relay the permission prompts of Claude Code, Antigravity CLI and OpenCode (over ACP) once you turn that on (it is off by default; see [Security model](#security-model)). Answer in Agent chat or Station.
- **Connect any MCP client.** Agents can find ready tasks, claim them, and report back.

## Board, Station and Settings

Every project has three faces, switched in the top bar:

- **Board** is where you plan: graph, timeline, pages and dependencies.
- **Station** is where your agents work together: what waits on you (questions, permission prompts, reviews, PRs to merge or fix, usage limits, the daily budget), the live war room, who is working on what, who did what in the last hour, today's spend, your runners, and Agent chat. Autopilot drives a goal's tasks from here, and **Pause everything** pauses your autopilots and cancels your runs that have not started.
- **Settings** has a search box and four sections: **General** (appearance, language, text size, notifications, voice copilot), **Agents** (runners, and under Advanced the daily budget, routing rules, roles, workflow stages, usage details and agent performance), **This project** (lifecycle, node types, office style, milestone types, people, backup and history), and **Account** (on a team server, your account and two-factor sign-in, plus server accounts and the audit log for administrators).

![Station: the agents, the war room, who worked on what, and what waits on you](./docs/screenshots/station-warroom.png)

![War room: the workspace as a pixel-art building, with this project's office, the other projects' offices, a lounge for idle agents, and a server room with a rack per runner](./docs/screenshots/game-warroom.png)

Every agent, item, office, and rack in the war room is a button: an agent's menu opens its run, changes, node or goal, pauses its autopilot, or cancels the run. When an agent asks you something or wants to run a tool, hits its usage limit, or the budget runs out, the browser tells you.

## Quick start

**Standalone, on one machine.** No account, mail, or two-factor sign-in. Needs Go 1.26.10, Node.js 22, and Git:

```bash
git clone https://github.com/staphit/Nodevas.git && cd Nodevas
npm ci --prefix web && npm run build --prefix web && go build -o nodevas ./cmd/nodevas
mkdir -p workspace && ./nodevas serve --project ./workspace
```

Open <http://127.0.0.1:5666>. The first start downloads a pinned PostgreSQL build once (about 40–93 MB); press Ctrl+C twice to stop. The same command starts a runner for coding agents; add `--repo ~/code/my-app` to let runs work in a repository, or `--no-runner` to leave it out. More, including Docker on Linux: [Standalone](./docs/getting-started.md#standalone).

**Team server.** Run the hub with Docker (`ghcr.io/staphit/nodevas`) on a server or NAS behind HTTPS, with an admin account (`nodevas user add`) and an SMTP relay for sign-in passcodes; people open it in a browser. Each person can add a runner on their PC with `scripts/install.sh --hub <url> --link`, which downloads a GitHub-verified build, or builds from a checkout with Go 1.26 or newer ([details](./docs/install.md)). Steps: [Team server](./docs/getting-started.md#team-server).

## Run agents on your machine

https://github.com/user-attachments/assets/8e494381-8aeb-4c58-9cf8-251f4c0f3060

The server shows the board; a small **runner** on your PC runs the agents with your own logins, in your own repositories, and only the runs you accept. Setup, security, autopilot, and more demos: [Agent workstation](./docs/workstation.md).

The agent controls are hidden at first. They appear when you turn on **Settings → Agents → Let agents work here** or open Station, and by themselves once the workspace has a runner, a run, or a connected agent.

## Security model

- **Standalone and desktop trust this machine.** A standalone hub (loopback, with no proxy, TLS or Access) has no accounts, so any program running on this computer, under any user, can reach it and act as you. Use them on a single-user machine. A per-launch token that closes this is a possible future change; open an issue if you need it.
- **A team hub** needs accounts, HTTPS and mail. Settings that decide whom it trusts (public host name, proxy, TLS, Cloudflare Access, copilot bypass, a copilot or SMTP server off the machine) come from `--config` / `NODEVAS_CONFIG`, the environment or flags, never from the workspace's own `nodevas.yaml`; see [Operations](./docs/operations.md#configuration).
- **The runner runs what the hub sends, within its own limits.** You accept each run at its terminal, read-only runs stay read only, and an agent that may run any shell command is left to a person. Auto-accept is off by default: keep it off, or limited to repositories and agents you would let anyone who controls the hub run.
- **The permission relay is a trade-off.** With `permissionRelay` on, your **Allow** reaches the runner through the hub, so a compromised hub could forge one. It never widens a read-only run or the CLI's own deny rules. It is off by default; keep it off unless you trust the hub.
- **Known limits.** Someone who knows a member's sign-in address can keep that member from signing in for a few minutes at a time by typing wrong passcodes. Behind a proxy, a `trusted_proxy` that misses the proxy makes the hub take HTTPS requests for plain HTTP: its cookies lose `Secure` and it sends no HSTS. A standalone hub or dev server reached through a proxy or tunnel on this machine that adds no forwarding header (raw TCP tunnels, nginx with default headers) treats the visitor as you.

## Learn more

- [Concepts](./docs/concepts.md) · [Timeline](./docs/timeline.md) · [MCP](./docs/mcp.md) · [Voice copilot](./COPILOT.md) · [Token usage](./docs/usage.md)
- [Install a runner](./docs/install.md) · [Operations](./docs/operations.md) · [History and backup](./docs/history-and-backup.md) · [Team server and co-editing](./docs/collaboration.md)
- Examples: [AI harness](./demo/ai-harness-engineering) · [Daily schedule](./demo/daily-work-schedule) · [Novel writing](./demo/novel-writing)
- [Contributing](./docs/contributing.md) · [Code signing policy](./docs/code-signing-policy.md)

The agent workstation, MCP, co-editing, backup, and version history are in beta and may change.

## License

[GPL-3.0](./LICENSE)
