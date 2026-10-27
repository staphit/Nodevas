# Install a runner

A runner is the machine your coding agents run on. The hub (`nodevas serve`) shows the board, the runs and their changes, but never starts an agent; your runner connects out to it, runs Claude Code, Codex, Gemini CLI, OpenCode or Antigravity CLI with your own logins, and sends the results back. Your agent logins and your `gh` login stay on the runner.

`scripts/install.sh` does the whole setup in one command, and is safe to run again:

1. Fetches the `nodevas` binary that GitHub Actions built from the commit your hub runs, checks it against GitHub (see [How the binary is checked](#how-the-binary-is-checked)), and installs it to `~/.local/bin`. Go is not needed. (Downloads work once GitHub Actions has published a build to `ghcr.io/staphit/nodevas-cli`; until then, use a checkout with Go.) In a checkout with Go 1.26 or newer it builds that checkout instead (`--download` fetches anyway); `--binary <path>` takes one you have.
2. Logs the runner in to the hub: with a one-time device link (`--link`, the easy way on a new PC; see below), or with a runner token. Either comes from a hidden prompt (or, for an unattended install, `NODEVAS_RUNNER_LINK` / `NODEVAS_RUNNER_TOKEN`), never from a flag.
3. Adds the Cloudflare Access service token when the hub sits behind Access (`--access-client-id`; its secret from a hidden prompt, or `NODEVAS_ACCESS_CLIENT_SECRET`). Once logged in, the script unsets both variables, so the runner it starts (and the agents that runner spawns) never inherit them.
4. Allows runs in each `--repo`.
5. Wires the agents installed here into Agent chat (`nodevas setup-agent`). It shows the changes first and asks, unless you pass `--yes`.
6. Starts the runner at login:
   - Windows: a script in your Startup folder. It needs no administrator rights, runs without a window, and logs to `%LOCALAPPDATA%\nodevas\runner.log`.
   - Linux: a systemd user unit.
   - macOS: a LaunchAgent.

It works in Git Bash on Windows, and on Linux and macOS. It needs `curl`; a clone of the repository is not needed:

```bash
curl -fsSLO https://raw.githubusercontent.com/staphit/Nodevas/master/scripts/install.sh
less install.sh    # read what you are about to run
bash install.sh --hub https://nodevas.example.com --link --repo ~/code/my-app
```

## Hub and runner on one machine

Using Nodevas alone on one PC, skip the install script and start both with one command:

```sh
nodevas serve --project ./workspace --repo ~/code/myapp
```

A standalone `serve` (loopback `--listen`, no `--behind-proxy`, TLS or Cloudflare Access) starts its runner unasked; `--no-runner` turns that off. `serve` registers a runner named "this machine" for itself (as **Settings → Runners → Add runner** would), keeps its token in a settings file of its own under your user config directory (`nodevas/local-runners/<id>.json`, owner-only; it is printed at startup, and your usual `runner.json` is left alone), and starts `nodevas runner` with it as a separate process. A restart reuses the runner instead of adding another. Each `--repo` (repeatable) is allowed for runs, as `nodevas runner repo add --yes` would, and stays allowed. Without `--repo` the runner uses the repositories it already allows; with none, it says so at startup and no run can start. The current directory is never added for you.

Nothing is skipped: the runner signs in with its token like any other, and each run is asked about in the terminal you started `serve` in (the question appears among the log lines; type `y` and Enter). Started without a terminal, it declines every run its auto-accept policy does not cover. Auto-accept stays off until you turn it on with `nodevas runner autoaccept … --config <that file>`; other settings take the same `--config`.

If the runner cannot start, `serve` warns and serves without it. `--with-runner` (or `--repo`) makes that fatal instead. The runner stops with `serve`; on Windows and Linux even a killed `serve` takes it along. Both work only on a loopback `--listen` without `--behind-proxy` or TLS. A hub other people can reach never starts a runner: it has accounts, and its runners are linked by their owners as below.

## Set up a new PC with a device link

A device link carries everything a new machine needs: the hub's address, a one-time code that becomes the runner token, and, when an administrator stored one and an administrator made the link, the Cloudflare Access service token runners pass the edge with. A member's links never carry it (the token passes the edge for the whole hostname); a member behind Access gets the pair from an administrator and adds `--access-client-id`. Nothing has to be copied by hand from Cloudflare or the Runners page.

1. On the hub, open **Settings → Runners**, then **Link a new device** (設定 → Runner → 連結新裝置). Name the device; that becomes the runner's name. Verify it's you when asked (a fresh step-up is required).
2. The hub shows the link (`nvlink1.…`) once. It works once and for 10 minutes. Copy it.
3. On the new machine, either
   ```bash
   nodevas runner login --link          # nodevas already installed
   scripts/install.sh --hub https://nodevas.example.com --link --repo ~/code/my-app
   ```
   and paste the link at the prompt. It is not echoed.
4. Start the runner (`nodevas runner`; the install script does that for you).

The link is a credential: whoever holds it within those 10 minutes can add a runner to your account, and it may contain the Access service token. Paste it only into the new machine's prompt, never into a chat, a ticket or a command line. `nodevas runner login` refuses a link given as an argument, because every process on the machine can read a command line; pipe it on stdin (`nodevas runner login --link < file`) if you must script it. The hub keeps only a hash of the code, in memory (a restart forgets every unused link), allows at most five unused links per person, and refuses, for up to ten minutes, a source that sent five wrong codes. You get an email when a link is used, if the hub can send mail.

The runner is created when the link is redeemed and belongs to whoever made the link. A link made before your account changed (removed, renamed, new password, address or role) no longer works.

### Store the runner access token (administrators)

Behind Cloudflare Access a runner cannot do the browser sign-in, so it passes the edge with a service token. Create one in Cloudflare Zero Trust (Access, Service Auth), allow it in the hub's Access application policy (a Service Auth rule), then, as an administrator on the hub: **Runners**, **Runner access token (Cloudflare Access)**, enter the client ID and the client secret, and Save (a fresh step-up is required). The hub keeps the pair encrypted (`secrets/runner-access-*.enc` beside its other secrets, under the machine key or `NODEVAS_SECRET_KEY`) and never shows the secret again; from then on every device link carries it. Remove it there to stop putting it into new links. Runners already set up keep their copy, so to cut them off revoke or rotate the service token in Cloudflare.

Access is never bypassed: the redeem request goes through the edge with that service token like every other runner request. Without a stored token, a link still works for a hub that is not behind Access.

### Revoke a device

On the hub, **Runners**, the trash icon on the runner's card. Its token stops working at once and a connected runner is disconnected. Revoke a lost or retired PC there; the Access service token it holds is shared with your other runners, so rotate it in Cloudflare (and store the new one on the hub) if the PC was stolen.

## Install with a runner token

Create a runner token on the hub first (Runners, then Add runner). Then, with the script fetched as above (or from a clone of the repository):

```bash
scripts/install.sh \
  --hub https://nodevas.example.com \
  --repo ~/code/my-app \
  --repo ~/code/other-app
```

The script asks for the token without echoing it. Avoid `NODEVAS_RUNNER_TOKEN=... scripts/install.sh`: a token typed on the command line lands in your shell history. Set the variable only where nobody types (a provisioning script that reads it from a secret store). Preview everything without changing anything:

```bash
scripts/install.sh --dry-run --hub https://nodevas.example.com --repo ~/code/my-app
```

A dry run still fetches (or builds) the binary into a temporary directory, so it can show the agent config changes it would make.

Behind Cloudflare Access:

```bash
scripts/install.sh --hub https://nodevas.example.com --access-client-id <id>.access
```

It asks for the runner token, then the Access client secret, both hidden.

## Options

| Option | What it does |
| --- | --- |
| `--download` | Fetch GitHub's build even in a checkout where Go could build one. |
| `--commit <sha>` | Fetch GitHub's build of this commit (all 40 characters) instead of the hub's. |
| `--binary <path>` / `--from-source <dir>` | Use a built binary, or build from a checkout (Go 1.26+). |
| `--standalone` | Install the binary only, for one machine with no hub (see below). |
| `--prefix <dir>` | Install somewhere other than `~/.local/bin`. |
| `--link` | Log in with a device link instead of a token (prompted hidden; `NODEVAS_RUNNER_LINK` unattended). It brings the Access service token; do not combine it with `--access-client-id`. |
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
- **Interactive runs are on by default** (`nodevas runner set interactive false` to decline them). Each is still accepted at the terminal like any run; auto-accept covers them only after `nodevas runner autoaccept mode add interactive`. Their terminal is view only: the dispatcher's keystrokes reach the agent only when you answer yes to "Allow remote typing?" while accepting, or, for auto-accepted runs, after `nodevas runner autoaccept remote-typing on`. The runner logs the size and time of each typed chunk, never its content.
- **Remote merge is off by default.** An approval on the hub is refused until you run `nodevas runner set remoteMerge true`; otherwise merge on GitHub yourself.
- **Reviews and plans run read only** and stop after 20 and 30 minutes (`reviewTimeoutMinutes`, `planTimeoutMinutes`). Their worktree holds someone else's pull request, or a base a collaborator pushed to, so the agent does not load the repository's own agent config, which could run programs as you (hooks, plugins, MCP servers): Claude Code runs with `--setting-sources user` and `--strict-mcp-config`, OpenCode with `OPENCODE_DISABLE_PROJECT_CONFIG=1` and no LSP servers or formatters (OpenCode 2 runs get a private server, `run --standalone`, because its shared background service would ignore that variable), and before the agent starts, and again after it exits, the runner deletes from the worktree's root `.claude/settings.json`, `.claude/settings.local.json`, `.mcp.json`, `opencode.json(c)`, `.opencode/`, `tui.json(c)`, `.gemini/`, `.agents/`, `_agents/`, `.agent/`, `.env` and `.codex/`. Deleting is the only way for Gemini CLI, agy and Codex, which have no switch for it (Codex loads a trusted project's `.codex/config.toml`, and a run's worktree inherits the trust you gave the checkout), and for OpenCode 1.18, whose newer config loader ignores the variable. The worktree is thrown away after, so nothing deleted is committed. Implement and revise runs work on your own branches and keep the repository's agent config, as trusted as the repository itself.
- **Gemini CLI runs trust their worktree** (`GEMINI_CLI_TRUST_WORKSPACE=true`): headless Gemini CLI 0.65 refuses to start in a folder it does not trust. It also ignores a system settings file not owned by an administrator, so a review or plan gets the nodevas MCP server from a `.gemini/settings.json` the runner writes into its throwaway worktree; an implement run gets only the `nodevas` server in your own `~/.gemini/settings.json`, if you have one.
- **Git hooks never run from runs.** Every git command the runner runs (commit, push, worktree, diff) uses an empty `core.hooksPath`, so neither the repository's hooks nor hooks a run wrote run as you. That includes Git LFS's `pre-push` hook: LFS objects a run adds are not uploaded when the runner pushes, so a repository that needs LFS uploads has to push them another way (`git lfs push --all origin <branch>` from your own checkout).
- **A remote hub gets results, not your sessions.** With `--hub` on another machine, runs report their events, diff, commit, PR and usage through the runner; the agent sessions you start by hand stay on this PC and are not joined to the hub. The agents step then only runs `nodevas setup-agent --runs-only`, which changes nothing but Antigravity's permission rule. `--agents-server <url>` joins your own sessions to a server they can reach on this machine.
- **Antigravity CLI** runs need `mcp(nodevas/*)` in its permissions (`nodevas setup-agent --runs-only --agents antigravity`). The runner itself never edits agy's settings.
- **A fast dispatch advisor for the autopilot (optional).** Laya runs on this PC, in a virtual environment of its own and bound to this machine only (`python -m venv ~/.laya && ~/.laya/bin/pip install "laya[serve]==0.4.14"`, then `LAYA_HOST=127.0.0.1 ~/.laya/bin/laya-serve`; on Windows the venv's programs are in `~/.laya/Scripts/`; its checkpoints are about 2.3 GB on first use), then `nodevas runner set advisorProvider systemone`. For TypeSafe's hosted Jev instead: `nodevas runner set advisorProvider systemone`, `nodevas runner set advisorBaseURL https://api.typesafe.ai`, `nodevas runner set advisorModel jev-latest`, and `nodevas runner advisor-key` (the key from stdin). See COPILOT.md.
- **Upgrading** is running the script again: it fetches GitHub's build of the commit the hub runs now (or, in a checkout with Go, builds the updated checkout). Nothing updates a runner by itself, and the hub cannot make it. On Windows, the running `nodevas.exe` is renamed to `nodevas.exe.old` so the new one can take its place. The runner picks up the new binary once restarted (`--restart`).

## How the binary is checked

The hub is not trusted to hand out the program that runs on your PC: a compromised hub can offer runs, which you still accept here, but it cannot put a binary of its own on this machine. GitHub is the trust root instead:

1. On every push to `master` (and every `v*` tag), the `cli` job in `.github/workflows/container.yml`, the workflow that also builds the hub's container image, builds `nodevas` for macOS (arm64, amd64), Linux (amd64, arm64) and Windows (amd64) with the image's flags, records a [GitHub artifact attestation](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations) of each binary's build provenance, and publishes it to GitHub's container registry as `ghcr.io/staphit/nodevas-cli:<commit>-<os>-<arch>` (and `latest-<os>-<arch>`).
2. The hub's image is built with its commit, and tells it to runners only: `nodevas runner hub-version` asks `GET /api/runner/version`, which takes a runner token (and passes Cloudflare Access with the runner's service token). On a first install the script fetches the latest build, logs in with it, asks, and fetches the hub's commit if it is another one.
3. The commit only selects a build. The script fetches it from ghcr.io over TLS and checks that the file's SHA-256 is the digest the registry lists, that the artifact names the platform and the commit asked for, and, when `gh` is installed and logged in here, `gh attestation verify --repo staphit/Nodevas --signer-workflow staphit/Nodevas/.github/workflows/container.yml --source-digest <commit>`. A build that fails any of these is not installed. Without a logged-in `gh` the provenance check is skipped with a warning; the digest still comes from GitHub, not the hub.

So a hub that lies about its commit can at worst pick another genuine GitHub build, older or newer. A hub that cannot say (one built before this, or a local build) keeps the latest build, with a warning; `--commit <sha>` picks one by hand.

To check an installed binary yourself:

```bash
nodevas runner status | grep -E '^(version|commit)'     # the commit it was built from
gh attestation verify ~/.local/bin/nodevas --repo staphit/Nodevas \
  --signer-workflow staphit/Nodevas/.github/workflows/container.yml
```

Or with [oras](https://oras.land): `oras pull ghcr.io/staphit/nodevas-cli:<commit>` gets every platform's binary and their `SHA256SUMS`; compare with `sha256sum` (macOS: `shasum -a 256`; Windows: `certutil -hashfile nodevas.exe SHA256`).

## Standalone: one machine, no hub

For Nodevas on your own PC alone (loopback, no accounts, no mail), install just the binary; no hub, link or token:

```bash
curl -fsSLO https://raw.githubusercontent.com/staphit/Nodevas/master/scripts/install.sh
bash install.sh --standalone
mkdir -p ~/nodevas-workspace
nodevas serve --project ~/nodevas-workspace     # then open http://127.0.0.1:5666
```

It fetches GitHub's latest build for this OS and checks it the same way. Run it again to upgrade. `--commit <sha>`, `--prefix` and `--dry-run` apply; the hub, login, agent and service options do not.

## Check and remove

```bash
scripts/install.sh status      # runner settings, agents found, login service
scripts/install.sh uninstall   # stop the runner, remove the login service, unwire the agents, delete the binary
scripts/install.sh uninstall --purge   # also delete the runner settings (token, repositories)
```

Uninstall asks before each removal, unless you pass `--yes`, and keeps the runner settings unless you pass `--purge`.

`scripts/install.sh server` points to the guides for deploying the hub itself. The script never changes a server.
