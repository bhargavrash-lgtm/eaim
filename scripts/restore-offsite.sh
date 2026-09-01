#!/bin/sh
# restore-offsite.sh — Downloads and decrypts a backup produced by
# backup-offsite.sh, producing a plain .dump file. Run this FIRST, then
# hand its output to the existing, unmodified scripts/restore-db.sh — this
# script never talks to Postgres itself, it only recovers the plaintext
# dump from the offsite copy. See RECOVERY.md for the full procedure.
#
# POSIX sh (not bash) — same runtime as backup-db.sh/restore-db.sh.
#
# Usage: restore-offsite.sh <remote-path-to-.age-file> <age-identity-file> <output-dump-path>
#   remote-path-to-.age-file — full rclone remote:path to the encrypted
#                              backup, e.g.
#                              "eami-offsite:my-bucket/eami-backups/eami_20260901T030000Z.dump.age"
#   age-identity-file        — path to the customer's age PRIVATE key
#                              (identity) file. This never lives inside
#                              any EAMI container/image by design (see
#                              backup-offsite.sh) — supply it from wherever
#                              the customer actually keeps it (a USB key,
#                              a secrets manager, etc.) at restore time
#                              only.
#   output-dump-path         — where to write the decrypted plaintext
#                              .dump file, e.g. /backups/from-offsite.dump
#
# Required env: OFFSITE_RCLONE_CONFIG — path to the rclone config file
# holding the customer's own remote credentials (default
# /rclone/rclone.conf, same contract as backup-offsite.sh).

set -eu

log() {
    level="$1"
    shift
    printf '%s %s restore-offsite: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$*"
}

REMOTE_PATH="${1:-}"
IDENTITY_FILE="${2:-}"
OUTPUT_PATH="${3:-}"

if [ -z "$REMOTE_PATH" ] || [ -z "$IDENTITY_FILE" ] || [ -z "$OUTPUT_PATH" ]; then
    log ERROR "usage: restore-offsite.sh <remote-path-to-.age-file> <age-identity-file> <output-dump-path>" >&2
    exit 1
fi

if [ ! -f "$IDENTITY_FILE" ]; then
    log ERROR "age identity file not found: ${IDENTITY_FILE}" >&2
    exit 1
fi

OFFSITE_RCLONE_CONFIG="${OFFSITE_RCLONE_CONFIG:-/rclone/rclone.conf}"
if [ ! -s "$OFFSITE_RCLONE_CONFIG" ]; then
    log ERROR "rclone config not found or empty: ${OFFSITE_RCLONE_CONFIG}" >&2
    exit 1
fi

TMP_ENC="$(mktemp /tmp/eami-restore-offsite.XXXXXX).age"

log INFO "downloading ${REMOTE_PATH} ..."
if ! rclone copyto "$REMOTE_PATH" "$TMP_ENC" --config "$OFFSITE_RCLONE_CONFIG"; then
    STATUS=$?
    rm -f "$TMP_ENC"
    log ERROR "rclone download FAILED (exit ${STATUS})" >&2
    exit "$STATUS"
fi

log INFO "decrypting -> ${OUTPUT_PATH} ..."
if ! age -d -i "$IDENTITY_FILE" -o "$OUTPUT_PATH" "$TMP_ENC"; then
    STATUS=$?
    rm -f "$TMP_ENC" "$OUTPUT_PATH"
    log ERROR "age decryption FAILED (exit ${STATUS}) — wrong identity file, or the file is corrupt/not a real age archive" >&2
    exit "$STATUS"
fi

rm -f "$TMP_ENC"
log INFO "offsite backup recovered -> ${OUTPUT_PATH}. Now run: sh /scripts/restore-db.sh ${OUTPUT_PATH}"
