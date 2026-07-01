# EAMI Agent — Installer Guide

Platform-specific installers for the EAMI endpoint agent. All platforms share the same philosophy: silent install, config injected at deploy time, no user interaction.

| Platform | Format | Config location | Service manager |
|---|---|---|---|
| Windows | MSI (WiX v4) | `HKLM\SOFTWARE\EAMI\Agent` | Windows Service (`EAMIAgent`) |
| macOS | .pkg (pkgbuild) | `/etc/eami/agent.yaml` | launchd (`io.eami.agent`) |
| Linux | .deb / .rpm (nfpm) | `/etc/eami/agent.yaml` | systemd (`eami-agent`) |

---

## Windows — MSI

### What the MSI does

| Action | Detail |
|---|---|
| Installs binary | `C:\Program Files\EAMI\Agent\eami-agent.exe` |
| Writes registry | `HKLM\SOFTWARE\EAMI\Agent\` — see keys below |
| Registers service | `EAMIAgent`, start type **Automatic**, account **LocalSystem** |
| Starts service | Immediately after install |
| Uninstall | Stops and removes service, deletes binary, removes registry key |

### Registry keys written on install

| Key | Type | Value |
|---|---|---|
| `CollectorUrl` | REG_SZ | Value of `COLLECTOR_URL` property |
| `CollectorApiKey` | REG_SZ | Value of `COLLECTOR_API_KEY` property |
| `InstallDir` | REG_SZ | Resolved install path |
| `Version` | REG_SZ | MSI version string |
| `IntervalSeconds` | REG_DWORD | `300` (scan interval, default) |

> **Note (ADR-014):** The agent binary reads `CollectorUrl` and `CollectorApiKey` from
> these registry keys at startup. This requires the registry-fallback feature in
> `eami-agent/internal/config/` (owned by BE-Collector). Until that ships, the agent
> falls back to `eami-agent.yaml` in the install directory.

### Build locally

**Prerequisites:** Windows 10/11+, [.NET SDK 6+](https://dotnet.microsoft.com/download)

```powershell
# 1. Build the binary (from Linux/Mac/WSL)
cd eami-agent
$Env:GOOS = 'windows'; $Env:GOARCH = 'amd64'; $Env:CGO_ENABLED = '0'
go build -ldflags='-w -s' -o eami-agent-windows-amd64.exe ./cmd/agent/

# 2. Build the MSI (from Windows PowerShell)
.\eami-agent\installer\build.ps1 -Version 1.0.0
```

Output: `eami-agent\installer\dist\eami-agent-1.0.0-windows-amd64.msi`

### Silent install

```cmd
msiexec /i eami-agent-1.0.0-windows-amd64.msi /qn ^
    COLLECTOR_URL=https://collector.corp.internal:8888 ^
    COLLECTOR_API_KEY=eami_k_your_key_here
```

### Silent uninstall

```cmd
msiexec /x eami-agent-1.0.0-windows-amd64.msi /qn
```

### Group Policy deployment (GPO)

1. Copy the MSI to `\\corp.internal\Software\EAMI\eami-agent-1.0.0-windows-amd64.msi`
2. Create a GPO under **Computer Configuration → Windows Settings → Scripts → Startup**:
   ```cmd
   msiexec /i \\corp.internal\Software\EAMI\eami-agent-1.0.0-windows-amd64.msi /qn ^
       COLLECTOR_URL=https://collector.corp.internal:8888 ^
       COLLECTOR_API_KEY=eami_k_your_key_here
   ```

### SCCM / Intune

- **Install command:** `msiexec /i eami-agent.msi /qn COLLECTOR_URL=... COLLECTOR_API_KEY=...`
- **Uninstall command:** `msiexec /x eami-agent.msi /qn`
- **Detection rule (Intune/SCCM):** Registry key `HKLM\SOFTWARE\EAMI\Agent`, value `Version` exists

### Troubleshooting

| Symptom | Check |
|---|---|
| Service does not start | `Event Viewer → Windows Logs → Application` — look for EAMIAgent errors |
| Registry keys missing | Re-run MSI with `/l*v install.log` and check WriteRegistryValues errors |
| Silent install times out | Add `C:\Program Files\EAMI\Agent\` to AV exclusions |
| MSI build fails | Run `wix --version`; check .NET SDK is 6+ |

---

## macOS — .pkg

### What the pkg does

| Action | Detail |
|---|---|
| Installs binary | `/usr/local/bin/eami-agent` |
| Installs plist | `/Library/LaunchDaemons/io.eami.agent.plist` |
| Writes config | `/etc/eami/agent.yaml` (from env vars or Jamf script parameters) |
| Starts service | launchd loads and starts the daemon immediately (RunAtLoad=true, KeepAlive=true) |
| Uninstall | Run `sudo /usr/local/share/eami/uninstall.sh` or use `uninstall.sh` from this directory |

### Build locally

**Prerequisites:** macOS with Xcode Command Line Tools (`xcode-select --install`)

```bash
# 1. Build the binary
cd eami-agent
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 \
    go build -ldflags='-w -s' -o eami-agent-darwin-amd64 ./cmd/agent/

