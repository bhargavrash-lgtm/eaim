# Task: Smoke-test all agent installers on real machines
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** normal  
**Blocked by:** TASK-035 (agent service must be fixed), TASK-046 (browser scanner), TASK-052 (setup.sh)

## What I need

Test each installer on a real (or VM) target OS. The agent must install, start, connect to the collector, and appear in the Discover UI.

### Test matrix

| Installer | Target OS | Pass criteria |
|---|---|---|
| `eami-agent-{version}-windows-amd64.msi` | Windows 11 (fresh VM) | Service installs, starts, appears in Discover |
| `eami-agent-{version}-darwin-amd64.pkg` | macOS 14 Intel | Launchd plist loads, agent starts, appears in Discover |
| `eami-agent-{version}-darwin-arm64.pkg` | macOS 15 Apple Silicon | Same |
| `eami-agent_{version}_amd64.deb` | Ubuntu 24.04 LTS | systemd service starts, appears in Discover |
| `eami-agent_{version}_amd64.rpm` | RHEL 9 / Rocky 9 | systemd service starts, appears in Discover |

### Test procedure (per installer)

1. Start with a clean OS install (or fresh VM snapshot).
2. Ensure EAMI server is running and reachable from the test machine.
3. Install the agent:
   - Windows: `msiexec /i eami-agent.msi COLLECTOR_URL=http://server:8888 API_KEY=<key> /quiet`
   - macOS: `sudo installer -pkg eami-agent.pkg -target /`; set config via env or file
   - Linux (deb): `sudo EAMI_COLLECTOR_URL=http://server:8888 EAMI_API_KEY=<key> dpkg -i eami-agent.deb`
   - Linux (rpm): `sudo EAMI_COLLECTOR_URL=http://server:8888 EAMI_API_KEY=<key> rpm -i eami-agent.rpm`
4. Wait 2 minutes.
5. Check UI → Discover — the hostname should appear.
6. Check UI → Discover → click the endpoint — AI apps/models/MCP servers should be populated.

### On failure

Document the failure in `tasks/TASK-054-results.md` with:
- Exact error message
- OS version
- Installer version
- Logs (`journalctl -u eami-agent`, Event Viewer, launchd log)
- Proposed fix (or flag to BE-Collector)

### Uninstall test (for each platform)

After the smoke test, verify clean uninstall:
- Windows: `msiexec /x eami-agent.msi /quiet` — service removed, no leftover files in `C:\Program Files\EAMI`
- macOS: `sudo pkgutil --forget com.eami.agent` — plist removed
- Linux: `sudo apt remove eami-agent` / `sudo rpm -e eami-agent` — service disabled, config preserved

## Acceptance criteria

- [ ] All 5 installer targets pass the smoke test (endpoint appears in Discover UI)
- [ ] Uninstall leaves no orphaned services or files
- [ ] `tasks/TASK-054-results.md` has pass/fail for each target
- [ ] Any failures are either fixed or documented as known issues with workaround

## Files to create

- `tasks/TASK-054-results.md`
