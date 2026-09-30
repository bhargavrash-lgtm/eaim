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

# rpm passes %preun the number of package instances left after this
# operation: 0 means erase, 1 or more means upgrade. On an rpm upgrade this
# is the OLD package's script, and it runs AFTER the new package's %post, so
# stopping, disabling and unregistering here would undo the new install
# (found live, B-273). Do nothing on an rpm upgrade; the new package's
# %posttrans (posttrans.sh) restarts the service. dpkg passes "upgrade" and
# runs prerm BEFORE the new postinst, which restarts everything, so the deb
# upgrade path keeps the full stop below.
case "${1:-}" in
  [1-9]*)
    echo "eami-agent: rpm upgrade -- leaving the service and native-messaging registration in place"
    exit 0
    ;;
esac

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
