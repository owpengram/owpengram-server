#!/usr/bin/env bash
# Moves owpengram's PostgreSQL data from the external (Docker) instance an
# install has been using to a native one on the same machine, without ever
# touching the Docker instance's data: dump mode only reads from it,
# restore mode only writes to the destination. Two separate steps so the
# (quick, safe, read-only) dump can happen right away, and the restore --
# the step that actually overwrites something -- waits until the native
# PostgreSQL is installed and ready, as its own deliberate action.
#
#   ./scripts/migrate-postgres-to-native.sh dump
#       pg_dump's the Docker instance (custom format, compressed) into
#       ./pgmigrate/owpengram_<timestamp>.dump. Safe to run any time; the
#       source is only ever read.
#
#   ./scripts/migrate-postgres-to-native.sh restore <dump-file> <dest-dsn>
#       pg_restore's that dump into <dest-dsn> -- the native PostgreSQL's own
#       connection string, e.g.
#       postgres://owpengram:owpengram@127.0.0.1:5432/owpengram?sslmode=disable
#       The destination database must already exist with the schema a
#       current owpengram-server checkout creates (run owpengram-ctl once,
#       go through its storage step with "external" PostgreSQL pointed at
#       this native instance, then stop it before restoring -- its own
#       migration run is what gets the schema to the right starting point;
#       --clean below then empties it back out so the dump's rows replace
#       it instead of colliding with it). After a successful restore, start
#       owpengram-server normally -- its own migration runner applies
#       anything newer than what the dump's schema_migrations table recorded.
#
# Environment variables (all optional, defaults match this deployment):
#   DOCKER_CONTAINER   Postgres container name (default: owpengram-postgres)
#   DOCKER_DB          database name inside it   (default: owpengram)
#   DOCKER_USER        role name inside it       (default: owpengram)
#   PG_RESTORE         pg_restore binary to use for restore (default: pg_restore
#                      on PATH -- install the postgresql-client package matching
#                      the native server's major version)
set -euo pipefail
cd "$(dirname "$0")/.."

ok()   { printf '[migrate-postgres] %s\n' "$*"; }
warn() { printf '[migrate-postgres] WARNING: %s\n' "$*" >&2; }
die()  { printf '[migrate-postgres] ERROR: %s\n' "$*" >&2; exit 1; }

DOCKER_CONTAINER="${DOCKER_CONTAINER:-owpengram-postgres}"
DOCKER_DB="${DOCKER_DB:-owpengram}"
DOCKER_USER="${DOCKER_USER:-owpengram}"
PG_RESTORE="${PG_RESTORE:-pg_restore}"

cmd="${1:-}"

cmd_dump() {
	command -v docker >/dev/null 2>&1 || die "docker not found on PATH -- this reads from the Docker Postgres container"
	docker inspect "$DOCKER_CONTAINER" >/dev/null 2>&1 || die "no container named '$DOCKER_CONTAINER' (set DOCKER_CONTAINER= if yours is named differently)"

	mkdir -p pgmigrate
	local stamp out
	stamp="$(date +%Y%m%d-%H%M%S)"
	out="pgmigrate/owpengram_${stamp}.dump"

	ok "dumping '$DOCKER_DB' from container '$DOCKER_CONTAINER' (custom format, compressed)..."
	# -Fc (custom format) rather than plain SQL: compressed, and pg_restore
	# can parallelize and skip/retarget objects on the way back in -- a
	# plain .sql dump would need a straight `psql < dump`, no flexibility if
	# the destination role name or ownership ends up different.
	docker exec -i "$DOCKER_CONTAINER" pg_dump -U "$DOCKER_USER" -d "$DOCKER_DB" -Fc >"$out"

	local size
	size="$(du -h "$out" | cut -f1)"
	ok "done: $out ($size)"
	ok "next: install native PostgreSQL, start owpengram-ctl once with external"
	ok "mode pointed at it so it creates the schema, stop owpengram-ctl, then run:"
	ok "  $0 restore $out <dest-dsn>"
}

cmd_restore() {
	local dump="${1:-}" dsn="${2:-}"
	[ -n "$dump" ] && [ -n "$dsn" ] || die "usage: $0 restore <dump-file> <dest-dsn>"
	[ -f "$dump" ] || die "no such dump file: $dump"
	command -v "$PG_RESTORE" >/dev/null 2>&1 || die "$PG_RESTORE not found on PATH -- install the postgresql-client package matching the destination server's major version (set PG_RESTORE= to point at a specific binary)"

	ok "restoring $dump into the destination..."
	ok "this DROPS and recreates every object the dump contains in that database first (--clean --if-exists) -- nothing outside what the dump itself describes is touched."
	# --no-owner/--no-privileges: the dump's own ownership/grants almost
	# certainly do not exist as real roles on a brand-new native instance
	# (the Docker instance's "owpengram" role is not guaranteed to be the
	# same role, even with the same name, as whatever bootstrap created on
	# the native one) -- restoring as whichever role this connection
	# authenticates as, which already owns the schema a fresh owpengram-ctl
	# run created, is what actually works here.
	"$PG_RESTORE" --clean --if-exists --no-owner --no-privileges --dbname="$dsn" "$dump"

	ok "restore finished. Start owpengram-server (or owpengram-ctl) normally next --"
	ok "its own startup migration run applies anything newer than what this dump's"
	ok "schema_migrations table recorded."
}

case "$cmd" in
dump)
	cmd_dump
	;;
restore)
	shift
	cmd_restore "$@"
	;;
*)
	sed -n '2,33p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
