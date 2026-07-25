#!/bin/sh
# backup-entrypoint.sh — Entrypoint for the eami-backup container.
#
# Sets up a real cron schedule (Alpine's busybox crond) that runs
# backup-db.sh, then runs crond in the foreground as the container's main
# process. Two dockerized-cron gotchas handled explicitly (verified live,
# not assumed):
#   1. crond does not pass the container's environment through to jobs —
#      the env vars backup-db.sh needs are captured into a sourceable file
#      at startup and the crontab line sources it before running the job.
#   2. crond's job output does not reach `docker compose logs` by default —
#      the crontab line redirects the job's stdout/stderr into the
#      container's own PID 1 file descriptors (/proc/1/fd/1, /proc/1/fd/2),
#      which is what `docker logs` actually reads.
#
# POSIX sh — runs under Alpine's ash, no bash in this image.

set -eu

BACKUP_SCHEDULE="${BACKUP_SCHEDULE:-0 3 * * *}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"

# sq_escape POSIX-escapes $1 for safe embedding inside single quotes in a
# generated shell file (handles a literal ' in the value, e.g. a
# hand-typed POSTGRES_PASSWORD rather than one of setup.sh's generated
# alphanumeric-only secrets).
sq_escape() {
    printf '%s' "$1" | sed "s/'/'\\\\''/g"
}

# A cron schedule must have exactly 5 whitespace-separated fields; a
# malformed value would otherwise degrade silently to "only the one-time
# initial backup below ever runs, cron never fires again" with no clear
# signal. Falls back to the documented default rather than failing the
# whole container over a typo'd schedule.
FIELD_COUNT=$(echo "$BACKUP_SCHEDULE" | wc -w)
if [ "$FIELD_COUNT" -ne 5 ]; then
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) ERROR eami-backup: BACKUP_SCHEDULE '${BACKUP_SCHEDULE}' does not have 5 fields — falling back to default '0 3 * * *'" >&2
    BACKUP_SCHEDULE="0 3 * * *"
fi

mkdir -p "$BACKUP_DIR"

# Capture the env vars backup-db.sh needs into a file cron jobs can source.
ENV_FILE="/etc/eami-backup-env.sh"
{
    printf "export DATABASE_URL='%s'\n" "$(sq_escape "${DATABASE_URL:-}")"
    printf "export BACKUP_DIR='%s'\n" "$(sq_escape "${BACKUP_DIR}")"
    printf "export BACKUP_RETENTION_COUNT='%s'\n" "$(sq_escape "${BACKUP_RETENTION_COUNT:-14}")"
} > "$ENV_FILE"
chmod 600 "$ENV_FILE"

# Write the crontab. sh (not the bind-mounted file's own exec bit, which
# bind mounts from the host can't be relied on) runs backup-db.sh directly.
mkdir -p /etc/crontabs
echo "${BACKUP_SCHEDULE} . ${ENV_FILE} && sh /scripts/backup-db.sh >> /proc/1/fd/1 2>> /proc/1/fd/2" > /etc/crontabs/root

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) INFO eami-backup: schedule installed (${BACKUP_SCHEDULE}), running an initial backup now"

# Run an initial backup immediately so a fresh deployment has a baseline
# backup within seconds, rather than waiting up to one full schedule
# interval for the first one. Failure here is logged (by backup-db.sh
# itself) but must not prevent the scheduler from starting.
. "$ENV_FILE"
sh /scripts/backup-db.sh || echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) WARN eami-backup: initial backup failed, scheduler will retry on schedule"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) INFO eami-backup: starting cron scheduler"
exec crond -f -l 2
