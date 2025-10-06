#!/usr/bin/env bash
set -euo pipefail

# The entrypoint sets POSTGRES_USER / POSTGRES_PASSWORD already
: "${POSTGRES_USER:?Need POSTGRES_USER}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
  CREATE DATABASE resilient_payment_processor;
EOSQL

# Now connect to the new DB to create the dedicated schema
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname resilient_payment_processor <<-EOSQL
  CREATE SCHEMA IF NOT EXISTS svc_schema;
EOSQL
