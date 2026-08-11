#!/usr/bin/env bash
#
# Build here and install on the instance, from an operator's own machine.
#
# This exists because .github/workflows/deploy.yml cannot run its second job on
# a hosted runner: OCI's security list admits SSH from one address
# (ssh_allowed_cidr), and a hosted runner's egress is a large, changing range
# that would have to be replaced with 0.0.0.0/0 to make it work. Until a
# self-hosted runner exists inside that address, the machine that is already
# inside it is the one you are sitting at.
#
# It is deliberately the same sequence the workflow runs, in the same order,
# with the same checks. Two paths to production that do different things are
# two things to debug at the moment you least want to; where this one is
# shorter, it is because a step was CI-specific, never because a check was
# skipped.
#
# What it does NOT do, on purpose:
#
#   * It does not touch /etc/nodevas/nodevas.env. Configuration changes are
#     made by a person who has read what they are changing, not folded into a
#     deploy where a bad line takes the site down as a side effect.
#   * It does not run the end-to-end suite, for the same reason the workflow
#     does not: it installs a browser, and that is minutes standing between an
#     operator and a fix they are trying to ship.
#   * It does not roll back. nodevas-deploy.sh on the instance already does,
#     automatically, when a new binary starts but will not serve — and it does
#     it there, where it still works when this laptop, the network or GitHub is
#     the thing that broke.
#
# Usage:
#   deploy/oci/push.sh --host 203.0.113.10 --key ~/.ssh/nodevas_oci
#   deploy/oci/push.sh --host ... --key ... --dry-run
#
# Defaults come from the environment so a shell that deploys often can set them
# once: NODEVAS_DEPLOY_HOST, NODEVAS_DEPLOY_USER, NODEVAS_DEPLOY_KEY,
# NODEVAS_DEPLOY_ARCH.

set -euo pipefail

# Exit codes from sysexits(3), matching nodevas-deploy.sh on the instance. A
# deploy is read after the fact, and "I refused to start" and "I installed
# something that would not serve" are very different mornings.
readonly EX_USAGE=64       # bad or missing arguments
readonly EX_SOFTWARE=70    # a check here failed: tests, typecheck, build
readonly EX_UNAVAILABLE=69 # a tool is missing, or the far side refused

HOST="${NODEVAS_DEPLOY_HOST:-}"
USER_NAME="${NODEVAS_DEPLOY_USER:-ubuntu}"
KEY="${NODEVAS_DEPLOY_KEY:-}"
ARCH="${NODEVAS_DEPLOY_ARCH:-}"
DRY_RUN=0
SKIP_TESTS=0

# The same build tag every other build of ./cmd/nodevas uses. Dropping gin's
# MessagePack codec saves about 9 MB, and building the deployed binary without
# it would put an artifact on the server that differs from the one the test
# suite ran against.
export GOFLAGS="${GOFLAGS:--tags=nomsgpack}"

usage() {
	cat <<'EOF'
Usage: deploy/oci/push.sh --host ADDRESS [options]

  --host ADDRESS     The instance's public address. Required.
  --user NAME        SSH user (default: ubuntu).
  --key PATH         SSH private key. Required unless an agent holds it.
  --arch ARCH        arm64 or amd64. Guessed from the Terraform state if it
                     can be read, otherwise required.
  --dry-run          Build and verify, but do not touch the server.
  --skip-tests       Build without running the suite. For a second attempt at
                     a deploy whose tests already passed on this exact tree,
                     and for nothing else.
  -h, --help         This.

The instance's environment file is never read or written by this script.
EOF
}

fail() {
	local code="$1"
	shift
	echo "[push] $*" >&2
	exit "${code}"
}

note() { echo "[push] $*"; }

while [ "$#" -gt 0 ]; do
	case "$1" in
	--host) HOST="${2:-}"; shift 2 ;;
	--user) USER_NAME="${2:-}"; shift 2 ;;
	--key) KEY="${2:-}"; shift 2 ;;
	--arch) ARCH="${2:-}"; shift 2 ;;
	--dry-run) DRY_RUN=1; shift ;;
	--skip-tests) SKIP_TESTS=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; fail "${EX_USAGE}" "unknown argument: $1" ;;
	esac
done

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
readonly REPO="${PWD}"

for tool in go npm ssh scp; do
	command -v "${tool}" >/dev/null 2>&1 ||
		fail "${EX_UNAVAILABLE}" "${tool} is not on PATH"
done

