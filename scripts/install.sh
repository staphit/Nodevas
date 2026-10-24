#!/usr/bin/env bash
# Nodevas client installer: one command that sets up this machine as a
# runner for a Nodevas hub. It builds (or takes) the nodevas binary, installs
# it, logs the runner in, allows repositories, wires the coding agents found
# here into the agent network, and starts the runner at login.
#
# Works in Git Bash / MSYS on Windows, on Linux and on macOS (bash 3.2 and
# up). Re-running it is safe: every step checks what is already there.
# Secrets (the runner token, the Cloudflare Access client secret) come from
# the environment or a prompt, never from flags, and are never printed.
#
#   scripts/install.sh --hub https://nodevas.example.com --repo ~/code/app
#   scripts/install.sh --dry-run --hub https://nodevas.example.com
#   scripts/install.sh status
#   scripts/install.sh uninstall
#
# See docs/install.md.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
	cat <<'EOF'
usage: install.sh [install] [options]     set this machine up as a runner (default)
       install.sh status [--config <file>] show the runner's settings and agents
       install.sh uninstall [options]     remove the login service, agent wiring and binary
       install.sh server                  how to deploy the hub itself (prints the steps)

binary:
  --binary <path>          use this nodevas binary instead of building one
  --from-source <dir>      build from this checkout (default: the checkout this
                           script is in, when it is one); needs Go 1.26+
  --prefix <dir>           where to install it (default: ~/.local/bin)

hub and login:
  --hub <url>              the hub, e.g. https://nodevas.example.com
                           runner token: prompted (hidden); $NODEVAS_RUNNER_TOKEN only for unattended installs
                           (create one on the hub: Runners -> Add runner)
  --name <label>           this machine's name in the hub's log lines
  --access-client-id <id>  Cloudflare Access service token in front of the hub;
                           its secret: $NODEVAS_ACCESS_CLIENT_SECRET, else prompted
  --relogin                log in again even when a token is already saved
  --config <file>          runner settings file (default: the runner's own default)

work:
  --repo <path>            allow runs in this git repository (repeatable)
  --data-dir <dir>         where run worktrees go
  --auto-accept            start covered offers without asking here (see warning)
  --no-auto-accept         turn auto-accept off

agents:
  --agents <list>          agents to wire (claude,codex,gemini,opencode,antigravity);
                           default: every one installed here
  --agents-server <url>    also join the sessions you start by hand to this server
                           (default: only when --hub is on this machine; with a
                           remote hub runs report results, your sessions stay local)
  --no-agents              do not touch any agent's config

service:
  --no-service             do not start the runner at login
  --restart                restart a runner that is already running

general:
  --yes, -y                do not ask before changing agent configs or removing files
  --dry-run, -n            show every step, change nothing (a build still goes to a temp dir)
  --purge                  uninstall: also delete the runner settings file
  -h, --help               this text
EOF
}

# --- options ----------------------------------------------------------------

COMMAND=install
BINARY=""
SOURCE=""
PREFIX="${HOME}/.local/bin"
HUB=""
NAME=""
ACCESS_ID=""
RELOGIN=0
CONFIG=""
REPOS=()
DATA_DIR=""
AUTO_ACCEPT=""
AGENTS=""
AGENTS_SERVER=""
NO_AGENTS=0
NO_SERVICE=0
RESTART=0
YES=0
DRY=0
PURGE=0
NEEDS_RESTART=0

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

