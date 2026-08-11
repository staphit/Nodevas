#!/usr/bin/env bash
#
# Take one consistent snapshot of the workspace database, verify it, and put it
# in OCI Object Storage under a timestamped name — plus whatever error-level log
# lines journald has collected since the last run.
#
# Each run writes a new object rather than replacing one. That is not a
# preference: the IAM policy in ../terraform grants this instance CREATE and
# INSPECT and withholds OVERWRITE and DELETE, so a machine somebody has broken
# into can add backups but cannot destroy the ones already there. Expiry is the
# bucket lifecycle policy's job, where the instance has no say in it.
#
# Nothing here needs an API key on the box. Authentication is the instance's own
# identity (instance principal), which means the credential cannot be stolen off
# the disk and is revoked by removing the instance from its dynamic group.

set -euo pipefail

# --------------------------------------------------------------------------
# Exit codes, from sysexits(3), because a backup that fails silently is how
# people discover at restore time that they have nothing. systemd records the
# exit status of the unit, so these are the difference between "the timer ran"
# and "the timer ran and the snapshot is good".
# --------------------------------------------------------------------------
readonly EX_USAGE=64       # bad or missing arguments
readonly EX_DATAERR=65     # the snapshot failed its integrity check
readonly EX_UNAVAILABLE=69 # a dependency or the database itself is missing
readonly EX_SOFTWARE=70    # something this script did went wrong
readonly EX_IOERR=74       # the upload failed
readonly EX_TEMPFAIL=75    # another run holds the lock

# Where things live. Every one of these can be overridden from the environment
# (systemd EnvironmentFile) or by a flag; none of them is hardcoded to one
# tenancy, because a script with someone's bucket baked in is a script that gets
# copied and edited rather than configured.
DB_PATH="${NODEVAS_DB:-}"
WORKSPACE="${NODEVAS_WORKSPACE:-/var/lib/nodevas/workspace}"
BUCKET="${NODEVAS_BACKUP_BUCKET:-}"
NAMESPACE="${NODEVAS_OCI_NAMESPACE:-}"
# Object names are rooted at db/ and logs/ and are not configurable: the
# bucket's lifecycle policy expires by prefix, so a name outside those two roots
# is a backup that never expires. See DB_OBJECT below.
UNIT="${NODEVAS_UNIT:-nodevas}"
STATE_DIR="${NODEVAS_BACKUP_STATE_DIR:-/var/lib/nodevas/backup}"
LOCK_FILE="${NODEVAS_BACKUP_LOCK:-/run/lock/nodevas-backup.lock}"

usage() {
	cat <<'EOF'
Usage: nodevas-backup.sh [options]

Snapshot the nodevas database and upload it to OCI Object Storage under a new
timestamped name. Also uploads error-level journal entries since the last
successful run, when there are any.

Options:
  --db PATH           Database file. Default: <workspace>/.vised/nodevas.db
  --workspace PATH    Workspace root, used to derive --db.   [$NODEVAS_WORKSPACE]
  --bucket NAME       Object Storage bucket.                 [$NODEVAS_BACKUP_BUCKET]
  --namespace NAME    Tenancy namespace. Discovered from the
                      instance principal when omitted.       [$NODEVAS_OCI_NAMESPACE]
  --unit NAME         systemd unit whose errors to archive.  [$NODEVAS_UNIT]
  -h, --help          This text.

Exit codes: 0 ok, 64 usage, 65 snapshot corrupt, 69 missing dependency,
70 internal error, 74 upload failed, 75 another run in progress.
EOF
}

# Logging goes to stderr because systemd captures stderr into the journal, where
# it lands next to the server's own records for the same day. The level prefix
# is what makes `nodevas-logs errors` show a failed backup alongside a failed
# request.
log() { printf '[nodevas-backup] %s\n' "$*" >&2; }
die() {
	local code="$1"
	shift
	printf '[nodevas-backup] ERROR: %s\n' "$*" >&2
	exit "$code"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--db) DB_PATH="${2:-}"; shift 2 ;;
	--workspace) WORKSPACE="${2:-}"; shift 2 ;;
	--bucket) BUCKET="${2:-}"; shift 2 ;;
	--namespace) NAMESPACE="${2:-}"; shift 2 ;;
	--unit) UNIT="${2:-}"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "$EX_USAGE" "unknown argument: $1" ;;
	esac
