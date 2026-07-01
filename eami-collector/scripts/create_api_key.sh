#!/usr/bin/env bash
# create_api_key.sh — generate and register an EAMI collector API key.
#
# Usage:
#   ./scripts/create_api_key.sh <label> [db_path]
#
# Example:
#   ./scripts/create_api_key.sh "office-floor-3" ./data/buffer.db
#
# The generated key is printed once to stdout and the SHA-256 hash is
# stored in the api_keys table. Keep the printed key safe — it cannot
# be recovered from the database.

set -euo pipefail

LABEL="${1:?Usage: $0 <label> [db_path]}"
DB_PATH="${2:-./data/buffer.db}"

if ! command -v sqlite3 &>/dev/null; then
  echo "Error: sqlite3 is required" >&2
  exit 1
fi

# Generate a cryptographically random 32-byte key, hex-encoded.
RAW_KEY="eami-$(openssl rand -hex 32)"
KEY_HASH=$(echo -n "$RAW_KEY" | sha256sum | awk '{print $1}')
KEY_ID=$(python3 -c "import uuid; print(str(uuid.uuid4()))")
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

sqlite3 "$DB_PATH" <<SQL
INSERT INTO api_keys (id, key_hash, label, created_at)
VALUES ('${KEY_ID}', '${KEY_HASH}', '${LABEL}', '${NOW}');
SQL

echo ""
echo "API key created:"
echo "  Label:    ${LABEL}"
echo "  ID:       ${KEY_ID}"
echo "  Key:      ${RAW_KEY}"
echo ""
echo "Store this key securely — it will not be shown again."
echo "Configure agents with: collector.api_key: ${RAW_KEY}"
