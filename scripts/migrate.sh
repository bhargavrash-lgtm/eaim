#!/usr/bin/env bash
# =============================================================================
# EAMI — Database migration runner wrapper (B-051)
# =============================================================================
# Thin wrapper around `docker compose run --rm migrate ...`, reusing the
# exact same image/mount already defined in docker-compose.yml /
# docker-compose.prod.yml -- not a second, divergent definition of how
# migrations run. Same mechanism the `migrate` service uses automatically
# at `docker compose up`; this script is for running it manually against
# an already-up stack (the real-world "apply an update" case).
#
# Usage:
#   scripts/migrate.sh [up|version|force VERSION] [-f COMPOSE_FILE]
#
#   up              Apply all pending migrations (default if no command given)
#   version         Show the currently-applied migration version
#   force VERSION   Stamp the database at VERSION without running any SQL --
#                   for marking an existing, already-correct database as
#                   caught up (e.g. an install that predates this runner).
#                   Does NOT run migration files. Use with care: only force
#                   a version you've confirmed the database's actual schema
#                   already matches.
#
#   -f COMPOSE_FILE Compose file to use (default: docker-compose.yml)
#
# Before running against a live system with real data, take a backup first
# (scripts/backup-db.sh, or trigger the eami-backup service) -- this is the
# actual rollback story for this migration runner, not a `down` migration.
# See RECOVERY.md.
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="docker-compose.yml"
COMMAND="up"
FORCE_VERSION=""

# ── Parse args ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        up|version)
            COMMAND="$1"
            shift
            ;;
        force)
            COMMAND="force"
            FORCE_VERSION="${2:?force requires a VERSION argument, e.g. 'scripts/migrate.sh force 1'}"
            shift 2
            ;;
        -f|--file)
            COMPOSE_FILE="${2:?-f requires a filename}"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1" >&2
            echo "Usage: scripts/migrate.sh [up|version|force VERSION] [-f COMPOSE_FILE]" >&2
            exit 1
            ;;
    esac
done

cd "$REPO_ROOT"

if ! [[ -f "$COMPOSE_FILE" ]]; then
    echo "Compose file not found: $COMPOSE_FILE" >&2
    exit 1
fi

if docker compose version &>/dev/null 2>&1; then
    COMPOSE="docker compose"
elif command -v docker-compose &>/dev/null; then
    COMPOSE="docker-compose"
else
    echo "docker compose (or docker-compose) not found." >&2
    exit 1
fi

: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD must be set}"

# Percent-encode the password before it goes into a postgres:// URL --
# defense-in-depth found during this brief's own security review: a
# base64-generated password (+, /, =) is syntactically significant in a
# URL and can misparse or, on some golang-migrate versions, echo into
# error output. setup.sh/.env.example both now generate hex-only
# passwords (see .env.example's own comment), so this is belt-and-suspenders
# for anyone who set POSTGRES_PASSWORD by another means.
urlencode() {
    local s="$1" out= c i
    for (( i = 0; i < ${#s}; i++ )); do
        c="${s:i:1}"
        case "$c" in
            [a-zA-Z0-9.~_-]) out+="$c" ;;
            *) printf -v c '%%%02X' "'$c"; out+="$c" ;;
        esac
    done
    printf '%s' "$out"
}

DB_URL="postgres://eami_app:$(urlencode "$POSTGRES_PASSWORD")@postgres:5432/eami?sslmode=disable"

echo "[migrate] compose file: ${COMPOSE_FILE}"
echo "[migrate] command: ${COMMAND}${FORCE_VERSION:+ $FORCE_VERSION}"
echo ""

case "$COMMAND" in
    up)
        $COMPOSE -f "$COMPOSE_FILE" run --rm migrate -path=/migrations -database="$DB_URL" up
        ;;
    version)
        $COMPOSE -f "$COMPOSE_FILE" run --rm migrate -path=/migrations -database="$DB_URL" version
        ;;
    force)
        echo "[migrate] WARNING: 'force' stamps the database at version ${FORCE_VERSION}"
        echo "[migrate] WITHOUT running any migration SQL. Only do this if you have"
        echo "[migrate] already confirmed the database's real schema matches that version."
        read -r -p "  Continue? [y/N]: " confirm
        if [[ "${confirm,,}" != "y" ]]; then
            echo "[migrate] Aborted."
            exit 0
        fi
        $COMPOSE -f "$COMPOSE_FILE" run --rm migrate -path=/migrations -database="$DB_URL" force "$FORCE_VERSION"
        ;;
esac
