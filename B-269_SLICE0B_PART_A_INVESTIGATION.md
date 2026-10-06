# B-269 Slice 0b — Minimal admin audit trail: Part A investigation

**Date:** 2026-10-06 · **Author:** Claude Code · **Status:** investigation only, no code. Awaiting founder decisions (§10).
**Roadmap:** master sequence item 8 (B-269), Slice 0b, decision D2 (`DISCOVERY_PRESETS_DESIGN.md` §12). It is the first slice of B-224 (durable admin-write audit trail).

**Read for this report:**
- `DISCOVERY_PRESETS_DESIGN.md` §8 and §12 (D2).
- B-224, B-133, B-232 and B-233 (`BACKLOG.md`, `B-232_VERIFICATION.md`, `B-233_VERIFICATION.md`).
- ADR-007's chain as built: `eami-gateway/internal/audit/writer.go` and `eami-api/internal/store/verify.go`.
- The `audit_log` DDL (`000001_baseline`) and the precedent event tables (`000009 agent_lifecycle_events`, `000018 license_events`).
- `router.go` (every mutating route), and the transaction and advisory-lock patterns (`license.go`, `bootstrap.go`, `provisioning.go`, `store/cmdb.sql.go`).
- The throwaway test-database helpers (`bootstrap_test.go`, `eami-gateway/internal/testdb`).
- Read-only checks on the live dev database.

---

## 0. Summary

- **Recommendation: a new table, `admin_audit_events`, with its own per-org hash chain.** Don't reuse `audit_log`.
  - Reusing it would need either a second process writing to a chain that is only safe with one writer, or changes to `audit_log`'s hash formula and to six readers. The brief rules out both ("do not touch existing `audit_log` semantics").
  - A separate per-org chain is cheap: about 1 migration, 1 store file and 1 handler file.
  - It's also *stronger* than `audit_log`'s chain: per-org sequence numbers with gap detection, a length-prefixed hash encoding, and no cross-org linkage.
- **Writes happen inside the change's own transaction.** A per-org advisory lock serialises them, and `UNIQUE(org_id, seq)` backs the lock up. If the change rolls back, the event rolls back with it. If the event can't be written, the change fails (fail closed).
- **What it proves:** in-place edits, interior deletions, reordering and cross-org splicing are all detected by the verifier.
- **What it doesn't prove:** it does **not** stop someone with database write access from deleting the newest rows or recomputing the whole chain. The chain has no secret key and no external anchor. 0b must be documented as **"tamper-evident against edits, not tamper-proof against a database administrator."**
- **Two pre-existing findings outside 0b's scope** (§9). Proposed for minting, not minted:
  - `audit_log` has **no partitions after 2027-12 and no DEFAULT partition**, so gateway audit inserts will fail from 2028-01-01. The partition job only ever existed in the retired migrations path.
  - The application connects as a **Postgres superuser** (`eami_app`, `rolsuper = t`). The `REVOKE UPDATE, DELETE ON audit_log` in the baseline is a comment and was never applied, so the existing audit trail is append-only by convention only.

---

## 1. Reuse `audit_log`, or a separate table with its own chain?

### What reusing it would cost

**1. Writer safety.** The chain is single-writer by construction.
- `writer.go` serialises with an in-process `sync.Mutex`.
- It reads the head with `SELECT hash FROM audit_log ORDER BY timestamp DESC LIMIT 1`, with no lock and across all orgs.
- An `eami-api` writer would be a second process with its own mutex, so the two would race and fork the chain. This is the same failure as B-140: 205 of 1,636 rows were already broken links in B-134's restore drill.
- `license_events` was kept off `audit_log` for exactly this reason.
- Making a second writer safe means changing how the gateway's writer locks, which is a change to `audit_log` semantics.