done

[ -n "$DB_PATH" ] || DB_PATH="$WORKSPACE/.vised/nodevas.db"
[ -n "$BUCKET" ] || die "$EX_USAGE" "no bucket: pass --bucket or set NODEVAS_BACKUP_BUCKET"

for tool in sqlite3 oci gzip; do
	command -v "$tool" >/dev/null 2>&1 ||
		die "$EX_UNAVAILABLE" "$tool is not installed"
done

[ -f "$DB_PATH" ] || die "$EX_UNAVAILABLE" "no database at $DB_PATH"

# SQLite's VACUUM INTO takes its destination as a single-quoted SQL string
# literal. A path containing a quote would end the literal early, so refuse one
# rather than build a statement that means something other than it looks like.
case "$DB_PATH" in
*\'*) die "$EX_USAGE" "database path contains a single quote: $DB_PATH" ;;
esac

# --------------------------------------------------------------------------
# Only one run at a time. Two overlapping runs would each VACUUM INTO their own
# temporary file, which is harmless, and then race each other to overwrite the
# same object, which is not: the loser's upload can land after the winner's and
# leave the older snapshot in place. flock -n fails immediately rather than
# queueing, because a backup that starts an hour late because it waited for a
# stuck predecessor is not a backup anyone wanted.
# --------------------------------------------------------------------------
mkdir -p "$(dirname "$LOCK_FILE")" 2>/dev/null || true
exec 9>"$LOCK_FILE" || die "$EX_SOFTWARE" "cannot open lock file $LOCK_FILE"
if ! flock -n 9; then
	die "$EX_TEMPFAIL" "another backup run holds $LOCK_FILE; giving up"
fi

