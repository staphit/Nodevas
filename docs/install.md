# Install a runner

A runner is the machine your coding agents run on. The hub (`nodevas serve`) shows the board, the runs and their changes, but never starts an agent; your runner connects out to it, runs Claude Code, Codex, Gemini CLI, OpenCode or Antigravity CLI with your own logins, and sends the results back. Your agent logins and your `gh` login stay on the runner.

`scripts/install.sh` does the whole setup in one command, and is safe to run again:

1. Builds `nodevas` from the checkout it sits in (Go 1.26 or newer), or takes `--binary <path>`, and installs it to `~/.local/bin`.
2. Logs the runner in to the hub. The runner token comes from a hidden prompt (or, for an unattended install, `NODEVAS_RUNNER_TOKEN`), never from a flag.
3. Adds the Cloudflare Access service token when the hub sits behind Access (`--access-client-id`; its secret from a hidden prompt, or `NODEVAS_ACCESS_CLIENT_SECRET`). Once logged in, the script unsets both variables, so the runner it starts (and the agents that runner spawns) never inherit them.
4. Allows runs in each `--repo`.
5. Wires the agents installed here into the agent network (`nodevas setup-agent`). It shows the changes first and asks, unless you pass `--yes`.
6. Starts the runner at login:
   - Windows: a script in your Startup folder. It needs no administrator rights, runs without a window, and logs to `%LOCALAPPDATA%\nodevas\runner.log`.
   - Linux: a systemd user unit.
   - macOS: a LaunchAgent.

It works in Git Bash on Windows, and on Linux and macOS.

## Install

Create a runner token on the hub first (Runners, then Add runner). Then:

```bash
git clone https://github.com/staphit/Nodevas.git
cd Nodevas
scripts/install.sh \
  --hub https://nodevas.example.com \
  --repo ~/code/my-app \
  --repo ~/code/other-app
```

The script asks for the token without echoing it. Avoid `NODEVAS_RUNNER_TOKEN=... scripts/install.sh`: a token typed on the command line lands in your shell history. Set the variable only where nobody types (a provisioning script that reads it from a secret store). Preview everything without changing anything:

```bash
scripts/install.sh --dry-run --hub https://nodevas.example.com --repo ~/code/my-app
```

A dry run still builds the binary into a temporary directory, so it can show the agent config changes it would make.

Behind Cloudflare Access:

```bash
scripts/install.sh --hub https://nodevas.example.com --access-client-id <id>.access
```

It asks for the runner token, then the Access client secret, both hidden.

## Options

| Option | What it does |
| --- | --- |
| `--binary <path>` / `--from-source <dir>` | Use a built binary instead of building, or build from another checkout. |
| `--prefix <dir>` | Install somewhere other than `~/.local/bin`. |
| `--name <label>` | This machine's name in the hub's logs. |
| `--relogin` | Replace the saved token (prompts for the new one; `NODEVAS_RUNNER_TOKEN` does the same unattended). |
| `--data-dir <dir>` | Where run worktrees go. |
| `--auto-accept` / `--no-auto-accept` | Start offers that the auto-accept list covers without asking. |
| `--agents <list>` | Wire only these agents: `claude,codex,gemini,opencode,antigravity`. |
| `--agents-server <url>` | The server that agent sessions you start by hand talk to (defaults to `--hub`). |
| `--no-agents` | Leave every agent config alone. |
| `--no-service` | Do not start the runner at login; run `nodevas runner` yourself. |
| `--restart` | Restart a runner that is already running, to pick up changes. |
| `--yes` | Do not ask before changing agent configs or deleting files. |
| `--config <file>` | Use a runner settings file other than the default. |

## Things to know

- **Offers are accepted at a terminal.** A runner started at login has no terminal, so it declines every offer that auto-accept does not cover. Either turn auto-accept on (`--auto-accept`) and review it with `nodevas runner autoaccept show`, or run `nodevas runner` in a terminal and answer each offer there.
- **Auto-accept leaves shell access to a person.** An offer whose agent may run any shell command under your settings (Codex `danger-full-access`, OpenCode or agy `full`, agy `auto`, Gemini `yolo`, Claude `bypassPermissions`/`auto` or `Bash` in `allowedTools`) is asked about at the terminal even when auto-accept covers it, unless you run `nodevas runner autoaccept allow-shell on`. The step-up requirement proves the dispatcher re-entered a passcode on the hub; it does **not** protect you from a compromised hub, which can mark any job as stepped up. Keep auto-accept to repositories and agents you would let anyone who controls the hub run.
- **Interactive runs are off by default** (`nodevas runner set interactive true` to take them). Their terminal is view only: the dispatcher's keystrokes reach the agent only when you answer yes to "Allow remote typing?" while accepting, or, for auto-accepted runs, after `nodevas runner autoaccept remote-typing on`. The runner logs the size and time of each typed chunk, never its content.
- **Remote merge is off by default.** An approval on the hub is refused until you run `nodevas runner set remoteMerge true`; otherwise merge on GitHub yourself.
- **Reviews and plans run read only** and stop after 20 and 30 minutes (`reviewTimeoutMinutes`, `planTimeoutMinutes`).
- **A remote hub gets results, not your sessions.** With `--hub` on another machine, runs report their events, diff, commit, PR and usage through the runner; the agent sessions you start by hand stay on this PC and are not joined to the hub. The agents step then only runs `nodevas setup-agent --runs-only`, which changes nothing but Antigravity's permission rule. `--agents-server <url>` joins your own sessions to a server they can reach on this machine.
- **Antigravity CLI** runs need `mcp(nodevas/*)` in its permissions (`nodevas setup-agent --runs-only --agents antigravity`). The runner itself never edits agy's settings.
- **A fast dispatch advisor for the autopilot (optional).** Laya runs on this PC, in a virtual environment of its own and bound to this machine only (`python -m venv ~/.laya && ~/.laya/bin/pip install "laya[serve]==0.4.14"`, then `LAYA_HOST=127.0.0.1 ~/.laya/bin/laya-serve`; on Windows the venv's programs are in `~/.laya/Scripts/`; its checkpoints are about 2.3 GB on first use), then `nodevas runner set advisorProvider systemone`. For TypeSafe's hosted Jev instead: `nodevas runner set advisorProvider systemone`, `nodevas runner set advisorBaseURL https://api.typesafe.ai`, `nodevas runner set advisorModel jev-latest`, and `nodevas runner advisor-key` (the key from stdin). See COPILOT.md.
- **Upgrading** is running the script again from an updated checkout. On Windows, the running `nodevas.exe` is renamed to `nodevas.exe.old` so the new one can take its place. The runner picks up the new binary once restarted (`--restart`).

## Check and remove

```bash
scripts/install.sh status      # runner settings, agents found, login service
scripts/install.sh uninstall   # stop the runner, remove the login service, unwire the agents, delete the binary
scripts/install.sh uninstall --purge   # also delete the runner settings (token, repositories)
```

Uninstall asks before each removal, unless you pass `--yes`, and keeps the runner settings unless you pass `--purge`.

`scripts/install.sh server` points to the guides for deploying the hub itself. The script never changes a server.
