#!/bin/sh
# restore-db.sh — Restores a pg_dump backup (produced by backup-db.sh) into
# DATABASE_URL, then automatically repairs the one known class of pg_restore
# failure this schema hits: FK constraints on TimescaleDB hypertables.
#
# POSIX sh (not bash): runs inside the eami-backup container under Alpine's
# busybox ash, invoked manually during a real restore
# (docker compose exec eami-backup /scripts/restore-db.sh /backups/<file>).
#
# Required: DATABASE_URL (postgresql://user:pass@host:port/db) — same
# "must be set, no guessable fallback" contract as backup-db.sh.
# Required argument: path to the dump file to restore (as seen inside this
# container, e.g. /backups/eami_20260901T142124Z.dump).
#
# WHY THIS SCRIPT EXISTS (see RECOVERY.md and B-143/B-134 for the full
# investigation): pg_restore always generates single-table ALTER TABLE
# statements qualified with ONLY. TimescaleDB hypertables reject that
# qualifier for ADD CONSTRAINT the same way they already reject it for
# --clean's DROP CONSTRAINT (the reason RECOVERY.md tells you not to use
# --clean at all). Row data is unaffected -- pg_restore's COPY statements
# run before the failing ALTERs -- but the affected FK constraints are
# silently absent unless something re-applies them without ONLY. Doing
# that by hand, correctly, under real disaster-recovery stress is exactly
# the kind of step a documented-but-manual procedure gets skipped or
# fumbled on -- hence a script, not a paragraph in RECOVERY.md.
#
# The repair is GENERIC, not a hardcoded list of today's known tables: it
# parses pg_restore's own stderr for the exact error shape TimescaleDB
# produces (an error message mentioning "hypertable", paired with a failed
# "ALTER TABLE ONLY ... ADD CONSTRAINT ..." statement), strips ONLY, and
# re-applies. Any pg_restore error that does NOT match that shape is left
# strictly alone and reported -- this script fails loudly on anything it
# doesn't specifically recognize rather than guessing. A real disaster
# recovery restore is the wrong place for a tool that silently declares
# success on a partial result.

set -eu

log() {
    level="$1"
    shift
    printf '%s %s restore-db: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$*"
}

if [ -z "${DATABASE_URL:-}" ]; then
    log ERROR "DATABASE_URL must be set — see .env.example. Refusing to run." >&2
    exit 1
fi

DUMP_FILE="${1:-}"
if [ -z "$DUMP_FILE" ]; then
    log ERROR "usage: restore-db.sh <path-to-dump-file> (e.g. /backups/eami_20260901T142124Z.dump)" >&2
    exit 1
fi
if [ ! -f "$DUMP_FILE" ]; then
    log ERROR "dump file not found: ${DUMP_FILE}" >&2
    exit 1
fi

WORKDIR="$(mktemp -d /tmp/eami-restore.XXXXXX)"
# Not cleaned up on failure, deliberately -- see the final ERROR path below,
# which points the operator at $WORKDIR for the raw diagnostics.

STDERR_FILE="${WORKDIR}/restore_stderr.txt"
PARSE_AWK="${WORKDIR}/parse-restore-errors.awk"
FIXED_SQL="${WORKDIR}/fixed_constraints.sql"
UNHANDLED_TXT="${WORKDIR}/unhandled_errors.txt"

# ─── the parser: separates "hypertable ONLY-constraint" failures (safe to
# retry without ONLY) from every other kind of pg_restore error (not safe
# to guess about -- reported and left for a human) ─────────────────────────
cat > "$PARSE_AWK" <<'AWK_EOF'
BEGIN {
    pending_err = ""
    pending_cmd = ""
    collecting = 0
    fixable_count = 0
    unhandled_count = 0
}

