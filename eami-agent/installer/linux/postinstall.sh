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

# ── Enable and start the systemd service ─────────────────────────────────────
# Reload unit files so systemd picks up the newly installed .service file
systemctl daemon-reload

# Enable: create the WantedBy symlink so the service starts on next boot
# --now:  also start it immediately without a separate systemctl start
systemctl enable --now eami-agent

echo "eami-agent: service enabled and started"
echo "  Status:  systemctl status eami-agent"
echo "  Logs:    journalctl -u eami-agent -f"