**2. Hash content.**
- The hash covers fixed gateway fields: `prevHash||id||orgID||agentName||toolName||action||decision||timestamp`.
- Admin events have no `agent_name`, `tool_name` or `decision`, so they'd be stuffed into those columns. `decision` has a CHECK of `allowed | denied | escalated`.
- The change summary would sit in `parameters`, which **the hash does not cover**. So the part an auditor most cares about would not be tamper-evident.

**3. Readers.** Every one would need to start excluding admin rows:

| Reader | What it does today | Effect of admin rows mixed in |
|---|---|---|
| `alerting/engine.go` (2 queries) | counts `decision='denied'` / `'escalated'` | would count admin rows if they reused those decisions |
| `store/agent_connections.sql.go`, `agent_lineage.sql.go` | read by agent and tool | admin rows would show up as fake connections or lineage |
| `store/alerts.sql.go` | reads by decision | as for alerting |
| `store/audit.sql.go` (list, count, export, export count) | the Audit page and CSV export | admin rows in the tool-call view and CSV; FinOps-style token columns would be null |
| `store/verify.go` | per-org self-hash plus a global prev-hash set | still passes, but only for fields the hash covers; the summary isn't covered |

**4. Tenancy.** The chain is global across orgs. Every org's chain is interleaved with every other org's, so a gap or edit can't be pinned to one org without the global hash set. That's what `verify.go` reads today.

**Conclusion:** reuse is neither cheap nor safe. D2 said "reuse the existing hash chain if cheap", and it isn't.

### What a separate table costs

- **One migration** (§3), **one store file** (append, list, verify), **one handler file** (two GET routes), and tests.
- None of the six readers change. The Audit page, CSV export, FinOps, Lineage and alert rules are untouched.
- The chain is reimplemented, roughly 60 lines plus a verifier. That's small, and it fixes the existing chain's three weaknesses:
  - **Per-org chains:** each org has its own genesis and its own lock. No cross-org linkage, no global head.
  - **A per-org `seq`:** 1, 2, 3 and so on, so a deleted interior row shows up as a gap, and ordering never depends on timestamps that can tie.
  - **Length-prefixed hash input:** `audit_log` concatenates fields with no delimiter, which is ambiguous in theory.

**One-spine check:** this is an Audit concern. When B-224 builds a UI, it goes under the existing **Audit** page (for example an "Admin changes" tab), not a new top-level section. 0b has no UI.

---

## 2. Partitioning and retention (B-133)

- **Volume is tiny.** Admin writes run to tens per org per day; a bulk adopt is **one** event carrying a count (design §7). Even at 10,000 a day it's about 3.6 million rows a year.
- **Recommendation: not partitioned.** One plain table with an `(org_id, seq)` unique index and an `(org_id, occurred_at)` index.
  - This avoids `audit_log`'s partition cliff entirely (§9 F1).
  - It also keeps the verifier's per-org scan on one index.
  - Partitioning can be added later, by a migration that copies rows into a partitioned table. The chain stays valid because the hash doesn't depend on physical layout.
- **Retention: nothing is ever deleted by the product.** Every row is permanent.
- **Never deleted:** all of it. Admin events are the record of who changed what governs the fleet.
- **Organization deletion:** **no `ON DELETE CASCADE`** (unlike `agent_lifecycle_events` and `license_events`). Use `ON DELETE RESTRICT`.
  - No organization-delete path exists today; a grep found no `DELETE FROM organizations` outside tests.
  - So RESTRICT changes nothing now. It forces any future org-offboarding feature to decide explicitly (export, then purge under a documented procedure). That decision belongs to B-133, not 0b.
- **Append-only enforcement in the database:**
  - a `BEFORE UPDATE OR DELETE` row trigger and a `BEFORE TRUNCATE` statement trigger, both raising an exception;
  - the down migration **refuses to drop the table if it holds any rows**, so a routine `migrate down` can't destroy the trail.
  - The app connects as a superuser (§9 F2), so the triggers guard against application bugs and accidents, not against a DBA (§5).
- **B-133 note:** that item names `audit_log`, `token_usage` and `paste_events`. Add `admin_audit_events` to its "never delete" list when B-133 is investigated.