need_value() {
	[ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"
}

if [ $# -gt 0 ]; then
	case "$1" in
	install | status | uninstall | server)
		COMMAND="$1"
		shift
		;;
	esac
fi

while [ $# -gt 0 ]; do
	case "$1" in
	--*=*)
		# --opt=value: split it and go round again.
		opt="${1%%=*}"
		val="${1#*=}"
		shift
		set -- "$opt" "$val" "$@"
		continue
		;;
	esac
	case "$1" in
	--binary) need_value "$@"; BINARY="$2"; shift 2 ;;
	--from-source) need_value "$@"; SOURCE="$2"; shift 2 ;;
	--prefix) need_value "$@"; PREFIX="$2"; shift 2 ;;
	--hub) need_value "$@"; HUB="${2%/}"; shift 2 ;;
	--name) need_value "$@"; NAME="$2"; shift 2 ;;
	--access-client-id) need_value "$@"; ACCESS_ID="$2"; shift 2 ;;
	--relogin) RELOGIN=1; shift ;;
	--config) need_value "$@"; CONFIG="$2"; shift 2 ;;
	--repo) need_value "$@"; REPOS+=("$2"); shift 2 ;;
	--data-dir) need_value "$@"; DATA_DIR="$2"; shift 2 ;;
	--auto-accept) AUTO_ACCEPT=true; shift ;;
	--no-auto-accept) AUTO_ACCEPT=false; shift ;;
	--agents) need_value "$@"; AGENTS="$2"; shift 2 ;;
	--agents-server) need_value "$@"; AGENTS_SERVER="${2%/}"; shift 2 ;;
	--no-agents) NO_AGENTS=1; shift ;;
	--no-service) NO_SERVICE=1; shift ;;
	--restart) RESTART=1; shift ;;
	--yes | -y) YES=1; shift ;;
	--dry-run | -n) DRY=1; shift ;;
	--purge) PURGE=1; shift ;;
	-h | --help) usage; exit 0 ;;
	--token | --access-client-secret)
		die "$1: secrets are not taken as flags; set NODEVAS_RUNNER_TOKEN / NODEVAS_ACCESS_CLIENT_SECRET or answer the prompt"
		;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

# --- platform and helpers ---------------------------------------------------

case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) OS=windows; EXE=.exe ;;
Darwin) OS=macos; EXE="" ;;
Linux) OS=linux; EXE="" ;;
*) die "unsupported system: $(uname -s)" ;;
esac

say() { printf '==> %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }

# show prints a command the way it would run, on stderr, so a command whose
# output is discarded still shows.
show() {
	local out="" arg
	for arg in "$@"; do
		out+="$(printf '%q' "$arg") "
	done
	printf '    $ %s\n' "${out% }" >&2
}

# run shows a command and runs it, unless this is a dry run.
run() {
	show "$@"
	if [ "$DRY" = 1 ]; then
		return 0
	fi
	"$@"
}

# native turns a path into the form nodevas (a native program) takes: on
# Windows C:\..., elsewhere unchanged.
native() {
	if [ "$OS" = windows ] && command -v cygpath >/dev/null 2>&1; then
		cygpath -w "$1"
	else
		printf '%s\n' "$1"
	fi
}

# unix turns a path from the environment (Windows-style on Windows) into one
# this shell can use.
unix() {
	if [ "$OS" = windows ] && command -v cygpath >/dev/null 2>&1; then
		cygpath -u "$1"
	else
		printf '%s\n' "$1"
	fi
}

confirm() {
	local prompt="$1" answer
	if [ "$YES" = 1 ] || [ "$DRY" = 1 ]; then
		# A dry run asks nothing: run only shows what a yes would do.
		return 0
	fi
	if [ ! -t 0 ]; then
		note "no terminal to ask \"$prompt\"; skipped (pass --yes to do it anyway)"
		return 1
	fi
	read -r -p "$prompt [y/N] " answer
	case "$answer" in
	y | Y | yes | YES) return 0 ;;
	esac
	return 1
}

