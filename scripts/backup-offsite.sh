#!/bin/sh
# backup-offsite.sh — Optional final step, invoked by backup-db.sh after a
# local backup completes: encrypts it with a customer-supplied age public
# key, then transports the encrypted file to a customer-configured remote
# via rclone. See RECOVERY.md for setup and the offsite-restore procedure
# (scripts/restore-offsite.sh).
#
# POSIX sh (not bash) — same runtime as backup-db.sh.
#
# Disabled by design, not by a separate on/off flag: this script no-ops
# (exit 0, one INFO line) unless ALL THREE of OFFSITE_AGE_RECIPIENT,
# OFFSITE_RCLONE_REMOTE, and a real file at OFFSITE_RCLONE_CONFIG are
# present. A customer who hasn't configured all three sees zero behavior
# change from B-029/B-143 -- there is no separate boolean to fall out of
# sync with the actual gating condition.
#
# Encryption happens BEFORE any network access is attempted -- the
# unencrypted dump never touches the network, and EAMI's own process only
# ever holds the customer's *public* key (OFFSITE_AGE_RECIPIENT), never
# their private key/identity, so it has no ability to read the offsite
# copy's contents either.
#
# Required argument: path to the already-produced local dump file (as
# backup-db.sh just wrote it).
# Required env (only checked/used if all three are present -- see above):
#   OFFSITE_AGE_RECIPIENT — customer's age public key (e.g. age1...)
#   OFFSITE_RCLONE_REMOTE — rclone remote:path spec, e.g.
#                            "eami-offsite:my-bucket/eami-backups"
#   OFFSITE_RCLONE_CONFIG — path to the customer-supplied rclone config
#                            file inside this container (default
#                            /rclone/rclone.conf, bind-mounted read-only —
#                            see docker-compose.yml). Holds the customer's
#                            own remote credentials; never an EAMI-held
#                            secret.

set -eu

log() {
    level="$1"
    shift
    printf '%s %s backup-offsite: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$*"
}

DUMP_FILE="${1:-}"
if [ -z "$DUMP_FILE" ]; then
    log ERROR "usage: backup-offsite.sh <path-to-local-dump-file>" >&2
    exit 1
fi

OFFSITE_AGE_RECIPIENT="${OFFSITE_AGE_RECIPIENT:-}"
OFFSITE_RCLONE_REMOTE="${OFFSITE_RCLONE_REMOTE:-}"
OFFSITE_RCLONE_CONFIG="${OFFSITE_RCLONE_CONFIG:-/rclone/rclone.conf}"

if [ -z "$OFFSITE_AGE_RECIPIENT" ] || [ -z "$OFFSITE_RCLONE_REMOTE" ] || [ ! -s "$OFFSITE_RCLONE_CONFIG" ]; then
    log INFO "offsite backup not configured (OFFSITE_AGE_RECIPIENT/OFFSITE_RCLONE_REMOTE/${OFFSITE_RCLONE_CONFIG} not all present) — skipping, local backup only"
    exit 0
fi

if [ ! -f "$DUMP_FILE" ]; then
    log ERROR "dump file not found: ${DUMP_FILE}" >&2
    exit 1
fi

ENC_FILE="${DUMP_FILE}.age"

log INFO "encrypting ${DUMP_FILE} for offsite transport..."
if ! age -r "$OFFSITE_AGE_RECIPIENT" -o "$ENC_FILE" "$DUMP_FILE"; then
    STATUS=$?
    rm -f "$ENC_FILE"
    log ERROR "age encryption FAILED (exit ${STATUS}) — offsite backup NOT sent, local backup is still intact" >&2
    exit "$STATUS"
fi

log INFO "uploading $(basename "$ENC_FILE") -> ${OFFSITE_RCLONE_REMOTE} ..."
if ! rclone copyto "$ENC_FILE" "${OFFSITE_RCLONE_REMOTE%/}/$(basename "$ENC_FILE")" --config "$OFFSITE_RCLONE_CONFIG"; then
    STATUS=$?
    log ERROR "rclone upload FAILED (exit ${STATUS}) — encrypted file left at ${ENC_FILE} for a manual retry; local backup is still intact" >&2
    exit "$STATUS"
fi

rm -f "$ENC_FILE"
log INFO "offsite backup complete -> ${OFFSITE_RCLONE_REMOTE%/}/$(basename "$ENC_FILE")"
