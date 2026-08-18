#!/bin/bash
# postinstall.sh — runs as root after the .deb or .rpm payload is extracted
#
# Called automatically by:
#   dpkg (as the maintainer postinst script)
#   rpm  (as the %post scriptlet)
#
# Config resolution:
#   EAMI_COLLECTOR_URL           — URL of the on-prem collector (required)
#   EAMI_COLLECTOR_API_KEY       — API key for this endpoint (required)
#   EAMI_COLLECTOR_CA_CERT_PATH  — path to a CA cert file (B-072, optional)
#
# Set these before installing:
#   sudo EAMI_COLLECTOR_URL=https://collector.corp.com:8888 \
#        EAMI_COLLECTOR_API_KEY=eami_k_abc123 \
#        dpkg -i eami-agent-1.0.0-linux-amd64.deb
#
# Or for rpm:
#   sudo EAMI_COLLECTOR_URL=https://collector.corp.com:8888 \
#        EAMI_COLLECTOR_API_KEY=eami_k_abc123 \
#        rpm -i eami-agent-1.0.0-linux-amd64.rpm
#
# EAMI_COLLECTOR_CA_CERT_PATH (B-072, optional): only needed when the
# collector's TLS certificate is the appliance's own default self-signed
# one (eami-proxy's Caddy, B-071/B-072) rather than a real, publicly-
# trusted certificate. Points at a CA cert file already staged on this
# machine by the deployment tool (matches the Windows MSI's
# COLLECTOR_CA_CERT_PATH property, same reasoning: a file path, not inline
# PEM content, since embedding multi-line content through env vars/sudo
# has its own real quoting fragility). See appliance/README.md's "TLS
# certificates for eami-collector" section for how to extract and stage it.
#
# SECURITY (a real finding from this brief's own mandatory security
# review, fixed before shipping): stage this file somewhere NOT
# world-writable -- e.g. wherever your deployment tool (Ansible, SCCM,
# Intune) already places other install-time payload files, never a shared
# directory like /tmp. This script independently verifies the source
# file's ownership/permissions before trusting it (see below) and refuses
# to proceed if they look wrong, but a safe staging location is still the
# real first line of defense.
#   sudo EAMI_COLLECTOR_URL=https://collector.corp.com:8888 \
#        EAMI_COLLECTOR_API_KEY=eami_k_abc123 \
#        EAMI_COLLECTOR_CA_CERT_PATH=/root/eami-collector-ca.pem \
#        dpkg -i eami-agent-1.0.0-linux-amd64.deb

set -e

COLLECTOR_URL="${EAMI_COLLECTOR_URL:-http://localhost:8888}"
COLLECTOR_API_KEY="${EAMI_COLLECTOR_API_KEY:-REPLACE_WITH_YOUR_API_KEY}"
COLLECTOR_CA_CERT_PATH="${EAMI_COLLECTOR_CA_CERT_PATH:-}"

# ── Write agent config ────────────────────────────────────────────────────────
mkdir -p /etc/eami
chmod 755 /etc/eami

# If a CA cert was supplied, copy it into /etc/eami so it survives even if
# the staged source path (e.g. a temp download location) is cleaned up
# later -- agent.yaml then references this stable copy, not the original.
#
# SECURITY: verify ownership/permissions on the source file before
# trusting it (a real finding from this brief's own mandatory security
# review) -- this script runs as root, and without this check, a local
# unprivileged attacker who can write to whatever path an admin documents
# (e.g. a shared directory like /tmp) could pre-plant their own CA there
# before the real install runs, and this script would silently copy and
# permanently trust it. Requiring the source to be root-owned and not
# world-writable means an attacker would need root already to plant it --
# at which point they don't need this attack at all.
CA_CERT_YAML_PATH=""
if [ -n "$COLLECTOR_CA_CERT_PATH" ]; then
    if [ ! -f "$COLLECTOR_CA_CERT_PATH" ]; then
        echo "eami-agent: WARNING -- EAMI_COLLECTOR_CA_CERT_PATH=$COLLECTOR_CA_CERT_PATH does not exist, skipping (agent will use the OS default trust store)" >&2
    else
        # GNU stat (Linux) uses -c; BSD stat (macOS, in case this script is
        # ever reused there) uses -f -- try GNU first, fall back to BSD.
        owner_uid="$(stat -c '%u' "$COLLECTOR_CA_CERT_PATH" 2>/dev/null || stat -f '%u' "$COLLECTOR_CA_CERT_PATH")"
        perm="$(stat -c '%a' "$COLLECTOR_CA_CERT_PATH" 2>/dev/null || stat -f '%Lp' "$COLLECTOR_CA_CERT_PATH")"
        # Bitwise, not numeric-magnitude, comparison -- a mode like 624
        # (group=write-only, no read) is numerically LESS than 644 but is
        # still group-writable; only checking "$perm -gt 644" would have
        # missed that. Testing bit 2 (write) on each of the group/other
        # octal digits individually is correct regardless of what the
        # read/execute bits are set to.
        group_digit="${perm: -2:1}"
        other_digit="${perm: -1:1}"
        if [ "$owner_uid" != "0" ]; then
            echo "eami-agent: ERROR -- EAMI_COLLECTOR_CA_CERT_PATH=$COLLECTOR_CA_CERT_PATH is not owned by root; refusing to trust it (stage the CA cert from a location only root can write to, not a shared directory like /tmp)" >&2
        elif [ $(( group_digit & 2 )) -ne 0 ] || [ $(( other_digit & 2 )) -ne 0 ]; then
            echo "eami-agent: ERROR -- EAMI_COLLECTOR_CA_CERT_PATH=$COLLECTOR_CA_CERT_PATH is group- or world-writable (mode $perm); refusing to trust it" >&2
        else
            cp "$COLLECTOR_CA_CERT_PATH" /etc/eami/collector-ca.pem
            chmod 644 /etc/eami/collector-ca.pem
            CA_CERT_YAML_PATH="/etc/eami/collector-ca.pem"
            echo "eami-agent: collector CA cert copied to /etc/eami/collector-ca.pem"
        fi
    fi
