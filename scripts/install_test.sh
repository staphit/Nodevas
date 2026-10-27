#!/usr/bin/env bash
# Tests for scripts/install.sh's GitHub download path: the validators, the
# manifest parsing, and fetch_verified / step_match_hub end to end against a
# throwaway HTTPS registry on 127.0.0.1 (python3 and openssl; skipped without
# them). Nothing outside a temp dir changes and nothing reaches the network.
#
#   bash scripts/install_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
set --
# shellcheck source=scripts/install.sh
. "$HERE/install.sh"
# install.sh set -e; a failing assertion reports instead of stopping here.
set +e

WORK="$(mktemp -d)"
SERVER_PID=""
finish() {
	if [ -n "$SERVER_PID" ]; then
		kill "$SERVER_PID" 2>/dev/null
		wait "$SERVER_PID" 2>/dev/null
	fi
	rm -rf "$WORK"
}
trap finish EXIT

FAILED=0
PASSED=0
pass() { PASSED=$((PASSED + 1)); }
fail() {
	FAILED=$((FAILED + 1))
	printf 'FAIL: %s\n' "$*" >&2
}
expect_eq() {
	if [ "$2" = "$3" ]; then pass; else fail "$1: got \"$2\", want \"$3\""; fi
}
expect_ok() {
	local what="$1"
	shift
	if "$@" >/dev/null 2>&1; then pass; else fail "$what: failed"; fi
}
expect_fail() {
	local what="$1"
	shift
	if ("$@") >/dev/null 2>&1; then fail "$what: succeeded"; else pass; fi
}
# expect_die: the command stops the script, saying message.
expect_die() {
	local what="$1" message="$2" out
	shift 2
	if out="$( ("$@") 2>&1)"; then
		fail "$what: succeeded"
		return
	fi
	case "$out" in
	*"$message"*) pass ;;
	*) fail "$what: said \"$out\", want \"$message\"" ;;
	esac
}

C1="0123456789abcdef0123456789abcdef01234567"
C2="89abcdef0123456789abcdef0123456789abcdef"

# --- is_commit --------------------------------------------------------------

expect_ok "full commit" is_commit "$C1"
for bad in "" 0123456 "${C1}0" "${C1%?}" "$(printf '%s' "$C1" | tr a-f A-F)" "../../$C1" "${C1%??}/x" "latest" "$C1
"; do
	expect_fail "is_commit \"$bad\"" is_commit "$bad"
done

# --- platform ---------------------------------------------------------------

platform_of() {
	local os="$1" fake_machine="$2"
	(
		OS="$os"
		uname() { printf '%s\n' "$fake_machine"; }
		sysctl() { printf '0\n'; }
		platform
	)
}
expect_eq "linux x86_64" "$(platform_of linux x86_64)" linux-amd64
expect_eq "linux aarch64" "$(platform_of linux aarch64)" linux-arm64
expect_eq "macos arm64" "$(platform_of macos arm64)" darwin-arm64
expect_eq "macos x86_64" "$(platform_of macos x86_64)" darwin-amd64
expect_eq "windows x86_64" "$(platform_of windows x86_64)" windows-amd64
expect_eq "windows arm64" "$(platform_of windows arm64)" windows-amd64
expect_eq "rosetta" "$(OS=macos; uname() { echo x86_64; }; sysctl() { echo 1; }; platform)" darwin-arm64
expect_die "riscv64" "not riscv64" platform_of linux riscv64

# --- sha256_of --------------------------------------------------------------

printf 'hello\n' >"$WORK/hello"
expect_eq "sha256_of" "$(sha256_of "$WORK/hello")" 5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03

# --- manifest parsing -------------------------------------------------------

D1="sha256:$(printf '%064d' 1)"
D2="sha256:$(printf '%064d' 2)"
EMPTY="sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"

# manifest prints an OCI manifest the way oras pushes one.
manifest() {
	local digest="$1" title="$2" revision="$3"
	printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.nodevas.cli.v1",'
	printf '"config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"%s","size":2,"data":"e30="},' "$EMPTY"
	printf '"layers":[{"mediaType":"application/octet-stream","digest":"%s","size":5,"annotations":{"org.opencontainers.image.title":"%s"}}],' "$digest" "$title"
	printf '"annotations":{"org.opencontainers.image.created":"2026-10-26T00:00:00Z","org.opencontainers.image.revision":"%s","org.opencontainers.image.source":"https://github.com/staphit/Nodevas"}}' "$revision"
}

manifest "$D1" nodevas-linux-amd64 "$C1" >"$WORK/m.json"
expect_eq "layer digest" "$(manifest_layer_digest "$WORK/m.json")" "$D1"
expect_eq "layer title" "$(manifest_layer_title "$WORK/m.json")" nodevas-linux-amd64
expect_eq "revision" "$(manifest_revision "$WORK/m.json")" "$C1"

# Pretty-printed, the same values come out.
sed 's/,/,\n  /g; s/{/{\n  /g' "$WORK/m.json" >"$WORK/pretty.json"
expect_eq "pretty layer digest" "$(manifest_layer_digest "$WORK/pretty.json")" "$D1"
expect_eq "pretty revision" "$(manifest_revision "$WORK/pretty.json")" "$C1"

