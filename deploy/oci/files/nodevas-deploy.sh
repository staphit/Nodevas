#!/usr/bin/env bash
#
# Install a binary that was built somewhere else, and undo it again when the
# new one does not serve.
#
# The frontend is embedded in the Go binary, so a deploy is one file: there is
# nothing to unpack, no assets to sync, and no way for the UI and the server to
# be different versions of each other. That makes the interesting part not the
# copying but the order, and the order here is chosen so that every step which
# can fail happens while the service is still up and serving the old binary:
#
#   1. Check the uploaded file before it can replace anything. Installing a
#      truncated scp and then restarting is how a deploy becomes an outage.
#   2. Keep the old binary next to the new one. Rollback is then one `mv` on a
#      box with no network — which is the state you are in when GitHub, CI or
#      the operator's laptop is the thing that is broken.
#   3. Stop, swap, start. The stop is allowed to take its time: the server
#      spends up to 5 seconds draining HTTP and up to 30 more finishing a last
#      cloud backup, and killing it inside that window abandons the backup.
#   4. Ask the port for a real answer before calling the deploy good. A unit
#      systemd reports as active can still be failing every request.
#   5. If it does not answer, put the previous binary back automatically. An
#      operator reading a workflow log is not a health check.
#
# The workspace at /var/lib/nodevas is never touched. Nothing below writes,
# moves or reads anything under it — the database, the accounts and every
# document live there, and a deploy has no business anywhere near them.

set -euo pipefail

# --------------------------------------------------------------------------
# Exit codes, from sysexits(3). A deploy is run by a machine and read by a
# person afterwards, so the status has to carry which half went wrong: "the
# upload was bad and nothing changed" and "the new binary is installed but
# would not serve, so the old one is back" are the same failed workflow run and
# very different mornings.
# --------------------------------------------------------------------------
readonly EX_USAGE=64       # bad or missing arguments
readonly EX_DATAERR=65     # the uploaded binary is corrupt or is not this program
readonly EX_UNAVAILABLE=69 # a dependency is missing, or the service would not serve
readonly EX_SOFTWARE=70    # something this script did went wrong
readonly EX_TEMPFAIL=75    # another deploy holds the lock
readonly EX_NOPERM=77      # not root

BINARY=""
EXPECTED_SHA256=""
TARGET="${NODEVAS_BIN:-/usr/local/bin/nodevas}"
UNIT="${NODEVAS_UNIT:-nodevas}"
ENV_FILE="${NODEVAS_ENV_FILE:-/etc/nodevas/nodevas.env}"
PORT="${NODEVAS_PORT:-}"
HEALTH_TIMEOUT="${NODEVAS_HEALTH_TIMEOUT:-90}"
LOCK_FILE="${NODEVAS_DEPLOY_LOCK:-/run/lock/nodevas-deploy.lock}"
MODE=deploy

usage() {
	cat <<'EOF'
Usage: nodevas-deploy.sh --binary PATH [options]
       nodevas-deploy.sh --rollback [options]

Install an already-built nodevas binary and verify that the service still
serves afterwards, rolling back automatically when it does not.

On the first install there is no account or rollback binary yet. The binary is
installed and the service is left stopped so an administrator can create the
first account; later deployments restart and health-check normally.

Options:
  --binary PATH       The new binary, as uploaded. Required unless --rollback.
  --sha256 SUM        Expected hex digest of --binary, from the machine that
                      built it. Checked before anything is installed.
  --rollback          Swap the current binary and <target>.previous, then
                      restart. For a failure that shows up later.
  --target PATH       Installed path.              [$NODEVAS_BIN]
  --unit NAME         systemd unit to restart.     [$NODEVAS_UNIT]
  --port N            Loopback port to health-check. Read from the unit's
                      environment file when omitted.
  --timeout SECONDS   How long to wait for the port to answer. Default 90.
  -h, --help          This text.

The previous binary is kept at <target>.previous. Rolling back by hand, when
this script is not available:

  systemctl stop nodevas
  mv /usr/local/bin/nodevas.previous /usr/local/bin/nodevas
  restorecon -F /usr/local/bin/nodevas
  systemctl start nodevas

Exit codes: 0 ok, 64 usage, 65 the upload is corrupt, 69 the service would not
serve (or a dependency is missing), 70 internal error, 75 another deploy is in
progress, 77 not root.
EOF
}