---

## 3. Minimal fields (proposed schema)

```sql
CREATE TABLE admin_audit_events (
  id            UUID        NOT NULL DEFAULT uuid_generate_v4() PRIMARY KEY,
  org_id        UUID        NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  seq           BIGINT      NOT NULL CHECK (seq >= 1),           -- per-org, gap-free
  occurred_at   TIMESTAMPTZ NOT NULL,                             -- set by the writer (µs precision)
  actor_type    TEXT        NOT NULL CHECK (actor_type IN ('user','system')),
  actor_user_id UUID,                                             -- no FK: users are soft-deleted; NULL iff system
  actor_role    TEXT        NOT NULL CHECK (actor_role ~ '^[a-z_]{1,32}$'),  -- snapshot at write time
  action        TEXT        NOT NULL CHECK (action ~ '^[a-z][a-z_]{0,40}\.[a-z][a-z_]{0,40}$'),
  target_type   TEXT        NOT NULL CHECK (target_type ~ '^[a-z][a-z_]{0,40}$'),
  target_id     TEXT        NOT NULL CHECK (target_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
  summary       TEXT        NOT NULL CHECK (summary IS JSON OBJECT AND octet_length(summary) <= 8192),
  source        TEXT        NOT NULL CHECK (source IN ('api','system')),
  request_id    TEXT                 CHECK (request_id ~ '^[A-Za-z0-9._/-]{1,64}$'),
  prev_hash     TEXT        NOT NULL CHECK (prev_hash ~ '^[0-9a-f]{64}$'),
  hash          TEXT        NOT NULL CHECK (hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT admin_audit_events_actor_ck CHECK ((actor_type = 'user') = (actor_user_id IS NOT NULL)),
  UNIQUE (org_id, seq)
);
CREATE INDEX ON admin_audit_events (org_id, occurred_at DESC, seq DESC);
CREATE INDEX ON admin_audit_events (org_id, target_type, target_id, seq DESC);
```

**Field notes**
- **Actor:** user ID plus a **role snapshot** taken from the JWT claims at write time. No email or name is stored; they're personal data, and the read API joins `users` (which are soft-deleted, so the rows still resolve).
- **Action code:** `<target_type>.<verb>` (§7). **The database checks the format only; Go checks an allowlist.** So B-224 can add codes without a migration (§6).
- **`summary` is `TEXT` holding canonical JSON, not `JSONB`.**
  - JSONB reorders keys and re-renders numbers. Hashing the stored JSONB would need a canonicaliser on both sides, and that's exactly where verifiers drift.
  - With `TEXT`, the bytes hashed are the bytes stored. `IS JSON OBJECT` (Postgres 16) still guarantees it parses.
- **`request_id`:**
  - chi's `middleware.RequestID` **accepts a client-supplied `X-Request-Id`**, so it isn't trustworthy as-is.
  - Proposal: the writer stores it only if it matches the strict pattern, otherwise NULL. It's for correlation with local logs, never authority.
  - Decision B0b-5.
- **Client IP is left out of 0b.** It's personal data, and the brief's minimal field list doesn't include it. It can be added under B-224 if wanted.

### The summary never carries values (standing check; the brief's hard rule)

The summary is built **only** by typed Go constructors; no handler passes a free-form map. Its shape:

```json
{"v":1,
 "changes":[{"field":"model_scan_paths","old":"sha256:…16hex","new":"sha256:…16hex",
             "added":2,"removed":1,"items_old":["sha256:…"],"items_new":["sha256:…"]},
            {"field":"scanners","old_count":10,"new_count":8,"added":[],"removed":["models","network_activity"]}],
 "codes":["path_profile_parent"],
 "refs":{"preset_version":3,"from_version":2,"count":42}}
```

