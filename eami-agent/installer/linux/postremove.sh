#!/bin/bash
# postremove.sh — runs as root after the .deb or .rpm payload is removed
#
# Called automatically by:
#   dpkg (as the maintainer postrm script): $1 is remove | purge | upgrade | ...
#   rpm  (as the %postun scriptlet):        $1 is 0 on erase, 1+ on upgrade
#
# Deletes the agent's saved remote config (B-293) only when the package is
# being purged (deb) or erased (rpm; rpm has no separate purge, so an erase
# removes it, unlike /etc/eami/agent.yaml). A plain `dpkg -r` keeps it, and
# an upgrade never touches it.
# The path is a literal: nothing here can expand to another directory.

set -e

case "${1:-}" in
  purge|0)
    rm -rf /var/lib/eami-agent
    echo "eami-agent: removed saved remote config (/var/lib/eami-agent)"
    ;;
esac

exit 0