# stderr, because this runs under ssh from CI where stdout and stderr are the
# same log, and by hand where the level prefix is what separates a step from
# its output.
log() { printf '[nodevas-deploy] %s\n' "$*" >&2; }
die() {
	local code="$1"
	shift
	printf '[nodevas-deploy] ERROR: %s\n' "$*" >&2
	exit "$code"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--binary) BINARY="${2:-}"; shift 2 ;;
	--sha256) EXPECTED_SHA256="${2:-}"; shift 2 ;;
	--rollback) MODE=rollback; shift ;;
	--target) TARGET="${2:-}"; shift 2 ;;
	--unit) UNIT="${2:-}"; shift 2 ;;
	--port) PORT="${2:-}"; shift 2 ;;
	--timeout) HEALTH_TIMEOUT="${2:-}"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "$EX_USAGE" "unknown argument: $1" ;;
	esac
done

case "$MODE" in
deploy) [ -n "$BINARY" ] || { usage >&2; die "$EX_USAGE" "no --binary"; } ;;
rollback) [ -z "$BINARY" ] || die "$EX_USAGE" "--rollback takes no --binary" ;;
esac

case "$HEALTH_TIMEOUT" in
'' | *[!0-9]*) die "$EX_USAGE" "--timeout wants whole seconds, got: $HEALTH_TIMEOUT" ;;
esac

# Writing /usr/local/bin and restarting a unit are both root's work, and half a
# deploy done as somebody else is worse than none: the check belongs here, at
# the top, not at the first mv.
[ "$(id -u)" -eq 0 ] || die "$EX_NOPERM" "must run as root (try sudo)"

for tool in systemctl curl sha256sum; do
	command -v "$tool" >/dev/null 2>&1 ||
		die "$EX_UNAVAILABLE" "$tool is not installed"
done

# The workspace is the one thing on this machine that cannot be rebuilt from
# git, and every path this script writes is derived from --target. A --target
# somewhere under /var/lib/nodevas would point all of that at the data, so it is
# refused rather than trusted to be a typo nobody makes.
case "$TARGET" in
/var/lib/nodevas | /var/lib/nodevas/*)
	die "$EX_USAGE" "--target is inside the workspace volume; refusing: $TARGET"
	;;
esac

PREVIOUS="$TARGET.previous"
INCOMING="$TARGET.incoming"

# --------------------------------------------------------------------------
# The port to knock on.
#
# Read out of the unit's environment file rather than sourced from it: that
# file is 0600 root:root because it holds the SMTP relay password, and sourcing
# it would pull that password into this script's environment and into the
# environment of everything it runs, for the sake of one integer. The last
# assignment wins, matching how systemd reads the same file.
# --------------------------------------------------------------------------
if [ -z "$PORT" ] && [ -r "$ENV_FILE" ]; then
	PORT="$(sed -n 's/^[[:space:]]*NODEVAS_PORT[[:space:]]*=[[:space:]]*//p' "$ENV_FILE" |
		tail -n1 | tr -d '"'\''[:space:]')" || PORT=""
fi
[ -n "$PORT" ] || PORT=5666
case "$PORT" in
'' | *[!0-9]*) die "$EX_USAGE" "port is not a number: $PORT" ;;
esac

# --------------------------------------------------------------------------
# One deploy at a time.
#
# Two runs interleaving would each move a binary the other one is also moving,
# and the losing run's "previous" ends up being the other run's new binary —
# so the rollback path, the whole point of keeping it, restores the wrong
# thing. flock -n rather than waiting: a deploy queued behind another deploy
# installs a binary somebody has already superseded, and the operator watching
# the log would rather be told to run it again.
# --------------------------------------------------------------------------
mkdir -p "$(dirname "$LOCK_FILE")" 2>/dev/null || true
exec 9>"$LOCK_FILE" || die "$EX_SOFTWARE" "cannot open lock file $LOCK_FILE"
if ! flock -n 9; then
	die "$EX_TEMPFAIL" "another deploy holds $LOCK_FILE; giving up"
