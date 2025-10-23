#!/usr/bin/env bash
set -euo pipefail
echo "host all            all            0.0.0.0/0            scram-sha-256" >> "$PGDATA/pg_hba.conf"
echo "host replication    repl_user      0.0.0.0/0            scram-sha-256" >> "$PGDATA/pg_hba.conf"
