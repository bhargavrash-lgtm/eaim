#!/bin/sh
# Fix volume ownership at runtime, then drop to the eami user.
# Docker named volumes are created as root:root; chown here ensures the
# non-root process can write to /app/data regardless of host volume state.
set -e

DATA_DIR="${COLLECTOR_DATA_DIR:-/app/data}"
mkdir -p "$DATA_DIR"
chown -R eami:eami "$DATA_DIR"

exec su-exec eami "$@"
