# RECOVERY.md — Postgres Backup & Disaster Recovery (B-029)

This document covers EAMI's Postgres backup mechanism and the restore
procedure. It exists because, before B-029, there was **no backup
mechanism at all**: `docker compose down -v` or a lost/corrupted data disk
meant permanent loss of everything in Postgres — including `audit_log`,
the immutable audit trail that is this product's core value proposition.

`scripts/reseed.sql` is **not** a backup/restore tool — see
["reseed.sql vs. real restore"](#reseedsql-vs-real-restore) below.

## What this covers (v1)

- **Mechanism:** scheduled `pg_dump` (custom format, `-Fc`), full database
  — all schemas, tables, data, and extensions (`pgvector`, `TimescaleDB`,
  `pgcrypto`, `uuid-ossp`).
- **Schedule:** cron, via a dedicated `eami-backup` container (Alpine's
  busybox `crond`). Default `0 3 * * *` (daily, 03:00 UTC), configurable
  via `BACKUP_SCHEDULE` in `.env`.
- **Storage:** a local named Docker volume (`postgres_backups`), separate
  from `postgres_data` — so losing/recreating the Postgres data volume
  does not also destroy the backups. Mounted read-only into the `postgres`
  service itself, so restore commands can reference `/backups/<file>`
  directly with no manual file-copying between containers.
- **Retention:** the newest `BACKUP_RETENTION_COUNT` dumps are kept
  (default 14); older ones are pruned automatically after each run.
- **Failure visibility:** a failed backup logs a clear `ERROR` line and
  the script exits non-zero; no partial/corrupt file is ever left in
  place (writes to a `.tmp` file, only `mv`s it into place on success).
  Check with `docker compose logs eami-backup`.
- **Offsite transport (optional, B-144):** off by default — see
  ["Offsite backup"](#offsite-backup-b-144) below. When configured, each
  local backup is additionally encrypted with your own key and sent to a
  remote destination you control.

## What this does NOT cover (v1 — explicitly out of scope)

- **Point-in-time recovery / WAL archiving.** v1 is periodic full dumps
  only — you can restore to the moment of the last successful backup, not
  to an arbitrary point in time. Data written after the last backup and
  before a disaster is lost.
- **Automated/scripted restore.** Restore is a manual, documented
  procedure (below) — deliberately, so a human confirms which backup and
  which target database before anything destructive happens.

## How it fits the deployment model

The `eami-backup` service is defined in both `docker-compose.yml` (dev)
and `docker-compose.prod.yml` — `docker compose up -d` (or
`scripts/setup.sh`, which calls that under the hood) brings it up
automatically alongside every other service, no separate setup step
required. This is also why it's built as a small sidecar **container**
(Alpine + cron) rather than relying on the host's own cron: a Docker
Compose stack has no OS-specific host to install cron into, and a VM
appliance built around `docker compose up` (the direction referenced in
earlier roadmap discussion) gets backups "for free" the same way, with no
appliance-specific first-boot logic needed in `scripts/setup.sh` beyond
the one-line summary mention it already prints.

**Why not `pg_cron`?** The `postgres` container already runs `pg_cron`
(used today for monthly `audit_log` partition creation — see
`schema/migrations/007_audit_partitions.sql`), so it was the first thing
investigated for this task. It doesn't fit here: `pg_cron` jobs execute
as SQL *inside* the Postgres backend process. They can call SQL functions
on a schedule, but they cannot invoke an external client program like
`pg_dump` to shell out and write a file to disk — that's not something
SQL running inside Postgres can do without exotic, fragile workarounds
(e.g. `COPY ... TO PROGRAM`, which needs superuser and isn't designed for
taking a full binary dump). A real `pg_dump` needs to run as a separate
client process — hence the dedicated `eami-backup` sidecar with real cron
instead.

## Verifying backups are running

```bash
# Tail the backup container's logs — look for periodic
# "backup-db: backup complete" lines (and no ERROR lines).
docker compose logs -f eami-backup

# List the actual backup files.
docker compose exec postgres ls -la /backups
# (or: docker compose exec eami-backup ls -la /backups)

# Trigger one manually at any time (does not wait for the schedule;
# useful right after standing up a fresh deployment, or before a risky
# change).
docker compose exec eami-backup sh /scripts/backup-db.sh
```

A fresh deployment also gets an automatic **first backup within seconds
of `docker compose up`** (the `eami-backup` entrypoint runs one
immediately before starting the cron scheduler) — you don't have to wait
up to a full day for the first backup to exist.

## Restore procedure

**Tested end-to-end on 2026-07-25** (B-029) and **re-verified fresh against
the current schema on 2026-09-01** (B-134/B-143 — see `BUILT.md` for both).
The steps below are that tested procedure, not a theoretical one.

### ⚠️ Important: don't use `pg_restore --clean` here

A naive `pg_restore --clean ... backup.dump` **fails partway through**
with `ERROR: ONLY option not supported on hypertable operations` —
TimescaleDB hypertables (e.g. tables converted via `create_hypertable()`)
don't support the `ALTER TABLE ... ONLY DROP CONSTRAINT` statements
`pg_restore --clean` generates, so cleanup stops partway and you're left
with a mix of old and new objects. This was discovered by actually
running the naive version first — do not repeat it. Step 2 below (wiping
the schema directly) avoids the whole problem.

### ⚠️ A second, related TimescaleDB/`pg_restore` issue — now handled automatically

Even *without* `--clean`, `pg_restore` still fails to apply foreign key
constraints that target a hypertable — same root cause (it always
generates single-table `ALTER TABLE ONLY ...`, and hypertables reject
`ONLY`), just hit on `ADD CONSTRAINT` instead of `DROP CONSTRAINT`.
Confirmed live (B-134/B-143, 2026-09-01) against the current schema: 4 FK
constraints on `gateway_node_metrics` and `paste_events` silently fail to
apply this way. Row *data* is unaffected (`pg_restore` loads data before
applying constraints), but the affected tables lose FK enforcement until
this is fixed. **`scripts/restore-db.sh` (below) handles this
automatically** — it isn't a manual step to remember. It parses
`pg_restore`'s own error output for exactly this error shape (an error
message mentioning "hypertable", paired with a failed
`ALTER TABLE ONLY ... ADD CONSTRAINT` statement), re-applies each one
without `ONLY`, and — this is the important part for a disaster-recovery
tool — **fails loudly and stops** if it hits any `pg_restore` error that
doesn't match that exact shape, rather than guessing. If that happens,
the raw diagnostics are left in the temp directory the script logs, for
a human to look at before trusting the restore.

### Steps

**1. Identify the backup to restore.**

```bash
docker compose exec postgres ls -la /backups
# pick a file, e.g. eami_20260725T030000Z.dump
```

**2. Bring up a target Postgres with an empty schema.**

If this is disaster recovery (the `postgres_data` volume itself was lost
or corrupted):

```bash
docker compose down          # do NOT pass -v — that would also delete postgres_backups
docker volume rm eaim_postgres_data     # only the data volume; confirm the exact name via `docker volume ls`
docker compose up -d postgres
docker compose exec postgres pg_isready -U eami_app -d eami   # wait until this succeeds
```

A fresh `postgres_data` volume starts **completely empty** — no tables,
no schema (confirmed empirically, 2026-09-01: `\dt` on a truly fresh
volume returns "Did not find any relations"). Schema provisioning is
owned entirely by the `migrate` service (`docker-compose.yml`, B-051),
which applies `schema/migrations-v2/*.sql` — there is no
`docker-entrypoint-initdb.d/schema.sql` auto-apply step any more (an
earlier version of this doc claimed there was; that stopped being true
when B-051 moved schema ownership to `migrate`). **You do not need to run
`migrate` before restoring** — `pg_restore` recreates the complete schema
(tables, extensions, everything) directly from the dump regardless of
what's already there. Step 3's schema wipe below is what actually matters
before restoring, on a fresh volume or not.

If you're restoring into an *already-running* stack instead (e.g. to
recover from a bad migration or bad data, not full disk loss), skip the
volume-recreation above and go straight to step 3 against the running
`postgres` service — understanding that step 3 destroys everything
currently in that database first.

