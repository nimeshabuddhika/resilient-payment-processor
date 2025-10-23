#!/usr/bin/env bash
set -euo pipefail

: "${PRIMARY_HOST:?PRIMARY_HOST missing}"
: "${PRIMARY_PORT:?PRIMARY_PORT missing}"
: "${REPL_USER:?REPL_USER missing}"
: "${REPL_PASSWORD:?REPL_PASSWORD missing}"
: "${REPL_SLOT:?REPL_SLOT missing}"

# If PGDATA already initialized, just start Postgres
if [ -s "$PGDATA/PG_VERSION" ]; then
  exec docker-entrypoint.sh postgres
fi

# Wait for primary
until pg_isready -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$REPL_USER" >/dev/null 2>&1; do
  echo "waiting_for_primary..."
  sleep 1
done

export PGPASSWORD="$REPL_PASSWORD"
rm -rf "$PGDATA"/*

# Take base backup; -R writes primary_conninfo and creates standby.signal
# -C with -S creates/uses a physical replication slot to avoid WAL loss
pg_basebackup \
  -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" \
  -D "$PGDATA" -U "$REPL_USER" \
  -X stream -R -C -S "$REPL_SLOT" -v

# Tag application_name so synchronous_standby_names can match (if used later)
echo "primary_slot_name=$REPL_SLOT" >> "$PGDATA/postgresql.auto.conf"
echo "hot_standby=on"                >> "$PGDATA/postgresql.auto.conf"

chown -R postgres:postgres "$PGDATA"
exec docker-entrypoint.sh postgres
