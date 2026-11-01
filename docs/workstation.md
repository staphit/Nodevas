# Agent workstation

Traditional Chinese: [workstation.zh-TW.md](./workstation.zh-TW.md)

The server (the **hub**) shows the board, the runs, and their changes, but never starts a program. A **runner** (`nodevas runner`) on your own PC connects out to the hub, takes the runs you accept, and runs the agent CLIs with your own logins. Your code, your agent logins, and your `gh` login stay on the runner, and it runs only what you accept, within its own limits (see the [Security model](../README.md#security-model)).

- **Runs.** Each run is either headless, streaming its events to the node, or interactive, with the agent's TUI in your browser. The runner commits the work, pushes, and opens a PR with `gh`, then reports the diff and CI status.
- **Models.** Choose the agent and model per run, or let routing rules and fallback chains choose. Rate limits, used-up quota, and missing CLIs fall through to the next model in the chain. OpenCode can also be driven over the [Agent Client Protocol](https://agentclientprotocol.com) (`nodevas runner set opencodeProtocol acp`, experimental, OpenCode 1.x only; OpenCode 2 runs fall back to `opencode run`), where each tool call streams as it happens; permission prompts are covered under [Permission prompts](#permission-prompts-off-by-default).
- **Review pipelines.** An implement run can be followed by a review from other agents or models, a person, or both. Requested changes go back for a revise run. A runner pushes revisions only to branches its own runs opened for that node (its PR watch list); revising another member's PR, or one whose watch entry is gone (a closed PR reopened, say), is declined.
- **Agent chat.** Agents see who else is working, message each other, and can stop to ask you something through the hub (`ask_user`); see [Answering your agents](#answering-your-agents).
- **Station.** Each project's Station (the top bar's Board | Station | Settings switch) puts all of this in one place:
  - **Pause everything**, at the top: it pauses the autopilots you started and cancels runs that have not started (optionally the running ones too). **Resume all** then resumes those autopilots where they stopped;
  - **Waiting on you**: questions and permission prompts, held messages, reviews, PRs to merge or fix, runs a runner has not accepted yet, usage limits and the daily budget;
  - Autopilot, for a goal: the **Autopilot** button at the top of the goal's node panel opens it here;
  - the war room: the workspace as a pixel-art building, with this project's office, the other projects' offices, a lounge for idle agents and a server room with a rack per runner. Each agent is a button: its menu opens its run, changes, node or goal, pauses or resumes its autopilot, or cancels the run;
  - who worked on what in the last hour;
  - at the side, today's spend, the runners, and Agent chat with a composer.

  Questions, permission prompts, usage limits and the daily budget also raise a browser notification.
- **Safe defaults.**
  - Runs need your acceptance at the runner's terminal. Remote dispatch needs a step-up passcode and an auto-accept allowlist you set on the runner.
  - Remote merge is off until you turn it on.
  - Reviews, plans and judges (goal QA and checklist checks) run read only, without the repository's own agent config; Claude Code also gets no WebFetch, WebSearch or Monitor there.
  - Agent config a run adds or changes (`.claude/settings.json`, `.mcp.json`, `.codex/`, `.gemini/`, `opencode.json` and the like) is left out of its commit, and the worktree is kept so you can commit it yourself. `nodevas runner status` lists kept worktrees; `nodevas runner prune-kept [--dry-run]` removes those with nothing but the left-out config (branches stay); nothing removes them on its own.
  - Permission prompts are refused at a headless run unless you [relay them](#permission-prompts-off-by-default), and an auto-accepted run is relayed only with `allow-shell` on.
  - The project explorer's **Files** tab shows your runners' repositories to you alone, read-only, and hides files that look like secrets (`.env`, keys, `.ssh`, credentials). `nodevas runner set repoBrowse off` and a runner restart turn that off.
  - Put the hub behind Cloudflare Access; the runner connects with a service token.
  - Turn on two-factor authentication with an authenticator app (**Settings → Account**). Sign-in then needs the app's code too, and step-up asks the app instead of sending email.

The agent parts of the app (runners, runs, autopilot, Agent chat and usage) stay hidden until you turn on **Settings → Agents → Let agents work here**, or until this workspace has a runner, a run or a connected agent.

Set it up in two steps:

1. Deploy the hub: see [Getting started: Team server](./getting-started.md#team-server).
2. On the hub, open **Settings → Agents**, turn on **Let agents work here**, then **Link a new device** and copy the one-time link. On your PC, run [`scripts/install.sh --hub <url> --link`](./install.md#set-up-a-new-pc-with-a-device-link) with the repositories that runs may use, and paste the link when asked. The link works once, for ten minutes, and carries the hub's Access service token if an administrator stored one, so nothing is copied from Cloudflare by hand.

Alone on one machine, one command starts both: `nodevas serve --project ./workspace [--repo ~/code/myapp]` (`--no-runner` for the hub alone). The runner still runs as its own process with its own token, and you still accept each run, in the terminal running `serve`. See [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine).

## Answering your agents

An agent can stop and ask you through Nodevas (`ask_user`): an approval (**Approve** or **Deny**, with an optional note), a question you answer in words, or a choice of 2 to 8 options. A question or a choice can also be left unanswered with **Dismiss**. The agent waits up to 10 minutes.

Answer in Agent chat (Ctrl+Shift+A, or its card at the side of Station), from Station's **Waiting on you**, or from the agent in the war room; the browser notifies you. A run's timeline (node panel → **Activity** → the run) lists each of its asks and how it ended, and asks still open when the run ends expire. Only the person whose agent asked, or an administrator, can answer.

### Permission prompts (off by default)

A headless run has nobody to ask, so whatever its CLI would ask permission for is refused. To be asked instead, run `nodevas runner set permissionRelay true` on the runner and restart it (for the runner `nodevas serve` starts, add the `--config` path that `serve` prints at start). Then these send their prompts to Nodevas:

- Claude Code, in any permission mode but `plan` and `dontAsk`;
- Antigravity in `accept-edits`, for shell commands;
- OpenCode over ACP in `edit`, for shell commands.

Codex, Gemini CLI and `opencode run` cannot relay prompts, and an interactive run asks in its own terminal. A relayed run's start line ends with "permission prompts relayed to Nodevas".

Only the runner's owner can answer a permission prompt, in the same places as other asks:

- **Allow**: this call only.
- **Allow *tool* for the rest of this run**: later calls of that tool in the run go ahead without asking. For a shell command the button is **Allow this exact command for the rest of this run** and covers that command only. Neither is offered when the input was cut at 4 KB.
- **Deny**.

The web has no note field on permission prompts. A prompt nobody answers is denied after 5 minutes, and open prompts expire when the run ends.

With the relay on, your answer reaches the runner through the hub, so a compromised hub could allow a prompt you never saw. Keep it off unless you trust the hub. It never widens a run: the CLI asks only after its own deny rules and permission mode, so a read-only run stays read only. A run auto-accepted without `nodevas runner autoaccept allow-shell on` is not relayed; its start line ends with "permission prompts not relayed: auto-accepted without allow-shell (denied headless)".

## Usage limits and handoff

When a headless implement run stops because its agent hit a usage limit or a rate limit, the runner keeps the partial work instead of throwing it away:

- It commits the worktree to a local branch, `nodevas/handoff/<run id>`, on the runner only. Nothing is pushed and no PR is opened.
- It writes a **handoff note**: what the agent was asked, its last messages, the files changed, its last command, and what was left of its own plan. Secrets are redacted and the note is capped at 8 KB.
- The next run of the task continues from that branch, whether it is the next model in the fallback chain or the same model after the limit resets. The note goes into its prompt as data, not instructions. The PR still targets the original base, and the review covers the whole diff.
- With no fallback left, the run waits for the limit to reset. The time comes from the agent's own message ("resets 3pm", "try again in 2 hours", Retry-After) or a default backoff (5 minutes for a rate limit, 1 hour for a quota). The run shows **Waiting for limit reset · retry at HH:MM** with **Retry now** and **Cancel**. The wait survives a hub restart. When it ends, the run is offered like any other: the runner still has to accept it, and the daily budget still applies. A task waits at most three times. Autopilot counts a waiting task as queued, not failed.
- The runner only continues from handoff branches it made itself, for the same repository and node, still at the commit it recorded. The hub can name such a branch but not any other ref. Each time the runner makes a handoff branch, it deletes those older than 30 days or beyond the newest 200, if they are still at their recorded commit.

A model the login cannot use, or a missing CLI, fails before the agent's first turn. There is nothing to keep then and waiting would not help, so those failures only fall through the chain, as before.

## Agent roles and workflow stages

Each run acts in a **role**: Planner, QA, Tester, Executor, Reviewer, Researcher, or a project's own `custom-role-*`. A role decides which MCP tools the run is shown (fewer tool definitions, fewer tokens on every model call) and what the hub lets it do. The hub enforces the role on every request, whatever the agent sends: a Tester cannot claim, an Executor writes only its own node and cannot set it done while stages are pending, a Reviewer writes nothing, and no agent can change a project's roles, stages or locked acceptance criteria. A run's role comes from its run record, never from a header.

Each project picks its **workflow stages** in **Settings → Agents → Advanced → Workflow stages · \<project\>**: Light (Executor only, about 1× the tokens), Standard (Tester → Executor → Reviewer, about 2–3×), Full (Standard plus goal QA, a CI gate and a person's review of the goal, about 3–4×), or Custom. A goal can override its project's stages. New projects start on Standard; existing projects keep today's behavior until someone picks a preset. Who runs each role (agent, model, fallbacks, budget), what it may do, and custom roles are under **Settings → Agents → Advanced → Roles**.

Under Standard, a task needs **acceptance criteria** (shown at the top of its Content tab) before a Tester or Executor runs. The staged pipeline is: the Tester writes failing tests and opens a draft PR → the runner runs the repository's test command and the tests must **fail** (red) → the Executor makes them pass → the test command must **pass** (green) → the hub checks the Executor's diff did not touch the Tester's files or test infrastructure → CI (Full) → review → the hub marks the node done, with the red, green and guard evidence on its history.

The test command lives on the runner only; the hub learns only that one is set:

```bash
nodevas runner repo set myapp --test-command "go test ./..." [--test-timeout 20]
nodevas runner autoaccept verify on   # optional: with auto-accept on, take verify jobs unasked for its listed repos
```

The command runs without a shell, so write `sh -c '…'` for `&&` or pipes; `--test-command ""` clears it. The timeout is in minutes (default 20, at most 180). Restart the runner after a change.

A verify job runs no agent and no model. It runs code an agent wrote, outside that agent's sandbox, as you, so it is opt-in per repository and has its own auto-accept switch (off by default). It runs in a throwaway worktree at the PR head, with no git hooks, without the runner's or GitHub's tokens in its environment, and the whole process tree is stopped at the timeout. A goal QA judge runs the test command too, at its base, before its agent starts: auto-accept takes it only with `autoaccept verify on`, and at the terminal its offer shows a `base:` and a `tests:` line. Roles are a guardrail on the board, not an OS boundary: an agent with a shell on your account could reach another run's proxy address. The red/green runs, the diff guard and the hub's gates are checks the agent cannot touch.

## Autopilot merges

With **Merge approved PRs** on, the autopilot merges a task's approved PR once its checks pass; your runner merges with your `gh` login. That needs a person's review (**Who approves the PR**: **A person** or **Agents, then a person**) unless you also turn on **Merge without a person's review**. Without a person's review, a PR waits for you ("Not merged without you: …"; **Approve** to merge, or **Request changes**) when:

- it changes a sensitive path (below); the reason names up to 8 of them;
- its changed files cannot be read, its runner reported none, or it changes 2000 files or more;
- its head is not a commit the autopilot's own runs made.

A path is sensitive when any folder in it, or the file itself, has one of these names: `.github`, `.gitlab`, `.circleci`, `.buildkite`, `.husky`, `.devcontainer`, `.claude`, `.cursor`, `.vscode`, `scripts`, `.codex`, `.gemini`, `.agents`, `_agents`, `.agent`, `.opencode`. A file is sensitive in any folder by its name:

- CI and hooks: `.gitlab-ci.yml`, `.travis.yml`, `azure-pipelines.yml`, `Jenkinsfile`, `bitbucket-pipelines.yml`, `.drone.yml`, `appveyor.yml`, `.pre-commit-config.yaml` (or `.yml`), `lefthook.yml`;
- git: any name starting with `.git` (`.gitignore`, `.gitattributes`, `.gitmodules`);
- environment: `.env`, `.env.*`, `.envrc`;
- containers: names starting with `Dockerfile`, `Containerfile` or `docker-compose`, names ending in `.dockerfile`, `compose.yaml`, `compose.yml`, `.dockerignore`;
- build: `Makefile`, `GNUmakefile`, `justfile`, `Taskfile.yml`;
- agent instructions and config: `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.mcp.json`, `opencode.json`, `opencode.jsonc`, `tui.json`, `tui.jsonc`;
- JavaScript: `package.json`, `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, `pnpm-workspace.yaml`, `bun.lock`, `bun.lockb`, `.npmrc`, `.yarnrc`, `.yarnrc.yml`, `.pnpmfile.cjs`;
- Go: `go.mod`, `go.sum`, `go.work`, `go.work.sum`;
- Rust: `Cargo.toml`, `Cargo.lock`, `build.rs`;
- Python: `pyproject.toml`, `poetry.lock`, `uv.lock`, `Pipfile`, `Pipfile.lock`, `setup.py`, `setup.cfg`, `tox.ini`, `noxfile.py`, `conftest.py`, `pytest.ini`, `requirements*.txt`;
- Ruby, PHP, JVM and .NET: `Gemfile`, `Gemfile.lock`, `composer.json`, `composer.lock`, `pom.xml`, `build.gradle`, `build.gradle.kts`, `settings.gradle`, `settings.gradle.kts`, `gradle-wrapper.properties`, `NuGet.config`, `Directory.Build.props`, `Directory.Build.targets`, `packages.lock.json`;
- test runners, names starting with: `vitest.config.`, `vitest.workspace.`, `vite.config.`, `jest.config.`, `playwright.config.`, `cypress.config.`, `karma.conf.`, `.mocharc`, `phpunit.xml`.

Names match in any letter case, and a rename counts by its old and new path. The staged pipeline's diff guard uses the same list: an Executor change to one of these paths needs the reviewers' approval, or a person's when the stages have no Reviewer.

## Agent performance

**Settings → Agents → Advanced → Agent performance** turns run history into **Insights** (cost per delivered task, first-pass rate, rework, waste), **Scorecards** per role, agent and model, and tiers (Senior, Mid, Probation), by fixed rules: no model is called and no tokens are spent. Its suggestions change a routing rule, a role's daily budget or auto routing only after you confirm.

Under **Tiers & routing**, **Auto routing in this project** (off by default) picks the agent and model for a dispatch or an autopilot task when none of your routing rules matches: the cheapest combination that meets the role's required tier, with a share of tasks (10% by default) given to Probation combinations to gather samples. Budgets still apply.

Every 6 hours the hub deletes raw events older than 180 days (finished runs' log lines, agent session actions and routing picks, and the audit rows of refused sign-ins, step-ups, authenticator codes and device links) and blanks old diffs, keeping their file lists and line counts (the diff view then says history retention cleared the diff); runs, usage, verdicts, scorecards and the rest of the audit log stay. Administrators change this under **Keep raw events for** (30 to 3650 days, or Forever).

## Demo videos

Recorded from the real app by `npm run demo:videos` in `web/`; the runner, agent and hub data in them are staged.

### Dispatch a node to an agent

Pick a runner, agent and model; accept it at the runner; watch OpenCode work over ACP; read the diff; merge the PR.

https://github.com/user-attachments/assets/8e494381-8aeb-4c58-9cf8-251f4c0f3060

### Autopilot

The goal's **Autopilot** button in the node panel opens it in Station. The goal is broken into tasks; each one is implemented, checked by CI, reviewed by other models and revised, then waits for you.

The requirement can change mid-way. Tell the voice copilot how it changes: it pauses the goal's autopilot, records the change on the goal under **Revisions**, and a replan run re-evaluates the tasks — done ones stay, obsolete ones are skipped (kept with their history), ones in progress are only flagged, new ones are added. While it runs the pill reads **Revising…**. When it ends, the copilot tells you what was kept, changed, skipped and added, and asks whether to resume; nothing resumes until you say yes. See [COPILOT.md](../COPILOT.md#revising-a-goal-mid-way).

https://github.com/user-attachments/assets/1108f05c-50f3-414c-8541-ad1fab9fbbe9

### Two-factor authentication and device links

In **Settings → Account**, turn on an authenticator app and save the recovery codes. Then link a new PC from **Settings → Agents**: step-up asks the app, and the hub hands back a one-time link.

https://github.com/user-attachments/assets/f7f93bb4-304d-4c0f-b39a-5af4d7acebe8

### Token usage

Tokens and API-equivalent cost by day, agent, model and session, in **Settings → Agents → Advanced → Usage details**; the daily budget is a row of its own.

https://github.com/user-attachments/assets/57324ad9-5170-495a-8024-159ce63102aa

### Agent chat

Who is working on what, messages between agents, and a held message waiting for your approval.

https://github.com/user-attachments/assets/fa0c5a33-1d02-41bd-9dbc-6ff0aac0e75d

