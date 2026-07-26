#!/bin/bash
# postinstall.sh — runs as root after the .deb or .rpm payload is extracted
#
# Called automatically by:
#   dpkg (as the maintainer postinst script)
#   rpm  (as the %post scriptlet)
#
# Config resolution:
#   EAMI_COLLECTOR_URL      — URL of the on-prem collector (required)
#   EAMI_COLLECTOR_API_KEY  — API key for this endpoint (required)
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

set -e

COLLECTOR_URL="${EAMI_COLLECTOR_URL:-http://localhost:8888}"
COLLECTOR_API_KEY="${EAMI_COLLECTOR_API_KEY:-REPLACE_WITH_YOUR_API_KEY}"

# ── Write agent config ────────────────────────────────────────────────────────
mkdir -p /etc/eami
chmod 755 /etc/eami

cat > /etc/eami/agent.yaml <<EOF
agent:
  id: "$(hostname)"
  interval_secs: 300
  log_level: info

collector:
  url: "${COLLECTOR_URL}"
  api_key: "${COLLECTOR_API_KEY}"
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

# allowed_origins is a PLACEHOLDER -- there is no B1 (browser extension)
# yet, so this host cannot be invoked by any real extension until the
# real, published extension ID replaces PLACEHOLDER_EXTENSION_ID_REPLACE_AFTER_B1
# below (see installer/native-messaging/README.md).
NM_MANIFEST_JSON='{
  "name": "com.eami.agent",
  "description": "EAMI Agent native messaging host (real-time paste-event relay)",
  "path": "/usr/bin/eami-agent-nmhost",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://PLACEHOLDER_EXTENSION_ID_REPLACE_AFTER_B1/"
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