fi

# --------------------------------------------------------------------------
# Helpers shared by the deploy and rollback paths.
# --------------------------------------------------------------------------

# SELinux is enforcing on the Oracle Linux image this runs on. A file copied
# out of /tmp carries a tmp label, and a unit trying to exec it fails with a
# permission error that has nothing to do with the file's mode — the classic
# "I chmod 755'd it and it still will not start". Relabelling is what makes the
# swapped-in file executable *by systemd*, so it happens on every path that
# puts a file at $TARGET, including the rollback.
relabel() {
	if command -v restorecon >/dev/null 2>&1; then
		restorecon -F "$1" 2>/dev/null || log "WARNING: restorecon failed on $1"
	fi
}

stop_service() {
	# No timeout of our own, and no kill. The unit's TimeoutStopSec is 60s
	# because shutdown legitimately takes up to 35: five seconds draining
	# in-flight HTTP, then up to thirty finishing a final cloud backup. systemd
	# already bounds the wait and escalates on its own; adding a shorter bound
	# here would only cut that backup short on the one occasion it matters.
	log "stopping $UNIT (it may take ~35s to finish a last backup)"
	systemctl stop "$UNIT" || die "$EX_SOFTWARE" "systemctl stop $UNIT failed"
}

# health_check reports whether the server is answering HTTP on the loopback
# port, within the timeout.
#
# "systemctl is-active" is not the question. A binary can start, fail to open
# its database, and sit there active while every request 500s; a listener can
# be bound before the router is wired up. So the check is an actual request
# over the same loopback port Caddy proxies to, and what counts as healthy is a
# status line below 500 — a 401 or a 403 is a server that started, read its
# configuration and decided about the request, which is exactly what is being
# proved here. 000 is curl's way of saying it never got an answer.
#
# The Host header is left as the address curl connects to: the server always
# answers to its own loopback names, so this cannot fail on the hostname
# check the way a request forged with the public name would.
health_check() {
	local deadline=$((SECONDS + HEALTH_TIMEOUT)) code=""
	log "waiting up to ${HEALTH_TIMEOUT}s for http://127.0.0.1:$PORT/ to answer"
	while [ "$SECONDS" -lt "$deadline" ]; do
		# A unit that has already given up is a definite no, and waiting out
		# the rest of the timeout for it only delays the rollback.
		if ! systemctl is-active --quiet "$UNIT" 2>/dev/null; then
			if systemctl is-failed --quiet "$UNIT" 2>/dev/null; then
				log "$UNIT entered a failed state"
				return 1
			fi
		fi
		# The status goes into the variable and curl's own exit status decides
		# nothing else: on a refused connection curl prints 000 *and* exits
		# non-zero, and appending a fallback with `|| echo` would produce two
		# codes in one string that then matches neither pattern below.
		code="$(curl -sS -o /dev/null -m 5 -w '%{http_code}' \
			"http://127.0.0.1:$PORT/" 2>/dev/null)" || code=000
		case "$code" in
		000 | '') ;;
		5??) log "port answered $code; still waiting" ;;
		*)
			log "port answered $code"
			return 0
			;;
		esac
		sleep 2
	done
	log "no usable answer from 127.0.0.1:$PORT within ${HEALTH_TIMEOUT}s (last: ${code:-none})"
	return 1
}

# Last words before a non-zero exit. journalctl is where the reason actually
# is, and an operator reading a CI log has no shell on this box — so the log
# comes to them rather than the other way round.
show_recent_log() {
	if command -v journalctl >/dev/null 2>&1; then
		log "last 30 journal lines for $UNIT:"
		journalctl -u "$UNIT" -n 30 --no-pager >&2 || true
	fi
}