# For Apple Silicon:
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
    go build -ldflags='-w -s' -o eami-agent-darwin-arm64 ./cmd/agent/

# 2. Build the pkg
./eami-agent/installer/macos/build.sh 1.0.0 amd64 ./eami-agent/eami-agent-darwin-amd64
./eami-agent/installer/macos/build.sh 1.0.0 arm64 ./eami-agent/eami-agent-darwin-arm64
```

Output: `eami-agent/installer/macos/dist/eami-agent-1.0.0-darwin-{amd64,arm64}.pkg`

> **Code signing:** The pkg built above is unsigned. Apple Gatekeeper will block
> unsigned pkgs on direct download. For internal MDM deployment (Jamf, Mosyle,
> Kandji), unsigned pkgs uploaded directly to the MDM system work fine. For
> distribution outside MDM, sign with `productsign --sign "Developer ID Installer: ..."`.

### Silent install

```bash
# Basic install (config uses placeholder values — edit /etc/eami/agent.yaml after)
sudo installer -pkg eami-agent-1.0.0-darwin-amd64.pkg -target /

# Install with config injected at install time
sudo EAMI_COLLECTOR_URL=https://collector.corp.internal:8888 \
     EAMI_COLLECTOR_API_KEY=eami_k_your_key_here \
     installer -pkg eami-agent-1.0.0-darwin-amd64.pkg -target /
```

### Jamf Pro deployment

1. Upload the `.pkg` to **Jamf Pro → Packages**.
2. Create a **Policy** targeting the device group.
3. Under **Scripts**, add a pre-install script to set environment variables, or use
   **Script Parameters** (Jamf passes `$4` and `$5` to the postinstall script):
   - Parameter 4: `https://collector.corp.internal:8888`  ← COLLECTOR_URL
   - Parameter 5: `eami_k_your_key_here`                 ← COLLECTOR_API_KEY
4. Add the package to the policy under **Packages**.
5. Set the policy trigger to **Enrollment Complete** or **Recurring Check-In**.

### Mosyle / Kandji deployment

1. Upload the `.pkg` to the MDM console.
2. Add a **custom script** that runs before the pkg installs:
   ```bash
   export EAMI_COLLECTOR_URL="https://collector.corp.internal:8888"
   export EAMI_COLLECTOR_API_KEY="eami_k_your_key_here"
   ```
3. Deploy to the target device group.

### Checking service status

```bash
# Is the service running?
sudo launchctl list io.eami.agent

# View logs
tail -f /var/log/eami-agent.log
```

### Uninstall

```bash
sudo bash eami-agent/installer/macos/uninstall.sh

# Keep logs:
sudo bash eami-agent/installer/macos/uninstall.sh -k
```

---

## Linux — .deb / .rpm

### What the package does

