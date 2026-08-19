#!/usr/bin/env bash
# create_api_key.sh — generate and register a per-agent EAMI collector API
# key (B-073).
#
# This is a host-side/dev-tool alternative for machines with sqlite3/
# openssl/python3 already installed. On the appliance/production
# deployment, prefer the collector binary's own admin subcommands instead
# (no extra tools needed inside the minimal Alpine runtime image):
#
#   docker compose exec eami-collector /app/collector mint-key --agent-id <agent-id> --label <label>
#   docker compose exec eami-collector /app/collector revoke-key --agent-id <agent-id>
#   docker compose exec eami-collector /app/collector list-keys
#
# Usage:
#   ./scripts/create_api_key.sh <agent-id> <label> [db_path]
#
# Example:
#   ./scripts/create_api_key.sh workstation-42 "Office floor 3, workstation 42" ./data/buffer.db
#
# agent-id is the identity this key authenticates as -- it must match the
# value the agent itself reports as agent_id (its hostname, by default; see
# eami-agent/internal/payload/builder.go). A report submitted under this
# key claiming a DIFFERENT agent_id is rejected by the collector (403).
#
# The generated key is printed once to stdout and the SHA-256 hash is
# stored in the api_keys table. Keep the printed key safe — it cannot
# be recovered from the database.

set -euo pipefail

AGENT_ID="${1:?Usage: $0 <agent-id> <label> [db_path]}"
LABEL="${2:?Usage: $0 <agent-id> <label> [db_path]}"
DB_PATH="${3:-./data/buffer.db}"

if ! command -v sqlite3 &>/dev/null; then
  echo "Error: sqlite3 is required" >&2
  exit 1
fi

# Generate a cryptographically random 32-byte key, hex-encoded.
RAW_KEY="eami-$(openssl rand -hex 32)"
KEY_HASH=$(echo -n "$RAW_KEY" | sha256sum | awk '{print $1}')
KEY_ID=$(python3 -c "import uuid; print(str(uuid.uuid4()))")
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Escape single quotes (SQL-standard '' doubling) before interpolating into
# the literal below -- AGENT_ID/LABEL are free-form admin-supplied values,
# not script-generated like KEY_ID/KEY_HASH/NOW, so they're the two fields
# that need this (flagged by this brief's own security review: a value
# containing a single quote could otherwise break out of the string
# literal). This is defense in depth for this dev-tool script specifically
# -- the collector binary's own `mint-key` subcommand (recommended above)
# uses Go's parameterized database/sql placeholders and was never affected.
AGENT_ID_SQL="${AGENT_ID//\'/\'\'}"
LABEL_SQL="${LABEL//\'/\'\'}"

# INSERT enforces the same partial unique index the collector binary's own
# mint-key uses (idx_api_keys_agent_active) -- this fails loudly with a
# constraint error if agent-id already has an active key, rather than
# silently creating a second one.
sqlite3 "$DB_PATH" <<SQL
INSERT INTO api_keys (id, key_hash, label, agent_id, created_at)
VALUES ('${KEY_ID}', '${KEY_HASH}', '${LABEL_SQL}', '${AGENT_ID_SQL}', '${NOW}');
SQL

echo ""
echo "API key created:"
echo "  Agent ID: ${AGENT_ID}"
echo "  Label:    ${LABEL}"
echo "  ID:       ${KEY_ID}"
echo "  Key:      ${RAW_KEY}"
echo ""
echo "Store this key securely — it will not be shown again."
echo "Configure the agent with: collector.api_key: ${RAW_KEY}"