**3. Wipe the schema cleanly, then restore.**

Use `scripts/restore-db.sh` rather than a raw `pg_restore` invocation —
it performs the same schema wipe as before, but also automatically
detects and repairs the hypertable-FK-constraint issue described above
(and fails loudly, without guessing, on any other kind of restore error):

```bash
docker compose exec eami-backup sh /scripts/restore-db.sh /backups/eami_20260725T030000Z.dump
```

Requires `DATABASE_URL` to be set in the `eami-backup` container's
environment — it already is, via `docker-compose.yml`/`docker-compose.prod.yml`,
same as `backup-db.sh`.

A clean run ends with `restore complete` and exit code 0 — either because
`pg_restore` reported zero errors, or because the only errors were the
known hypertable-constraint case and they were automatically repaired
(the log line says which). If the script exits non-zero, **do not proceed
to step 4** — it means a restore error it didn't recognize occurred; the
raw `pg_restore` output and diagnostics are left in the temp directory
named in its last log line, and that needs investigating before trusting
anything in the target database.

**4. Verify data integrity before trusting the restore.**

```bash
docker compose exec postgres psql -U eami_app -d eami -c "
SELECT (SELECT count(*) FROM orgs) AS orgs,
       (SELECT count(*) FROM users) AS users,
       (SELECT count(*) FROM audit_log) AS audit_rows,
       (SELECT count(*) FROM gateway_agents) AS agents;
"
```

