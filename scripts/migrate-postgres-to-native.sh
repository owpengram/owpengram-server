#!/usr/bin/env bash
# Moves owpengram's PostgreSQL data from the external (Docker) instance to the
# embedded (native) PostgreSQL that owpengram-ctl manages -- automatically,
# end to end, with no manual steps.
#
# The embedded PostgreSQL is bootstrapped from scratch when it isn't there yet:
# the binaries are taken from data/pgcache/ (or downloaded from Maven Central,
# the same source the embedded-postgres library uses), initdb'd with the exact
# flags the library uses (see internal/embeddedpg.go and the
# fergusstrange/embedded-postgres prepare_database.go), and the "owpengram"
# database is created. Then the Docker database is dumped and restored into
# it, and the embedded PostgreSQL is left stopped for owpengram-ctl to take
# over.
#
# Usage:
#   ./scripts/migrate-postgres-to-native.sh            # or: ... migrate
#       Run the whole migration automatically.
#
#   ./scripts/migrate-postgres-to-native.sh dump
#       Just pg_dump the Docker instance into ./pgmigrate/ (read-only).
#
#   ./scripts/migrate-postgres-to-native.sh restore <dump-file> [<dest-dsn>]
#       Just restore a dump into <dest-dsn> (defaults to the embedded DSN).
#
#   ./scripts/migrate-postgres-to-native.sh bootstrap
#       Just obtain the embedded binaries + initdb the data dir (idempotent),
#       without touching the Docker instance or restoring anything.
#
#   ./scripts/migrate-postgres-to-native.sh start-pg | stop-pg
#       Start / stop the embedded PostgreSQL by hand (via its own pg_ctl).
#
# Environment variables (all optional, defaults match this deployment):
#   DOCKER_CONTAINER              Postgres container name (default: owpengram-postgres)
#   DOCKER_DB                     database name inside it   (default: owpengram)
#   DOCKER_USER                   role name inside it       (default: owpengram)
#   PG_RESTORE                    pg_restore binary to use  (default: the embedded
#                                 PostgreSQL's own)
#   TELESRV_EMBEDDED_POSTGRES_DIR data dir                    (default: data/postgres)
#   TELESRV_EMBEDDED_POSTGRES_PORT port                       (default: 15433)
#   FORCE=1                       stop owpengram-ctl automatically if it is
#                                 running (otherwise the script refuses and
#                                 asks you to stop it first)
#
# NOTES
#   The embedded PostgreSQL is pinned to 17.5.0 (internal/embeddedpg.go's
#   V17), matching the optional docker-compose.yml's postgres:17-alpine, so a
#   dump moves between the two without surprises. The credentials are fixed at
#   owpengram/owpengram/owpengram (also from internal/embeddedpg.go), and the
#   server binds 127.0.0.1 only.
#
#   The restore step DROPS and recreates every object the dump contains in the
#   destination database first (--clean --if-exists) -- nothing outside what
#   the dump itself describes is touched. --no-owner/--no-privileges are passed
#   because the Docker instance's "owpengram" role is not guaranteed to be the
#   same role as the freshly initdb'd one; restoring as the connecting role
#   (which owns the schema) is what works.
set -euo pipefail
cd "$(dirname "$0")/.."

ok()   { printf '[migrate-postgres] %s\n' "$*"; }
warn() { printf '[migrate-postgres] WARNING: %s\n' "$*" >&2; }
die()  { printf '[migrate-postgres] ERROR: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Defaults / environment
# ---------------------------------------------------------------------------
DOCKER_CONTAINER="${DOCKER_CONTAINER:-owpengram-postgres}"
DOCKER_DB="${DOCKER_DB:-owpengram}"
DOCKER_USER="${DOCKER_USER:-owpengram}"
PG_DIR="${TELESRV_EMBEDDED_POSTGRES_DIR:-data/postgres}"
PG_PORT="${TELESRV_EMBEDDED_POSTGRES_PORT:-15433}"
PG_VERSION="17.5.0"     # must match internal/embeddedpg.go (V17)
PG_USER="owpengram"     # fixed by internal/embeddedpg.go
PG_PASS="owpengram"
PG_DB="owpengram"

PG_BIN="$PG_DIR/runtime/bin"
PG_DATA="$PG_DIR/data"
PG_CACHE_DIR="$(dirname "$PG_DIR")/pgcache"   # internal/embeddedpg.go: filepath.Dir(dataDir)/pgcache
PG_CTL="$PG_BIN/pg_ctl"
PG_RESTORE_BIN="$PG_BIN/pg_restore"
PSQL="$PG_BIN/psql"
INITDB="$PG_BIN/initdb"
PG_LOG="$PG_DIR/pg_ctl.log"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
# zonkyio/embedded-postgres-binaries artifact name for this host, matching the
# library's version_strategy.go (linux amd64 / arm64v8 / arm32v6 / arm32v7,
# plus "-alpine" when /etc/alpine-release is present).
os_arch() {
	local os arch
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	case "$(uname -m)" in
		x86_64|amd64)   arch="amd64" ;;
		aarch64|arm64)  arch="arm64v8" ;;
		armv7l|armv7*)  arch="arm32v7" ;;
		armv6l|armv6*)  arch="arm32v6" ;;
		*)              arch="$(uname -m)" ;;
	esac
	[ "$os" = "linux" ] && [ -f /etc/alpine-release ] && arch="${arch}-alpine"
	printf '%s-%s' "$os" "$arch"
}