**What may appear**
- Field **names** from a per-target allowlist.
- **Hashes** of old and new values: `sha256:` plus 16 hex characters, over the canonical value.
- **Counts**, version numbers, UUID references.
- Stable **codes**: path-warning codes and limit codes.
- Values from a **closed enum**. For example, scanner names come from the fixed 10-scanner set and aren't free text, so they are safe to list.

**Scan paths stay useful without values**
- per-path hashes, added and removed counts, and the `path_warnings` codes in force;
- an admin can match a hash against the preset version's content, which they can already read.

**Never present**
- enrollment key values, prefixes, or key hashes (a key is referenced only by its row ID);
- credentials;
- raw config text;
- error text;
- free text typed by an admin, such as names, descriptions or publish notes (decision B0b-3).

**Enforcement**
- a size cap (8 KiB in the database);
- a test that feeds sentinel secrets through every constructor and asserts the sentinels appear nowhere in the row or in API output.

**Honest caveat:** a truncated hash of a low-entropy value, such as a common path, can be guessed by dictionary. **The hashes are change identifiers, not confidentiality.** The real protection is that values never enter the row. Anyone who can read the trail (admins only) can already read the preset itself.

---

## 4. Atomicity and concurrent writes

**Same transaction as the change.** The writer's signature forces this:

```go
func AppendAdminAuditEvent(ctx context.Context, tx pgx.Tx, ev AdminAuditEvent) (AdminAuditEventRow, error)
```

