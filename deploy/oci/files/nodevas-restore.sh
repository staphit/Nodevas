#!/usr/bin/env bash
#
# Put a snapshot taken by nodevas-backup.sh back on disk.
#
# This is the half nobody writes, and it is the half that decides whether the
# other half was worth running. A backup you have never restored is a file of
# unknown value; the point of this script existing is that restoring is a thing
# you can rehearse on a spare instance in five minutes rather than improvise
# during an outage.
#
# The rules it follows, in order of how badly each one bites when broken:
#
#   1. Verify the downloaded snapshot completely before touching anything live.
#      A corrupt download that has already replaced the database has destroyed
#      the thing you were trying to recover.
#   2. Refuse to run while the server is up. SQLite would let you swap the file
#      out from under an open connection; what you get afterwards is a database
#      that half exists in a process's page cache and half on disk.
#   3. Move the existing database aside, never overwrite it. Restoring the wrong
#      snapshot is a normal mistake. It should be a recoverable one.
#   4. Say exactly what is about to happen and wait to be told to proceed.

set -euo pipefail

readonly EX_USAGE=64
readonly EX_DATAERR=65
readonly EX_UNAVAILABLE=69
readonly EX_SOFTWARE=70
readonly EX_IOERR=74
readonly EX_TEMPFAIL=75

DB_PATH="${NODEVAS_DB:-}"
WORKSPACE="${NODEVAS_WORKSPACE:-/var/lib/nodevas/workspace}"
BUCKET="${NODEVAS_BACKUP_BUCKET:-}"
NAMESPACE="${NODEVAS_OCI_NAMESPACE:-}"
# No prefix variable: object names are rooted at db/ by the backup script so
# the bucket lifecycle policy can expire them, and the default is discovered
# rather than derived. Pass --object to pin an exact snapshot.
UNIT="${NODEVAS_UNIT:-nodevas}"
LOCK_FILE="${NODEVAS_BACKUP_LOCK:-/run/lock/nodevas-backup.lock}"
OBJECT=""
LOCAL_FILE=""
ASSUME_YES=0

usage() {
	cat <<'EOF'
Usage: nodevas-restore.sh [options]

Download the nodevas database snapshot from OCI Object Storage, verify it, and
install it in place of the current database. The current database is renamed,
not deleted.

Options:
  --file PATH         Restore from a local .db or .db.gz instead of downloading.
  --db PATH           Database to replace. Default: <workspace>/.vised/nodevas.db
  --workspace PATH    Workspace root, used to derive --db.
  --bucket NAME       Object Storage bucket.       [$NODEVAS_BACKUP_BUCKET]
  --namespace NAME    Tenancy namespace. Discovered when omitted.
  --object NAME       Exact snapshot to restore. Default: newest under db/.
  --unit NAME         Service that must be stopped. [$NODEVAS_UNIT]
  -y, --yes           Do not ask for confirmation (for automated rehearsals).
  -h, --help          This text.

Afterwards:
  systemctl start <unit>

Examples:
  # Ordinary restore of the most recent snapshot
  systemctl stop nodevas
  nodevas-restore.sh --bucket nodevas-backups

  # Rehearse on a throwaway path, unattended
  nodevas-restore.sh --bucket nodevas-backups --db /tmp/rehearsal.db --yes

Exit codes: 0 ok, 64 usage, 65 snapshot corrupt, 69 missing dependency,
70 internal error, 74 download failed, 75 the service is still running.
EOF
}

log() { printf '[nodevas-restore] %s\n' "$*" >&2; }
die() {
	local code="$1"
	shift
	printf '[nodevas-restore] ERROR: %s\n' "$*" >&2
	exit "$code"
}