TMP_DIR=""
cleanup() {
	if [ -n "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
}
trap cleanup EXIT

INSTALLED="${PREFIX%/}/nodevas${EXE}"
# NV is the binary the steps run: the installed one, or (in a dry run, before
# anything is installed) the one just built.
NV="$INSTALLED"

# CFG is --config <file> for every runner subcommand, when one was given.
# It is expanded as ${CFG[@]+"${CFG[@]}"}: bash before 4.4 (macOS ships 3.2)
# treats an empty array as unset under set -u.
CFG=()
if [ -n "$CONFIG" ]; then
	CFG=(--config "$(native "$CONFIG")")
fi

# STATUS caches `nodevas runner status`, which probes every agent CLI and
# takes a while; forget_status drops it after a change.
STATUS=""
forget_status() { STATUS=""; }

# status_field prints one field of `nodevas runner status`: hub, token,
# access or config.
status_field() {
	if [ -z "$STATUS" ]; then
		STATUS="$("$NV" runner status ${CFG[@]+"${CFG[@]}"} 2>/dev/null || true)"
	fi
	printf '%s\n' "$STATUS" | sed -n "s/^$1: *//p" | head -n 1
}

# --- steps ------------------------------------------------------------------

go_is_new_enough() {
	local v major minor
	v="$(go env GOVERSION 2>/dev/null)" || return 1
	v="${v#go}"
	major="${v%%.*}"
	minor="${v#*.}"
	minor="${minor%%.*}"
	minor="${minor%%[!0-9]*}"
	[ "${major:-0}" -gt 1 ] || { [ "${major:-0}" -eq 1 ] && [ "${minor:-0}" -ge 26 ]; }
}

step_binary() {
	local built
	if [ -n "$BINARY" ]; then
		[ -f "$BINARY" ] || die "--binary $BINARY: no such file"
		built="$BINARY"
		say "using the binary $BINARY"
	else
		local src="$SOURCE"
		if [ -z "$src" ] && [ -f "$SCRIPT_DIR/../go.mod" ] && [ -d "$SCRIPT_DIR/../cmd/nodevas" ]; then
			src="$SCRIPT_DIR/.."
		fi
		if [ -z "$src" ]; then
			die "no binary: pass --binary <path>, or run this script from a nodevas checkout (or --from-source <dir>)"
		fi
		src="$(cd "$src" && pwd)"
		[ -d "$src/cmd/nodevas" ] || die "$src is not a nodevas checkout (no cmd/nodevas)"
		command -v go >/dev/null 2>&1 || die "building needs Go 1.26 or newer (https://go.dev/dl/), or pass --binary"
		go_is_new_enough || die "building needs Go 1.26 or newer; this is $(go env GOVERSION)"
		TMP_DIR="$(mktemp -d)"
		built="$TMP_DIR/nodevas${EXE}"
		say "building nodevas from $src"
		show go build -tags nomsgpack -trimpath -o "$built" ./cmd/nodevas
		# Even a dry run builds, into a temp dir: the later steps preview
		# themselves with it. Nothing outside the temp dir changes.
		(cd "$src" && CGO_ENABLED=0 go build -tags nomsgpack -trimpath -o "$(native "$built")" ./cmd/nodevas)
	fi

	say "installing to $INSTALLED"
	if [ "$DRY" = 1 ]; then
		show mkdir -p "$PREFIX"
		show cp "$built" "$INSTALLED"
		if [ ! -f "$INSTALLED" ]; then
			NV="$built"
		fi
	elif [ -f "$INSTALLED" ] && cmp -s "$built" "$INSTALLED"; then
		note "already up to date"
	else
		run mkdir -p "$PREFIX"
		if [ -f "$INSTALLED" ] && [ "$OS" = windows ]; then
			# A running nodevas.exe cannot be overwritten, but it can be
			# renamed; the old copy goes on the next install.
			rm -f "$INSTALLED.old" 2>/dev/null || true
			run mv -f "$INSTALLED" "$INSTALLED.old"
		fi
		run cp "$built" "$INSTALLED"
		run chmod +x "$INSTALLED"
		NEEDS_RESTART=1
	fi
	case ":$PATH:" in
	*":$PREFIX:"* | *":${PREFIX%/}:"*) ;;
	*)
		note "$PREFIX is not on PATH. Add it, e.g. in ~/.bashrc:"
		note "  export PATH=\"$PREFIX:\$PATH\""
		if [ "$OS" = windows ]; then
			note "and for Windows programs (PowerShell):"
			note "  [Environment]::SetEnvironmentVariable('Path', \$env:Path + ';$(native "$PREFIX")', 'User')"
		fi
		;;
	esac
}

