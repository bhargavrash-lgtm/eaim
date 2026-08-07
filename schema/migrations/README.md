# Legacy migration files — historical record, not runnable

The 10 files in this directory (`001_init.sql` through `010_gateway_tools_action_paths.sql`)
document how `schema/schema.sql` evolved over time, via an inline
`-- migration 00N` comment convention on each affected column. They were
**never** individually applied by any tool — `schema.sql` was the only
thing ever actually run (once, via Postgres's `docker-entrypoint-initdb.d`
mechanism, on a fresh database), and these files were hand-maintained
alongside it as a change log.

**Do not run these files against any database, individually or in
sequence.** Confirmed during B-051 (the real migration-runner project,
2026-08-07) that only 3 of the 10 — `005_token_usage.sql`,
`007_audit_partitions.sql`, and `008_paste_events.sql` — are actually
idempotency-guarded (`IF NOT EXISTS` throughout). The other 7, including
`001_init.sql` itself, use bare `CREATE TABLE`/`ALTER TABLE ADD COLUMN`/
`CREATE INDEX`/`CREATE TRIGGER` that will error (`already exists`) if run
against a database that already has schema.sql's cumulative state applied
— which is true of every real EAMI database today.

## Where the real migration mechanism lives now

`schema/migrations-v2/` — applied via [golang-migrate](https://github.com/golang-migrate/migrate)
(`scripts/migrate.sh`, or automatically at `docker compose up` via the
`migrate` service). `000001_baseline.up.sql` in that directory is the
verified-accurate cumulative result of everything these 10 legacy files
did — checked table-by-table against `schema.sql` before being written,
not assumed. Any new schema change from now on gets a new numbered file
in `migrations-v2/`, not appended here.

These files stay in place as historical documentation of the schema's
early evolution — not deleted, not renumbered into the new system.