ctl_running() {
	command -v pgrep >/dev/null 2>&1 && pgrep -f 'owpengram-ctl' >/dev/null 2>&1
}

# Obtains the embedded PostgreSQL binaries into $PG_BIN if they are missing.
ensure_binaries() {
	[ -x "$PG_CTL" ] && return 0
	ok "embedded PostgreSQL binaries not found at $PG_BIN -- obtaining them"
	mkdir -p "$PG_CACHE_DIR" "$PG_DIR"

	local key cache url jar tmpdir txz
	key="$(os_arch)"
	cache="$PG_CACHE_DIR/embedded-postgres-binaries-${key}-${PG_VERSION}.txz"

	if [ ! -f "$cache" ]; then
		url="https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-${key}/${PG_VERSION}/embedded-postgres-binaries-${key}-${PG_VERSION}.jar"
		ok "downloading $url"
		jar="$(mktemp --suffix=.jar)"
		if command -v curl >/dev/null 2>&1; then
			curl -fsSL "$url" -o "$jar" || die "download failed: $url"
		elif command -v wget >/dev/null 2>&1; then
			wget -q "$url" -O "$jar" || die "download failed: $url"
		else
			die "need curl or wget to download the embedded PostgreSQL binaries (or pre-place the .txz in $PG_CACHE_DIR)"
		fi
		tmpdir="$(mktemp -d)"
		if command -v unzip >/dev/null 2>&1; then
			unzip -o -q "$jar" -d "$tmpdir"
		else
			python3 - "$jar" "$tmpdir" <<'PY'
import sys, zipfile
zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])
PY
		fi
		txz="$(find "$tmpdir" -name '*.txz' -print -quit)"
		[ -n "$txz" ] || die "no .txz found inside $jar"
		mv "$txz" "$cache"
		rm -rf "$jar" "$tmpdir"
	fi

	ok "extracting $cache -> $PG_DIR/runtime"
	mkdir -p "$PG_DIR/runtime"
	tar -xJf "$cache" -C "$PG_DIR/runtime"
	[ -x "$PG_CTL" ] || die "extraction failed: $PG_CTL still missing"
}

# initdb's the data dir with the exact flags the embedded-postgres library
# uses (prepare_database.go), unless it is already a 17.x data dir.
ensure_initdb() {
	if [ -f "$PG_DATA/PG_VERSION" ]; then
		local v; v="$(cat "$PG_DATA/PG_VERSION")"
		case "$v" in
			17*) return 0 ;;
			*) die "existing data dir is PostgreSQL $v, expected 17.x -- refusing to overwrite it; move $PG_DATA aside first" ;;
		esac
	fi
	ok "initializing embedded PostgreSQL (initdb)..."
	mkdir -p "$PG_DIR/runtime"
	local pwfile; pwfile="$PG_DIR/runtime/pwfile"
	printf '%s' "$PG_PASS" > "$pwfile"
	"$INITDB" -A password -U "$PG_USER" -D "$PG_DATA" --pwfile="$pwfile" --locale=C --encoding=UTF8
	rm -f "$pwfile"
}

stop_if_running() {
	if [ -f "$PG_DATA/postmaster.pid" ]; then
		ok "embedded PostgreSQL appears to be running -- stopping it first"
		"$PG_CTL" stop -D "$PG_DATA" -m fast >/dev/null 2>&1 || warn "could not stop a running embedded PostgreSQL (it may already be stopped)"
	fi
}

start_pg() {
	[ -x "$PG_CTL" ] || die "pg_ctl not found at $PG_CTL -- run '$0 bootstrap' first"
	[ -f "$PG_DATA/PG_VERSION" ] || die "data dir not initialized -- run '$0 bootstrap' first"
	ok "starting embedded PostgreSQL on port $PG_PORT (data in $PG_DATA)..."
	"$PG_CTL" start -w -D "$PG_DATA" -o "-p $PG_PORT" -l "$PG_LOG"
}

