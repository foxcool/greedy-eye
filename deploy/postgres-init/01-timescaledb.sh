#!/bin/bash
set -e
# Use variables passed to the container
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
    CREATE EXTENSION IF NOT EXISTS timescaledb;
EOSQL
echo "TimescaleDB extension enabled."