sed "s/}}\]/}},{\"mediaType\":\"application\/octet-stream\",\"digest\":\"$D2\",\"size\":5}]/" "$WORK/m.json" >"$WORK/two.json"
expect_fail "two layers" manifest_layer_digest "$WORK/two.json"
manifest "$D1" nodevas-linux-amd64 "${C1%?}" >"$WORK/short.json"
expect_fail "short revision" manifest_revision "$WORK/short.json"
manifest "sha256:abc" nodevas-linux-amd64 "$C1" >"$WORK/baddigest.json"
expect_fail "short digest" manifest_layer_digest "$WORK/baddigest.json"

# --- fetch_verified against a local registry --------------------------------

if ! command -v python3 >/dev/null 2>&1 || ! command -v openssl >/dev/null 2>&1; then
	printf 'skipped the registry tests: they need python3 and openssl\n' >&2
elif case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) true ;; *) false ;; esac; then
	# Git for Windows' curl verifies with schannel, which ignores
	# CURL_CA_BUNDLE, so it cannot trust the test registry's certificate.
	# The same code is covered on Linux and macOS.
	printf 'skipped the registry tests: curl on Windows ignores CURL_CA_BUNDLE\n' >&2
else
	REG="$WORK/registry"
	mkdir -p "$REG/manifests" "$REG/blobs"
	# Git Bash on Windows would rewrite /CN=… as a path; exclude just that
	# argument, since the key and certificate paths still need converting.
	if ! MSYS2_ARG_CONV_EXCL=/CN openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=127.0.0.1 \
		-addext subjectAltName=IP:127.0.0.1 -keyout "$WORK/key.pem" -out "$WORK/cert.pem" >"$WORK/openssl.log" 2>&1; then
		cat "$WORK/openssl.log" >&2
		fail "could not make the test registry's certificate"
		exit 1
	fi
	export CURL_CA_BUNDLE="$WORK/cert.pem"

	# A registry that answers like ghcr.io for one repository: an anonymous
	# token, manifests by tag, and blobs through a redirect.
	python3 -I -c '
import http.server, os, ssl, sys
root, portfile, cert, key = sys.argv[1:5]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def send(self, code, body=b"", ctype="application/json", extra=None):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        p = self.path
        prefix = "/v2/staphit/nodevas-cli/"
        if p.startswith("/token?"):
            return self.send(200, b"{\"token\":\"anon.Token-1\"}")
        if p.startswith("/store/"):
            name = os.path.basename(p)
            path = os.path.join(root, "blobs", name)
            if os.path.isfile(path):
                return self.send(200, open(path, "rb").read(), "application/octet-stream")
            return self.send(404)
        if self.headers.get("Authorization") != "Bearer anon.Token-1":
            return self.send(401)
        if p.startswith(prefix + "manifests/"):
            path = os.path.join(root, "manifests", os.path.basename(p))
            if os.path.isfile(path):
                return self.send(200, open(path, "rb").read(), "application/vnd.oci.image.manifest.v1+json")
            return self.send(404)
        if p.startswith(prefix + "blobs/sha256:"):
            return self.send(307, extra={"Location": "/store/" + p.rsplit(":", 1)[1]})
        self.send(404)