- It takes a `pgx.Tx`, not `*Queries`, so a caller can't write outside a transaction.
- If the change rolls back, the event rolls back too: no phantom events.
- If the append fails, the handler returns a generic 500 ("could not record audit event") and the change rolls back. That's **fail closed**.
- **Cost:** an audit write failure blocks the admin action. Accepted for presets, keys and assignment. (Precedent: `agent_lifecycle_events` is written best-effort, outside any transaction; `license_events` is written inside the upload's transaction.)

**Serialising a per-org chain under concurrency.** Inside that transaction, as the **last** step before commit:

1. `SELECT pg_advisory_xact_lock(hashtextextended('admin_audit:' || $org, 0))`.
   - This is the repo's established pattern (`license.go`, `provisioning.go`, `store/cmdb.sql.go`).
   - The key is namespaced and 64-bit, so it doesn't share `license.go`'s unprefixed `hashtext(orgID)` key.
2. `SELECT seq, hash FROM admin_audit_events WHERE org_id=$1 ORDER BY seq DESC LIMIT 1`. If there's no row, use genesis.
3. Set `occurred_at` (truncated to microseconds **before** hashing, because Postgres stores µs), compute the hash, then `INSERT … seq = head + 1`.
4. The lock is released at commit or rollback.

**Why this is safe**
- The lock is held until commit, so the next writer for that org sees the committed head.
- Rows commit in `seq` order.
- Different orgs never wait on each other.
- **`UNIQUE(org_id, seq)` is the backstop:** a writer that skipped the lock gets a unique-violation and fails closed, rather than forking the chain.
- **Deadlock:** only this function takes this lock, always last, one key per transaction. A multi-org transaction can't happen, because every admin action is org-scoped from the JWT.

**Hash**
- `SHA-256` over a **length-prefixed** encoding of: `v1`, `prev_hash`, `id`, `org_id`, `seq`, `occurred_at` (UTC, RFC 3339, µs), `actor_type`, `actor_user_id`, `actor_role`, `action`, `target_type`, `target_id`, `summary` bytes, `source` and `request_id`.
- **Every column except `hash` itself is covered.**
- Genesis: `SHA-256("eami-admin-audit-genesis-v1" || org_id)`. It's different per org, so one org's rows can't be spliced into another org's chain.

---

## 5. Tamper-evidence: what 0b guarantees and what it doesn't

**What the verifier detects** (`GET …/verify`, and the store function):

| Tamper | Detected? | How |
|---|---|---|
| Edit any column of any row | **Yes** | self-hash mismatch |
| Delete an interior row | **Yes** | `seq` gap, plus the next row's `prev_hash` breaks |
| Reorder or re-sequence rows | **Yes** | `prev_hash` chain, `seq` |
| Copy a row from another org | **Yes** | org-specific genesis and `org_id` inside the hash |
| Insert a forged row mid-chain | **Yes**, unless the whole tail is recomputed | the link breaks |
| **Delete the newest N rows (truncate the tail)** | **No** | the remaining chain is still valid |
| **Recompute the whole chain after editing** | **No** | no secret key and no external anchor; anyone with write access can recompute SHA-256 |
| Delete every row for an org | **No** (it looks like "no events") | — |
| Disable the triggers, then edit | Detected only if they don't also recompute | the triggers stop app bugs, not a superuser |

**What a database administrator can still do:** anything in the "No" rows. That includes a superuser, and today it includes **the application role itself** (§9 F2).

**What 0b states in its docs and API description:**
- "Tamper-evident against modification and interior deletion; **not tamper-proof against database administrators**, and does not detect removal of the most recent events."
- The verify response says **what** it checked: `{valid, checked, head_seq, head_hash, first_bad_seq}`.

**Cheap, later ways to close the "No" rows (not in 0b):**
- **Head anchoring:** publish `head_seq` and `head_hash` periodically to the customer's SIEM or log store (B-135). Tail truncation and recompute both become detectable against the anchor.
- **HMAC with a key outside the database** (API environment or secret file): a DB-only attacker can no longer recompute the chain. This touches secrets and key rotation, so it's out of scope for 0b and needs its own brief.

---

## 6. Read side

**Who reads it:** **org `admin` only.**
- Design §8 makes every preset, key and assignment mutation admin-only. The trail reveals who changed governance and when.
- `/v1/audit` today is admin, operator and viewer. **The new routes must not join that group.** Decision B0b-2 covers whether viewers or operators should ever see it.

**Routes**, per `API_CONVENTION.md`. They sit under Audit, per the one-spine rule:

| Route | Role | Purpose |
|---|---|---|
| `GET /v1/audit/admin-events` | admin | paginated, filterable list |
| `GET /v1/audit/admin-events/verify` | admin | verify the caller's own org chain |

**List parameters**
- **Filters:** `action`, `target_type`, `target_id`, `actor_user_id`, `from`, `to` (RFC 3339).
  - All allowlisted or format-checked. A bad value is a **400 with a fixed message, never reflected** (principle 4).
  - **There is no `org_id` parameter.** The org always comes from the JWT.
- **Paging:** `page` and `per_page` through the shared `pagination()` helper (max 100). This avoids `audit.go`'s hand-rolled B-225 overflow.
- **Sorting** (§8): `sort=seq` only, `order=asc|desc`, default `seq desc`. `seq` is already unique, so no extra tie-break is needed.

**Response**
- Shape: `{data, meta}`.
- `summary` is returned as a parsed object.
- Actor display: a joined user email for `user` actors, `null` for `system`.
- Error bodies: fixed messages, never `err.Error()`.

**Contract**
- Both routes are new, so they need `API_CONTRACT_DRIFT.md` rows **in the build commit**. `api/openapi.yaml` is Architect-EAMI's file.
- The routes are additive under `/v1`, so no version bump is needed.

**How B-224 adopts other actions with no migration**
- The database checks only the **format** of `action` and `target_type`.
- The **allowlist** lives in Go: one registry, `store/admin_audit_actions.go`, mapping each action to its target type and its allowed summary fields.
- Adopting a new action means three things:
  1. add a registry entry and a typed constructor;
  2. move the handler's write into a transaction (several aren't transactional today; that's B-224's real cost);
  3. call `AppendAdminAuditEvent`.
- The list filter validates `action` against the same registry, so new codes become filterable automatically.

---

## 7. Every candidate write site (action codes chosen once)

Codes are `<target_type>.<verb>`. 0b **registers only the Slice 1 and Slice 3 codes** (marked ★). The rest are reserved here so B-224 doesn't re-litigate them.