# Instance principal, never an API key: the credential is the machine's own
# identity, so there is nothing on disk to steal and access is revoked by taking
# the instance out of its dynamic group.
oci_cli() { oci --auth instance_principal "$@"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--object) OBJECT="${2:-}"; shift 2 ;;
	--file) LOCAL_FILE="${2:-}"; shift 2 ;;
	--db) DB_PATH="${2:-}"; shift 2 ;;
	--workspace) WORKSPACE="${2:-}"; shift 2 ;;
	--bucket) BUCKET="${2:-}"; shift 2 ;;
	--namespace) NAMESPACE="${2:-}"; shift 2 ;;
	--unit) UNIT="${2:-}"; shift 2 ;;
	-y | --yes) ASSUME_YES=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "$EX_USAGE" "unknown argument: $1" ;;
	esac
done

[ -n "$DB_PATH" ] || DB_PATH="$WORKSPACE/.vised/nodevas.db"

# Snapshots carry a timestamp in the name because the instance is not allowed to
# overwrite or delete objects — see the comment on DB_OBJECT in
# nodevas-backup.sh. So there is no fixed name to fall back on, and the default
# has to be discovered: the last name under db/ in lexical order, which is the
# newest because the timestamp is ISO-8601 basic UTC and therefore sorts as it
# reads.
#
# Discovery is deliberately not silent. Restoring is the operation where "it
# picked something" is not good enough, so the chosen name is printed and lands
# in the confirmation plan below.
if [ -z "$OBJECT" ] && [ -z "$LOCAL_FILE" ]; then
	[ -n "$BUCKET" ] || die "$EX_USAGE" "no bucket: pass --bucket, --object or --file"
	if [ -z "$NAMESPACE" ]; then
		NAMESPACE="$(oci_cli os ns get --query data --raw-output)" ||
			die "$EX_IOERR" "cannot determine the Object Storage namespace"
	fi
	OBJECT="$(oci_cli os object list \
		--namespace "$NAMESPACE" \
		--bucket-name "$BUCKET" \
		--prefix 'db/' \
		--query 'sort_by(data,&name)[-1].name' \
		--raw-output 2>/dev/null)" ||
		die "$EX_IOERR" "cannot list backups in $BUCKET"
	# An empty query result comes back as the literal "null" from the CLI, not
	# as an empty string, and restoring an object called "null" would fail much
	# later with a confusing message.
	case "$OBJECT" in
	"" | null) die "$EX_UNAVAILABLE" "no backups found under db/ in $BUCKET" ;;
	esac
	log "no --object given; using the newest snapshot: $OBJECT"
fi

command -v sqlite3 >/dev/null 2>&1 || die "$EX_UNAVAILABLE" "sqlite3 is not installed"
command -v flock >/dev/null 2>&1 || die "$EX_UNAVAILABLE" "flock is not installed"
if [ -z "$LOCAL_FILE" ]; then
	command -v oci >/dev/null 2>&1 || die "$EX_UNAVAILABLE" "the OCI CLI is not installed"
	[ -n "$BUCKET" ] || die "$EX_USAGE" "no bucket: pass --bucket or --file"
fi

DB_DIR="$(dirname "$DB_PATH")"
[ -d "$DB_DIR" ] || die "$EX_USAGE" "$DB_DIR does not exist; is --workspace right?"

# Share the backup lock. A restore and SQLite VACUUM INTO must never inspect or
# replace the live database at the same time.
exec 9>"$LOCK_FILE" || die "$EX_TEMPFAIL" "cannot open lock file $LOCK_FILE"
flock -n 9 || die "$EX_TEMPFAIL" "a backup or another restore is already running"

# --------------------------------------------------------------------------
# Rule 2, checked first, because everything after it is destructive.
#
# `systemctl is-active` is the question actually being asked — not "does a
# process exist" — and it stays correct if the unit is renamed or templated. The
# check is skipped when systemctl is absent (a container, a rehearsal on a
# laptop) rather than made fatal, because there is no unit to conflict with.
# --------------------------------------------------------------------------
if command -v systemctl >/dev/null 2>&1; then
	active_state="$(systemctl show --property=ActiveState --value "$UNIT" 2>/dev/null || true)"
	case "$active_state" in
	"" | inactive | failed) ;;
	*) die "$EX_TEMPFAIL" "$UNIT state is $active_state. Stop it first: systemctl stop $UNIT" ;;
	esac