Compare against what you expect (a known row count, a specific org's
`slug`, etc.) — an empty-but-schema-valid database would pass step 3
without error, so step 4 is not optional.

If `restore-db.sh` logged that it repaired any constraints, you can
confirm they actually landed:

```bash
docker compose exec postgres psql -U eami_app -d eami -c "
SELECT conname, conrelid::regclass FROM pg_constraint
WHERE conrelid IN ('gateway_node_metrics'::regclass, 'paste_events'::regclass)
ORDER BY conname;
"
```

Expect `gateway_node_metrics_node_id_fkey`, `paste_events_org_id_fkey`,
and `paste_events_source_endpoint_id_fkey` in the output.

**5. Restart the application services** (they were likely stopped or are
failing to connect during steps 2–4):

```bash
docker compose up -d eami-gateway eami-api eami-collector eami-ui
```

## `reseed.sql` vs. real restore

`scripts/reseed.sql` inserts (or upserts) exactly one demo org and one
demo admin user into whatever database it's pointed at. It was originally
labeled "re-seed org and admin user after `docker compose down -v`,"
which made it easy to mistake for a real recovery tool. It is not one:

| | `reseed.sql` | Restore (this document) |
|---|---|---|
| Recovers your actual prior data (agents, policies, audit log, episodes, token usage, ...) | ❌ No | ✅ Yes |
| Use case | Get a working demo login on an empty DB | Recover from real data loss |
| Source of truth | Hardcoded values in the script | A real `pg_dump` backup file |

If you just need *a* working login to poke around a fresh/empty
deployment, `reseed.sql` is fine. If you actually lost data you care
about, use the restore procedure above.

## Offsite backup (B-144)

Local backups (above) protect against Postgres data-volume loss or
corruption, but not against losing the whole host — `postgres_backups`
lives on that same host. This section covers the optional offsite
transport that closes that gap.