| Action | Detail |
|---|---|
| Installs binary | `/usr/bin/eami-agent` |
| Installs service unit | `/lib/systemd/system/eami-agent.service` |
| Writes config | `/etc/eami/agent.yaml` (from env vars at install time) |
| Enables service | `systemctl enable --now eami-agent` (runs on next boot + starts immediately) |
| Uninstall | `dpkg -r eami-agent` or `rpm -e eami-agent` — stops service, removes binary + unit |

> Config at `/etc/eami/agent.yaml` is **not** removed on uninstall — preserved for reinstalls.
> To also remove config: `sudo rm -rf /etc/eami`

### Build locally

**Prerequisites:** [nfpm v2](https://nfpm.goreleaser.com) (`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`), Go binary for linux/amd64

```bash
# 1. Build the binary
cd eami-agent
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -ldflags='-w -s' -o eami-agent-linux-amd64 ./cmd/agent/

# 2. Copy binary to installer directory
cp eami-agent-linux-amd64 installer/linux/

# 3. Build packages
cd installer/linux
VERSION=1.0.0 nfpm package --config nfpm.yaml --packager deb --target dist/
VERSION=1.0.0 nfpm package --config nfpm.yaml --packager rpm --target dist/
```

Output: `dist/eami-agent_1.0.0_amd64.deb` and `dist/eami-agent-1.0.0.x86_64.rpm`

### Install — Debian / Ubuntu

```bash
# Install with config from env vars (postinstall script reads these)
sudo EAMI_COLLECTOR_URL=https://collector.corp.internal:8888 \
     EAMI_COLLECTOR_API_KEY=eami_k_your_key_here \
     dpkg -i eami-agent_1.0.0_amd64.deb

# Verify service started
systemctl status eami-agent
journalctl -u eami-agent -f
```

### Install — RHEL / Rocky / Fedora

```bash
sudo EAMI_COLLECTOR_URL=https://collector.corp.internal:8888 \
     EAMI_COLLECTOR_API_KEY=eami_k_your_key_here \
     rpm -i eami-agent-1.0.0.x86_64.rpm

systemctl status eami-agent
```

### Ansible deployment

```yaml
- name: Install EAMI agent
  ansible.builtin.package:
    name: "{{ lookup('fileglob', 'dist/eami-agent*.deb') | first }}"
    state: present
  environment:
    EAMI_COLLECTOR_URL: "https://collector.corp.internal:8888"
    EAMI_COLLECTOR_API_KEY: "{{ eami_api_key }}"
```

### Uninstall

```bash
# Debian/Ubuntu (config preserved)
sudo dpkg -r eami-agent

# RHEL/Rocky (config preserved)
sudo rpm -e eami-agent

# Also remove config
sudo rm -rf /etc/eami
```

### Troubleshooting

| Symptom | Check |
|---|---|
| Service fails to start | `journalctl -u eami-agent -n 50` |
| Config not written | Re-install with `EAMI_COLLECTOR_URL` and `EAMI_COLLECTOR_API_KEY` set |
| Service keeps restarting | Check for binary crash in logs; verify config is valid YAML |
| `rpm -i` fails on Ubuntu | Use `.deb` package; `.rpm` is for RHEL/Fedora/Rocky |

---

## CI/CD

The GitHub Actions workflow at `.github/workflows/build.yml` builds all installers automatically.

| Trigger | Output |
|---|---|
| Push to `main` | All packages with version `0.0.0-{sha}`, uploaded as workflow artifacts |
| Git tag `v*` | All packages with version from the tag (e.g., `v1.2.3`), attached to GitHub Release |

### Artifacts produced per release

| Artifact | Job |
|---|---|
| `eami-agent-{v}-windows-amd64.msi` | `build-msi` (windows-latest) |
| `eami-agent-{v}-darwin-amd64.pkg` | `build-pkg` (macos-latest) |
| `eami-agent-{v}-darwin-arm64.pkg` | `build-pkg` (macos-latest) |
| `eami-agent_{v}_amd64.deb` | `build-linux-packages` (ubuntu-latest) |
| `eami-agent-{v}.x86_64.rpm` | `build-linux-packages` (ubuntu-latest) |

Download from **Actions** tab → latest run → Artifacts, or from the **Releases** page for tagged versions.
