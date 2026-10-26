# Agent workstation

Traditional Chinese: [workstation.zh-TW.md](./workstation.zh-TW.md)

The server (the **hub**) shows the board, the runs, and their changes, but never starts a program. A **runner** (`nodevas runner`) on your own PC connects out to the hub, takes the runs you accept, and runs the agent CLIs with your own logins. Your code, your agent logins, and your `gh` login stay on the runner.

- **Runs.** Each run is either headless, streaming its events to the node, or interactive, with the agent's TUI in your browser. The runner commits the work, pushes, and opens a PR with `gh`, then reports the diff and CI status.
- **Models.** Choose the agent and model per run, or let routing rules and fallback chains choose. Rate limits, used-up quota, and missing CLIs fall through to the next model in the chain. OpenCode can also be driven over the [Agent Client Protocol](https://agentclientprotocol.com) (`nodevas runner set opencodeProtocol acp`, experimental, OpenCode 1.x only; OpenCode 2 runs fall back to `opencode run`), where each tool call streams as it happens and any permission it asks for is refused, since nobody is at a headless run to grant it.
- **Review pipelines.** An implement run can be followed by a review from other agents or models, a person, or both. Requested changes go back for a revise run.
- **Agent network.** Agents see who else is working, message each other, and can ask you a question (`ask_user`) through the hub.
- **Station.** Each project's Station (the top bar's Info | Station | Settings switch) puts all of this in one place:
  - the agents and what each is doing;
  - the war room or boss map;
  - who worked on what in the last hour;
  - what waits on you;
  - the agents' conversation, with a composer;
  - Autopilot, for a goal: the **Autopilot** pill at the top of the goal's node panel opens it here.
  
  Questions, usage limits and the daily budget also raise a browser notification.
- **Safe defaults.**
  - Runs need your acceptance at the runner's terminal. Remote dispatch needs a step-up passcode and an auto-accept allowlist you set on the runner.
  - Remote merge is off until you turn it on.
  - Reviews and plans run read only, without the repository's own agent config.
  - Put the hub behind Cloudflare Access; the runner connects with a service token.
  - Turn on two-factor authentication with an authenticator app (**Settings → Your account**). Sign-in then needs the app's code too, and step-up asks the app instead of sending email.

Set it up in two steps:

1. Deploy the hub: see [Getting started: Team server](./getting-started.md#team-server).
2. On the hub, open **Settings → Runners → Link a new device** and copy the one-time link. On your PC, run [`scripts/install.sh --hub <url> --link`](./install.md#set-up-a-new-pc-with-a-device-link) with the repositories that runs may use, and paste the link when asked. The link works once, for ten minutes, and carries the hub's Access service token if an administrator stored one, so nothing is copied from Cloudflare by hand.

Alone on one machine, one command starts both: `nodevas serve --project ./workspace [--repo ~/code/myapp]` (`--no-runner` for the hub alone). The runner still runs as its own process with its own token, and you still accept each run, in the terminal running `serve`. See [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine).

## Demo videos

Recorded from the real app by `npm run demo:videos` in `web/`; the runner, agent and hub data in them are staged. Click a clip for the full-size MP4.

### Dispatch a node to an agent

Pick a runner, agent and model; accept it at the runner; watch OpenCode work over ACP; read the diff; merge the PR.

https://github.com/user-attachments/assets/8e494381-8aeb-4c58-9cf8-251f4c0f3060

### Autopilot

The goal's **Autopilot** pill in the node panel opens it in Station. The goal is broken into tasks; each one is implemented, checked by CI, reviewed by other models and revised, then waits for you.

https://github.com/user-attachments/assets/1108f05c-50f3-414c-8541-ad1fab9fbbe9

### Two-factor authentication and device links

In **Settings → Your account**, turn on an authenticator app and save the recovery codes. Then link a new PC from **Settings → Runners**: step-up asks the app, and the hub hands back a one-time link.

https://github.com/user-attachments/assets/f7f93bb4-304d-4c0f-b39a-5af4d7acebe8

### Token usage

Tokens and API-equivalent cost by day, agent, model and session, in **Settings → Usage**; the daily budget is a part of its own.

https://github.com/user-attachments/assets/57324ad9-5170-495a-8024-159ce63102aa

### Agent network

Who is working on what, messages between agents, and a held message waiting for your approval.

https://github.com/user-attachments/assets/fa0c5a33-1d02-41bd-9dbc-6ff0aac0e75d

