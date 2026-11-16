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
  - Agent activity: the workspace as a pixel-art building, with this project's office, the other projects' offices, a lounge for idle agents and a server room with a rack per runner. Each agent is a button: its menu opens its run, changes, node or goal, pauses or resumes its autopilot, or cancels the run;
  - who worked on what in the last hour;
  - at the side, today's spend, the runners, and Agent chat with a composer;
  - **Global agents**, a tab beside this project's: agents working in your runners' repositories outside any project; see [Global agents](#global-agents).

  Questions, permission prompts, usage limits and the daily budget also raise a browser notification.
- **Safe defaults.**
  - **Alone on one PC (single-user edition):** a run you start in the app begins right away on this computer, without a terminal: the click is the consent. One guard stays: a run that starts on its own (Autopilot, a review or revise round, a fallback, or continuing after a usage limit) for an agent you set to **Run any command on this computer** waits for your OK in the app, unless you ticked **Don't ask again** for that agent. Agents write board nodes and Autopilot turns nodes into prompts, so a prompt-injected agent could otherwise plant a task that a full-access agent then runs unasked. With the shipped settings no agent may run any command. **Ask me before every run starts** asks before every run. Only the runner `serve` started for itself, connected straight from this machine, is treated this way.
  - **Shared PC:** the single-user edition trusts everyone who can use this computer. Another user account on the same PC can open the app on loopback and, since runs start directly, start agents as you. Use it on a computer only you use; on a shared one, run a team hub with accounts. The one guard is not a sandbox either: any program on this computer that is not sandboxed (an agent allowed to run any command among them) can answer it as you.
  - **Team hub:** runs need your acceptance on the runner's own PC, at its terminal; the hub only shows "waiting for approval on <PC>" and why. It never approves a run, so a compromised hub cannot start one there; the hub's word on who started a run is shown, never trusted. Remote dispatch needs a step-up passcode and an auto-accept allowlist you set on the runner.
  - Remote merge is off until you turn it on.
  - Reviews, plans and judges (goal QA and checklist checks) run read only, without the repository's own agent config; Claude Code also gets no WebFetch, WebSearch or Monitor there.
  - Agent config a run adds or changes (`.claude/settings.json`, `.mcp.json`, `.codex/`, `.gemini/`, `opencode.json` and the like) is left out of its commit, and the worktree is kept so you can commit it yourself. `nodevas runner status` lists kept worktrees; `nodevas runner prune-kept [--dry-run]` removes those with nothing but the left-out config (branches stay); nothing removes them on its own. The list is locked while it is read or written (`kept-worktrees.lock` in the runner's data folder), so `prune-kept` or a second runner on the same data folder never drops a worktree a run just kept.
  - Permission prompts are refused at a headless run unless you [relay them](#permission-prompts-off-by-default), and an auto-accepted run is relayed only with `allow-shell` on.
  - The project explorer's **Files** tab shows your runners' repositories to you alone, read-only, and hides files that look like secrets (`.env`, keys, `.ssh`, credentials). `nodevas runner set repoBrowse off` and a runner restart turn that off.
  - A run's **Workspace** (its worktree, branch and changes) is yours alone to browse, merge locally or discard, never an administrator's or an agent's; see [Agent workspace](#agent-workspace). `nodevas runner set workspaceActions false` and a runner restart turn merging and discarding off.
  - A **global agent** starts only on your own runner, in a repository added there, gets no board and keeps its branch on the runner unless you ask for a PR; see [Global agents](#global-agents). `nodevas runner set globalRuns off` and a runner restart decline them.
  - Put the hub behind Cloudflare Access; the runner connects with a service token.
  - Turn on two-factor authentication with an authenticator app (**Settings → Account**). Sign-in then needs the app's code too, and step-up asks the app instead of sending email.

The agent parts of the app (runners, runs, autopilot, Agent chat and usage) stay hidden until you turn on **Settings → Agents → Let agents work here** or open Station, or until this workspace has a runner, a run or a connected agent. Alone on one machine the built-in runner is always there, so it counts only once you have added a repository to it.

Set it up in two steps:

1. Deploy the hub: see [Getting started: Team server](./getting-started.md#team-server).
2. On the hub, open **Settings → Agents**, turn on **Let agents work here**, then **Link a new device** and copy the one-time link. On your PC, run [`scripts/install.sh --hub <url> --link`](./install.md#set-up-a-new-pc-with-a-device-link) with the repositories that runs may use, and paste the link when asked. The link works once, for ten minutes, and carries the hub's Access service token if an administrator stored one, so nothing is copied from Cloudflare by hand.

Alone on one machine, one command starts both: `nodevas serve --project ./workspace` (`--no-runner` for the hub alone), or the desktop app. The runner is part of the app: add repositories with **+ Add repo** (or **Settings → Agents → This computer**, where you also choose what each agent may do), and runs you start begin right away, with no terminal involved. See [Hub and runner on one machine](./install.md#hub-and-runner-on-one-machine).

## Answering your agents

An agent can stop and ask you through Nodevas (`ask_user`): an approval (**Approve** or **Deny**, with an optional note), a question you answer in words, or a choice of 2 to 8 options. A question or a choice can also be left unanswered with **Dismiss**. The agent waits up to 10 minutes.

Answer in Agent chat (Ctrl+Shift+A, or its card at the side of Station), from Station's **Waiting on you**, or from the agent in the Agent activity view; the browser notifies you. A run's timeline (node panel → **Activity** → the run) lists each of its asks and how it ended, and asks still open when the run ends expire. Only the person whose agent asked, or an administrator, can answer.

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
- The runner only continues from handoff branches it made itself, for the same repository and node, still at the commit it recorded. The hub can name such a branch but not any other ref. A handoff branch recorded without its node, by a runner from before node checks, is not continued; the job is declined ("has no node on record"). Each time the runner makes a handoff branch, it deletes those older than 30 days or beyond the newest 200, if they are still at their recorded commit.

A model the login cannot use, or a missing CLI, fails before the agent's first turn. There is nothing to keep then and waiting would not help, so those failures only fall through the chain.

## Agent workspace

Each run has a **Workspace** panel: open it from the run's row, Station's run menu, or a person or desk in the Agent activity view. It shows the agent and model, the node, the runner, the run's branch and its base, the run's state, and its PR and CI. Its tabs are **Files** (the run's worktree on your runner, read-only, live), **Changes** (the diff against the base) and **Terminal** for an interactive run or **Log** otherwise.

Only the runner's owner sees the files and gets the actions. An administrator is no exception (the disk is the owner's), an agent never gets them, and the runner checks again that the request is its owner's. Files follow the Explorer's rules: secrets and `.git` hidden, nothing read through a link (a symlink or a Windows junction) that leads out of the worktree, the repository's git filters never run. `nodevas runner set repoBrowse off` turns Files off too.

- **Merge.** A run with an open PR merges there, as before. A run without one (autoPR off, a failed push, offline) can be **merged into its base locally** by your runner, never pushed. The runner merges only the run's own branch (`agent/<node>-<run>`), only at the commit you saw, and never touches a checkout with uncommitted changes:
  - the base checked out in the registered repo, with no change to a tracked file and no merge, rebase, cherry-pick, revert or bisect in progress: it merges there with `--no-ff` (untracked and ignored files are fine; the merge refuses to overwrite one, so your own `.env` or local config is never replaced), and only if that checkout is still on the base when the merge starts;
  - the base checked out nowhere: it merges in a temporary worktree under the runner's data folder and removes it, with every filter the repository's config names (as read there) emptied and no recursion into submodules; a merge that would sign (or check signatures) with a program the repository's own config names is refused;
  - the base checked out in any other worktree, or the run's worktree holding uncommitted files, or the repository configuring a merge driver, or another unfinished run (or the run's pipeline) still using the branch: refused, with the reason;
  - a conflict: the merge is aborted and everything is left as it was.

  Merged, the run's worktree and branch are removed. A run of another kind (a review, a revise) is never merged from here.
- **Discard**, once the run has ended: closes its open PR and deletes the PR's branch on GitHub (`gh pr close --delete-branch`), then removes the run's worktree, uncommitted files and all, deletes its local branch and drops it from the kept worktrees. If `gh` cannot close the PR nothing is removed. A revise or review run's PR is the implement run's, so discarding one leaves the PR and its branch alone. Handoff branches stay. Commits added to the branch (or the run's worktree) after the run's own, kept on no other branch, stop the discard before anything is removed. While another run (a revise or review on the same PR) or the run's pipeline still uses the branch, Discard waits. Discarding again, or a run whose worktree is already gone, is fine. A run that never got a worktree (declined, cancelled before it started) is discarded on the hub alone. The confirm names only what the runner will really remove, and says when it could not count the uncommitted files.

Both ask you to confirm. Discarded or merged is shown on the run; its status stays what it was, so history, scorecards and autopilot are unchanged. A request waits up to 15 minutes for an offline runner, which answers a request sent twice only once. Every request, answer and expiry is in the audit log (`run.workspace.*`).

## Global agents

Station's **Global agents** tab starts an agent that belongs to no project and no board: a task in one of your runner's repositories, for work that is not a node (tidy a repository, try an idea, fix a script). Write the task; its first line becomes its title.

- **Who and where.** Only you, on your own runner (an administrator is no exception), and only in a repository added on that runner: the form shows the `nodevas runner repo add <path>` to run on that machine when it has none. The hub never names a folder, and an agent cannot start a global agent. The tab lists the global agents you started or that ran on your runners, and your runners.
- **No board.** The runner gives a global agent no `nodevas` MCP server: it claims nothing, sets no status and cannot ask you anything, so its permission prompts are never relayed either. Run in the background, it decides alone and says what it assumed in its summary; choose **Interactive** to talk with it in its terminal. Your runner's own limits (permission mode, sandbox, auto-accept policy) apply as to any run. Claude Code and Gemini CLI load no MCP server at all for it, OpenCode switches every one off, your own `nodevas` entry included; Codex and agy still load what your own config (`config.toml`, `~/.gemini/config`) lists, but a `nodevas mcp` there refuses to serve inside a global agent's run: the runner puts `NODEVAS_NO_BOARD` in the agent's environment and, while the agent runs, a `nodevas-no-board` file in its worktree's git folder (for Codex, which starts its MCP servers with a cleared environment in the worktree). It stops before it reads its flags or reaches the hub, so neither a run's door through the runner nor a loopback hub's trust in this machine gives it a board. Left out: a `nodevas` on your `PATH` older than the runner, and an MCP entry of yours that starts it outside the worktree under a CLI that clears its environment. A runner declines any run that names no project or node and is not a global agent's.
- **Branch and PR.** Its branch is `agent/<slug>-<run>`, the slug taken from the title (lowercase letters, digits and single dashes, at most 40; `task` when the title has none) unless you name it. By default no PR is opened: the commit stays on that branch, in its worktree on the runner, to **Merge locally** or **Discard** from its [Workspace](#agent-workspace). Tick **Open a pull request when done** to have the runner push and open one, as for a node's run, and only while its `autoPR` is on. The commit and the PR name the run (`Nodevas-Run`) but no node.
- **Not for global agents:** review pipelines, plans, roles and stages, routing rules, fallback chains, usage-limit waits and handoffs, autopilot. A global agent stopped by a usage limit fails, and what it did so far is committed on its branch.
- **How busy your runners are.** Per runner, the tab shows the runs at work and queued (every project's and global ones) and today's runs, tokens and cost. **At once** sets the runner's `maxConcurrent` from the hub, up to its `maxConcurrentCeiling` (default 8, or `maxConcurrent` if that is higher). The runner applies it at once and saves it to its `runner.json`, that key only, read fresh so a `nodevas runner set` since its start is kept; a lower number stops no run, it only holds new ones back.

On the runner: `nodevas runner set globalRuns off` declines global agents, `nodevas runner set remoteSettings off` keeps `maxConcurrent` a setting of that machine only, and `nodevas runner set maxConcurrentCeiling <1-16>` sets the most the hub may ask for. Each takes effect after a runner restart. Only the runner's owner may change its settings from the hub, and the runner checks that again.

A global agent's updates go only to you and its runner's owner, never to a project. Starting one is in the audit log as `run.global.start` (without the task's text), a change from **At once** as `runner.settings.update`.

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
- its head is not a commit the autopilot's own runs made, or the run that opened it is not known.

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

https://github.com/user-attachments/assets/ab98a136-677c-473e-9f94-45a330ec74e6

### Autopilot

The goal's **Autopilot** button in the node panel opens it in Station. The goal is broken into tasks; each one is implemented, checked by CI, reviewed by other models and revised, then waits for you.

The requirement can change mid-way. Tell the voice copilot how it changes: it pauses the goal's autopilot, records the change on the goal under **Revisions**, and a replan run re-evaluates the tasks — done ones stay, obsolete ones are skipped (kept with their history), ones in progress are only flagged, new ones are added. While it runs the pill reads **Revising…**. When it ends, the copilot tells you what was kept, changed, skipped and added, and asks whether to resume; nothing resumes until you say yes. See [COPILOT.md](../COPILOT.md#revising-a-goal-mid-way).

https://github.com/user-attachments/assets/dbbfbb85-5ddd-484d-a669-4582c849ab5b

### Two-factor authentication and device links

In **Settings → Account**, turn on an authenticator app and save the recovery codes. Then link a new PC from **Settings → Agents**: step-up asks the app, and the hub hands back a one-time link.

https://github.com/user-attachments/assets/df225a24-840e-4ade-b297-7a6d9dd6660e

### Token usage

Tokens and API-equivalent cost by day, agent, model and session, in **Settings → Agents → Advanced → Usage details**; the daily budget is a row of its own.

https://github.com/user-attachments/assets/19fba922-8c4f-4ad9-8c8f-7cfac0ebe8c3

### Agent chat

Who is working on what, a relayed permission prompt you allow, messages between agents, and a held message waiting for your approval.

https://github.com/user-attachments/assets/d9943e60-9a91-4f0b-97fe-dd9c31754ba9

