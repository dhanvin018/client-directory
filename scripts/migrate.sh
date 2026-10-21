#!/bin/sh
# Applies pending migrations from db/migrations to the configured database.
#
# Migrations are files named NNNN_description.sql, applied in version order.
# Each applied version is recorded in schema_migrations, so a file runs once
# per database and this script is safe on every container start and in CI.
# Never edit a migration that has been applied anywhere; add a new one.
#
# All pending migrations run in one transaction under an advisory lock, so
# replicas starting at the same time do not race and a failure leaves the
# database unchanged. (So a migration must not use statements that cannot run
# in a transaction, such as CREATE INDEX CONCURRENTLY.)
#
# Settings come from config.json (path in $CONFIG_FILE, default ../config.json
# next to this script); these environment variables override it:
#   DATABASE_URL     database.url
#   SCHEMA_DIR       migrations.schemaDir, the migrations directory (relative paths
#                    are relative to the config file)
#   DB_WAIT_SECONDS  migrations.waitSeconds
#   SKIP_MIGRATIONS  migrations.skip ("1" or "true" to skip)
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/../config.json}"

log() { echo "migrate: $*"; }

# cfg <jq path> prints the value from the config file, or nothing if the file
# or key is missing.
cfg() {
	[ -f "$CONFIG_FILE" ] || return 0
	jq -r "$1 // empty" "$CONFIG_FILE"
}

if [ -f "$CONFIG_FILE" ]; then
	if ! command -v jq >/dev/null 2>&1; then
		log "jq is required to read $CONFIG_FILE" >&2
		exit 1
	fi
	CONFIG_DIR="$(cd "$(dirname "$CONFIG_FILE")" && pwd)"
	log "using config $CONFIG_FILE"
else
	CONFIG_DIR="$SCRIPT_DIR/.."
	log "no config file at $CONFIG_FILE; using environment only"
fi

SKIP="${SKIP_MIGRATIONS:-$(cfg .migrations.skip)}"
case "$SKIP" in
	1 | true)
		log "skipped (migrations.skip / SKIP_MIGRATIONS)"
		exit 0
		;;
esac

DATABASE_URL="${DATABASE_URL:-$(cfg .database.url)}"
if [ -z "$DATABASE_URL" ]; then
	log "no database URL: set database.url in $CONFIG_FILE or DATABASE_URL" >&2
	exit 1
fi

if [ -z "${SCHEMA_DIR:-}" ]; then
	SCHEMA_DIR="$(cfg .migrations.schemaDir)"
	SCHEMA_DIR="${SCHEMA_DIR:-db/migrations}"
	case "$SCHEMA_DIR" in
		/*) ;;
		*) SCHEMA_DIR="$CONFIG_DIR/$SCHEMA_DIR" ;;
	esac
fi

DB_WAIT_SECONDS="${DB_WAIT_SECONDS:-$(cfg .migrations.waitSeconds)}"
DB_WAIT_SECONDS="${DB_WAIT_SECONDS:-60}"

# Arbitrary constant shared by every instance of this script.
LOCK_ID=4242001

# Collect migrations in version order and reject duplicate versions.
MIGRATIONS=""
seen=" "
for path in "$SCHEMA_DIR"/[0-9][0-9][0-9][0-9]_*.sql; do
	[ -f "$path" ] || continue
	name="$(basename "$path")"
	version="$(expr "${name%%_*}" + 0)"
	case "$seen" in
		*" $version "*)
			log "duplicate migration version $version ($name)" >&2
			exit 1
			;;
	esac
	seen="$seen$version "
	MIGRATIONS="$MIGRATIONS $name"
done
if [ -z "$MIGRATIONS" ]; then
	log "no migrations found in $SCHEMA_DIR" >&2
	exit 1
fi

log "waiting for database (up to ${DB_WAIT_SECONDS}s)"
waited=0
until psql "$DATABASE_URL" -qtAc 'SELECT 1' >/dev/null 2>&1; do
	waited=$((waited + 1))
	if [ "$waited" -ge "$DB_WAIT_SECONDS" ]; then
		log "database not reachable after ${DB_WAIT_SECONDS}s" >&2
		exit 1
	fi
	sleep 1
done

{
	echo 'BEGIN;'
	echo 'SET LOCAL client_min_messages = warning;' # hide "already exists, skipping" notices
	echo "DO \$\$ BEGIN PERFORM pg_advisory_xact_lock($LOCK_ID); END \$\$;"
	echo 'CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER     PRIMARY KEY,
		name       TEXT        NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);'
	# printf, not echo: some sh implementations (dash) interpret backslashes in
	# echo, which would mangle psql meta-commands.
	for name in $MIGRATIONS; do
		version="$(expr "${name%%_*}" + 0)"
		printf '%s\n' \
			"SELECT NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $version) AS pending \\gset" \
			'\if :pending' \
			"\\echo migrate: applying $name" \
			"\\i '$SCHEMA_DIR/$name'" \
			"INSERT INTO schema_migrations (version, name) VALUES ($version, '$name');" \
			'\endif'
	done
	echo 'COMMIT;'
} | psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1

log "schema up to date"
