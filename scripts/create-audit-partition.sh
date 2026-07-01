#!/usr/bin/env bash
# create-audit-partition.sh — Creates next month's audit_log partition.
#
# Run monthly via cron (on the 25th so it's ready before month rollover):
#   0 0 25 * * /opt/eami/scripts/create-audit-partition.sh
#
# Requires: psql, DATABASE_URL env var (or defaults to local dev).

set -euo pipefail

DB_URL="${DATABASE_URL:-postgresql://eami_app:devpassword@localhost:5432/eami}"

NEXT_MONTH=$(date -d "+1 month" +%Y_%m)
NEXT_START=$(date -d "+1 month" +%Y-%m-01)
NEXT_END=$(date -d  "+2 months" +%Y-%m-01)

echo "Creating partition: audit_log_${NEXT_MONTH} (${NEXT_START} to ${NEXT_END})"

psql "$DB_URL" -c "
CREATE TABLE IF NOT EXISTS audit_log_${NEXT_MONTH}
    PARTITION OF audit_log
    FOR VALUES FROM ('${NEXT_START}') TO ('${NEXT_END}');
"

echo "Partition audit_log_${NEXT_MONTH} created (or already existed)."
