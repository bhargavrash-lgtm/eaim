# Task: Smoke-test all agent installers on real machines
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** normal  
**Blocked by:** TASK-052 (setup.sh — done)

## What I need

Test each installer on a real (or VM) target OS using the `v1.0.0-rc1` artifacts from:
`https://github.com/bhargavrash-lgtm/eaim/releases/tag/v1.0.0-rc1`

Criteria are split into two tiers. **Only Tier A is required to close this task and unblock TASK-055.**

---

## Tier A — v1.0.0 gate (run now, no collector needed)

Verify the installer mechanics work correctly on each platform. No running EAMI stack required.

### Test matrix

| Installer | Target OS | Pass criteria |
|-----------|-----------|---------------|
| `eami-agent-1.0.0-rc1-windows-amd64.msi` | Windows 11 (fresh VM) | Service installs, starts, no crash in Event Viewer |
| `eami-agent-1.0.0-rc1-darwin-amd64.pkg` | macOS 14 Intel | Launchd plist loads, agent process running (`launchctl list`) |
| `eami-agent-1.0.0-rc1-darwin-arm64.pkg` | macOS 15 Apple Silicon | Same |
| `eami-agent-1.0.0-rc1_amd64.deb` | Ubuntu 24.04 LTS | systemd service active (`systemctl status eami-agent`) |
| `eami-agent-1.0.0-rc1_amd64.rpm` | RHEL 9 / Rocky 9 | systemd service active |

### Install procedure (per platform)

**Windows:**
```powershell
msiexec /i eami-agent-1.0.0-rc1-windows-amd64.msi COLLECTOR_URL=http://localhost:8888 /quiet /log install.log
Get-Service eami-agent
```

**macOS:**
```bash
sudo installer -pkg eami-agent-1.0.0-rc1-darwin-arm64.pkg -target /
launchctl list | grep eami
```

**Ubuntu:**
```bash
sudo dpkg -i eami-agent-1.0.0-rc1_amd64.deb
systemctl status eami-agent
```

**RHEL/Rocky:**
```bash
sudo rpm -i eami-agent-1.0.0-rc1_amd64.rpm
systemctl status eami-agent
```

### Uninstall test (each platform)

Verify clean removal — no orphaned service, no leftover files:

- **Windows:** `msiexec /x eami-agent-1.0.0-rc1-windows-amd64.msi /quiet` → `C:\Program Files\EAMI` gone, service removed
- **macOS:** `sudo pkgutil --forget com.eami.agent` → launchd plist removed
- **Ubuntu:** `sudo apt remove eami-agent` → service disabled, `/usr/bin/eami-agent` removed
- **RHEL:** `sudo rpm -e eami-agent` → service disabled, binary removed

---

## Tier B — Post-v1.0 backlog (do not block TASK-055 on these)

These require a running EAMI stack. File as a follow-up after v1.0.0 ships.

- Agent connects to collector and sends first report
- Endpoint hostname appears in Discover UI
- AI apps / models / MCP servers populated in Discover detail view

---

## Acceptance criteria (Tier A only — required for TASK-055)

- [ ] All 5 installer targets: service installs and starts without crash
- [ ] All 5 uninstalls: no orphaned service or binary remains
- [ ] `tasks/TASK-054-results.md` has pass/fail for each target with log snippet
- [ ] Any Tier A failure is either fixed or documented with a workaround

## Files to update

- `tasks/TASK-054-results.md`