step_login() {
	local saved_hub saved_token saved_access
	saved_hub="$(status_field hub)"
	saved_token="$(status_field token)"
	saved_access="$(status_field access)"
	[ "$saved_hub" = "(not set)" ] && saved_hub=""

	if [ -z "$HUB" ]; then
		[ -n "$saved_hub" ] || die "--hub <url> is needed the first time"
		HUB="$saved_hub"
	fi
	if [ "$saved_hub" = "$HUB" ] && [ "$saved_token" = "(set)" ] && [ "$RELOGIN" = 0 ] &&
		[ -z "${NODEVAS_RUNNER_TOKEN:-}" ] && { [ -z "$ACCESS_ID" ] || [ -n "$saved_access" ]; }; then
		say "already logged in to $HUB (pass --relogin, or NODEVAS_RUNNER_TOKEN, to replace the token)"
		return 0
	fi
	if [ -n "$saved_access" ] && [ -z "$ACCESS_ID" ]; then
		die "this runner passes a Cloudflare Access service token; logging in again without --access-client-id would drop it. Pass --access-client-id <id> (secret in NODEVAS_ACCESS_CLIENT_SECRET)"
	fi

	say "logging the runner in to $HUB"
	local -a args=(runner login --hub "$HUB")
	if [ -n "$ACCESS_ID" ]; then
		args+=(--access-client-id "$ACCESS_ID")
	fi
	if [ -n "$NAME" ]; then
		args+=(--name "$NAME")
	fi
	args+=(${CFG[@]+"${CFG[@]}"})
	show "$NV" "${args[@]}"
	note "(the token goes on its stdin, not the command line)"
	if [ "$DRY" = 1 ]; then
		return 0
	fi

	local token="${NODEVAS_RUNNER_TOKEN:-}" secret="${NODEVAS_ACCESS_CLIENT_SECRET:-}"
	if [ -z "$token" ]; then
		[ -t 0 ] || die "no runner token: set NODEVAS_RUNNER_TOKEN (create one on the hub: Runners -> Add runner)"
		read -r -s -p "runner token (from the hub's Runners -> Add runner): " token
		echo
	fi
	[ -n "$token" ] || die "no runner token given"
	if [ -n "$ACCESS_ID" ] && [ -z "$secret" ]; then
		[ -t 0 ] || die "no Access client secret: set NODEVAS_ACCESS_CLIENT_SECRET"
		read -r -s -p "Cloudflare Access client secret: " secret
		echo
		[ -n "$secret" ] || die "no Access client secret given"
	fi
	# printf is a shell builtin: the secrets never show in a process list.
	if [ -n "$ACCESS_ID" ]; then
		printf '%s\n%s\n' "$token" "$secret" | "$NV" "${args[@]}" | sed 's/^/    /'
	else
		printf '%s\n' "$token" | "$NV" "${args[@]}" | sed 's/^/    /'
	fi
	token="" secret=""
	forget_status
	NEEDS_RESTART=1
}

step_repos() {
	local repo abs out
	for repo in ${REPOS[@]+"${REPOS[@]}"}; do
		[ -d "$repo" ] || die "--repo $repo: no such directory"
		abs="$(cd "$repo" && pwd)"
		git -C "$abs" rev-parse --git-dir >/dev/null 2>&1 || die "--repo $repo is not a git repository"
		say "allowing runs in $abs"
		show "$NV" runner repo add --yes "$(native "$abs")" ${CFG[@]+"${CFG[@]}"}
		[ "$DRY" = 1 ] && continue
		if out="$("$NV" runner repo add --yes "$(native "$abs")" ${CFG[@]+"${CFG[@]}"} 2>&1)"; then
			note "$out"
			NEEDS_RESTART=1
		elif printf '%s' "$out" | grep -q "already added"; then
			note "already allowed"
		else
			printf '%s\n' "$out" >&2
			die "could not add $abs"
		fi
	done
}