server = http.server.HTTPServer(("127.0.0.1", 0), H)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
server.socket = ctx.wrap_socket(server.socket, server_side=True)
open(portfile + ".tmp", "w").write(str(server.server_address[1]))
os.rename(portfile + ".tmp", portfile)
server.serve_forever()
' "$REG" "$WORK/port" "$WORK/cert.pem" "$WORK/key.pem" 2>"$WORK/registry.log" &
	SERVER_PID=$!
	# CI runners can take a while to start Python; wait up to 30s.
	tries=0
	while [ ! -f "$WORK/port" ] && [ "$tries" -lt 120 ]; do
		sleep 0.25
		tries=$((tries + 1))
	done
	[ -f "$WORK/port" ] || {
		cat "$WORK/registry.log" >&2
		fail "the test registry did not start"
		exit 1
	}
	REGISTRY="https://127.0.0.1:$(cat "$WORK/port")"

	PLAT="$(platform)"
	NAME="nodevas-$PLAT"
	if [ "${PLAT%%-*}" = windows ]; then
		NAME="$NAME.exe"
	fi

	# publish <tag> <content> <revision> [<title>] [<digest>] puts a build in
	# the registry; <digest> lies about the content when given.
	publish() {
		local tag="$1" content="$2" revision="$3" title="${4:-$NAME}" digest="${5:-}" hex
		printf '%s\n' "$content" >"$WORK/blob"
		hex="$(sha256_of "$WORK/blob")"
		digest="${digest:-sha256:$hex}"
		# A lying digest serves this content under the digest it claims.
		cp "$WORK/blob" "$REG/blobs/${digest#sha256:}"
		manifest "$digest" "$title" "$revision" >"$REG/manifests/$tag"
	}
	publish "latest-$PLAT" "build of C2" "$C2"
	publish "$C1-$PLAT" "build of C1" "$C1"
	publish "$C2-$PLAT" "build of C2" "$C2"

	TMP_DIR="$WORK/tmp"
	mkdir -p "$TMP_DIR"
	PREFIX="$WORK/bin"
	INSTALLED="$PREFIX/nodevas${EXE}"
	DRY=0

	# gh: absent unless a test says otherwise.
	GH_MODE=absent
	gh() {
		case "$GH_MODE $1" in
		"absent "*) return 127 ;;
		*" auth") return 0 ;;
		esac
		if [ "$1 $2 $3" = "attestation verify --help" ]; then
			printf '      --deny-self-hosted-runners\n      --source-digest string\n'
			return 0
		fi
		printf '%s\n' "$*" >"$WORK/gh-args"
		[ "$GH_MODE" = verifies ]
	}
	command() {
		if [ "$1 $2" = "-v gh" ] && [ "$GH_MODE" = absent ]; then
			return 1
		fi
		builtin command "$@"
	}

	# fetch runs fetch_verified in a subshell (it exits on a bad build) and
	# prints its status and the commit it got.
	fetch() {
		(
			fetch_verified "$@" >"$WORK/fetch.log" 2>&1
			status=$?
			printf '%s %s\n' "$status" "$FETCHED_COMMIT"
		)
	}

	expect_eq "fetch a commit" "$(fetch "$C1" "$WORK/out/a")" "0 $C1"
	expect_eq "fetch a commit: content" "$(cat "$WORK/out/a" 2>/dev/null)" "build of C1"
	expect_eq "fetch latest" "$(fetch latest "$WORK/out/b")" "0 $C2"
	expect_eq "no such build" "$(fetch "$(printf '%040d' 7)" "$WORK/out/c")" "1 "

	expect_die "not a commit" "not a full commit" fetch_verified "../$C1" "$WORK/out/d"

	C3="$(printf '%040d' 3)"
	publish "$C3-$PLAT" "evil" "$C3" "" "$D1"
	expect_die "digest mismatch" "does not match its digest" fetch_verified "$C3" "$WORK/out/e"
	if [ -e "$WORK/out/e" ]; then fail "a binary that does not match its digest was left behind"; else pass; fi

	C4="$(printf '%040d' 4)"
	publish "$C4-$PLAT" "build of C1" "$C1"
	expect_die "revision mismatch" "was built from $C1, not $C4" fetch_verified "$C4" "$WORK/out/f"

	C5="$(printf '%040d' 5)"
	publish "$C5-$PLAT" "other file" "$C5" "nodevas-plan9-mips"
	expect_die "title mismatch" "holds \"nodevas-plan9-mips\"" fetch_verified "$C5" "$WORK/out/g"

	GH_MODE=refuses
	expect_die "attestation does not verify" "attestation does not verify" fetch_verified "$C1" "$WORK/out/h"
	GH_MODE=verifies
	expect_eq "attestation verifies" "$(fetch "$C1" "$WORK/out/i")" "0 $C1"
	gh_args="$(cat "$WORK/gh-args" 2>/dev/null)"
	case "$gh_args" in
	*"--repo staphit/Nodevas --signer-workflow staphit/Nodevas/.github/workflows/container.yml --deny-self-hosted-runners --source-digest $C1") pass ;;
	*) fail "gh attestation verify args: $gh_args" ;;
	esac
	GH_MODE=absent

	# --- step_match_hub -----------------------------------------------------

	# NV is a runner that says the hub runs $HUB_SAYS.
	NV="$WORK/fake-nodevas"
	printf '#!/bin/sh\n[ "$1 $2" = "runner hub-version" ] || exit 2\ncat "%s" || exit 1\n' "$WORK/hub-says" >"$NV"
	chmod +x "$NV"
	match() {
		printf '%s\n' "$1" >"$WORK/hub-says"
		rm -f "$INSTALLED"
		(
			DOWNLOADED=1 COMMIT="" FETCHED_COMMIT="$C2"
			step_match_hub >/dev/null 2>&1
		)
	}

	match "$C1"
	expect_eq "hub at another commit: installed" "$(cat "$INSTALLED" 2>/dev/null)" "build of C1"
	match "$C2"
	if [ -e "$INSTALLED" ]; then fail "hub at the same commit: fetched again"; else pass; fi
	match "../../$C1"
	if [ -e "$INSTALLED" ]; then fail "hub answered a path: something was installed"; else pass; fi
	match "$(printf '%040d' 9)"
	expect_eq "hub at a commit GitHub never built: keeps going" "$?" 0
	if [ -e "$INSTALLED" ]; then fail "hub at an unbuilt commit: something was installed"; else pass; fi
	rm -f "$WORK/hub-says"
	(
		DOWNLOADED=1 COMMIT="" FETCHED_COMMIT="$C2"
		step_match_hub >/dev/null 2>&1
	)
	expect_eq "hub does not say: keeps going" "$?" 0
fi

printf '%d passed, %d failed\n' "$PASSED" "$FAILED"
[ "$FAILED" = 0 ]
