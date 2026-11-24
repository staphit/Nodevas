# Agent workstation

Traditional Chinese: [workstation.zh-TW.md](./workstation.zh-TW.md)

The server (the **hub**) shows the board, the runs, and their changes, but never starts a program. A **runner** (`nodevas runner`) on your own PC connects out to the hub, takes the runs you accept, and runs the agent CLIs with your own logins. Your code, your agent logins, and your `gh` login stay on the runner, and it runs only what you accept, within its own limits (see the [Security model](../README.md#security-model)).

- **Runs.** Each run is either interactive, with the agent's TUI relayed into your browser from a hidden terminal on the runner (opening no window), or headless (background), streaming its events to the node. Interactive is the default on your own runner when supported; see [Interactive runs](#interactive-runs). The runner commits the work, pushes, and opens a PR with `gh`, then reports the diff and CI status.
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
  - Reviews, plans, contest comparisons (`compare`) and judges (goal QA and checklist checks) run read only, without the repository's own agent config; Claude Code also gets no WebFetch, WebSearch or Monitor there.
  - Agent config a run adds or changes (`.claude/settings.json`, `.mcp.json`, `.codex/`, `.gemini/`, `opencode.json` and the like) is left out of its commit, and the worktree is kept so you can commit it yourself. `nodevas runner status` lists kept worktrees; `nodevas runner prune-kept [--dry-run]` removes those with nothing but the left-out config (branches stay); nothing removes them on its own. The list is locked while it is read or written (`kept-worktrees.lock` in the runner's data folder), so `prune-kept` or a second runner on the same data folder never drops a worktree a run just kept.
  - Permission prompts are refused at a headless run unless you [relay them](#permission-prompts-off-by-default), and an auto-accepted run is relayed only with `allow-shell` on.
  - The project explorer's **Files** tab shows your runners' repositories to you alone, read-only, and hides files that look like secrets (`.env`, keys, `.ssh`, credentials). `nodevas runner set repoBrowse off` and a runner restart turn that off.
  - A run's **Workspace** (its worktree, branch and changes) is yours alone to browse, merge locally or discard, never an administrator's or an agent's; see [Agent workspace](#agent-workspace). `nodevas runner set workspaceActions false` and a runner restart turn merging and discarding off.
  - A **global agent** starts only on your own runner, in repositories added there, gets no board and keeps its branch on the runner unless you ask for a PR; it supports follow-up turns and multi-repo groups. See [Global agents](#global-agents). `nodevas runner set globalRuns off` and a runner restart decline them.
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

## Interactive runs

A single-folder run starts **interactive** by default on your own runner when the runner and chosen agent support it, the agent has trusted the runner's work folders, auto-accept (if enabled) covers interactive runs and allows remote typing, and—for node runs—the dispatch is a plain implement run with no workflow stages, review pipeline or fallback chain. Otherwise it starts in the background (with a one-line reason). Multi-repo groups, contests, Autopilot, pipeline rounds, fallbacks and runs on a teammate's runner always run in the background. A follow-up uses the same rule, read for the earlier run's runner and agent; a reply that has to wait (behind a running turn, or for a usage limit to reset) always runs in the background (see [Follow-ups and PR updates](#follow-ups-and-pr-updates)).

The start form remembers whether you last picked **Talk with it** (interactive) or **Background**. A saved interactive choice never overrides a reason that forces the background: a runner or agent that cannot run in a terminal, a folder its CLI has not trusted, a teammate's runner, auto-accept that does not allow typing, a staged node, a pipeline or fallbacks, a group or a contest. Then the **Talk with it** card is disabled and the run starts in the background. Auto-accept that covers background runs only is softer: it makes **Background** the default, but you may still pick **Talk with it** and answer the prompts yourself.

The agent's TUI runs inside a hidden PTY on the runner (never opening a desktop terminal window) and streams into the run dialog, where you can type directly or use the composer and key row. In Simple mode, the agent's screen sits inside a plain-word frame; an untrusted work folder falls back to a background run automatically, and a signed-out agent cannot start in either mode.

- **View-only.** On a team hub, if the runner's owner did not allow remote typing for an interactive run, the runner marks it view-only. The browser shows the terminal as read-only and disables the composer instead of silently dropping input.
- **End (Done, keep the work).** Unlike **Stop** (cancel, which discards unfinished work), **Done, keep the work** finishes an interactive run cleanly and publishes its work as succeeded—committing changes, keeping the branch and opening a PR if requested. The runner sends the CLI's quit sequence, waits up to 10 seconds for it to exit on its own, and only then terminates the process tree so the kill exit code is not reported as a failure. An End sent before the terminal opens is held and applied as soon as it starts. Only people allowed to type into the run (the dispatcher or the runner's owner, never an agent; administrators get no extra right) may End it, and on a view-only run **only the runner's owner** may End. Logged as `run.end`.
- **20-minute quiet timeout.** Every interactive run has a fixed 20-minute quiet timeout on the runner (not configurable, and active in Simple mode as well). If there is no meaningful screen output (idle TUI repaints are ignored) and no typed input for 20 minutes, the runner ends the session the same way as End and keeps its work. If the screen was showing a question or menu prompt when it timed out, the dialog reports that it stopped while waiting for your answer rather than as an ordinary quiet finish. The 60-minute overall run timeout (`runTimeoutMinutes`) still applies as a failed timeout.
- **After the run ends.** The live terminal buffer stays readable for 10 minutes. For Claude Code interactive runs, the runner also tails the session's transcript file (`~/.claude/projects/*/<session-id>.jsonl`, read-only, regular files owned by the runner user with no symlinks) into redacted structured events so **Steps** and the final summary remain available after the screen expires.

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

Station's **Global agents** tab starts an agent that belongs to no project and no board: a task in one or more of your runner's repositories, for work that is not a node (tidy a repository, upgrade a dependency across services, adjust CI, fix a CVE). Write the task; its first line becomes its title.

- **Who and where.** Only you, on your own runner (an administrator is no exception), and only in repositories added on that runner: the form shows the `nodevas runner repo add <path>` to run on that machine when it has none. The hub never names a folder, and an agent cannot start a global agent. The tab lists the global agents, groups and contests you started or that ran on your runners, and your runners.
- **No board.** The runner gives a global agent no `nodevas` MCP server: it claims nothing, sets no status and cannot ask you anything through `ask_user`, so its permission prompts are never relayed either. By default, a single-repository global agent runs **interactive** so you can talk with it and answer prompts in its terminal (see [Interactive runs](#interactive-runs)). Run in the background, it decides alone—and if it needs a decision, it can ask at the end of its message so your reply continues the work. Your runner's own limits (permission mode, sandbox, auto-accept policy) apply as to any run. Claude Code and Gemini CLI load no MCP server at all for it, OpenCode switches every one off, your own `nodevas` entry included; Codex and agy still load what your own config (`config.toml`, `~/.gemini/config`) lists, but a `nodevas mcp` there refuses to serve inside a global agent's run: the runner puts `NODEVAS_NO_BOARD` in the agent's environment and, while the agent runs, a `nodevas-no-board` file in its worktree's git folder (for Codex, which starts its MCP servers with a cleared environment in the worktree). It stops before it reads its flags or reaches the hub, so neither a run's door through the runner nor a loopback hub's trust in this machine gives it a board. Left out: a `nodevas` on your `PATH` older than the runner, and an MCP entry of yours that starts it outside the worktree under a CLI that clears its environment. A runner declines any run that names no project or node and is not a global agent's.
- **Branch and PR.** Its branch is `agent/<slug>-<run>`, the slug taken from the title (lowercase letters, digits and single dashes, at most 40; `task` when the title has none) unless you name it. By default no PR is opened: the commit stays on that branch, in its worktree on the runner, to **Merge locally** or **Discard** from its [Workspace](#agent-workspace). Tick **Open a pull request when done** to have the runner push and open one, as for a node's run, and only while its `autoPR` is on. The commit and the PR name the run (`Nodevas-Run`) but no node.
- **Not for global agents:** review pipelines, plans, roles and stages, routing rules, fallback chains, automatic usage-limit handoffs, autopilot. A global agent stopped by a usage limit fails, and what it did so far is committed on its branch; you can continue it with a follow-up (which waits until the recorded reset time if one is still in the future).
- **How busy your runners are.** Per runner, the tab shows the runs at work and queued (every project's and global ones) and today's runs, tokens and cost. **At once** sets the runner's `maxConcurrent` from the hub, up to its `maxConcurrentCeiling` (default 8, or `maxConcurrent` if that is higher). The runner applies it at once and saves it to its `runner.json`, that key only, read fresh so a `nodevas runner set` since its start is kept; a lower number stops no run, it only holds new ones back.

### Follow-ups and PR updates

Replying in a global run's dialog after it ends (succeeded, failed, cancelled or usage-limited) starts a follow-up turn linked to the same work: same runner, same worktree folder and same branch.

- **Interactive or background.** A follow-up starts in the same mode a first start would (see [Interactive runs](#interactive-runs)), read for the earlier run's runner and agent, with your saved choice. **Start new work with this** follows the same rule.
- **Continuing the session.** The runner resumes the CLI's own session from its local record when it can: Claude Code, OpenCode and Antigravity in either mode, and Codex in the background only (`codex exec resume`). Gemini CLI never resumes. Otherwise (Gemini CLI, Codex in a terminal, a different agent, no session on record, or a headless resume that exits non-zero within 30 seconds with no text output) the runner starts a fresh session. The hub sends the earlier task and that turn's summary (redacted, at most 4 KiB) with every follow-up, and a fresh session gets them in its prompt, marked as data, so an agent without resume still knows the earlier work.
- **Queued replies.** Typing while a **background** global run is still running creates a queued follow-up that waits until the current turn ends; click **Take back** to withdraw it before it starts. (While an interactive run is live, type directly into its screen instead.) If the previous turn stopped at a usage limit with a future reset time, the follow-up waits until that time. A reply that waits always runs in the background.
- **Turn ownership and lifecycle.** Only you (the starter and runner owner) can reply. Only the latest turn can be continued, merged locally or discarded; earlier turns stay read-only and link down to the latest turn. A follow-up taken back or declined before it starts frees the slot. Once the work is merged locally, discarded, or its PR is merged or closed, the dialog offers **Start new work with this** to start fresh from base.
- **Updating an open PR.** If the work already has an open PR, a follow-up updates it instead of opening a second PR:
  - before the turn starts, the runner re-checks the PR (`gh pr view`) and remote branch. It declines if the PR was merged or closed (`pr_ended`), if the PR's base changed (`pr_moved`), or if the remote branch diverged (`branch_moved`, fast-forwarding only when local HEAD has no unpushed commits and the worktree is clean);
  - failed, cancelled or usage-limited turns commit locally and are never pushed; the next succeeding turn pushes those commits together;
  - each succeeding turn with new commits pushes fast-forward only (never `--force` or `--force-with-lease`), attaches the existing PR and posts one PR comment (`Turn N: …` plus the turn's redacted summary). Draft PRs are never automatically marked ready. Every push re-runs CI and may dismiss approvals, while the PR title and body remain those of the first turn;
  - if someone pushed to the branch while the turn ran, the push is refused (`branch_moved`), the turn's commits stay local and no PR comment is posted. A no-change turn keeps the root branch and pushes nothing;
  - right before pushing, the runner looks at the PR again. If it was merged or closed while the turn ran, the runner does not push and posts no comment: the commits stay on the local branch and the run shows the PR as ended (`pr_ended`). A PR the runner cannot look at is not pushed to either;
  - while any turn is unfinished, remote merges are blocked and the PR watcher keeps the worktree until the turn ends.
- **Runner-side record.** The hub only names run IDs. The runner resolves the worktree path, branch, HEAD, pushed HEAD, PR URL and CLI session ID solely from its own `work.json` (mode `0600`, kept 30 days, up to 500 entries) and verifies worktree ownership and git state on disk. Dirty worktrees are accepted only after a cancelled or failed turn; if a no-change first turn dropped its folder, the follow-up recreates it from base on the root branch.

### Multi-repo groups

Pick **2 to 30 folders** on one of your runners in **Global agents** to run the same task across independent repositories at once (dependency upgrades, CI workflow edits, CVE fixes).

- **Fan-out.** The hub creates one background global run per repository under a shared group row (and shows them as one figure with per-repository member tabs in the story room). Each member's prompt lists the sibling repositories changing together. Interactive mode is not used for groups; talk to any individual repository through a follow-up once its run finishes.
- **Budget and queue.** Daily run limits are checked for the whole group together: if there is not enough room for all selected repositories, the entire group is refused with the count that fits (no partial start). Members run in FIFO order under the runner's `maxConcurrent`; before offering each queued member, the hub re-checks daily cost and token limits and cancels remaining members if exceeded. On a team hub, each member is offered to your runner individually under its normal acceptance rules (there is no hub-side "accept all").
- **Usage-limit pause.** If a member stops at a quota or rate limit, the group pauses until the reset time (**Paused until HH:MM: usage limit**) so queued siblings wait; **Resume now** clears the hold early, and the stopped member continues via a follow-up.
- **Group actions.** Each repository keeps its own branch, optional PR (chosen at start), diff and result actions; repositories that needed no changes drop their worktree and show **Nothing to change here**. Group controls provide **Stop all**, **Apply all finished** (merges finished members locally one by one, stopping per repository on conflict and reporting each result), **Tell all** and **Tell failed** (sends a follow-up to eligible latest turns).

On the runner: `nodevas runner set globalRuns off` declines global agents, `nodevas runner set remoteSettings off` keeps `maxConcurrent` a setting of that machine only, and `nodevas runner set maxConcurrentCeiling <1-16>` sets the most the hub may ask for. Each takes effect after a runner restart. Only the runner's owner may change its settings from the hub, and the runner checks that again.

A global agent's updates go only to you and its runner's owner, never to a project. Audit log entries (never recording task or follow-up text): `run.global.start`, `run.global.followup`, `run.global.group`, and `runner.settings.update` for **At once**.

## Coordinating concurrent runs

Nodevas coordinates overlapping work in three layers while leaving the decision to you: ask before doubling up, warn early when two runs touch the same files, and support deliberate side-by-side contests.

### Busy choices

An unfinished writing run (implement, test, revise, or plan on a node)—or a running pipeline between steps—holds what it writes:

- starting a **node run** checks for unfinished writing runs on the same node (from any member) and your own unfinished global runs in the same repository;
- starting a **global run** checks for your own unfinished writing runs (global or node) in the same repository.

Two node runs on *different* nodes of the same repository never block each other, and neither do read-only runs (review, judge, verify, compare) or runs in the same lineage (pipeline, fallback chain, follow-up chain, group or contest).

When a target is busy, the start form warns upfront and the hub asks you to choose before creating the run:

- **Start mine after it ends** (queue): creates a fresh run that waits in strict first-come, first-served order behind every active or earlier-queued holder and its successors (fallbacks, usage-limit waits, remaining pipeline steps). When it starts, a summary of your own prior run is carried into its prompt (never another member's).
- **Add this to its work instead** (continue): offered only when starting a global run behind your own global run on the same runner and repository; turns your message into a follow-up on that run's folder, branch, agent, model and PR setting.
- **Stop it and start mine** (take over): stops the holders you were shown (if the holder set changed in the meantime, you are asked again) and queues your run right behind them. Stopping a running interactive run uses **End** so its work so far is kept (on a node take-over, End keeps the branch without opening a PR so you never get two competing PRs); stopping a headless run cancels it and any pipeline it belongs to.
- **Run both**: starts right away alongside the holder, and [Overlap notices](#overlap-notices) watch for shared files. When the runner's `maxConcurrent` is 1, the runs still execute one after another.
- **Multi-repo groups** offer only **Run both** and **Skip the busy folders** (dropping busy repositories before checking budget).
- **Automatic starts** (Autopilot) queue without asking. On a runner too old for coordination, a person starting a busy node is offered only **Stop it and start mine**, but the autopilot still waits behind the holder instead of failing. The copilot's tools answer that the target is busy, with what is running, rather than picking a choice on your behalf; see [When the target is busy](../COPILOT.md#when-the-target-is-busy).

**Team privacy and board claims.** On a team hub, another member's run appears only as a count ("1 more by someone else") with no name, task or details, and only its owner or an administrator may stop it (so **Stop it and start mine** is unavailable when a hidden holder exists). When two runs share a node (queue, take over, both or contest entries), the hub releases a stopped or waited-on holder's board claim (only its own session's). A **Run both** run and every contest entry run in coordinated mode, and so does a queued or take-over run that finds the node claimed or done when it is offered: its MCP server leaves out `claim_task`, `release_task`, `set_node_status` and `create_subgraph` (see [MCP: coordinated runs](./mcp.md#coordinated-runs)), so agents do not fight over the board; you update the node's status yourself. Logged as `run.busy` and `run.takeover` (without task text).

### Overlap notices

While a run is active, its runner watches the repo-relative files it touches (`git status` every 30 seconds, backing off up to 5 minutes on slow repositories, plus edit-tool paths for headless runs and committed files at finish; capped at 500 paths per run). It reads the worktree with the repository's filter drivers switched off, as every other read of a run's worktree does, so a filter the agent planted never runs. Secret paths, common lockfiles (`package-lock.json`, `go.sum`, `Cargo.lock`, `yarn.lock`, `pnpm-lock.yaml`, etc.) and `linguist-generated` files are skipped on the runner.

The hub compares touched files across runs in the same repository (including your own unapplied finished results without a PR from the last 24 hours):

- **Strong vs. weak overlaps.** Files where both runs only add lines are recorded as *weak* overlaps (shown in the list without triggering a notice or mail). As soon as either side modifies or deletes lines in a shared file (*strong* overlap), an overlap notice and badge appear.
- **Your own node runs:** the hub sends a one-time coordination message (sender `nodevas`, up to 5 per run) to each run's own MCP session listing the shared paths so the agents can coordinate with `send_message` or keep edits minimal.
- **Your own global runs:** global runs have no board or mail and never receive unasked mid-run input. Instead, the notice offers **Tell it**, which pre-fills the dialog composer (for you to send into an interactive terminal or queue as a background follow-up), and every later follow-up prompt includes the active overlap note.
- **Team hub privacy:** across different members, only active node runs in the same project are compared (two members' global runs are never compared). Because runner-reported paths are untrusted across members, a cross-member overlap shows only "Another person's work also changes some of the same files"—with no paths, counts, names or agent mail—even to an administrator.

Touched-file and overlap records are pruned 7 days after their runs finish, even when raw-event retention is set to Forever.

### Contests (比稿)

Toggle **Compare agents** (比稿 in Chinese) on a plain node dispatch (no workflow stages, pipelines or fallback chains) or a single-repository global run to run **2 or 3** headless entries (**A**, **B**, **C**) on the same task in separate worktrees on your own runner(s). Contests are started from full mode; Simple mode only displays comparison results when one exists.

- **Entry rules and budget.** Every entry runs headless with no PR, no fallback chain and no usage-limit wait; node contest entries run in coordinated mode so they do not claim or update the node's status. Budget is checked as one block for all entries plus the optional reviewer—if there is not enough room, nothing starts. On a team hub, each entry is offered to your runner(s) individually under normal acceptance rules.
- **Optional reviewer.** Pick a reviewer agent and model at start or later once entries finish. When every entry ends and at least two succeeded with commits, a read-only `compare` run starts (without the repository's agent config, like reviews). The hub drops secret-file hunks, redacts every diff, rotates entry order and labels entries anonymously as A/B/C without agent or model names. The reviewer returns 0–10 scores, short reasons and a suggested pick; scores are advice only and nothing is applied automatically.
- **Picking a winner.** A global contest's row in **Global agents** opens the comparison. **Compare and pick** opens it from a node contest's runs in the node's run view, and from a contest's pair of figures in the story room. Compare entries side by side (summary, files, diffs and scores), then click **Use this one** and choose how to apply it:
  - **Apply to my folder**: merges the winner's branch locally through the runner's workspace merge checks;
  - **Open a pull request**: pushes the winner's own branch without force and opens a PR (available for contest winners only);
  - **Keep for now**: leaves the winner's workspace intact for later.

  **Throw away the others** (ticked by default) runs the normal workspace discard action on the non-winning entries, preserving any entry that gained extra commits and reporting refusals. For a node contest, update the node's status yourself once you pick. Only the contest creator (every entry's runner owner) may request a reviewer or pick a winner; the creator or an administrator may cancel. Logged as `run.contest.start`, `run.contest.judge`, `run.contest.pick` and `run.contest.cancel` (plus `run.workspace.*` for apply and discard).

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

Every 6 hours the hub deletes raw events older than 180 days (finished runs' log lines, agent session actions and routing picks, and the audit rows of refused sign-ins, step-ups, authenticator codes and device links) and blanks old diffs, keeping their file lists and line counts (the diff view then says history retention cleared the diff); runs, groups, contests, usage, verdicts, scorecards and the rest of the audit log stay. Administrators change this under **Keep raw events for** (30 to 3650 days, or Forever). Touched-file and overlap records have their own fixed 7-day retention after their runs finish.

## Demo videos

Recorded from the real app by `npm run demo:videos` in `web/`; the runner, agent and hub data in them are staged.

### Dispatch a node to an agent

Pick a runner, agent and model; accept it at the runner; watch OpenCode work over ACP; read the diff; merge the PR.

https://github.com/user-attachments/assets/ab98a136-677c-473e-9f94-45a330ec74e6

### Autopilot

The goal's **Autopilot** button in the node panel opens it in Station. The goal is broken into tasks; each one is implemented, checked by CI, reviewed by other models and revised, then waits for you.

The requirement can change mid-way. Tell the copilot how it changes: it pauses the goal's autopilot, records the change on the goal under **Revisions**, and a replan run re-evaluates the tasks — done ones stay, obsolete ones are skipped (kept with their history), ones in progress are only flagged, new ones are added. While it runs the pill reads **Revising…**. When it ends, the copilot tells you what was kept, changed, skipped and added, and asks whether to resume; nothing resumes until you say yes. See [COPILOT.md](../COPILOT.md#revising-a-goal-mid-way).

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