# --------------------------------------------------------------------------
# --rollback: the failure that showed up ten minutes later.
#
# This is a swap, not a restore, so that running it twice puts you back where
# you started. Rolling back to a binary you cannot roll forward from is a
# corner nobody wants to find at 2am, and the cost of avoiding it is one
# temporary name.
# --------------------------------------------------------------------------
if [ "$MODE" = rollback ]; then
	[ -e "$PREVIOUS" ] || die "$EX_UNAVAILABLE" "no $PREVIOUS to roll back to"
	[ -e "$TARGET" ] || die "$EX_UNAVAILABLE" "no $TARGET to roll back from"

	log "rolling back: $PREVIOUS -> $TARGET"
	stop_service
	SWAP="$TARGET.swapping"
	mv -f -- "$TARGET" "$SWAP" || die "$EX_SOFTWARE" "cannot move $TARGET aside"
	mv -f -- "$PREVIOUS" "$TARGET" || die "$EX_SOFTWARE" "cannot install $PREVIOUS"
	mv -f -- "$SWAP" "$PREVIOUS" || die "$EX_SOFTWARE" "cannot park the rolled-back binary"
	relabel "$TARGET"

	systemctl start "$UNIT" || {
		show_recent_log
		die "$EX_UNAVAILABLE" "the previous binary would not start either; see the journal"
	}
	if ! health_check; then
		show_recent_log
		# Deliberately not automatic here. The automatic rollback exists to
		# undo a change this script just made; going back again from a manual
		# rollback would put the binary the operator explicitly rejected back
		# on the box, silently.
		die "$EX_UNAVAILABLE" "rolled back, but $UNIT still does not serve; this is not a binary problem"
	fi
	log "rolled back; $TARGET is now the previously-good binary"
	log "the binary that was replaced is at $PREVIOUS (run --rollback again to return to it)"
	exit 0
fi

# --------------------------------------------------------------------------
# Step 1. Everything that can reject the upload, before anything is replaced.
#
# All of this runs with the old binary still installed and the service still
# serving. A failure from here to the swap costs nothing but the workflow run.
# --------------------------------------------------------------------------
[ -f "$BINARY" ] || die "$EX_USAGE" "no such file: $BINARY"
[ -s "$BINARY" ] || die "$EX_DATAERR" "$BINARY is empty"

if [ -n "$EXPECTED_SHA256" ]; then
	# The digest comes from the machine that compiled it, so this is the check
	# that catches the failure mode nothing else does: an scp that stopped
	# early leaves a file that is a perfectly valid prefix of a binary, and a
	# truncated Go binary can still be a loadable ELF.
	actual="$(sha256sum <"$BINARY" | cut -d' ' -f1)" ||
		die "$EX_SOFTWARE" "cannot hash $BINARY"
	if [ "$actual" != "$EXPECTED_SHA256" ]; then
		die "$EX_DATAERR" \
			"sha256 mismatch: expected $EXPECTED_SHA256, got $actual. The upload is not what was built."
	fi
	log "sha256 matches what CI built"
else
	log "WARNING: no --sha256 given; a truncated upload can only be caught by running it"
fi

# Stage next to the target rather than executing out of /tmp, for two reasons
# that both bite at the worst moment: the final install is then a rename within
# one filesystem, which cannot half-happen and cannot fail with EXDEV after the
# old binary has already been moved aside; and /tmp is frequently mounted
# noexec, which would make the verification below fail on a file that is fine.
install -o root -g root -m 0755 -- "$BINARY" "$INCOMING" ||
	die "$EX_SOFTWARE" "cannot stage the new binary at $INCOMING"
relabel "$INCOMING"