step_settings() {
	if [ -n "$DATA_DIR" ]; then
		say "run worktrees go to $DATA_DIR"
		[ "$DRY" = 1 ] || mkdir -p "$DATA_DIR"
		run "$NV" runner set dataDir "$(native "$DATA_DIR")" ${CFG[@]+"${CFG[@]}"} >/dev/null
		NEEDS_RESTART=1
	fi
	if [ -n "$AUTO_ACCEPT" ]; then
		say "auto-accept $AUTO_ACCEPT"
		if [ "$AUTO_ACCEPT" = true ]; then
			warn "with auto-accept on, runs dispatched from the hub start here without anyone saying yes,"
			warn "within the repositories, agents and modes on the auto-accept list (nodevas runner autoaccept show)."
		fi
		run "$NV" runner set autoAccept "$AUTO_ACCEPT" ${CFG[@]+"${CFG[@]}"} >/dev/null
		NEEDS_RESTART=1
	fi
}

# loopback_url: whether a URL's host is this machine.
loopback_url() {
	local host="${1#*://}"
	host="${host%%/*}"
	case "$host" in
	\[*\]*) host="${host%%]*}]" ;;
	*) host="${host%%:*}" ;;
	esac
	case "$host" in
	localhost | 127.* | "[::1]") return 0 ;;
	esac
	return 1
}

step_agents() {
	if [ "$NO_AGENTS" = 1 ]; then
		return 0
	fi
	local server="${AGENTS_SERVER:-$HUB}" found="" cli
	for cli in claude codex gemini opencode agy; do
		if command -v "$cli" >/dev/null 2>&1; then
			found+="${found:+ }$cli"
		fi
	done
	local -a args=(setup-agent --server "$server")
	if [ -z "$AGENTS_SERVER" ] && ! loopback_url "$HUB"; then
		# The hub is elsewhere: runs report their results through the runner, and
		# the sessions you start by hand stay on this machine. Only what runs
		# need from your own config is changed (Antigravity's allow rule).
		say "preparing the coding agents for runner runs (on PATH: ${found:-none}); your own sessions stay off the hub"
		args=(setup-agent --runs-only)
		if [ -n "$AGENTS" ]; then
			args+=(--agents "$AGENTS")
		elif command -v agy >/dev/null 2>&1; then
			args+=(--agents antigravity)
		else
			note "nothing to change: runner runs bring their own MCP server"
			return 0
		fi
	else
		say "wiring the coding agents into the agent network (on PATH: ${found:-none})"
		if [ -n "$AGENTS" ]; then
			args+=(--agents "$AGENTS")
		fi
	fi
	show "$NV" "${args[@]}" --dry-run
	local preview
	if ! preview="$("$NV" "${args[@]}" --dry-run 2>&1)"; then
		printf '%s\n' "$preview" | sed 's/^/    /'
		note "skipped: nothing to wire"
		return 0
	fi
	printf '%s\n' "$preview" | sed 's/^/    /'
	if [ "$DRY" = 1 ]; then
		return 0
	fi
	if ! printf '%s' "$preview" | grep -q "would change"; then
		note "already wired"
		return 0
	fi
	if confirm "Change these agent configs (each is backed up once as <file>.bak-nodevas)?"; then
		run "$NV" "${args[@]}" | sed 's/^/    /'
	fi
}

# --- the login service ------------------------------------------------------

service_path() {
	case "$OS" in
	windows) printf '%s\n' "$(unix "${APPDATA:?APPDATA is not set}")/Microsoft/Windows/Start Menu/Programs/Startup/nodevas-runner.vbs" ;;
	linux) printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/nodevas-runner.service" ;;
	macos) printf '%s\n' "$HOME/Library/LaunchAgents/com.nodevas.runner.plist" ;;
	esac
}