fi

TMP_DIR=""
INSTALLING=""
cleanup() {
	local status=$?
	if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
		rm -rf -- "$TMP_DIR"
	fi
	if [ -n "$INSTALLING" ] && [ -e "$INSTALLING" ]; then
		rm -f -- "$INSTALLING"
	fi
	return "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/nodevas-restore.XXXXXXXX")" ||
	die "$EX_SOFTWARE" "cannot create a temporary directory"
chmod 700 "$TMP_DIR"

STAGED="$TMP_DIR/restored.db"

# --------------------------------------------------------------------------
# Fetch, into scratch space. Nothing under DB_PATH has been touched yet and
# nothing will be until the verification below passes.
# --------------------------------------------------------------------------
if [ -n "$LOCAL_FILE" ]; then
	[ -f "$LOCAL_FILE" ] || die "$EX_USAGE" "no such file: $LOCAL_FILE"
	SOURCE_DESC="local file $LOCAL_FILE"
	cp -- "$LOCAL_FILE" "$TMP_DIR/download" ||
		die "$EX_IOERR" "cannot read $LOCAL_FILE"
else
	if [ -z "$NAMESPACE" ]; then
		NAMESPACE="$(oci_cli os ns get --query data --raw-output 2>/dev/null)" ||
			die "$EX_IOERR" "cannot determine the Object Storage namespace"
	fi
	SOURCE_DESC="$BUCKET/$OBJECT (namespace $NAMESPACE)"
	log "downloading $SOURCE_DESC"
	oci --auth instance_principal os object get \
		--namespace "$NAMESPACE" \
		--bucket-name "$BUCKET" \
		--name "$OBJECT" \
		--file "$TMP_DIR/download" >/dev/null ||
		die "$EX_IOERR" "downloading $OBJECT failed; nothing has been changed"
fi

[ -s "$TMP_DIR/download" ] || die "$EX_DATAERR" "the downloaded snapshot is empty"

# gzip -t before gunzip: a truncated transfer is far more likely than a corrupt
# SQLite page, and it is much cheaper to detect.
if gzip -t "$TMP_DIR/download" 2>/dev/null; then
	gzip -dc -- "$TMP_DIR/download" >"$STAGED" ||
		die "$EX_DATAERR" "decompressing the snapshot failed"
else
	# An uncompressed .db is accepted so that a snapshot someone already
	# unpacked, or one taken by hand with VACUUM INTO, restores the same way.
	mv -- "$TMP_DIR/download" "$STAGED"
fi
chmod 600 "$STAGED"

# --------------------------------------------------------------------------
# Rule 1. Three questions, in increasing order of what they prove.
# --------------------------------------------------------------------------
log "verifying the snapshot before touching $DB_PATH"

integrity="$(sqlite3 "$STAGED" 'PRAGMA integrity_check;' 2>&1)" ||
	die "$EX_DATAERR" "this is not a readable SQLite database: $integrity"
[ "$integrity" = "ok" ] ||
	die "$EX_DATAERR" "snapshot failed integrity_check, refusing to restore: $integrity"

fk="$(sqlite3 "$STAGED" 'PRAGMA foreign_key_check;' 2>&1)" ||
	die "$EX_DATAERR" "foreign_key_check could not run: $fk"
[ -z "$fk" ] || die "$EX_DATAERR" "snapshot has broken foreign keys: $fk"

# A structurally perfect SQLite file from some other application would restore
# cleanly and then fail at startup. schema_migrations is the table this server
# creates before any other, so its presence is the cheapest proof that this is a
# nodevas database and not, say, someone's Firefox profile.
migrations="$(sqlite3 "$STAGED" \
	"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations';" 2>&1)" ||
	die "$EX_DATAERR" "cannot read the snapshot's schema: $migrations"
[ "$migrations" = "1" ] ||
	die "$EX_DATAERR" "this file has no schema_migrations table; it is not a nodevas database"