| Area | Route / site (`eami-api` unless noted) | Proposed code(s) |
|---|---|---|
| ★ Presets (Slice 1) | create, draft update, publish, revert, archive, unarchive | `preset.created`, `preset.draft_updated`, `preset.published`, `preset.reverted`, `preset.archived`, `preset.unarchived` |
| ★ Assignment (Slice 1/4) | assign, bulk adopt, change, unassign | `endpoint.preset_assigned`, `endpoint.preset_bulk_assigned` (one event with a count), `endpoint.preset_unassigned` |
| ★ Enrollment keys (Slice 3) | create, revoke; exchange at first registration | `enrollment_key.created`, `enrollment_key.revoked`, `enrollment_key.exchanged` (`system` actor) |
| Agent config | `PUT /v1/gateway/agents/{id}/config` (legacy, until Slice 4 retires it) | `agent.config_updated` |
| Agents | `POST /v1/gateway/agents`, `PATCH …/{id}` (incl. suspend/reactivate), `DELETE …/{id}` | `agent.created`, `agent.updated`, `agent.suspended`, `agent.reactivated`, `agent.deleted` |
| Endpoint link | `PATCH /v1/endpoints/{id}/link-agent` | `endpoint.agent_linked`, `endpoint.agent_unlinked` |
| Tools | `POST`/`PATCH`/`DELETE /v1/gateway/tools…` | `tool.created`, `tool.updated`, `tool.deleted` (`credentials` changes: field name only, never a hash) |
| Policies | `POST`/`PATCH`/`DELETE /v1/gateway/policies…`, `…/reorder` (PUT and POST) | `policy.created`, `policy.updated`, `policy.deleted`, `policy.reordered` |
| Workflows | workflows `POST`/`PATCH`/`DELETE`, `PUT /v1/gateway/workflow-steps/{id}/params` | `workflow.created`, `workflow.updated`, `workflow.deleted`, `workflow_step.params_updated` |
| Nodes | `DELETE /v1/gateway/nodes/{id}` | `node.deleted` |
| API keys | `POST /v1/auth/api-keys`, `DELETE …/{id}` | `api_key.created`, `api_key.revoked` |
| Users | invite, role, delete, admin reset link | `user.invited`, `user.role_changed`, `user.deleted`, `user.reset_link_issued` |
| Org settings | `PUT /v1/settings/org`, `PUT /v1/settings/notifications`, `POST …/notifications/test` | `org.settings_updated`, `org.notifications_updated`, `org.notification_tested` |
| License | `POST /v1/settings/license` (already writes `license_events` in-tx) | `license.uploaded` |
| CMDB | categories and types create/update/delete; asset classification | `cmdb_category.*`, `cmdb_type.*` (`created`/`updated`/`deleted`), `asset.classified` |
| Workspaces | create, update, delete; members add/role/remove; workspace policies create/update/delete | `workspace.created`/`updated`/`deleted`, `workspace.member_added`/`member_role_changed`/`member_removed`, `workspace_policy.created`/`updated`/`deleted` |
| Alert rules | create/update/delete/test | `alert_rule.created`/`updated`/`deleted`/`tested` |
| Alerts | acknowledge, resolve | `alert.acknowledged`, `alert.resolved` |
| Approvals | `POST /v1/approvals/{id}/decide` | `approval.decided`. **Already recorded** in `approval_requests` and the gateway's `audit_log`; B-224 decides whether to duplicate it. |
| Model pricing (platform_admin) | `POST`/`PATCH`/`DELETE /v1/admin/model-pricing…` | `model_pricing.created`/`updated`/`deleted`. **Not org-scoped** (platform level): needs a decision on which org row it lands in, or a platform trail (B-224). |
| OpenAPI discover, tool test | `POST /v1/gateway/openapi/discover`, `POST …/tools/{id}/test` | not state-changing, so not audited (B-224 may revisit) |
| Setup, bootstrap | `POST /v1/setup/bootstrap` | `org.bootstrapped` (`system`) |
| **Out of scope (auth/security events, not admin writes)** | login, refresh, accept-invite, request/reset password, `PATCH /users/me`, change-password | a separate auth-event item if wanted; not this trail |
| Existing event tables | `agent_lifecycle_events` (B-087), `license_events` | untouched by 0b. B-224 decides whether to fold them in or keep them as projections. |
| Slice 1 migration | D8: `agent_configs` → "Migrated" presets | **No events are written by the SQL migration**: reproducing the canonical hash encoding in SQL is fragile. The preset carries a `migrated` marker instead. To confirm in Slice 1. |