# windows_runners lists the PIDs of `nodevas runner [start]` processes run
# from the installed binary (or its renamed previous copy), and only those: a
# runner started from another copy, `runner status` and the like are not ours.
windows_runners() {
	local filter="Name='nodevas.exe' OR Name='nodevas.exe.old'"
	local pattern=' runner(\s+start)?(\s+--config\s+.*)?\s*$'
	local exe old
	exe="$(native "$INSTALLED")"
	old="$(native "$INSTALLED.old")"
	case "$exe" in
	*\'*) return 0 ;;
	esac
	powershell.exe -NoProfile -NonInteractive -Command \
		"Get-CimInstance Win32_Process -Filter \"$filter\" | Where-Object { (\$_.ExecutablePath -ieq '$exe' -or \$_.ExecutablePath -ieq '$old') -and \$_.CommandLine -match '$pattern' } | ForEach-Object { \$_.ProcessId }" \
		2>/dev/null | tr -d '\r' || true
}

# write_file writes content to path unless it is already there; it returns 1
# when nothing changed.
write_file() {
	local path="$1" content="$2"
	if [ -f "$path" ] && [ "$(cat "$path")" = "$content" ]; then
		note "$path is up to date"
		return 1
	fi
	show write "$path"
	if [ "$DRY" = 1 ]; then
		printf '%s\n' "$content" | sed 's/^/      | /'
		return 0
	fi
	mkdir -p "$(dirname "$path")"
	printf '%s\n' "$content" >"$path"
	return 0
}

