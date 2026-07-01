#!/usr/bin/env bash
# =============================================================================
# EAMI — First-Run On-Prem Setup Script
# =============================================================================
# Tested: Ubuntu 22.04 LTS, Debian 12, macOS 14 (Sonoma), macOS 15 (Sequoia)
#
# Usage (run as root or with sudo):
#   sudo bash scripts/setup.sh
#
# What this script does:
#   1. Checks prerequisites (Docker, docker compose, openssl, python3)
#   2. Prompts for three values: org name, admin email, admin password
#   3. Auto-generates all secrets (DB password, JWT key, API keys, RSA keypair)
#   4. Writes a complete .env file — no manual editing needed
#   5. Starts the full stack with docker-compose up -d
#   6. Waits for PostgreSQL to be healthy
#   7. Seeds the database with your org and admin user
#   8. Prints a summary with all URLs and credentials
#
# Idempotent: safe to re-run. If .env already exists, you will be asked
# whether to overwrite it before any changes are made.
#
# To skip prompts (CI/unattended use):
#   EAMI_ORG_NAME="Acme Corp" \
#   EAMI_ADMIN_EMAIL="admin@acme.com" \
#   EAMI_ADMIN_PASSWORD="s3cr3t" \
#   bash scripts/setup.sh
# =============================================================================

set -euo pipefail

# =============================================================================
# Platform detection — must run before anything else
# =============================================================================

OS="$(uname -s)"

case "$OS" in
  Darwin)  PLATFORM="macos" ;;
  Linux)   PLATFORM="linux" ;;
  *)       echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# =============================================================================
# Constants
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ENV_FILE="${REPO_ROOT}/.env"

POSTGRES_PORT=5432
COLLECTOR_PORT=8888
GATEWAY_API_PORT=8080
GATEWAY_MCP_PORT=3000
API_PORT=8081
UI_PORT=5173

POSTGRES_WAIT_SECONDS=120   # max wait for postgres to be healthy
POSTGRES_POLL_INTERVAL=3    # seconds between health checks

# =============================================================================
# Terminal colours (disabled automatically when not a TTY)
# =============================================================================