# --------------------------------------------------------------------------
# What is being deployed
#
# Said out loud before anything is built. A deploy from a tree with uncommitted
# changes is a deploy nobody can reproduce afterwards, which matters most at
# the point where somebody is trying to work out what is running.
# --------------------------------------------------------------------------
commit="$(git rev-parse HEAD)"
note "commit ${commit} ($(git log -1 --pretty=%s))"
if [ -n "$(git status --porcelain)" ]; then
	note "WARNING: the working tree is dirty; what ships is not ${commit}"
fi

# --------------------------------------------------------------------------
# Architecture
#
# The instance shape decides it, the shape is a Terraform variable, and getting
# it wrong ships a binary the machine cannot execute — which shows up as a
# restart loop, not as a build failure. Read it from the state file when there
# is one rather than asking the operator to remember.
# --------------------------------------------------------------------------
if [ -z "${ARCH}" ]; then
	shape="$(sed -n 's/^[[:space:]]*instance_shape[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' \
		deploy/oci/terraform/terraform.tfvars 2>/dev/null | head -n1)"
	case "${shape}" in
	*A1.Flex*) ARCH=arm64 ;;
	*E2*Micro* | *E5*Flex* | *E4*Flex*) ARCH=amd64 ;;
	esac
	[ -z "${ARCH}" ] || note "architecture ${ARCH}, from instance_shape ${shape}"
fi
case "${ARCH}" in
arm64 | amd64) ;;
"") fail "${EX_USAGE}" "no architecture: pass --arch arm64 or --arch amd64" ;;
*) fail "${EX_USAGE}" "--arch is '${ARCH}'; it must be arm64 or amd64" ;;
esac

if [ "${DRY_RUN}" -eq 0 ]; then
	[ -n "${HOST}" ] || fail "${EX_USAGE}" "no --host"
	if [ -n "${KEY}" ] && [ ! -r "${KEY}" ]; then
		fail "${EX_USAGE}" "cannot read the key at ${KEY}"
	fi
fi

# --------------------------------------------------------------------------
# The suite, before anything is built for the server
#
# A deploy that does not run the tests is a deploy that ships a regression onto
# the one machine holding everyone's documents.
# --------------------------------------------------------------------------
if [ "${SKIP_TESTS}" -eq 1 ]; then
	note "WARNING: --skip-tests; nothing below was verified at this commit"
else
	note "installing web dependencies"
	npm ci --prefix web >/dev/null

	note "go build"
	go build ./... || fail "${EX_SOFTWARE}" "go build failed"
	note "go vet"
	go vet ./... || fail "${EX_SOFTWARE}" "go vet failed"
	note "go test"
	go test ./... || fail "${EX_SOFTWARE}" "go test failed"
	# Run from inside web/ rather than with `npm --prefix web exec`, which is
	# what the workflow uses. --prefix tells npm where the package is; it does
	# not promise the command a working directory, and on Windows it does not
	# change one. tsc -b and vitest both resolve their config relative to the
	# working directory, so from the repository root they look for
	# tsconfig.json next to go.mod and fail with a path that reads like a
	# missing file rather than a wrong directory. A subshell, so nothing after
	# this block has to remember to come back.
	note "typescript"
	(cd web && npx tsc -b) || fail "${EX_SOFTWARE}" "tsc failed"
	note "web unit tests"
	(cd web && npx vitest run) || fail "${EX_SOFTWARE}" "vitest failed"
fi

# --------------------------------------------------------------------------
# The artifact
#
# The frontend is embedded into the Go binary by web/embed.go, so this order is
# not a preference. Build Go first and web.Dist() finds no index.html, the
# server logs one line about a missing build and serves a placeholder — a
# deploy that succeeds, restarts cleanly, and puts the wrong UI in front of
# everyone. The check below is what turns that into a failure here instead.
# --------------------------------------------------------------------------
note "building the frontend"
npm run build --prefix web >/dev/null || fail "${EX_SOFTWARE}" "the frontend build failed"
[ -s web/dist/index.html ] ||
	fail "${EX_SOFTWARE}" "web/dist/index.html is missing; the binary would embed a placeholder UI"

artifact="${REPO}/nodevas-linux-${ARCH}"
note "cross-compiling for linux/${ARCH}"
CGO_ENABLED=0 GOOS=linux GOARCH="${ARCH}" go build \
	-trimpath -ldflags="-s -w" -o "${artifact}" ./cmd/nodevas ||
	fail "${EX_SOFTWARE}" "the cross-compile failed"