---

## 8. Cross-org: never read or written across orgs, and the adversarial tests

**Structural guarantees**
- **Write:** `org_id` comes only from the caller's JWT claims (or, for `system` events, from the already org-scoped object being changed). It's never a request field. The lock, head read and insert all use that one value.
  - **The caller remains responsible** for proving the *target* belongs to that org before writing. In Slice 1 that is the same org-scoped `GetX(id, orgID)` check that guards the change itself (the B-232 layer 1 pattern).
- **Read:** every query has `WHERE org_id = $jwtOrg`, and the target filter is applied *within* that org. The verifier reads only the caller's org. There is **no global hash set**, unlike `verify.go`.

**Adversarial tests (B-232/B-233 standard: real Postgres, one test per layer, mutation-checked)**

All run in **per-test throwaway databases** (the `bootstrap_test.go` / `eami-gateway/internal/testdb` pattern, B-122). So no audit row is ever written to, or deleted from, the shared dev database, and the "never delete audit records, including fixtures" rule holds.

1. **List isolation:** org A has events, and org B's admin lists them. B sees only its own events. Filtering by A's `target_id` or `actor_user_id` gives an empty page, byte-identical to a nonexistent ID.
2. **Supplied `org_id` is ignored:** `?org_id=<A>` from B returns B's data (or a 400 if unknown parameters are rejected); never A's.
3. **Verify isolation:** tampering with A's row makes A's verify fail and leaves B's verify valid, and the other way round.
4. **Splice:** copy A's row into B's chain (the right `seq`, with `prev_hash` adjusted) and B's verify fails, because of the genesis and `org_id` inside the hash.
5. **Concurrency:** N goroutines append to A and to B at the same time, each in its own transaction. Both chains verify, `seq` is contiguous 1..N per org, and no A `prev_hash` equals any B hash.
6. **Lock backstop:** a writer that bypasses the lock and races fails with a unique-violation, and no fork is committed.
7. **Rollback:** a change transaction that appends and then rolls back leaves no row and an unchanged head.
8. **Store layer:** `ListAdminAuditEvents(orgB, filter=A's target)` returns 0 rows, separately from the HTTP layer.
9. **Roles:** operator, viewer, approver and platform_admin-without-org get 403 on both routes. An unauthenticated caller gets 401.
10. **Secrets:** sentinel key, credential and path values fed through every registered constructor never appear in `summary`, in any column, or in API output (a full-row text search).
11. **Append-only:** `UPDATE`, `DELETE` and `TRUNCATE` on the table raise. The down migration refuses when rows exist.
12. **Tamper matrix:** each "Yes" row of §5 is detected, and the tail-truncation case is asserted **not** detected. That documents the limit as a test, not just prose.

**Mutations to prove the tests bite:**
- drop the org filter from the list SQL (expect test 1 or 8 to fail);
- drop the lock (expect 5 or 6);
- drop `org_id` from the hash (expect 4);
- drop the trigger (expect 11);
- drop the role gate (expect 9).

---

## 9. Pre-existing findings outside 0b (proposed, **not minted**: B-ID assignment needs founder confirmation)