fi

cat > /etc/eami/agent.yaml <<EOF
agent:
  id: "$(hostname)"
  interval_secs: 300
  log_level: info

collector:
  url: "${COLLECTOR_URL}"
  api_key: "${COLLECTOR_API_KEY}"
  ca_cert_path: "${CA_CERT_YAML_PATH}"
  timeout_seconds: 30

detection:
  model_file_scan_paths: []
  model_file_size_mb: 100
EOF

chmod 600 /etc/eami/agent.yaml
echo "eami-agent: config written to /etc/eami/agent.yaml"

# ── Register the native-messaging host (paste-detection groundwork) ─────────
# No registry step on Linux (that's Windows-only, see
# internal/nmregister) -- Chrome/Chromium discover native messaging hosts
# by finding a manifest JSON file in a well-known, system-wide directory.
# Both Chrome's and Chromium's directories are written since either might
# be the browser actually installed on this machine; an unused manifest
# for the absent one is harmless.
#
# The manifest's "path" points at a hard-linked launcher copy of the real
# binary, NOT /usr/bin/eami-agent directly -- Chrome/Chromium's manifest
# schema has no field to pass --native-messaging-host as an argument, so
# the browser would otherwise launch the real binary in ordinary poll-loop
# mode instead of host mode (a real bug caught by this task's code review
# before any real extension existed to catch it in practice). eami-agent
# detects invocation under this launcher's name and behaves as if
# --native-messaging-host had been passed explicitly -- see
# nmregister.LauncherBaseName's doc comment. The hard link means the
# launcher is always byte-identical to /usr/bin/eami-agent (same inode),
# so a package upgrade that replaces that file in place needs no separate
# re-link step; ln -f handles the (normal, expected) case where a prior
# install already created it.
ln -f /usr/bin/eami-agent /usr/bin/eami-agent-nmhost

# allowed_origins is B1's real, stable extension ID -- eami-browser-extension's
# manifest.json pins this via its "key" field; see
# eami-browser-extension/README.md for the derivation.
NM_MANIFEST_JSON='{
  "name": "com.eami.agent",
  "description": "EAMI Agent native messaging host (real-time paste-event relay)",
  "path": "/usr/bin/eami-agent-nmhost",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://ngmdfnecljeoleiancdedbmhjdihaoaa/"
  ]
}'

for NM_DIR in /etc/opt/chrome/native-messaging-hosts /etc/chromium/native-messaging-hosts; do
  mkdir -p "$NM_DIR"
  echo "$NM_MANIFEST_JSON" > "$NM_DIR/com.eami.agent.json"
  chmod 644 "$NM_DIR/com.eami.agent.json"
done
echo "eami-agent: native-messaging manifest registered for Chrome and Chromium"

# ── Enable and start the systemd service ─────────────────────────────────────
# Reload unit files so systemd picks up the newly installed .service file
systemctl daemon-reload

# Enable: create the WantedBy symlink so the service starts on next boot
# --now:  also start it immediately without a separate systemctl start
systemctl enable --now eami-agent

echo "eami-agent: service enabled and started"
echo "  Status:  systemctl status eami-agent"
echo "  Logs:    journalctl -u eami-agent -f"