if [[ -t 1 ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    CYAN='\033[0;36m'
    BOLD='\033[1m'
    DIM='\033[2m'
    RESET='\033[0m'
else
    RED='' GREEN='' YELLOW='' CYAN='' BOLD='' DIM='' RESET=''
fi

# =============================================================================
# Logging helpers
# =============================================================================

log()  { echo -e "${CYAN}[EAMI]${RESET} $*"; }
ok()   { echo -e "${GREEN}[OK]${RESET}   $*"; }
warn() { echo -e "${YELLOW}[WARN]${RESET} $*"; }
die()  { echo -e "${RED}[ERR]${RESET}  $*" >&2; exit 1; }
sep()  { echo -e "${DIM}$(printf '%0.s-' {1..70})${RESET}"; }
header() {
    echo ""
    sep
    echo -e "${BOLD}  $*${RESET}"
    sep
}

# =============================================================================
# Prerequisite checks
# =============================================================================

check_prerequisites() {
    header "Checking prerequisites"

    local missing=0

    # Docker daemon
    if ! command -v docker &>/dev/null; then
        warn "docker not found."
        echo "  Install: https://docs.docker.com/engine/install/"
        missing=1
    else
        if ! docker info &>/dev/null 2>&1; then
            if [ "$PLATFORM" = "macos" ]; then
                die "Docker is installed but the daemon is not running. Start Docker Desktop and try again."
            else
                die "Docker is installed but the daemon is not running. Start it with: sudo systemctl start docker"
            fi
        fi
        ok "Docker $(docker --version | awk '{print $3}' | tr -d ',')"
    fi

    # docker compose (v2 subcommand or v1 standalone binary)
    if docker compose version &>/dev/null 2>&1; then
        COMPOSE="docker compose"
        ok "docker compose $(docker compose version --short 2>/dev/null || true)"
    elif command -v docker-compose &>/dev/null; then
        COMPOSE="docker-compose"
        ok "docker-compose $(docker-compose --version | awk '{print $3}' | tr -d ',')"
    else
        warn "docker compose not found."
        echo "  Install (v2 plugin): https://docs.docker.com/compose/install/"
        missing=1
    fi

    # openssl — required for secret and keypair generation
    if ! command -v openssl &>/dev/null; then
        if [ "$PLATFORM" = "macos" ]; then
            warn "openssl not found. Install: brew install openssl"
        else
            warn "openssl not found. Install: apt-get install -y openssl"
        fi
        missing=1
    else
        ok "openssl $(openssl version | awk '{print $2}')"
    fi

    # python3 — required for bcrypt password hashing
    if ! command -v python3 &>/dev/null; then
        if [ "$PLATFORM" = "macos" ]; then
            warn "python3 not found. python3 is pre-installed on macOS 14+. Check: python3 --version"
        else
            warn "python3 not found. Install: apt-get install -y python3"
        fi
        missing=1
    else
        ok "python3 $(python3 --version | awk '{print $2}')"
    fi

    if [[ $missing -ne 0 ]]; then
        die "One or more prerequisites are missing. Install them and re-run this script."
    fi
}

# =============================================================================
# Ensure python3-bcrypt is available (used for hashing the admin password)
# =============================================================================

ensure_bcrypt() {
    # Try python3 first
    if python3 -c "import bcrypt" &>/dev/null 2>&1; then
        BCRYPT_CMD="python3"; return 0
    fi
    # Try python (Windows)
    if python -c "import bcrypt" &>/dev/null 2>&1; then
        BCRYPT_CMD="python"; return 0
    fi
    log "Installing bcrypt for password hashing..."
    if python3 -m pip install bcrypt --quiet --break-system-packages 2>/dev/null \
       || python3 -m pip install bcrypt --quiet 2>/dev/null \
       || python -m pip install bcrypt --quiet 2>/dev/null; then
        command -v python3 &>/dev/null && BCRYPT_CMD="python3" || BCRYPT_CMD="python"
        return 0
    fi
    # Try apt (Ubuntu/Debian)
    if command -v apt-get &>/dev/null; then
        apt-get install -y -qq python3-bcrypt 2>/dev/null && BCRYPT_CMD="python3" && return 0
    fi
    # Docker fallback — Docker is available on this machine
    if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
        log "Using Docker for bcrypt (no local Python bcrypt found)..."
        BCRYPT_CMD="docker"
        return 0
    fi
    die "Cannot find bcrypt. Run: pip install bcrypt"
}
BCRYPT_CMD="python3"

# =============================================================================
# Secret generators
# =============================================================================

# 32-character random alphanumeric password
gen_password() {
    openssl rand -base64 32 | tr -dc 'a-zA-Z0-9' | head -c 32
}

# 64-character hex string (256 bits entropy) — for service-to-service auth keys
generate_secret() {
    openssl rand -hex 32
}

# URL-safe base64 secret suitable for JWT signing (HS256 fallback)
gen_jwt_secret() {
    openssl rand -base64 48
}

# UUID v4 (lower-case)
gen_uuid() {
    if command -v uuidgen &>/dev/null; then
        uuidgen | tr '[:upper:]' '[:lower:]'
    elif [[ -f /proc/sys/kernel/random/uuid ]]; then
        cat /proc/sys/kernel/random/uuid
    elif python3 -c "import uuid" &>/dev/null 2>&1; then
        python3 -c "import uuid; print(str(uuid.uuid4()))"
    elif python -c "import uuid" &>/dev/null 2>&1; then
        python -c "import uuid; print(str(uuid.uuid4()))"
    else
        # openssl fallback — always available
        openssl rand -hex 16 | sed 's/\(.\{8\}\)\(.\{4\}\)\(.\{4\}\)\(.\{4\}\)\(.\{12\}\)/\1-\2-\3-\4-\5/'
    fi
}

# bcrypt hash of $1 (12 rounds) — piped via stdin to avoid shell quoting issues
gen_bcrypt_hash() {
    local password="$1"
    local pycode="import bcrypt, sys; pw = sys.stdin.buffer.read(); print(bcrypt.hashpw(pw, bcrypt.gensalt(rounds=12)).decode())"
    if [[ "$BCRYPT_CMD" == "docker" ]]; then
        printf '%s' "$password" | docker run --rm -i python:3.11-alpine \
            sh -c "pip install bcrypt --quiet && python3 -c \"$pycode\""
    else
        printf '%s' "$password" | "$BCRYPT_CMD" -c "$pycode"
    fi
}

# SQL-safe: escape single quotes by doubling them
sql_escape() { printf '%s' "$1" | sed "s/'/''/g"; }

# Derive a URL slug from a string (lowercase, spaces to hyphens, strip specials)
slugify() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/^-//;s/-*$//'; }

# =============================================================================
# Generate RSA keypair for the gateway JWT signing (RS256)
# Written into the gateway_certs Docker volume via a temporary alpine container.
# =============================================================================

generate_gateway_keypair() {
    header "Generating gateway RSA keypair (RS256)"

    # Determine the compose project name (docker-compose prefixes volumes with it)
    local project_name
    project_name="${COMPOSE_PROJECT_NAME:-$(basename "$REPO_ROOT" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/-*$//')}"
    local volume_name="${project_name}_gateway_certs"

    log "Creating Docker volume: ${volume_name}"
    docker volume create "${volume_name}" &>/dev/null || true

    log "Generating 2048-bit RSA keypair in volume..."
    docker run --rm \
        -v "${volume_name}:/certs" \
        alpine sh -c "
            apk add --no-cache openssl -q 2>/dev/null
            openssl genrsa -out /certs/gateway.key 2048 2>/dev/null
            openssl rsa -in /certs/gateway.key -pubout -out /certs/gateway.pub 2>/dev/null
            chmod 600 /certs/gateway.key
            echo 'Keypair generated.'
        "

    ok "RSA keypair written to Docker volume '${volume_name}'"
    ok "  Private key : /certs/gateway.key  (inside container)"
    ok "  Public key  : /certs/gateway.pub   (inside container)"
}

# =============================================================================
# Collect three user inputs (or read from environment for unattended runs)
# =============================================================================

collect_inputs() {
    header "Organisation setup"
    echo "  This script needs three values to configure your EAMI installation."
    echo "  Everything else is generated automatically."
    echo ""

    # ORG_NAME
    if [[ -z "${EAMI_ORG_NAME:-}" ]]; then
        read -r -p "  Organisation name  [e.g. Acme Corp]: " EAMI_ORG_NAME
    else
        log "ORG_NAME set from environment: ${EAMI_ORG_NAME}"
    fi
    [[ -n "$EAMI_ORG_NAME" ]] || die "Organisation name cannot be empty."

    # ADMIN_EMAIL
    if [[ -z "${EAMI_ADMIN_EMAIL:-}" ]]; then
        read -r -p "  Admin email        [e.g. admin@acme.com]: " EAMI_ADMIN_EMAIL
    else
        log "ADMIN_EMAIL set from environment: ${EAMI_ADMIN_EMAIL}"
    fi
    [[ -n "$EAMI_ADMIN_EMAIL" ]] || die "Admin email cannot be empty."
    # Basic email format check
    if [[ "$EAMI_ADMIN_EMAIL" != *@*.* ]]; then
        die "Admin email does not look valid: ${EAMI_ADMIN_EMAIL}"
    fi

    # ADMIN_PASSWORD (hidden input with confirmation)
    if [[ -z "${EAMI_ADMIN_PASSWORD:-}" ]]; then
        local pw_confirm
        while true; do
            read -r -s -p "  Admin password     (min 8 chars, hidden): " EAMI_ADMIN_PASSWORD
            echo ""
            read -r -s -p "  Confirm password   : " pw_confirm
            echo ""
            if [[ "$EAMI_ADMIN_PASSWORD" != "$pw_confirm" ]]; then
                warn "Passwords do not match. Try again."
                continue
            fi
            if [[ ${#EAMI_ADMIN_PASSWORD} -lt 8 ]]; then
                warn "Password must be at least 8 characters."
                continue
            fi
            break
        done
    else
        log "ADMIN_PASSWORD set from environment."
        [[ ${#EAMI_ADMIN_PASSWORD} -ge 8 ]] || die "EAMI_ADMIN_PASSWORD must be at least 8 characters."
    fi

    echo ""
    ok "Inputs collected."
}

# =============================================================================
# Idempotency: check if .env already exists
# =============================================================================

check_existing_env() {
    if [[ ! -f "$ENV_FILE" ]]; then
        return 0
    fi

    warn ".env already exists at: ${ENV_FILE}"
    echo ""
    echo "  Options:"
    echo "    y  — overwrite it (generates new secrets — this will break a running stack)"
    echo "    n  — abort (keep the existing .env)"
    echo ""

    if [[ -n "${EAMI_OVERWRITE_ENV:-}" ]]; then
        if [[ "$EAMI_OVERWRITE_ENV" == "y" ]]; then
            warn "EAMI_OVERWRITE_ENV=y — overwriting existing .env."
            return 0
        else
            log "EAMI_OVERWRITE_ENV not 'y' — keeping existing .env. Nothing changed."
            exit 0
        fi
    fi

    local answer answer_lc
    read -r -p "  Overwrite? [y/N]: " answer
    answer_lc="$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')"
    if [[ "$answer_lc" != "y" ]]; then
        log "Keeping existing .env. Nothing changed. Exiting."
        exit 0
    fi
    warn "Overwriting .env with new secrets."
}

# =============================================================================
# Write the complete .env file
# =============================================================================

write_env() {
    local org_name="$1"
    local admin_email="$2"
    local pg_password="$3"
    local api_jwt_secret="$4"
    local collector_api_key="$5"
    local gateway_api_key="$6"
    local server_ip="$7"
    local service_key="$8"

    cat > "$ENV_FILE" <<EOF
# EAMI — Environment Configuration
# Generated by scripts/setup.sh on $(date -u '+%Y-%m-%dT%H:%M:%SZ')
# DO NOT commit this file to source control.

# =============================================================================
# PostgreSQL
# =============================================================================

POSTGRES_PASSWORD=${pg_password}
DATABASE_URL=postgresql://eami_app:${pg_password}@localhost:${POSTGRES_PORT}/eami

# =============================================================================
# eami-collector
# =============================================================================

COLLECTOR_PORT=${COLLECTOR_PORT}
COLLECTOR_URL=http://${server_ip}:${COLLECTOR_PORT}
COLLECTOR_SAAS_URL=http://eami-api:${API_PORT}
COLLECTOR_API_KEY=${collector_api_key}
COLLECTOR_BUFFER_DB_PATH=/data/buffer.db

# =============================================================================
# eami-gateway
# =============================================================================

GATEWAY_API_PORT=${GATEWAY_API_PORT}
GATEWAY_MCP_SSE_PORT=${GATEWAY_MCP_PORT}
GATEWAY_GOSSIP_PORT=7946
GATEWAY_JWT_KEY_PATH=/certs/gateway.key
GATEWAY_UI_BASE_URL=http://${server_ip}:${UI_PORT}
GATEWAY_ADMIN_API_KEY=${gateway_api_key}

# Shared service key — must match API_SERVICE_KEY in eami-api
GATEWAY_API_SERVICE_KEY=${service_key}

# Slack webhook for approval notifications (optional — leave blank to disable)
# Create at: https://api.slack.com/apps → Incoming Webhooks
GATEWAY_APPROVAL_SLACK_WEBHOOK=

# =============================================================================
# eami-api (SaaS REST API backend)
# =============================================================================

API_PORT=${API_PORT}
API_JWT_SECRET=${api_jwt_secret}

# Shared service key — must match GATEWAY_API_SERVICE_KEY in eami-gateway
API_SERVICE_KEY=${service_key}

# =============================================================================
# eami-ui (React frontend)
# =============================================================================

VITE_API_URL=http://${server_ip}:${API_PORT}
VITE_GATEWAY_URL=http://${server_ip}:${GATEWAY_API_PORT}

# =============================================================================
# Setup metadata (do not modify — used by re-runs and tooling)
# =============================================================================

EAMI_ORG_NAME=${org_name}
EAMI_ADMIN_EMAIL=${admin_email}
EAMI_SETUP_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
EOF

    chmod 600 "$ENV_FILE"
    ok ".env written and locked to owner-read-only (chmod 600)."
}

# =============================================================================
# Start the stack and wait for PostgreSQL to be healthy
# =============================================================================

start_stack() {
    header "Starting EAMI stack"

    cd "$REPO_ROOT"

    log "Running: ${COMPOSE} up -d"
    $COMPOSE up -d

    log "Waiting for PostgreSQL to be healthy (up to ${POSTGRES_WAIT_SECONDS}s)..."

    local elapsed=0
    while true; do
        if $COMPOSE exec -T postgres pg_isready -U eami_app -d eami &>/dev/null 2>&1; then
            ok "PostgreSQL is healthy (${elapsed}s)"
            break
        fi
        if [[ $elapsed -ge $POSTGRES_WAIT_SECONDS ]]; then
            echo ""
            die "PostgreSQL did not become healthy within ${POSTGRES_WAIT_SECONDS}s.
  Check logs: ${COMPOSE} logs postgres"
        fi
        printf "."
        sleep "$POSTGRES_POLL_INTERVAL"
        elapsed=$(( elapsed + POSTGRES_POLL_INTERVAL ))
    done
}

# =============================================================================
# Seed the database: insert the real org and admin user
# =============================================================================

seed_database() {
    local org_name="$1"
    local org_slug="$2"
    local admin_email="$3"
    local admin_name="$4"
    local admin_pw_hash="$5"

    header "Seeding database"

    cd "$REPO_ROOT"

    log "Hashing admin password with bcrypt (this may take a moment)..."
    # Hash generated before calling this function and passed in as arg

    log "Inserting org '${org_name}' and admin user '${admin_email}'..."

    # SQL uses dollar-quoting for the bcrypt hash to avoid escaping issues
    $COMPOSE exec -T postgres psql -U eami_app -d eami <<SQL
-- Setup org
INSERT INTO orgs (name, slug, plan)
VALUES (
    '$(sql_escape "$org_name")',
    '$(sql_escape "$org_slug")',
    'trial'
)
ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;

-- Store the new org id for subsequent inserts
DO \$\$
DECLARE
    v_org_id UUID;
BEGIN
    SELECT id INTO v_org_id FROM orgs WHERE slug = '$(sql_escape "$org_slug")';

    -- Admin user
    INSERT INTO users (org_id, email, name, role, password_hash)
    VALUES (
        v_org_id,
        '$(sql_escape "$admin_email")',
        '$(sql_escape "$admin_name")',
        'admin',
        \$pw\$${admin_pw_hash}\$pw\$
    )
    ON CONFLICT (email) DO UPDATE
        SET name          = EXCLUDED.name,
            role          = 'admin',
            password_hash = EXCLUDED.password_hash,
            updated_at    = NOW();

    -- Seed model pricing
    INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out) VALUES
        ('claude-opus-4-6',    0.015,   0.075),
        ('claude-sonnet-4-6',  0.003,   0.015),
        ('claude-haiku-4-5',   0.00025, 0.00125),
        ('gpt-4o',             0.005,   0.015),
        ('gpt-4o-mini',        0.00015, 0.0006),
        ('gpt-4-turbo',        0.010,   0.030)
    ON CONFLICT (model) DO NOTHING;

    -- Default gateway node entry
    INSERT INTO gateway_nodes (org_id, name, role, status, address, hostname, version)
    VALUES (v_org_id, 'gateway-primary', 'primary', 'healthy', '$(sql_escape "$(hostname -I 2>/dev/null | awk '{print $1}' || hostname)"):${GATEWAY_API_PORT}', '$(sql_escape "$(hostname)")', 'setup')
    ON CONFLICT DO NOTHING;

    -- Default policies
    INSERT INTO policies (org_id, name, description, priority, action, status)
    VALUES
        (v_org_id, 'Block production data deletion',
         'Deny any DELETE operation in the production environment', 1, 'deny', 'active'),
        (v_org_id, 'Escalate bulk updates',
         'Require approval for operations affecting more than 100 records', 2, 'escalate', 'active')
    ON CONFLICT DO NOTHING;

END;
\$\$;
SQL

    ok "Database seeded."
}

# =============================================================================
# Print completion summary
# =============================================================================

print_summary() {
    local org_name="$1"
    local admin_email="$2"
    local server_ip="$3"
    local collector_api_key="$4"
    local gateway_api_key="$5"

    echo ""
    echo ""
    sep
    echo -e "${GREEN}${BOLD}  EAMI setup complete!${RESET}"
    sep
    echo ""
    echo -e "  ${BOLD}Organisation:${RESET}  ${org_name}"
    echo ""
    echo -e "  ${BOLD}Service URLs${RESET}"
    echo -e "    Web UI         http://${server_ip}:${UI_PORT}"
    echo -e "    REST API       http://${server_ip}:${API_PORT}"
    echo -e "    Gateway API    http://${server_ip}:${GATEWAY_API_PORT}"
    echo -e "    MCP/SSE        http://${server_ip}:${GATEWAY_MCP_PORT}"
    echo -e "    Collector      http://${server_ip}:${COLLECTOR_PORT}"
    echo ""
    echo -e "  ${BOLD}Admin login${RESET}"
    echo -e "    Email          ${admin_email}"
    echo -e "    Password       (the password you entered during setup)"
    echo ""
    echo -e "  ${BOLD}Agent deployment keys${RESET}"
    echo -e "    Collector API key  ${collector_api_key}"
    echo -e "    Gateway API key    ${gateway_api_key}"
    echo ""
    echo -e "  ${BOLD}Install agent on Windows endpoints:${RESET}"
    echo -e "    msiexec /i eami-agent-*.msi /qn \\"
    echo -e "        COLLECTOR_URL=http://${server_ip}:${COLLECTOR_PORT} \\"
    echo -e "        COLLECTOR_API_KEY=${collector_api_key}"
    echo ""
    echo -e "  ${BOLD}View logs:${RESET}"
    echo -e "    docker compose logs -f"
    echo ""
    echo -e "  ${BOLD}Stop stack:${RESET}"
    echo -e "    docker compose down"
    echo ""
    echo -e "  ${DIM}All secrets saved to: ${ENV_FILE}${RESET}"
    sep
    echo ""
}

# =============================================================================
# Main
# =============================================================================

main() {
    echo ""
    echo -e "${BOLD}EAMI — First-Run Setup${RESET}"
    echo -e "${DIM}$(date)${RESET}"
    echo ""

    cd "$REPO_ROOT"

    # Step 1: Prerequisites
    check_prerequisites

    # Step 2: Idempotency — check for existing .env before any user prompts
    check_existing_env

    # Step 3: Collect three user inputs
    collect_inputs

    # Step 4: Ensure bcrypt is available (before generating hash)
    ensure_bcrypt

    # Step 5: Generate all secrets
    header "Generating secrets"
    log "Generating PostgreSQL password (32 chars)..."
    POSTGRES_PASSWORD="$(gen_password)"
    ok "POSTGRES_PASSWORD generated."

    log "Generating API JWT secret (48-byte base64)..."
    API_JWT_SECRET="$(gen_jwt_secret)"
    ok "API_JWT_SECRET generated."

    log "Generating service key for gateway-API authentication (hex-32)..."
    SERVICE_KEY="$(generate_secret)"
    ok "SERVICE_KEY generated."

    log "Generating collector API key (hex-32)..."
    COLLECTOR_API_KEY="$(generate_secret)"
    ok "COLLECTOR_API_KEY: ${COLLECTOR_API_KEY}"

    log "Generating gateway admin API key (UUID)..."
    GATEWAY_API_KEY="$(gen_uuid)"
    ok "GATEWAY_API_KEY: ${GATEWAY_API_KEY}"

    log "Hashing admin password with bcrypt (12 rounds)..."
    ADMIN_PW_HASH="$(gen_bcrypt_hash "$EAMI_ADMIN_PASSWORD")"
    ok "Admin password hashed."

    # Step 6: RSA keypair for gateway JWT signing
    generate_gateway_keypair

    # Step 7: Detect server IP for URL generation
    if [ "$PLATFORM" = "macos" ]; then
        # Try Wi-Fi (en0) then Ethernet (en1) on macOS
        SERVER_IP="$(ipconfig getifaddr en0 2>/dev/null \
                     || ipconfig getifaddr en1 2>/dev/null \
                     || true)"
    else
        SERVER_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
        SERVER_IP="${SERVER_IP%% *}"  # trim trailing spaces
    fi
    if [[ -z "$SERVER_IP" ]]; then
        SERVER_IP="localhost"
    fi
    log "Detected server IP: ${SERVER_IP}"

    # Step 8: Derive slug and admin display name
    ORG_SLUG="$(slugify "$EAMI_ORG_NAME")"
    ADMIN_NAME="${EAMI_ADMIN_EMAIL%%@*}"   # use the local-part as a display name

    # Step 9: Write .env
    header "Writing .env"
    write_env \
        "$EAMI_ORG_NAME" \
        "$EAMI_ADMIN_EMAIL" \
        "$POSTGRES_PASSWORD" \
        "$API_JWT_SECRET" \
        "$COLLECTOR_API_KEY" \
        "$GATEWAY_API_KEY" \
        "$SERVER_IP" \
        "$SERVICE_KEY"

    # Step 10: Start the stack
    start_stack

    # Step 11: Seed the database
    seed_database \
        "$EAMI_ORG_NAME" \
        "$ORG_SLUG" \
        "$EAMI_ADMIN_EMAIL" \
        "$ADMIN_NAME" \
        "$ADMIN_PW_HASH"

    # Step 12: Summary
    print_summary \
        "$EAMI_ORG_NAME" \
        "$EAMI_ADMIN_EMAIL" \
        "$SERVER_IP" \
        "$COLLECTOR_API_KEY" \
        "$GATEWAY_API_KEY"
}

main "$@"