stop_pg() {
	[ -x "$PG_CTL" ] || die "pg_ctl not found at $PG_CTL -- run '$0 bootstrap' first"
	ok "stopping embedded PostgreSQL..."
	"$PG_CTL" stop -D "$PG_DATA" -m fast || warn "pg_ctl stop reported an error (it may already be stopped)"
}

# Creates the "owpengram" database if it doesn't exist (fresh initdb only has
# postgres/template0/template1).
ensure_database() {
	local exists
	exists="$(PGPASSWORD="$PG_PASS" "$PSQL" -h 127.0.0.1 -p "$PG_PORT" -U "$PG_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$PG_DB'")"
	if [ "$exists" = "1" ]; then
		return 0
	fi
	ok "creating database '$PG_DB'"
	PGPASSWORD="$PG_PASS" "$PSQL" -h 127.0.0.1 -p "$PG_PORT" -U "$PG_USER" -d postgres -c "CREATE DATABASE \"$PG_DB\""
}

# ---------------------------------------------------------------------------
# Subcommands
# ---------------------------------------------------------------------------
cmd_dump() {
	command -v docker >/dev/null 2>&1 || die "docker not found on PATH -- this reads from the Docker Postgres container"
	docker inspect "$DOCKER_CONTAINER" >/dev/null 2>&1 || die "no container named '$DOCKER_CONTAINER' (set DOCKER_CONTAINER= if yours is named differently)"

	mkdir -p pgmigrate
	local stamp
	stamp="$(date +%Y%m%d-%H%M%S)"
	DUMPFILE="pgmigrate/owpengram_${stamp}.dump"

	ok "dumping '$DOCKER_DB' from container '$DOCKER_CONTAINER' (custom format, compressed)..."
	docker exec -i "$DOCKER_CONTAINER" pg_dump -U "$DOCKER_USER" -d "$DOCKER_DB" -Fc > "$DUMPFILE"
	ok "done: $DUMPFILE ($(du -h "$DUMPFILE" | cut -f1))"
}

cmd_restore() {
	local dump="${1:-}" dsn="${2:-postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable}"
	[ -n "$dump" ] || die "usage: $0 restore <dump-file> [<dest-dsn>]"
	[ -f "$dump" ] || die "no such dump file: $dump"
	local r="${PG_RESTORE:-$PG_RESTORE_BIN}"
	[ -x "$r" ] || die "$r not found -- run '$0 bootstrap' first, or set PG_RESTORE= to a pg_restore binary"

	ok "restoring $dump into the destination..."
	ok "this DROPS and recreates every object the dump contains in that database first (--clean --if-exists) -- nothing outside what the dump itself describes is touched."
	"$r" --clean --if-exists --no-owner --no-privileges --dbname="$dsn" "$dump"
	ok "restore finished."
}

cmd_bootstrap() {
	ensure_binaries
	stop_if_running
	ensure_initdb
	ok "embedded PostgreSQL is bootstrapped (binaries + initialized data dir); it is not running."
}

cmd_migrate() {
	command -v docker >/dev/null 2>&1 || die "docker not found on PATH"
	docker inspect "$DOCKER_CONTAINER" >/dev/null 2>&1 || die "no container named '$DOCKER_CONTAINER' (set DOCKER_CONTAINER= if yours is named differently)"

	if ctl_running; then
		if [ "${FORCE:-0}" = "1" ]; then
			warn "owpengram-ctl is running -- stopping it (FORCE=1)"
			pkill -f 'owpengram-ctl' || true
			sleep 2
		else
			die "owpengram-ctl is running -- stop it first, or set FORCE=1 to stop it automatically"
		fi
	fi

	ensure_binaries
	stop_if_running
	ensure_initdb

	cmd_dump

	start_pg
	ensure_database
	cmd_restore "$DUMPFILE"
	stop_pg

	ok "migration complete: Docker '$DOCKER_DB' -> embedded PostgreSQL (port $PG_PORT, data in $PG_DATA)."
	ok "next: run owpengram-ctl (it starts the embedded PostgreSQL and applies any"
	ok "migrations newer than the dump's schema_migrations table), then migrate"
	ok "blobs from MinIO to localfs with cmd/blob-migrate."
}

# ---------------------------------------------------------------------------
# Dispatch
# ---------------------------------------------------------------------------
cmd="${1:-migrate}"
case "$cmd" in
	migrate)  cmd_migrate ;;
	dump)     cmd_dump ;;
	restore)  shift; cmd_restore "$@" ;;
	bootstrap) cmd_bootstrap ;;
	start-pg) start_pg ;;
	stop-pg)  stop_pg ;;
	-h|--help|help) sed -n '2,56p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) die "unknown command: $cmd (see --help)" ;;
esac