service_windows() {
	local path="$1" exe log cfgline="" pids pid
	exe="$(native "$INSTALLED")"
	log="$(unix "${LOCALAPPDATA:?LOCALAPPDATA is not set}")/nodevas/runner.log"
	[ "$DRY" = 1 ] || mkdir -p "$(dirname "$log")"
	log="$(native "$log")"
	if [ -n "$CONFIG" ]; then
		cfgline=" --config \"\"$(native "$CONFIG")\"\""
	fi
	case "$exe$log$CONFIG" in
	*%* | *\"*) die "the binary, log or config path holds % or a quote, which the login script cannot quote" ;;
	esac
	# A Startup-folder script needs no administrator rights, and wscript
	# runs it without a window. In a VBScript string "" is one quote; the
	# command it runs is: cmd.exe /d /s /c ""<exe>" runner start >> "<log>" 2>&1"
	write_file "$path" "' nodevas runner: written by scripts/install.sh; starts the runner hidden at logon.
' Its log: $log
Set shell = CreateObject(\"WScript.Shell\")
shell.Run \"cmd.exe /d /s /c \"\"\"\"$exe\"\" runner start$cfgline >> \"\"$log\"\" 2>&1\"\"\", 0, False" || true
	pids="$(windows_runners)"
	if [ -n "$pids" ] && [ "$DRY" = 1 ]; then
		note "the runner is already running (pid $(echo $pids)); a real run would offer to restart it"
	elif [ -n "$pids" ]; then
		if [ "$RESTART" = 1 ] || { [ "$NEEDS_RESTART" = 1 ] && confirm "Restart the running runner (pid $(echo $pids)) to pick up the changes?"; }; then
			for pid in $pids; do
				run taskkill //PID "$pid" //F >/dev/null
			done
			run wscript.exe "$(native "$path")"
		elif [ "$NEEDS_RESTART" = 1 ]; then
			note "the runner is already running (pid $(echo $pids)); restart it to pick up the changes (--restart)"
		else
			note "the runner is running (pid $(echo $pids))"
		fi
	else
		run wscript.exe "$(native "$path")"
	fi
	note "log: $log"
}

service_linux() {
	local path="$1" execstart="" a changed=0
	# systemd splits ExecStart on spaces and honours double quotes.
	for a in "$INSTALLED" runner start ${CFG[@]+"${CFG[@]}"}; do
		case "$a" in
		*\"* | *\\*) die "$a holds a quote or a backslash, which a systemd unit cannot take as is" ;;
		esac
		execstart+="\"$a\" "
	done
	if write_file "$path" "# nodevas runner: written by scripts/install.sh
[Unit]
Description=Nodevas runner (connects this machine to its hub)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${execstart% }
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target"; then
		changed=1
	fi
	command -v systemctl >/dev/null 2>&1 || die "no systemctl: start the runner yourself (nodevas runner), or pass --no-service"
	run systemctl --user daemon-reload
	run systemctl --user enable nodevas-runner.service
	if [ "$changed" = 1 ] || [ "$NEEDS_RESTART" = 1 ] || [ "$RESTART" = 1 ]; then
		run systemctl --user restart nodevas-runner.service
	else
		run systemctl --user start nodevas-runner.service
	fi
	note "log: journalctl --user -u nodevas-runner -f"
	note "to keep it running while you are logged out: loginctl enable-linger $USER"
}

service_macos() {
	local path="$1" logf="$HOME/Library/Logs/nodevas-runner.log" argxml="" a changed=0
	local domain
	domain="gui/$(id -u)"
	for a in "$INSTALLED" runner start ${CFG[@]+"${CFG[@]}"}; do
		a="${a//&/&amp;}"
		a="${a//</&lt;}"
		a="${a//>/&gt;}"
		argxml+="    <string>$a</string>
"
	done
	if write_file "$path" "<?xml version=\"1.0\" encoding=\"UTF-8\"?>
<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">
<!-- nodevas runner: written by scripts/install.sh -->
<plist version=\"1.0\">
<dict>
  <key>Label</key><string>com.nodevas.runner</string>
  <key>ProgramArguments</key>
  <array>
${argxml}  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>$logf</string>
  <key>StandardErrorPath</key><string>$logf</string>
</dict>
</plist>"; then
		changed=1
	fi
	if launchctl print "$domain/com.nodevas.runner" >/dev/null 2>&1; then
		if [ "$changed" = 1 ]; then
			run launchctl bootout "$domain/com.nodevas.runner" || true
			run launchctl bootstrap "$domain" "$path"
		elif [ "$NEEDS_RESTART" = 1 ] || [ "$RESTART" = 1 ]; then
			run launchctl kickstart -k "$domain/com.nodevas.runner"
		fi
	else
		run launchctl bootstrap "$domain" "$path"
	fi
	note "log: $logf"
}

step_service() {
	if [ "$NO_SERVICE" = 1 ]; then
		say "not starting the runner at login (--no-service); start it with: nodevas runner"
		return 0
	fi
	local path
	path="$(service_path)"
	say "starting the runner at login ($path)"
	"service_$OS" "$path"
	note "a runner started at login has no terminal to ask at: offers auto-accept does not cover are"
	note "declined. Use --auto-accept (see nodevas runner autoaccept show), or run nodevas runner in a terminal."
}

remove_service() {
	local path pids pid
	path="$(service_path)"
	case "$OS" in
	windows)
		pids="$(windows_runners)"
		for pid in $pids; do
			run taskkill //PID "$pid" //F >/dev/null
		done
		;;
	linux)
		if command -v systemctl >/dev/null 2>&1 && [ -f "$path" ]; then
			run systemctl --user disable --now nodevas-runner.service || true
		fi
		;;
	macos)
		if launchctl print "gui/$(id -u)/com.nodevas.runner" >/dev/null 2>&1; then
			run launchctl bootout "gui/$(id -u)/com.nodevas.runner" || true
		fi
		;;
	esac
	if [ -f "$path" ]; then
		if grep -q "written by scripts/install.sh" "$path"; then
			run rm -f "$path"
			if [ "$OS" = linux ] && command -v systemctl >/dev/null 2>&1; then
				run systemctl --user daemon-reload
			fi
		else
			warn "$path was not written by this script; left in place"
		fi
	else
		note "no login service"
	fi
}

# --- commands ---------------------------------------------------------------

