#!/bin/bash
# posttrans.sh — rpm only (nfpm.yaml rpm.scripts.posttrans): runs as root
# once, after the whole rpm transaction. dpkg has no equivalent and doesn't
# need one (see below).
#
# Why this exists (B-273, found live on AlmaLinux 9): on `rpm -U`, the OLD
# package's %preun (preremove.sh) runs AFTER this package's %post
# (postinstall.sh). Every preremove.sh shipped before B-273 stops and
# disables the service and deletes the native-messaging registration, so
# every rpm upgrade ended with the agent stopped, disabled and unregistered.
# %posttrans is the only scriptlet that runs after the old %preun, so it's
# the only place a new package can repair upgrades from versions already
# deployed. dpkg runs the old prerm BEFORE the new postinst, so a deb upgrade
# never had this problem.
#
# On a fresh install this repeats what postinstall.sh just did. Every step is
# idempotent; the restart only costs one extra scan.
#
# Config (/etc/eami/agent.yaml) is deliberately not touched here: the old
# %preun never removes it, and postinstall.sh owns it.

set -e

# Native-messaging registration: the launcher and manifests must match
# postinstall.sh's "Register the native-messaging host" section. Keep the
# two in sync; that section explains the launcher and the extension ID.
ln -f /usr/bin/eami-agent /usr/bin/eami-agent-nmhost

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

# enable works without a running systemd (it only creates symlinks), so it
# runs unconditionally. daemon-reload and restart need a live systemd, so
# they are skipped in image builds, chroots and containers without one (the
# same /run/systemd/system test Fedora's %systemd_post macros use). A failed
# %posttrans can't roll the transaction back, so a failed restart is a loud
# warning rather than a scriptlet error.
#
# Restart, not start: after an upgrade from a package whose %preun no longer
# stops the service, the old process is still running the old binary. A
# restart puts it on the new binary and unit either way.
systemctl enable eami-agent
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  if systemctl restart eami-agent; then
    echo "eami-agent: rpm transaction complete -- service enabled and (re)started, native-messaging registration in place"
  else
    echo "eami-agent: WARNING -- service enabled but restart failed; check: systemctl status eami-agent" >&2
  fi
else
  echo "eami-agent: rpm transaction complete -- service enabled (systemd not running here, so not started), native-messaging registration in place"
fi