function flush() {
    if (pending_err == "") {
        return
    }
    if (pending_cmd != "" && pending_cmd ~ /^ALTER TABLE ONLY / && pending_cmd ~ /ADD CONSTRAINT/ && pending_err ~ /[Hh]ypertable/) {
        fixed = pending_cmd
        sub(/^ALTER TABLE ONLY /, "ALTER TABLE ", fixed)
        print fixed
        print ""
        fixable_count++
    } else {
        print "UNHANDLED pg_restore error (not an auto-fixable hypertable ONLY-constraint case):" > "/dev/stderr"
        print "  error:   " pending_err > "/dev/stderr"
        if (pending_cmd != "") {
            print "  command: " pending_cmd > "/dev/stderr"
        } else {
            print "  command: (no \"Command was:\" line found for this error)" > "/dev/stderr"
        }
        print "" > "/dev/stderr"
        unhandled_count++
    }
    pending_err = ""
    pending_cmd = ""
    collecting = 0
}

/^pg_restore: error: / {
    flush()
    pending_err = $0
    sub(/^pg_restore: error: /, "", pending_err)
    next
}

pending_err != "" && /^Command was: / {
    line = $0
    sub(/^Command was: /, "", line)
    pending_cmd = line
    collecting = 1
    if (pending_cmd ~ /;[ \t]*$/) {
        flush()
    }
    next
}

collecting == 1 {
    pending_cmd = pending_cmd "\n" $0
    if ($0 ~ /;[ \t]*$/) {
        flush()
    }
    next
}

END {
    flush()
    print "fixable=" fixable_count > (workdir "/fixable_count.txt")
    print "unhandled=" unhandled_count > (workdir "/unhandled_count.txt")
}
AWK_EOF

log INFO "wiping target schema before restore -> ${DATABASE_URL%%@*}@..."
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO CURRENT_USER;
"

log INFO "restoring ${DUMP_FILE} ..."
RESTORE_EXIT=0
pg_restore --no-owner --no-privileges -d "$DATABASE_URL" "$DUMP_FILE" 2> "$STDERR_FILE" || RESTORE_EXIT=$?

if [ "$RESTORE_EXIT" -eq 0 ] && [ ! -s "$STDERR_FILE" ]; then
    log INFO "pg_restore completed with zero errors — nothing to repair"
    rm -rf "$WORKDIR"
    log INFO "restore complete and verified clean"
    exit 0
fi

log INFO "pg_restore reported errors (exit ${RESTORE_EXIT}) — analyzing before deciding whether this is the known hypertable-constraint case"

awk -v workdir="$WORKDIR" -f "$PARSE_AWK" "$STDERR_FILE" > "$FIXED_SQL" 2> "$UNHANDLED_TXT"

FIXABLE_COUNT="$(cut -d= -f2 "${WORKDIR}/fixable_count.txt")"
UNHANDLED_COUNT="$(cut -d= -f2 "${WORKDIR}/unhandled_count.txt")"

if [ "$UNHANDLED_COUNT" -gt 0 ]; then
    log ERROR "restore produced ${UNHANDLED_COUNT} error(s) that are NOT the known hypertable-constraint case — refusing to guess a fix. Details:" >&2
    cat "$UNHANDLED_TXT" >&2
    log ERROR "raw pg_restore stderr and diagnostics left at ${WORKDIR} for investigation. Restore NOT complete — do not trust this database yet." >&2
    exit 1
fi

if [ "$FIXABLE_COUNT" -gt 0 ]; then
    log INFO "found ${FIXABLE_COUNT} constraint(s) that failed only because pg_restore qualified them with ONLY against a hypertable (see RECOVERY.md) — re-applying without ONLY"
    if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$FIXED_SQL" 2> "${WORKDIR}/repair_stderr.txt"; then
        log ERROR "automatic repair of ${FIXABLE_COUNT} constraint(s) FAILED — this is not the expected outcome for this known error class. Details:" >&2
        cat "${WORKDIR}/repair_stderr.txt" >&2
        log ERROR "raw diagnostics left at ${WORKDIR}. Restore NOT complete — do not trust this database yet." >&2
        exit 1
    fi
    log INFO "repaired ${FIXABLE_COUNT} constraint(s) successfully"
fi

rm -rf "$WORKDIR"
log INFO "restore complete — all pg_restore errors were the known hypertable-constraint case and have been repaired"