# Proved here because both are silent on the far side: that it is an executable
# for the machine it is going to, and what its digest is, so the instance can
# tell a complete upload from a truncated one.
if command -v file >/dev/null 2>&1; then
	case "${ARCH}" in
	arm64) want="ARM aarch64" ;;
	amd64) want="x86-64" ;;
	esac
	file "${artifact}" | grep -q "${want}" ||
		fail "${EX_SOFTWARE}" "the binary is not a linux/${ARCH} executable"
fi
sum="$(sha256sum "${artifact}" | cut -d' ' -f1)"
note "nodevas linux/${ARCH}: ${sum} ($(wc -c <"${artifact}" | tr -d ' ') bytes)"

if [ "${DRY_RUN}" -eq 1 ]; then
	note "dry run: the server was not touched"
	note "artifact left at ${artifact}"
	exit 0
fi

# --------------------------------------------------------------------------
# Install
#
# The staging directory carries a timestamp so two runs cannot write each
# other's files. Everything real happens under sudo from nodevas-deploy.sh on
# the instance; this only puts bytes on the box.
#
# Host key checking stays on. Turning it off is the usual shortcut and it means
# the first thing this does, every time, is hand a binary to whatever answers
# on that address. If the host is not in known_hosts yet, add it once from a
# machine you trust: ssh-keyscan -t ed25519 <address>
#
# nodevas-deploy.sh is reinstalled every time rather than only run from the
# staging directory, because `nodevas-deploy.sh --rollback` has to be on the
# box for an operator with no laptop and no network — which is the situation a
# rollback is for.
# --------------------------------------------------------------------------
ssh_opts=(-o StrictHostKeyChecking=yes -o ConnectTimeout=15 -o BatchMode=yes)
[ -z "${KEY}" ] || ssh_opts+=(-i "${KEY}")

remote_dir="/tmp/nodevas-push-$(date +%Y%m%d-%H%M%S)-$$"
target="${USER_NAME}@${HOST}"

note "staging on ${HOST} at ${remote_dir}"
ssh "${ssh_opts[@]}" "${target}" \
	"mkdir -p '${remote_dir}' && chmod 700 '${remote_dir}'" ||
	fail "${EX_UNAVAILABLE}" "cannot reach ${target} over SSH"

scp "${ssh_opts[@]}" \
	"${artifact}" "${REPO}/deploy/oci/files/nodevas-deploy.sh" \
	"${target}:${remote_dir}/" ||
	fail "${EX_UNAVAILABLE}" "the upload failed"

# The uploaded binary keeps the name the remote script expects. Renaming on the
# far side rather than locally keeps the local artifact's name saying which
# architecture it is.
# No -tt: this must never wait on a terminal. Everything the script needs is on
# its command line, and a sudo that wanted a password fails here rather than
# hanging until somebody notices.
note "installing"
status=0
ssh "${ssh_opts[@]}" "${target}" bash -s -- \
	"${remote_dir}" "$(basename "${artifact}")" "${sum}" "${commit}" <<'REMOTE' || status=$?
set -euo pipefail
remote_dir="$1"
uploaded="$2"
expected_sha="$3"
commit="$4"
echo "installing ${commit} from ${remote_dir}"
sudo -n install -o root -g root -m 0755 \
	"${remote_dir}/nodevas-deploy.sh" /usr/local/sbin/nodevas-deploy.sh
status=0
sudo -n /usr/local/sbin/nodevas-deploy.sh \
	--binary "${remote_dir}/${uploaded}" \
	--sha256 "${expected_sha}" || status=$?
# The staged copy goes whether the deploy worked or not: on success it is a
# duplicate of /usr/local/bin/nodevas, and on failure the script has already
# kept the interesting one at /usr/local/bin/nodevas.failed.
rm -rf -- "${remote_dir}"
exit "${status}"
REMOTE

if [ "${status}" -ne 0 ]; then
	cat >&2 <<EOF
[push] the deploy failed with status ${status}.

The on-box script rolls back by itself when a new binary starts but will not
serve, so the instance is most likely running the previous binary already.
Confirm before doing anything else:

  ssh ${target} systemctl status nodevas
  ssh ${target} journalctl -u nodevas -n 100 --no-pager
  ssh ${target} ls -l '/usr/local/bin/nodevas*'

.previous is the last good one, .failed is this build.
EOF
	exit "${status}"
fi

note "deployed ${commit} (${sum})"
note "to undo, on the instance: sudo nodevas-deploy.sh --rollback"
