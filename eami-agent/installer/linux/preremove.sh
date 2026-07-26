#!/bin/bash
# preremove.sh — runs as root before the .deb or .rpm payload is removed
#
# Called automatically by:
#   dpkg (as the maintainer prerm script)
#   rpm  (as the %preun scriptlet)
#
# Stops and disables the service so the binary can be safely deleted.
# Does NOT remove /etc/eami/agent.yaml — config is preserved across reinstalls.
# To remove config, run: sudo rm -rf /etc/eami

set -e

echo "eami-agent: stopping service before removal..."

# Stop the running service (ignore errors if it is already stopped)
systemctl stop eami-agent 2>/dev/null || true

# Disable: remove the WantedBy symlink so the service does not start on boot
systemctl disable eami-agent 2>/dev/null || true

# Reload unit files after disabling
systemctl daemon-reload 2>/dev/null || true

echo "eami-agent: service stopped and disabled"

# ── Remove native-messaging registration ─────────────────────────────────────
# Unlike /etc/eami/agent.yaml (deliberately preserved across
# reinstalls, see above), the launcher hard link and manifest files are
# not tracked in nfpm.yaml's contents: list -- dpkg/rpm won't remove them
# on their own. Removing them here mirrors the Windows installer's
# nmregister.Uninstall, which does clean these up (closing an asymmetry
# flagged by code review).
rm -f /usr/bin/eami-agent-nmhost
rm -f /etc/opt/chrome/native-messaging-hosts/com.eami.agent.json
rm -f /etc/chromium/native-messaging-hosts/com.eami.agent.json
echo "eami-agent: native-messaging registration removed"