accounts="$(sqlite3 "$STAGED" 'SELECT count(*) FROM accounts;' 2>/dev/null || echo '?')"
events="$(sqlite3 "$STAGED" 'SELECT count(*) FROM audit_events;' 2>/dev/null || echo '?')"
newest="$(sqlite3 "$STAGED" "SELECT coalesce(max(at), 'never') FROM audit_events;" 2>/dev/null ||
	echo 'unknown')"
snapshot_bytes="$(wc -c <"$STAGED" | tr -d ' ')"

# --------------------------------------------------------------------------
# Rule 4. Say it out loud. The most useful line here is the newest audit event:
# it is the only thing on screen that tells the operator *how old* the snapshot
# is, which is the one question that decides whether to go ahead.
# --------------------------------------------------------------------------
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
ASIDE="$DB_PATH.superseded-$STAMP"
INSTALLING="$DB_PATH.restoring-$STAMP"

for candidate in "$ASIDE" "$ASIDE-wal" "$ASIDE-shm" "$INSTALLING"; do
	[ ! -e "$candidate" ] || die "$EX_TEMPFAIL" "refusing to overwrite existing path: $candidate"
done

cat >&2 <<EOF

  About to restore
    from            $SOURCE_DESC
    into            $DB_PATH
    size            $snapshot_bytes bytes
    accounts        $accounts
    audit events    $events
    newest event    $newest

  The existing database will be renamed, not deleted:
    $DB_PATH        -> $ASIDE
    $DB_PATH-wal    -> $ASIDE-wal      (if present)
    $DB_PATH-shm    -> $ASIDE-shm      (if present)

  Undo, if this turns out to be the wrong snapshot:
    mv $ASIDE $DB_PATH

EOF

if [ "$ASSUME_YES" -eq 1 ]; then
	log "--yes given, proceeding without confirmation"
else
	# Read from the terminal rather than stdin, so that piping something into
	# this script cannot be mistaken for consent.
	if [ ! -r /dev/tty ]; then
		die "$EX_USAGE" "no terminal to confirm on; pass --yes if this is automated"
	fi
	printf '  Type "restore" to proceed: ' >&2
	read -r reply </dev/tty || reply=""
	[ "$reply" = "restore" ] || die "$EX_USAGE" "not confirmed; nothing has been changed"
fi

# --------------------------------------------------------------------------
# Rule 3. Prepare on the destination filesystem, move aside, then rename.
#
# The sidecars have to move too. Leaving a -wal behind means SQLite opens the
# restored file and immediately replays a write-ahead log belonging to a
# different database — which is a corruption you created during the recovery.
# --------------------------------------------------------------------------
# The verified file is copied before anything live moves. The final rename is
# atomic because INSTALLING and DB_PATH are in the same directory.
cp -- "$STAGED" "$INSTALLING" || die "$EX_SOFTWARE" "cannot stage $INSTALLING"
chmod 600 "$INSTALLING"

# Preserve ownership before moving the live file away.
if [ -e "$DB_PATH" ] && command -v stat >/dev/null 2>&1; then
	if owner="$(stat -c '%u:%g' -- "$DB_PATH" 2>/dev/null)" && [ -n "$owner" ]; then
		chown "$owner" -- "$INSTALLING" || die "$EX_SOFTWARE" "cannot chown $INSTALLING to $owner"
	fi
fi

if [ -e "$DB_PATH" ]; then
	mv -- "$DB_PATH" "$ASIDE" || die "$EX_SOFTWARE" "cannot move $DB_PATH aside"
	log "previous database kept at $ASIDE"
	for suffix in -wal -shm; do
		if [ -e "$DB_PATH$suffix" ]; then
			mv -- "$DB_PATH$suffix" "$ASIDE$suffix" ||
				die "$EX_SOFTWARE" "cannot move $DB_PATH$suffix aside"
		fi
	done
else
	log "no existing database at $DB_PATH; restoring into an empty workspace"
fi

mv -- "$INSTALLING" "$DB_PATH" || die "$EX_SOFTWARE" "cannot install $DB_PATH"
INSTALLING=""

log "restored $DB_PATH"
log "start the service when ready: systemctl start $UNIT"
