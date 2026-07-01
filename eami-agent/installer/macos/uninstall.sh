#!/usr/bin/env bash
# =============================================================================
# uninstall.sh — Removes the EAMI Agent from macOS
# =============================================================================
#
# Must be run as root:
#   sudo bash /usr/local/share/eami/uninstall.sh
#
# What this removes:
#   - launchd daemon (stopped and unloaded)
#   - /Library/LaunchDaemons/io.eami.agent.plist
#   - /usr/local/bin/eami-agent
#   - /etc/eami/  (entire directory, including agent.yaml)
#   - /var/log/eami-agent.log  (optional — skipped if -k flag is set)
#
# Usage:
#   sudo ./uninstall.sh         Remove everything including logs
#   sudo ./uninstall.sh -k      Keep /var/log/eami-agent.log
# =============================================================================

set -euo pipefail

KEEP_LOGS=0
if [[ "${1:-}" == "-k" ]]; then
    KEEP_LOGS=1
fi

# Require root
if [[ "$EUID" -ne 0 ]]; then
    echo "ERROR: This script must be run as root (sudo ./uninstall.sh)"
    exit 1
fi

PLIST="/Library/LaunchDaemons/io.eami.agent.plist"
BINARY="/usr/local/bin/eami-agent"
CONFIG_DIR="/etc/eami"
LOG_FILE="/var/log/eami-agent.log"

echo "Uninstalling EAMI Agent..."

# ── Stop and unload the launchd daemon ───────────────────────────────────────
if [[ -f "$PLIST" ]]; then
    echo "  Stopping launchd service..."
    launchctl unload "$PLIST" 2>/dev/null || true
    rm -f "$PLIST"
    echo "  Removed: ${PLIST}"
else
    echo "  Plist not found (already removed): ${PLIST}"
fi

# ── Remove binary ─────────────────────────────────────────────────────────────
if [[ -f "$BINARY" ]]; then
    rm -f "$BINARY"
    echo "  Removed: ${BINARY}"
else
    echo "  Binary not found (already removed): ${BINARY}"
fi

# ── Remove config directory ───────────────────────────────────────────────────
if [[ -d "$CONFIG_DIR" ]]; then
    rm -rf "$CONFIG_DIR"
    echo "  Removed: ${CONFIG_DIR}"
else
    echo "  Config dir not found (already removed): ${CONFIG_DIR}"
fi

# ── Remove log file (unless -k) ───────────────────────────────────────────────
if [[ -f "$LOG_FILE" ]]; then
    if [[ "$KEEP_LOGS" -eq 1 ]]; then
        echo "  Keeping log file (pass no flags to remove): ${LOG_FILE}"
    else
        rm -f "$LOG_FILE"
        echo "  Removed: ${LOG_FILE}"
    fi
fi

# ── Forget package receipt (allows clean reinstall) ───────────────────────────
if pkgutil --pkg-info io.eami.agent &>/dev/null 2>&1; then
    pkgutil --forget io.eami.agent 2>/dev/null || true
    echo "  Forgot package receipt: io.eami.agent"
fi

echo ""
echo "EAMI Agent uninstalled."
