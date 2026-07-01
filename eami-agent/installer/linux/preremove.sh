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
