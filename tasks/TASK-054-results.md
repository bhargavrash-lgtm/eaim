# TASK-054 Installer Smoke Test Results

**Status: BLOCKED — prerequisites not met**  
**Tester:** DevOps-EAMI  
**Date:** 2026-07-01

---

## Blockers (must resolve before tests can run)

| # | Blocker | Owner | Status |
|---|---------|-------|--------|
| 1 | **TASK-035** — eami-collector ingest not implemented; agents have no endpoint to connect to | BE-Collector | Open |
| 2 | **TASK-046** — browser scanner not merged; agent binary is incomplete without it | BE-Collector | Open |
| 3 | No release tag exists yet; CI has not produced installer artifacts | PM-EAMI / DevOps-EAMI | Pending TASK-055 (v1.0 tag) |
| 4 | Discover UI endpoint list requires eami-api AI asset indexing (TASK-037, TASK-039) | BE-Policy | Open |

These are hard blockers. The installer files (`.msi`, `.pkg`, `.deb`, `.rpm`) do not exist as
downloadable artifacts until a `v*` tag is pushed and the build.yml CI run completes.
The Discover UI pass criteria cannot be verified without a running collector that accepts
agent reports.

---

## Test matrix

| Installer | Target OS | Result | Notes |
|-----------|-----------|--------|-------|
| `eami-agent-{version}-windows-amd64.msi` | Windows 11 (fresh VM) | **PENDING** | Blocked — no artifact |
| `eami-agent-{version}-darwin-amd64.pkg` | macOS 14 Intel | **PENDING** | Blocked — no artifact |
| `eami-agent-{version}-darwin-arm64.pkg` | macOS 15 Apple Silicon | **PENDING** | Blocked — no artifact |
| `eami-agent_{version}_amd64.deb` | Ubuntu 24.04 LTS | **PENDING** | Blocked — no artifact |
| `eami-agent_{version}_amd64.rpm` | RHEL 9 / Rocky 9 | **PENDING** | Blocked — no artifact |

---

## Uninstall test matrix

| Platform | Method | Result | Notes |
|----------|--------|--------|-------|
| Windows | `msiexec /x eami-agent.msi /quiet` | **PENDING** | |
| macOS | `sudo /Library/EAMI/Agent/uninstall.sh` | **PENDING** | |
| Ubuntu | `sudo apt remove eami-agent` | **PENDING** | |
| RHEL 9 | `sudo rpm -e eami-agent` | **PENDING** | |

---

## How to run once unblocked

### 1. Trigger artifact build

```bash
# Tag a release commit — CI produces all installer artifacts
git tag v1.0.0
git push origin v1.0.0
```

CI jobs to watch in Actions → Build:
- `MSI — eami-agent installer` → `eami-agent-msi` artifact
- `pkg — eami-agent (darwin/amd64)` → `eami-agent-pkg-amd64` artifact
- `pkg — eami-agent (darwin/arm64)` → `eami-agent-pkg-arm64` artifact
- `deb + rpm — eami-agent (linux/amd64)` → `eami-agent-linux-packages` artifact

### 2. Start EAMI server

```bash
cp .env.example .env   # or run scripts/setup.sh
docker compose up -d
# Wait for postgres healthy: docker compose ps
```

### 3. Generate a collector API key

```bash
# From .env (setup.sh generates this automatically)
grep COLLECTOR_API_KEY .env
```

### 4. Install on each target (per installer)

**Windows:**
```powershell
msiexec /i eami-agent-1.0.0-windows-amd64.msi `
  COLLECTOR_URL=http://<server>:8888 `
  API_KEY=<collector_api_key> `
  /quiet /log install.log
```

**macOS (post-install config):**
```bash
sudo installer -pkg eami-agent-1.0.0-darwin-arm64.pkg -target /
# Edit config (installer writes skeleton):
sudo nano /Library/EAMI/Agent/eami-agent.yaml
# Set: collector.url and collector.api_key
sudo launchctl kickstart -k system/com.eami.agent
```

**Linux (.deb):**
```bash
sudo EAMI_COLLECTOR_URL=http://<server>:8888 \
     EAMI_API_KEY=<collector_api_key> \
     dpkg -i eami-agent_1.0.0_amd64.deb
sudo systemctl status eami-agent
```

**Linux (.rpm):**
```bash
sudo EAMI_COLLECTOR_URL=http://<server>:8888 \
     EAMI_API_KEY=<collector_api_key> \
     rpm -i eami-agent_1.0.0_amd64.rpm
sudo systemctl status eami-agent
```

### 5. Pass criteria (per target)

- [ ] Service/daemon starts without error (exit code 0)
- [ ] Logs show successful connection to collector (no "connection refused")
- [ ] After 2 minutes: hostname appears in UI → Discover
- [ ] Click endpoint in Discover: AI apps/models/MCP servers populated

### 6. Log collection on failure

```bash
# Linux
journalctl -u eami-agent --since "10 minutes ago" > eami-agent.log

# macOS
log show --predicate 'subsystem == "com.eami.agent"' --last 10m > eami-agent.log

# Windows (PowerShell)
Get-EventLog -LogName Application -Source "EAMIAgent" -Newest 50 | Format-List
```

---

## Update this file

Replace each PENDING row with PASS / FAIL once tests are run.
For any FAIL, add a sub-section below with: OS version, installer version,
exact error message, log excerpt, and proposed fix or escalation target.