**F1: `audit_log` stops accepting inserts on 2028-01-01.**
- **Partitions:** 20 monthly partitions, 2026-05 to 2027-12, and **no DEFAULT partition** (checked live).
- **Partition job:** the monthly creation job existed only in the retired `schema/migrations/007` path, through `pg_cron`.
- **Why it doesn't run:**
  - `migrations-v2` has no partition job;
  - B-189 found the `pg_cron` extension was never created in the dev database;
  - `scripts/create-audit-partition.sh` exists but nothing schedules it.
- **Note:** B-189's text still describes `audit_log`'s job as working, which is inaccurate.
- **Effect:** gateway audit writes fail from 2028-01-01, about 15 months away. Whether the tool call then fails or proceeds unaudited depends on the gateway's handling; that wasn't traced here.
- **Fix:** small. Add a DEFAULT partition, or a migration that creates partitions years ahead, plus a scheduled job.
- **Suggested severity:** Medium now, High as the date nears.

**F2: the app's database role is a superuser, so `audit_log` append-only isn't enforced.**
- `eami_app` has `rolsuper = t` and holds `UPDATE` and `DELETE` on `audit_log`, checked live.
- The baseline's `REVOKE UPDATE, DELETE ON audit_log FROM eami_app` is **a comment**.
- The `audit_insert_only` RLS policy doesn't constrain a superuser or the table owner.
- B-168 already noted that `eami_app` owns its tables.
- **Effect:** an injection bug or a compromised API process could rewrite or delete audit rows. Only the chain's tamper-evidence remains, and it has the §5 limits.
- **Fix:** a separate least-privilege runtime role, with migrations run as the owner. That touches deployment and compose, so it needs its own brief.
- **Suggested severity:** Medium.
- **This also bounds 0b's guarantee:** 0b's triggers can be disabled by the same role.

---

## 10. Decisions for the founder

| # | Decision | Recommendation |
|---|---|---|
| B0b-1 | Separate `admin_audit_events` table with its own per-org chain, not `audit_log` | **Yes** (§1) |
| B0b-2 | Readers: org `admin` only; routes `GET /v1/audit/admin-events` and `…/verify` under the Audit spine | **Yes**; viewer or operator access is revisited under B-224 |
| B0b-3 | No admin-typed free text in summaries (names, descriptions, publish notes): IDs, codes, counts and hashes only | **Yes** for 0b. Names are shown by joining the live object at read time, in Slice 2 or B-224. |
| B0b-4 | Fail closed: if the audit write fails, the change fails, in the same transaction | **Yes** |
| B0b-5 | `request_id`: store chi's ID only if it matches a strict pattern, otherwise NULL (it accepts client `X-Request-Id`) | **Yes**; the alternative is a server-generated UUID per event, logged locally |
| B0b-6 | Not partitioned; never deleted; org FK `ON DELETE RESTRICT`; append-only triggers; the down migration refuses if rows exist | **Yes** |
| B0b-7 | Tamper-evidence wording as in §5; anchoring and HMAC deferred | **Yes**; head anchoring folds into B-135 |
| B0b-8 | **Live verification without fabricated events.** 0b has no wiring, so: run the migration up/down/up on the real database; both routes live (empty list, `verify` valid on an empty chain, cross-org and role checks); the write path is proven in throwaway databases on the real Postgres server. **No synthetic "self-test" event is planted in Dev Org's permanent trail.** | **Yes**; the first real live events arrive with Slice 1 |
| B0b-9 | Mint F1 (`audit_log` 2028 partition cliff) and F2 (superuser app role) as new items | Your call. Both are pre-existing and outside 0b. Not minted; the next free B-ID reads **B-298**. |

**Build scope once approved:**
- migration `000029_admin_audit_events` (up and down);
- `schema.sql`;
- `store/admin_audit.go` (append, list, verify, registry, typed summary builders for the ★ codes);
- `api/admin_audit.go` (two routes);
- tests 1–12 plus the mutations;
- drift rows;
- both reviews, with a security review required;
- BUILT, BACKLOG and CONTEXT updates, and the Slice 0b tick.

No UI. No wiring, since presets, keys and assignment don't exist until Slices 1 and 3.