# --------------------------------------------------------------------------
# Scratch space, removed on every exit path including a failure and including a
# signal. The snapshot is a byte-for-byte copy of a file holding password and
# PIN hashes, so leaving it behind on the boot volume after a crash would be a
# credential left on disk, not just clutter.
#
# The rm targets only the directory mktemp created, and only after checking the
# variable is non-empty and is in fact a directory. It is never built from
# anything the caller supplied.
# --------------------------------------------------------------------------
TMP_DIR=""
cleanup() {
	local status=$?
	if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
		rm -rf -- "$TMP_DIR"
	fi
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/nodevas-backup.XXXXXXXX")" ||
	die "$EX_SOFTWARE" "cannot create a temporary directory"
chmod 700 "$TMP_DIR"

case "$TMP_DIR" in
*\'*) die "$EX_SOFTWARE" "temporary directory contains a single quote" ;;
esac

SNAPSHOT="$TMP_DIR/nodevas.db"

# --------------------------------------------------------------------------
# The snapshot.
#
# Do NOT `cp` the database. It runs in WAL mode (see internal/db/db.go), which
# means committed transactions live in nodevas.db-wal until a checkpoint folds
# them back — copying nodevas.db alone silently loses the most recent writes,
# which is exactly the tail you most wanted. Copying all three sidecars while
# the server is running is worse: they are read at three different instants and
# the set can be mutually inconsistent, so you get a file that restores and then
# fails an integrity check months later.
#
# VACUUM INTO is SQLite's own online backup. It reads inside one read
# transaction — a consistent snapshot, WAL contents included — and writes a
# single defragmented file with no sidecars, without blocking the server's
# writer and without needing the service stopped. The .timeout is there because
# the read still has to acquire a shared lock, and the server holds the write
# lock in short bursts.
# --------------------------------------------------------------------------
log "snapshotting $DB_PATH"
if ! sqlite3 -cmd '.timeout 30000' "$DB_PATH" "VACUUM INTO '$SNAPSHOT'"; then
	die "$EX_SOFTWARE" "VACUUM INTO failed; no snapshot was produced"
fi
[ -s "$SNAPSHOT" ] || die "$EX_SOFTWARE" "VACUUM INTO produced an empty file"
chmod 600 "$SNAPSHOT"

# --------------------------------------------------------------------------
# Verify before uploading, never after.
#
# The object being replaced is the last known-good copy. Uploading a corrupt
# snapshot over it does not leave you with a bad backup, it leaves you with no
# backup — and it does so quietly, at the exact moment everything still looks
# fine. So the integrity check gates the upload, and a failure is loud and
# non-zero.
# --------------------------------------------------------------------------
log "verifying snapshot"
integrity="$(sqlite3 "$SNAPSHOT" 'PRAGMA integrity_check;' 2>&1)" ||
	die "$EX_DATAERR" "integrity_check could not run: $integrity"
if [ "$integrity" != "ok" ]; then
	die "$EX_DATAERR" "snapshot failed integrity_check, refusing to upload: $integrity"
fi

# foreign_key_check prints nothing when clean. It catches a different class of
# damage from integrity_check — structurally valid pages that no longer satisfy
# the schema — and costs one more pass over a database measured in megabytes.
fk="$(sqlite3 "$SNAPSHOT" 'PRAGMA foreign_key_check;' 2>&1)" ||
	die "$EX_DATAERR" "foreign_key_check could not run: $fk"
if [ -n "$fk" ]; then
	die "$EX_DATAERR" "snapshot has broken foreign keys, refusing to upload: $fk"
fi

gzip -9 -n "$SNAPSHOT" || die "$EX_SOFTWARE" "compressing the snapshot failed"
SNAPSHOT="$SNAPSHOT.gz"
snapshot_bytes="$(wc -c <"$SNAPSHOT" | tr -d ' ')"

# --------------------------------------------------------------------------
# Object Storage.
# --------------------------------------------------------------------------
oci_cli() { oci --auth instance_principal "$@"; }

# The namespace is a property of the tenancy and never changes, but asking for
# it beats making the operator look it up and paste it into a unit file.
if [ -z "$NAMESPACE" ]; then
	NAMESPACE="$(oci_cli os ns get --query data --raw-output 2>/dev/null)" ||
		die "$EX_IOERR" "cannot determine the Object Storage namespace; is this instance in the dynamic group?"
	[ -n "$NAMESPACE" ] || die "$EX_IOERR" "empty Object Storage namespace"
fi

# Every run writes a new object rather than replacing one, for two reasons that
# both come from the Terraform in ../terraform.
#
# The IAM policy grants this instance OBJECT_INSPECT and OBJECT_CREATE and
# deliberately withholds OBJECT_OVERWRITE and OBJECT_DELETE, so that a machine
# somebody has broken into can add backups but cannot destroy or replace the
# ones already there. A fixed object name would need overwrite, and would fail
# with a permission error on the second run.
#
# The "db/" root is not decoration either: the bucket's lifecycle policy expires
# objects by prefix, anchored at the start of the name. Anything written outside
# db/ and logs/ is never expired and silently accumulates against the 20 GB
# Always Free allowance, so these two roots are fixed rather than configurable.
DB_OBJECT="db/nodevas-$(date -u +%Y%m%dT%H%M%SZ).db.gz"

# Object Storage replaces an object atomically: readers see the old bytes until
# the new ones are fully committed, so an upload killed halfway leaves the
# previous snapshot intact rather than a truncated one.
#
# --content-md5 makes the service reject a transfer corrupted in flight instead
# of storing it. It only applies to single-part uploads, so it is attached only
# below the CLI's multipart threshold; above that the CLI checksums each part
# itself.
md5_args=()
if [ "$snapshot_bytes" -lt $((128 * 1024 * 1024)) ] && command -v openssl >/dev/null 2>&1; then
	if md5_b64="$(openssl dgst -md5 -binary <"$SNAPSHOT" | openssl base64 -A)"; then
		md5_args=(--content-md5 "$md5_b64")
	fi
fi

log "uploading $DB_OBJECT ($snapshot_bytes bytes) to $BUCKET"
if ! oci_cli os object put \
	--namespace "$NAMESPACE" \
	--bucket-name "$BUCKET" \
	--name "$DB_OBJECT" \
	--file "$SNAPSHOT" \
	--content-type application/gzip \
	"${md5_args[@]+"${md5_args[@]}"}" \
	--force >/dev/null; then
	die "$EX_IOERR" "uploading the snapshot failed; the previous backup is unchanged"
fi

# Read back what we just wrote. An upload command that exits 0 is evidence the
# CLI was happy, not that the object is there — and this is the one script whose
# whole value is that the object is there.
if ! oci_cli os object head \
	--namespace "$NAMESPACE" \
	--bucket-name "$BUCKET" \
	--name "$DB_OBJECT" >/dev/null; then
	die "$EX_IOERR" "uploaded $DB_OBJECT but it cannot be read back"
fi
log "snapshot uploaded: $BUCKET/$DB_OBJECT"

# --------------------------------------------------------------------------
# Error-level logs.
#
# journald writes to the boot volume, which is part of the instance. Rebuild or
# lose the instance and every error it ever logged goes with it — including the
# ones that explain why it had to be rebuilt. Shipping just the error level is
# the cheap 95%: it is small enough to keep indefinitely and it is what anyone
# actually goes looking for.
#
# --cursor-file makes journald itself remember where the last run stopped, which
# is both exact and immune to clock changes; a --since window computed from a
# timestamp we stored would double-report or skip around DST and NTP steps. On
# the very first run the file does not exist, so the window is bounded to a day
# rather than dumping the entire journal.
# --------------------------------------------------------------------------
if command -v journalctl >/dev/null 2>&1; then
	mkdir -p "$STATE_DIR" 2>/dev/null || true
	CURSOR_FILE="$STATE_DIR/journal.cursor"
	NEXT_CURSOR="$TMP_DIR/journal.cursor"
	ERR_LOG="$TMP_DIR/errors.jsonl"

	since_args=()
	if [ -f "$CURSOR_FILE" ]; then
		cp -- "$CURSOR_FILE" "$NEXT_CURSOR"
	else
		since_args=(--since "-1 day")
	fi

	# journalctl exits non-zero when the filter matches nothing on some
	# versions, so its status is not treated as failure; the file's size is what
	# decides. -o cat emits the message alone, which for this server is the ECS
	# JSON line it wrote, so the archive is directly greppable with jq.
	journalctl -u "$UNIT" -p err -o cat --no-pager \
		--cursor-file="$NEXT_CURSOR" \
		"${since_args[@]+"${since_args[@]}"}" >"$ERR_LOG" 2>/dev/null || true

	if [ -s "$ERR_LOG" ]; then
		error_lines="$(wc -l <"$ERR_LOG" | tr -d ' ')"
		gzip -9 -n "$ERR_LOG"
		# Log objects are timestamped and accumulate, unlike the snapshot: each
		# one covers a distinct window, so overwriting would throw away
		# yesterday's errors to record today's. Bound the growth with a bucket
		# lifecycle rule rather than by deleting from here, where a bug would
		# delete the wrong thing.
		LOG_OBJECT="logs/errors-$(date -u +%Y%m%dT%H%M%SZ).jsonl.gz"
		log "uploading $error_lines error lines as $LOG_OBJECT"
		if ! oci_cli os object put \
			--namespace "$NAMESPACE" \
			--bucket-name "$BUCKET" \
			--name "$LOG_OBJECT" \
			--file "$ERR_LOG.gz" \
			--content-type application/gzip \
			--force >/dev/null; then
			# The database is already safely stored. Failing the whole run for
			# the logs would make an operator think the snapshot did not happen,
			# so this is a warning and a non-zero exit that says which half
			# failed.
			log "WARNING: the snapshot uploaded but the error log did not"
			exit "$EX_IOERR"
		fi
		[ ! -f "$NEXT_CURSOR" ] || install -o root -g root -m 0600 \
			"$NEXT_CURSOR" "$CURSOR_FILE"
	else
		# Nothing to say is worth saying nothing about. An empty object per day
		# is a bucket full of files that all have to be opened to discover they
		# are empty.
		log "no error-level entries since the last run"
		[ ! -f "$NEXT_CURSOR" ] || install -o root -g root -m 0600 \
			"$NEXT_CURSOR" "$CURSOR_FILE"
	fi
else
	log "WARNING: journalctl not found; error logs were not archived"
fi

log "done"
