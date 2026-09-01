#!/bin/sh
# backup-db.sh — Runs pg_dump against DATABASE_URL and writes a timestamped,
# compressed custom-format backup into BACKUP_DIR, then prunes old backups
# beyond BACKUP_RETENTION_COUNT.
#
# POSIX sh (not bash): runs inside the eami-backup container under Alpine's
# busybox ash, invoked either by cron (scripts/backup-entrypoint.sh) or
# manually (docker compose exec eami-backup /scripts/backup-db.sh).
#
# Required: DATABASE_URL (postgresql://user:pass@host:port/db) — same
# "must be set, no guessable fallback" contract as seed-db.sh /
# create-audit-partition.sh (B-030).
# Optional: BACKUP_DIR (default /backups), BACKUP_RETENTION_COUNT (default 14).
#
# See RECOVERY.md for the restore procedure and what this backup does/does
# not cover.

set -eu

BACKUP_DIR="${BACKUP_DIR:-/backups}"
BACKUP_RETENTION_COUNT="${BACKUP_RETENTION_COUNT:-14}"

log() {
    level="$1"
    shift
    printf '%s %s backup-db: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$*"
}

if [ -z "${DATABASE_URL:-}" ]; then
    log ERROR "DATABASE_URL must be set — see .env.example. Refusing to run." >&2
    exit 1
fi

mkdir -p "$BACKUP_DIR"

# Clean up any stale .tmp file left behind by a previous run that was
# killed mid-dump (e.g. OOM) — these are never valid backups and would
# otherwise leak disk space forever (retention pruning below only globs
# *.dump, not *.dump.tmp). Safe to do unconditionally: this run's own
# .tmp file (created below) doesn't exist yet at this point.
find "$BACKUP_DIR" -maxdepth 1 -name '*.dump.tmp' -type f -delete 2>/dev/null || true

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_FILE="${BACKUP_DIR}/eami_${TIMESTAMP}.dump"
TMP_FILE="${OUT_FILE}.tmp"

log INFO "starting backup -> ${OUT_FILE}"

# --no-owner/--no-privileges only affect plain-text (-Fp) output per
# pg_dump's own docs -- for archive formats (-Fc here) they're no-ops at
# dump time and must instead be given to pg_restore, which RECOVERY.md's
# restore command already does. Not passed here to avoid implying they
# do something at this step.
if pg_dump -Fc -f "$TMP_FILE" "$DATABASE_URL"; then
    mv "$TMP_FILE" "$OUT_FILE"
    SIZE="$(du -h "$OUT_FILE" | cut -f1)"
    log INFO "backup complete -> ${OUT_FILE} (${SIZE})"
else
    STATUS=$?
    rm -f "$TMP_FILE"
    log ERROR "pg_dump FAILED (exit ${STATUS}) — no backup produced for this run" >&2
    exit "$STATUS"
fi

# Retention: keep the newest BACKUP_RETENTION_COUNT dumps (filenames are
# UTC-timestamped and lexicographically sortable), delete the rest.
COUNT="$(find "$BACKUP_DIR" -maxdepth 1 -name 'eami_*.dump' -type f | wc -l)"
if [ "$COUNT" -gt "$BACKUP_RETENTION_COUNT" ]; then
    EXCESS=$((COUNT - BACKUP_RETENTION_COUNT))
    find "$BACKUP_DIR" -maxdepth 1 -name 'eami_*.dump' -type f | sort | head -n "$EXCESS" | while IFS= read -r f; do
        log INFO "pruning old backup ${f}"
        rm -f "$f"
    done
fi

# Optional offsite transport (B-144) — no-ops by construction unless a
# customer has configured all of OFFSITE_AGE_RECIPIENT/
# OFFSITE_RCLONE_REMOTE/OFFSITE_RCLONE_CONFIG (see backup-offsite.sh for
# the exact gating condition). Runs after the local backup above is
# already complete and safe; its exit code is propagated below so a
# configured-but-failing offsite step is never silent, even though the
# local backup this run produced remains valid either way.
if [ -f "$(dirname "$0")/backup-offsite.sh" ]; then
    sh "$(dirname "$0")/backup-offsite.sh" "$OUT_FILE"
fi
