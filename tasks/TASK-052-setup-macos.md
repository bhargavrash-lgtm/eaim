# Task: Fix setup.sh for macOS + generate random service key
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** normal  
**Blocked by:** TASK-038 (migrations), TASK-041 (service key added to config)

## What I need

`scripts/setup.sh` is tested on Ubuntu 22.04 and Debian 12. It likely fails on macOS because it uses `apt-get`. Fix it to work on macOS 14+ (Sonoma) and macOS 15 (Sequoia), Intel and Apple Silicon.

### Step 1 — Detect OS

At the top of `setup.sh`, detect the OS:

```bash
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin)  PLATFORM="macos" ;;
  Linux)   PLATFORM="linux" ;;
  *)       echo "Unsupported OS: $OS"; exit 1 ;;
esac
```

### Step 2 — Dependency installation

Replace any `apt-get install` calls with OS-specific blocks:

```bash
install_deps() {
  if [ "$PLATFORM" = "macos" ]; then
    if ! command -v brew &>/dev/null; then
      echo "Homebrew not found. Install from https://brew.sh and re-run."
      exit 1
    fi
    brew install openssl htpasswd docker 2>/dev/null || true
  else
    apt-get update -qq
    apt-get install -y -qq openssl apache2-utils curl
  fi
}
```

### Step 3 — bcrypt on macOS

`htpasswd` (for bcrypt) is in `apache2-utils` on Linux and available via `brew install httpd` on macOS, but it's painful. Use Python instead (available by default on macOS):

```bash
bcrypt_hash() {
  local password="$1"
  if command -v python3 &>/dev/null; then
    python3 -c "import bcrypt; print(bcrypt.hashpw(b'$password', bcrypt.gensalt()).decode())" 2>/dev/null
  elif command -v htpasswd &>/dev/null; then
    htpasswd -bnBC 10 "" "$password" | tr -d ':\n' | sed 's/\$2y/\$2b/'
  else
    echo "ERROR: Cannot hash password — install python3 or apache2-utils" >&2
    exit 1
  fi
}
```

### Step 4 — Generate a cryptographically random service key

The service key between gateway and API must be random — not "changeme". Add to `setup.sh`:

```bash
generate_secret() {
  openssl rand -hex 32
}

SERVICE_KEY=$(generate_secret)
COLLECTOR_API_KEY=$(generate_secret)
```

Write these to `.env` and ensure they are referenced in `docker-compose.yml` as:
```yaml
eami-api:
  environment:
    API_SERVICE_KEY: ${API_SERVICE_KEY}
eami-gateway:
  environment:
    GATEWAY_API_SERVICE_KEY: ${GATEWAY_API_SERVICE_KEY}
eami-collector:
  environment:
    COLLECTOR_API_KEY: ${COLLECTOR_API_KEY}
```

Update `.env.example` with the new keys (empty values with comments).

### Step 5 — Docker Compose v2 on macOS

On macOS with Docker Desktop, `docker compose` (v2) is the default. The current setup.sh auto-detects v1 vs v2 — verify this detection still works on macOS.

### Step 6 — Verify

Run the full setup on macOS (or simulate with `shellcheck` + manual review):

```bash
shellcheck scripts/setup.sh
bash -x scripts/setup.sh  # dry-run on macOS
```

## Acceptance criteria

- [ ] `shellcheck scripts/setup.sh` exits 0 (no errors or warnings)
- [ ] `./scripts/setup.sh` runs to completion on macOS 14+ (Homebrew present)
- [ ] `./scripts/setup.sh` runs to completion on Ubuntu 22.04 (still works)
- [ ] `.env` after setup contains a random `API_SERVICE_KEY` (not "changeme")
- [ ] `.env` after setup contains a random `COLLECTOR_API_KEY`
- [ ] `.env.example` documents `API_SERVICE_KEY` and `COLLECTOR_API_KEY`
- [ ] `docker-compose.yml` references both keys as env vars

## Files to modify

- `scripts/setup.sh`
- `.env.example`
- `docker-compose.yml`