# check_inputs fails on a mistyped argument before anything changes.
check_inputs() {
	local repo
	for repo in ${REPOS[@]+"${REPOS[@]}"}; do
		[ -d "$repo" ] || die "--repo $repo: no such directory"
		git -C "$repo" rev-parse --git-dir >/dev/null 2>&1 || die "--repo $repo is not a git repository"
	done
	case "$HUB" in
	"" | http://* | https://*) ;;
	*) die "--hub $HUB: give the full URL, e.g. https://nodevas.example.com" ;;
	esac
	case "$AGENTS_SERVER" in
	"" | http://* | https://*) ;;
	*) die "--agents-server $AGENTS_SERVER: give the full URL" ;;
	esac
}

cmd_install() {
	check_inputs
	if [ "$DRY" = 1 ]; then
		say "dry run: nothing outside a temp dir changes"
	fi
	step_binary
	step_login
	# The secrets are in runner.json now. Nothing started from here on (the
	# runner service, the agent CLIs it spawns) inherits them from this shell.
	unset NODEVAS_RUNNER_TOKEN NODEVAS_ACCESS_CLIENT_SECRET
	step_repos
	step_settings
	step_agents
	step_service
	say "done"
	if [ "$DRY" = 0 ]; then
		"$NV" runner status ${CFG[@]+"${CFG[@]}"} | sed 's/^/    /'
	fi
	cat <<'EOF'

next:
  - dispatch a run from the hub: open a node, Dispatch, pick this runner
  - allow more repositories:     nodevas runner repo add <path>
  - review what starts unasked:  nodevas runner autoaccept show
  - settings and agents here:    scripts/install.sh status
EOF
}

find_installed() {
	if [ ! -f "$NV" ]; then
		NV="$(command -v nodevas || true)"
	fi
}

cmd_status() {
	find_installed
	[ -n "$NV" ] || die "nodevas is not installed at $INSTALLED or on PATH"
	"$NV" runner status ${CFG[@]+"${CFG[@]}"}
	local path
	path="$(service_path)"
	if [ -f "$path" ]; then
		printf 'login service: %s\n' "$path"
	else
		printf 'login service: (none)\n'
	fi
}

cmd_uninstall() {
	find_installed
	say "removing the login service"
	remove_service
	if [ -n "$NV" ]; then
		if [ "$NO_AGENTS" = 0 ]; then
			say "taking nodevas out of the agents' configs"
			local -a args=(setup-agent --remove)
			if [ -n "$AGENTS" ]; then
				args+=(--agents "$AGENTS")
			fi
			if confirm "Remove nodevas's MCP server and hooks from the agent configs here?"; then
				run "$NV" "${args[@]}" | sed 's/^/    /' || true
			fi
		fi
		local cfgfile
		cfgfile="$(status_field config)"
		if [ -n "$cfgfile" ] && [ -f "$(unix "$cfgfile")" ]; then
			if [ "$PURGE" = 1 ]; then
				say "deleting the runner settings $cfgfile"
				if confirm "Delete $cfgfile (token, repositories, settings)?"; then
					run rm -f "$(unix "$cfgfile")"
				fi
			else
				note "kept the runner settings $cfgfile (--purge deletes them)"
			fi
		fi
	fi
	if [ -f "$INSTALLED" ]; then
		say "removing $INSTALLED"
		if confirm "Delete $INSTALLED?"; then
			run rm -f "$INSTALLED" "$INSTALLED.old"
		fi
	fi
	say "done"
}

cmd_server() {
	cat <<'EOF'
The hub (nodevas serve) runs on the server; a runner never serves. To deploy it:

  - TrueNAS / Docker Compose: deploy/truenas/README.md (compose.yaml, compose.postgres.yaml,
    the existing-tunnel variant, and the network fix script it describes)
  - Oracle Cloud:             deploy/oci/
  - behind Cloudflare Access: create a service token for runners, then install runners with
    --access-client-id <id> (secret in NODEVAS_ACCESS_CLIENT_SECRET)

This script does not change servers.
EOF
}

case "$COMMAND" in
install) cmd_install ;;
status) cmd_status ;;
uninstall) cmd_uninstall ;;
server) cmd_server ;;
esac