**Disabled by default.** If you don't configure anything below, every
local backup behaves exactly as documented above — there is no separate
on/off flag; the offsite step simply doesn't run unless it's fully
configured (see `scripts/backup-offsite.sh`'s gating condition).

**Trust boundary, matching ADR-020 Model A:** EAMI ships the mechanism;
you control the destination and every credential involved. EAMI's own
process never holds your remote storage credentials, and never holds
your decryption key — only your *public* encryption key ever reaches it.
The backup file is fully encrypted, on this host, before any network
request is made.

### Setup

**1. Generate an age keypair** (do this once, anywhere — not inside a
container):

```bash
age-keygen -o identity.txt
# Prints your public key, e.g.: Public key: age1qyqs...
```

Keep `identity.txt` (the **private** key) somewhere outside any EAMI
container or the `secrets/` directory below entirely — a password
manager, an offline USB key, whatever your organization already uses for
secrets like this. You'll only need it again at restore time.

**2. Set the public key and remote in `.env`:**

```bash
OFFSITE_AGE_RECIPIENT=age1qyqs...            # from step 1
OFFSITE_RCLONE_REMOTE=eami-offsite:my-bucket/eami-backups
```

**3. Create `secrets/rclone.conf`** (gitignored, holds your actual remote
credentials — the remote name must match what you used in
`OFFSITE_RCLONE_REMOTE` above, `eami-offsite` here):

S3-compatible:

```ini
[eami-offsite]
type = s3
provider = Other
access_key_id = <your access key>
secret_access_key = <your secret key>
endpoint = <your S3-compatible endpoint, e.g. https://s3.us-east-1.amazonaws.com>
```

SFTP:

```ini
[eami-offsite]
type = sftp
host = <your sftp host>
user = <your sftp user>
key_file = /rclone/id_ed25519
```

(For SFTP with a key file, also add the key itself into `secrets/` and
reference it as shown — it rides along on the same directory mount as
`rclone.conf`.)

**4. Restart `eami-backup`** (`docker compose up -d eami-backup`, or a
plain `docker compose up -d` for the whole stack) so the rebuilt image
(carrying `age`/`rclone`, see `docker/eami-backup/Dockerfile`) and the
new env vars/mount take effect. The next backup — scheduled or manual —
will encrypt and upload automatically; check
`docker compose logs eami-backup` for a `backup-offsite:` line confirming
success (or a clear `ERROR` if something's misconfigured).

### Restoring from the offsite copy

Two steps: recover the plaintext dump from the offsite copy, then hand
it to the existing, **unmodified** local restore procedure above.

**1. Recover the plaintext dump** (needs your `identity.txt` from setup
step 1 — supply it however you actually store it; it does not live in
any container by default):

```bash
docker compose cp identity.txt eami-backup:/tmp/identity.txt
docker compose exec eami-backup sh /scripts/restore-offsite.sh \
    eami-offsite:my-bucket/eami-backups/eami_20260901T030000Z.dump.age \
    /tmp/identity.txt \
    /backups/from-offsite.dump
docker compose exec eami-backup rm /tmp/identity.txt   # don't leave it sitting in the container
```

**2. Restore it exactly like a local backup** — `restore-db.sh` doesn't
know or care whether the dump came from `/backups` locally or via
`restore-offsite.sh`:

```bash
docker compose exec eami-backup sh /scripts/restore-db.sh /backups/from-offsite.dump
```

Then continue with steps 4–5 of the local restore procedure above
(verify data integrity, restart application services).

### Why whole-file encryption is enough

`gateway_tools.credentials_encrypted` is already AES-256-GCM-sealed at
the application layer and stays that way inside the dump either way. But
`audit_log` content and tool-call `parameters` (JSONB) are **not**
encrypted at the application layer — they'd be fully readable in a raw,
unencrypted dump. This isn't a gap specific to the offsite path: it's
true of the plain local `.dump` file too, which is why encrypting the
*entire* file before it leaves the host (rather than, say, encrypting
only specific columns) is the right fix here — every byte, including
that JSONB content, becomes opaque ciphertext the moment `age` runs,
before any network request is made. Confirmed empirically, not just
argued: a known plaintext string present in the raw local dump is absent
from the corresponding `.age` file (see B-144's live verification in
`BUILT.md`).

## Future work (not v1)

- Point-in-time recovery via WAL archiving, if continuous (not just
  periodic) recovery points become a real requirement.
- Automated restore tooling, once the manual procedure has enough
  real-world mileage to be confidently scripted (the offsite path above
  already automates its own encrypt/transport and download/decrypt
  sub-steps, but the overall trigger — which backup, which target
  database — is still a human decision by design, same as local restore).
