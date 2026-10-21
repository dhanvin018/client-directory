#!/bin/sh
# Container entrypoint: apply the schema, then exec the server so it runs as
# PID 1 and receives SIGTERM for graceful shutdown. Both read $CONFIG_FILE;
# set migrations.skip in it (or SKIP_MIGRATIONS=1) when a separate job applies
# the schema.
set -eu

/app/scripts/migrate.sh

exec "$@"