# Anything that leaves the staged copy behind is a half-deployed state for the
# next run to trip over, so it goes on every exit path from here on.
# shellcheck disable=SC2317,SC2329 # invoked indirectly by the EXIT trap below
cleanup() {
	local status=$?
	rm -f -- "$INCOMING" "$TARGET.swapping" 2>/dev/null || true
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# Run it. `serve -h` parses flags, prints usage and exits 0 without opening the
# workspace, touching the database or binding a port, which makes it safe to do
# while the old server is still running — and it proves the three things that
# actually go wrong with an uploaded artifact: that the file is a complete,
# loadable executable, that it was built for *this* architecture (an amd64
# binary on the Ampere shape fails here rather than in a restart loop), and
# that it is nodevas rather than something else that landed at this path.
log "verifying the uploaded binary runs on this machine"
if ! "$INCOMING" serve -h >/dev/null 2>&1; then
	die "$EX_DATAERR" \
		"$BINARY does not run here: wrong architecture, truncated, or not nodevas. Nothing has been changed."
fi

# --------------------------------------------------------------------------
# Step 2 and 3. Keep the old one, swap, start.
#
# The service is stopped *before* the swap rather than restarted after it. A
# running server whose binary is replaced underneath it keeps executing the
# deleted inode quite happily, so the two would appear to work — right up to
# the next crash-restart, which would come up on a different version than the
# deploy reported. Stopping first makes what is running and what is installed
# the same thing at every moment anyone could look.
# --------------------------------------------------------------------------
stop_service

HAD_PREVIOUS=0
if [ -e "$TARGET" ]; then
	# mv, not cp: the previous binary must be the exact file that was working,
	# and a copy that fails halfway leaves a truncated rollback target that
	# will pass no check when it is needed most.
	mv -f -- "$TARGET" "$PREVIOUS" ||
		die "$EX_SOFTWARE" "cannot keep the old binary at $PREVIOUS"
	HAD_PREVIOUS=1
	log "previous binary kept at $PREVIOUS"
else
	log "no binary at $TARGET yet; this is the first deploy and there is nothing to roll back to"
fi

mv -f -- "$INCOMING" "$TARGET" || die "$EX_SOFTWARE" "cannot install $TARGET"
relabel "$TARGET"
log "installed $TARGET ($(sha256sum <"$TARGET" | cut -d' ' -f1))"

# --------------------------------------------------------------------------
# Step 4 and 5. Prove it serves, or put the old one back.
#
# roll_back_and_fail is the reason this script exists rather than a three-line
# scp-and-restart in the workflow. By the time a human reads the log the site
# has been down for as long as it took them to notice; by the time this
# function returns it has been down for as long as one failed start plus one
# health check.
# --------------------------------------------------------------------------
roll_back_and_fail() {
	local reason="$1"
	show_recent_log
	if [ "$HAD_PREVIOUS" -eq 0 ]; then
		die "$EX_UNAVAILABLE" \
			"$reason, and there is no previous binary to restore. $UNIT is down; fix it by hand."
	fi
	log "ROLLING BACK: $reason"
	systemctl stop "$UNIT" || true
	# The new binary is kept, under a name nothing starts, so that whoever
	# investigates has the artifact that failed rather than a description of
	# it. One copy only: the name is fixed, so the next failure replaces it
	# instead of filling /usr/local/bin with dated corpses.
	mv -f -- "$TARGET" "$TARGET.failed" 2>/dev/null || true
	if ! mv -f -- "$PREVIOUS" "$TARGET"; then
		die "$EX_SOFTWARE" \
			"$reason, and restoring $PREVIOUS failed. $UNIT is down; the binary that failed is at $TARGET.failed."
	fi
	relabel "$TARGET"
	if ! systemctl start "$UNIT"; then
		show_recent_log
		die "$EX_UNAVAILABLE" \
			"$reason. The previous binary was restored but will not start either, so the fault is not the new binary."
	fi
	if ! health_check; then
		show_recent_log
		die "$EX_UNAVAILABLE" \
			"$reason. The previous binary is back but still does not serve, so the fault is not the new binary."
	fi
	die "$EX_UNAVAILABLE" \
		"$reason. Rolled back to the previous binary and it is serving again. The failed build is at $TARGET.failed."
}

if [ "$HAD_PREVIOUS" -eq 0 ]; then
	log "first install complete; service remains stopped until the first account exists"
	log "create the account, configure SMTP, then run: systemctl start $UNIT"
	exit 0
fi

if ! systemctl start "$UNIT"; then
	roll_back_and_fail "the new binary would not start"
fi
if ! health_check; then
	roll_back_and_fail "the new binary started but did not serve"
fi

log "deploy complete: $UNIT is serving on 127.0.0.1:$PORT"
[ "$HAD_PREVIOUS" -eq 1 ] &&
	log "to undo this later: nodevas-deploy.sh --rollback"
exit 0
