# Task: Tag and publish v1.0.0 GitHub Release
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** normal  
**Blocked by:** TASK-050, TASK-051, TASK-054 (all must pass before shipping)

## What I need

Cut the v1.0.0 release. All CI jobs must be green. All 5 installer artifacts must attach to the release.

### Pre-release checklist

Before tagging, verify:

- [ ] `go test ./...` passes in all Go services (TASK-040 clean)
- [ ] `tsc --noEmit` exits 0 in eami-ui (TASK-049 clean)
- [ ] Load test passed (TASK-050)
- [ ] Security review: zero Critical/High open findings (TASK-051)
- [ ] All 5 installers smoke-tested (TASK-054)
- [ ] `docs/quickstart.md` exists (TASK-053)
- [ ] `CHANGELOG.md` exists with v1.0.0 section
- [ ] `docker-compose.yml` version references point to `v1.0.0` image tags, not `latest`

### Step 1 — Write CHANGELOG.md

Create `CHANGELOG.md` with this format:

```markdown
# Changelog

## v1.0.0 — 2026-xx-xx

### What's new
- MCP gateway: proxy, policy enforcement, audit log, approval workflow
- Endpoint discovery agent: Windows, macOS, Linux — detects AI apps, models, MCP servers, browser extensions, cloud credentials
- Web UI: Dashboard, Discover, FinOps, Gateway config, Approvals, Audit, Alerts, Settings
- FinOps: real token spend tracking per agent per model
- Alert rules: configurable thresholds with Slack notifications
- Agent installers: MSI (Windows), .pkg (macOS arm64/amd64), .deb + .rpm (Linux)
- Single-command setup: `./scripts/setup.sh`

### Requirements
- Docker 24+ and Docker Compose v2
- Linux or macOS server (Windows via WSL2)
- PostgreSQL 16 with TimescaleDB + pgvector (included in docker-compose)
```

### Step 2 — Update image tags in docker-compose

```yaml
# Before:
image: ghcr.io/your-org/eami-api:latest
# After:
image: ghcr.io/your-org/eami-api:v1.0.0
```

Do this for all 4 service images (api, gateway, collector, ui).

### Step 3 — Tag and push

```bash
git add -A
git commit -m "chore: prepare v1.0.0 release"
git tag -a v1.0.0 -m "EAMI v1.0.0 — first customer release"
git push origin main
git push origin v1.0.0
```

### Step 4 — Verify CI

The GitHub Actions workflow triggers on `v*` tags and:
1. Builds Docker images and pushes to GHCR
2. Cross-compiles Go binaries for Windows/macOS/Linux
3. Builds MSI (windows-latest runner)
4. Builds .pkg for amd64 and arm64
5. Builds .deb and .rpm via nfpm

All 5 jobs must pass. Check the Actions tab — if any fail, fix and retag.

### Step 5 — GitHub Release

After CI passes, the workflow auto-creates a GitHub Release with all 5 artifacts attached. Edit the release on GitHub:
- Title: `EAMI v1.0.0`
- Description: paste the CHANGELOG.md v1.0.0 section
- Mark as "Latest release"

## Acceptance criteria

- [ ] Pre-release checklist all checked
- [ ] `CHANGELOG.md` exists
- [ ] `git tag v1.0.0` pushed
- [ ] All 5 GitHub Actions jobs pass (green CI)
- [ ] GitHub Release `v1.0.0` exists with 5 artifacts: MSI, .pkg (arm64+amd64), .deb, .rpm
- [ ] Docker images `ghcr.io/.../eami-*:v1.0.0` exist and are pullable

## Files to create or modify

- `CHANGELOG.md` — new file
- `docker-compose.yml` — update image tags to v1.0.0
