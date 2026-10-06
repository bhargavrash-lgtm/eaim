# B-269 Slice 0b — Admin audit trail: verification record

**Date:** 2026-10-06 · **Author:** Claude Code · **Design:** `B-269_SLICE0B_PART_A_INVESTIGATION.md` · **Decisions:** B0b-1..9 (founder, 2026-10-06; all "yes").
**Roadmap:** master sequence item 8 (B-269), Slice 0b; decision D2. First slice of B-224.

---

## 1. What the trail guarantees, and what it does not (plain statement, B0b-7)

**It guarantees:**
- **Every admin change it records is recorded atomically with the change.** The event is written in the change's own transaction. If the change fails, there is no event; if the event can't be written, the change is rolled back and the caller gets a fixed `audit_write_failed` error within the lock timeout, never a hang.
- **No org can read, count, filter-probe or verify another org's events**, and no event can be written into another org's chain through `RunAudited` (the org is pinned by the caller's JWT org).
- **Only org admins can read it.**
- **Rows carry no values:** no enrollment-key values, credentials, raw config, error text, or admin-typed names and descriptions. Only field names, short value hashes, counts, closed-list names, stable codes, IDs and version numbers.
- **The verifier detects:** an edit to any column of any row, deletion of a row in the middle, reordering, a forged replacement row, and an org's chain moved into another org.

**It does not guarantee:**
- **It cannot detect a database administrator.** Anyone with write access to the database can delete the newest events, delete all of an org's events, or rewrite history and recompute the whole chain, and the verifier will report the chain as valid. Both limits are asserted as tests (§4, test 12).
- **The append-only triggers don't stop a superuser.** Today the application's own database role is a superuser and owns the table (B-299), so the "append-only" property is a guard against application bugs, not against a compromised API process.
- **It is not tamper-proof.** Closing that needs an external anchor (publishing the head hash to the customer's SIEM, B-135) or a key held outside the database. Both are deferred.
- **Value hashes are change identifiers, not secrecy.** A short unsalted hash of a guessable value (an interval, a common path) can be confirmed by guessing. That's why free text gets no hash at all, and why values never enter a row.

---

## 2. Gateway trace: what happens when `audit_log` rejects an insert after 2028-01-01

**Answer: the tool call goes through, unaudited.** So **B-298 is rated High**, per the brief.

Trace (`eami-gateway/cmd/gateway/dispatcher.go`):
- **Allow branch (~line 820–870):** the downstream call runs first (`aiProviderRouter.Dispatch`, `toolRouter.Forward` or `fwdProxy.Forward`). Only then is `d.auditWriter.Write(...)` called. Its error goes into `DispatchOutcome.AuditWriteErr`, and the outcome still carries `Result: tr.Body`, so the agent gets the tool's result.
- **The failure is only logged:** `logAuditWriteFailureHook` (B-121) writes `slog.Error`; nothing denies or retries.
- **Escalate branch:** the `escalated` row's write error is carried the same way. The approval is still submitted, and after approval `dispatchApproved` executes the call; the resolution write fails the same way.
- **Deny branch:** the call is denied anyway; only the record is lost.
- **Writer state:** `internal/audit/writer.go` advances `lastHash` only after a successful insert, so the chain isn't corrupted. It just stops growing while every call proceeds.
- **Why inserts fail:** `audit_log`'s partitions end at `audit_log_2027_12` and there's no DEFAULT partition, so an insert with a 2028 timestamp (the gateway's `ReceivedAt`) has no partition to land in. A gateway host whose clock is wrong could hit this earlier.

---

## 3. What was built

| Piece | File |
|---|---|
| Migration (up, down) | `schema/migrations-v2/000029_admin_audit_events.{up,down}.sql`; mirrored in `schema/schema.sql` |
| Store: append, `RunAudited`, list, count, verify, registry, summary constructors | `eami-api/internal/store/admin_audit.go` |
| Read API and the fail-closed HTTP mapping for Slice 1 | `eami-api/internal/api/admin_audit.go`; routes in `router.go`'s admin-only group |
| Tests (15) | `eami-api/internal/api/admin_audit_test.go`, `admin_audit_export_test.go` |
| Drift | `API_CONTRACT_DRIFT.md` row C24, §B and headline counts |

**Table `admin_audit_events`:** `id, org_id (FK orgs, ON DELETE RESTRICT), seq (per org, UNIQUE with org_id), occurred_at, actor_type (user|system), actor_user_id, actor_role, action, target_type, target_id, summary (TEXT, IS JSON OBJECT, ≤ 8 KiB), source (api|system), request_id (strict pattern), prev_hash, hash`. No partitions. Triggers refuse UPDATE, DELETE and TRUNCATE (`42501`). The down migration locks the table and refuses while any row exists.

**Chain:** SHA-256 over the length-prefixed fields (version string, `prev_hash`, every column but `hash`); genesis = SHA-256(`eami-admin-audit-genesis-v1` + org id). Written under `pg_advisory_xact_lock(hashtextextended('admin_audit:'||org))` with a 5 s `lock_timeout`, READ COMMITTED required (checked).

**Registered action codes (only presets, assignment, enrollment keys):** `preset.created`, `preset.draft_updated`, `preset.published`, `preset.reverted`, `preset.archived`, `preset.unarchived`, `preset.bulk_assigned` (one event with a count), `endpoint.preset_assigned`, `endpoint.preset_unassigned`, `enrollment_key.created`, `enrollment_key.revoked`, `enrollment_key.exchanged`. The rest are reserved in the Part A report §7 for B-224. (`preset.bulk_assigned` replaces Part A's `endpoint.preset_bulk_assigned`: the target of a bulk adopt is the preset.)

**Summary rules:** preset `name` and `description` → "changed" only, no hash; scalar fields → `sha256:`+16-hex old/new hashes; `model_scan_paths` → whole-list and per-item hashes plus added/removed counts; `scanners` → added names must be in `AllScanners`, removed names only need to look like a name (so a retired scanner can still be removed); codes from the config-limit allowlist; typed refs (IDs, versions, count). Validation re-checks every shape, so a hand-built struct can't carry a value.

**For Slice 1:** handlers call `s.queries.RunAudited(ctx, uc.OrgID, func(tx) (event, error) {...})` with the change on `tx`, then `writeAdminAuditFailure(w, r, err)` to map an audit failure to 500 `audit_write_failed`. Actor via `adminAuditActor(uc)`, request ID via `adminAuditRequestID(r)`. **Slice 1's handler tests must use the throwaway-database pattern:** an org with audit events can never be deleted.

---

## 4. Tests: the 12 adversarial tests plus 3 from the reviews

All run in per-test **throwaway databases** on the real Postgres server (`bootstrap_test.go` pattern), so nothing is written to or deleted from the shared dev database.

| # | Test | Proves |
|---|---|---|
| 1 | `ListIsolation` | B's admin sees only B's events; A's target or actor filtered from B gives bytes identical to a nonexistent ID; A sees its own, with the actor email joined |
| 2 | `OrgIDParamRejected` | `org_id` (and any unknown parameter) is a 400 on both routes; bad values are 400 and never echoed |
| 3 | `VerifyIsolation` | tampering in A fails A's verify only, and vice versa; HTTP verify covers only the caller's org |
| 4 | `SpliceAcrossOrgs` | A's chain moved to org C row for row (ids, seq, hashes kept) fails at seq 1; A's emptied chain reads as "no events" (a stated limit) |
| 5 | `ConcurrentAppends` | 25×2 parallel appends in two orgs: both chains valid and gap-free, no cross-org links |
| 6 | `StaleWriterFailsClosed` | REPEATABLE READ and SERIALIZABLE appends are refused; a writer skipping the lock and reusing a seq hits `23505` |
| 7 | `RollbackAndFailClosed` | a failed change leaves no event; a held chain lock makes the append fail with `55P03` in ~5 s, the change rolled back; HTTP mapping is 500 `audit_write_failed` with no SQLSTATE or lock text; append then rollback leaves no event |
| 8 | `StoreScopedByOrg` | list, count and verify scope by org at the store layer alone |
| 9 | `RoleGate` | 401 without a token; 403 for operator, viewer, approver, platform_admin; 200 for admin |
| 10 | `NoValuesInRows` | sentinel key, path, name and description fed through every constructor never appear in any column or the API; hand-built values, unknown fields, out-of-list names, unknown codes, hashes on free text and unregistered actions are refused; off-format request IDs are dropped |
| 11 | `AppendOnlyAndDownMigration` | UPDATE, DELETE, TRUNCATE refused (`42501`); deleting the org fails on `admin_audit_events_org_id_fkey` (`23503`); down migration refused while rows exist; down/up works on an empty table |
| 12 | `TamperMatrix` | detected: column edit, timestamp edit, interior deletion, deletion plus renumbering, reordering, forged replacement row. **Not detected (asserted):** removal of the newest rows; a full recompute by a database administrator, using an independent reimplementation of the documented algorithm |
| 6b | `RunAuditedPinsOrg` | an event naming another org (or no org) is refused, and the change rolls back (security review M3) |
| 6c | `QueryDisciplineAndVerifyLimit` | duplicate parameters and bad `page`/`per_page` are 400 without echo; verify is rate limited per org (code review M1/L2, security review M2) |
| 6d | `RetiredEnumRemoval` | a scanner name retired from the closed list can still be recorded as removed (code review L4) |

**Deliberate-breakage proofs (each mutation applied alone, suite rerun, file restored):**

| Mutation | Caught by |
|---|---|
| M1 list SQL without the org filter | 1, 8 |
| M2 no chain lock | 5, 7 |
| M3 global genesis **and** org_id out of the hash | 4, 12 |
| M3a org_id out of the hash only | 12 (test 4 still passes: per-org genesis alone catches the splice) |
| M3b global genesis only | 12 (test 4 still passes: org_id in the hash alone catches the splice) |
| M4 no append-only triggers | 11 |
| M5 routes moved to the viewer read group | 9 |
| M6 audit failure ignored, commit anyway | 7, 10 |
| M7 request ID kept unchecked | 10 |
| M8 summary values not checked | 10 |
| M9 verify reads every org | 3, 4, 5, 8 |
| M10 `RunAudited` doesn't pin the org | 6b |
| M11 duplicate query parameters allowed | 6c |
| M12 verify not rate limited | 6c |
| M13 isolation level not checked | 6 |

**15 of 15 caught.** M3a and M3b show the two org-binding layers are redundant for a whole-chain splice: each alone still catches it. That's defence in depth, not a gap.

**Suites:** `go build ./...`, `go vet ./...` clean. `eami-api` `go test ./...`: **631 passed, 0 failed** (real Postgres). `schema/migrationtest` (fresh vs incremental schema equality, `GOWORK=off`): pass.

---

## 5. Reviews (both mandatory; security review required)

**Security review:** no Critical or High.

| Finding | Disposition |
|---|---|
| M1 B-299 must also move table ownership, not just drop superuser | **Recorded on B-299's scope** (owner/migration role; app role `SELECT, INSERT` only) |
| M2 verify unbounded | **Fixed:** read-only transaction with a 30 s `statement_timeout`, plus a per-org rate limit (6 a minute, 429 with `Retry-After`); test 6c |
| M3 org scoping of writes rests on caller discipline | **Fixed:** `RunAudited(ctx, orgID, change)` refuses an event for any other org; test 6b, mutation M10 |
| L1 free-text fields got guessable hashes | **Fixed:** `name` and `description` record "changed" only (`AdminAuditFieldChanged`); hashes refused |
| L2 down-migration check-then-drop race | **Fixed:** `LOCK TABLE … ACCESS EXCLUSIVE` first (the file runs as one implicit transaction) |
| L3 `request_id` client-spoofable | Accepted per B0b-5; documented in C24 as correlation only, never authority |
| L4 audit-failure log carried the client's path | **Fixed:** logs the chi route pattern |
| L5 `CREATE TABLE IF NOT EXISTS` could keep a weaker table | Accepted (repo migration convention); `migrationtest` compares fresh vs incremental schemas |
| I1–I4 (org deletion blocked, app clock for `occurred_at`, one org per transaction, lock-key namespacing) | I1 recorded on B-224 (erasure); I3 now enforced by the org pin; I2 and I4 noted |

**Code review:** no High.

| Finding | Disposition |
|---|---|
| M1 duplicate query parameters silently ignored | **Fixed:** 400; test 6c, mutation M11 |
| L2 bad `page`/`per_page` defaulted | **Fixed:** 400 for non-integers, < 1, or `per_page` > 100 |
| L3 READ COMMITTED assumed | **Fixed:** checked, other levels refused; test 6, mutation M13 |
| L4 closed list blocks removing a retired scanner | **Fixed:** removals need only a name shape; test 6d |
| L5 down-migration race | Fixed (as security L2) |
| L6 weak test assertions (any FK error; lock-timeout cause; holder rollback) | **Fixed:** `23503` + constraint name; `55P03`; `defer holder.Rollback` |
| L7 gofmt | **Fixed** (the new files and `router.go`'s realigned struct only) |
| L8 drift headline count | **Fixed:** 120 routes |
| Info: external verifier needs the algorithm; long hostnames drop chi's request ID; list/count drift under concurrent appends; Slice 1 tests need throwaway DBs | Algorithm now in C24; the rest noted here and in §3 |

**Standing checks:** no raw error text across a trust boundary (both reviews: pass); no orphaned actions or links (purely additive; the existing `/v1/audit`, `/export` and `/verify` still serve viewers live, §6); drift row C24 in the same commit (pass).

---

## 6. Live verification against the real database and stack

1. **Backup first:** `eami_20261006T063337Z.dump` (the backup service's rotation pruned its oldest file as usual).
2. **Migration up/down/up** on the real dev database: version 28 → 29 (table plus 2 triggers) → 28 (table gone; it was empty) → 29. `audit_log` untouched (1706 rows before and after).
3. **API rebuilt** (`docker compose up -d --build eami-api`), healthy on 8081.
4. **Routes, live (22 of 22 passed),** with fixture users created for this check (Dev Org admin, operator, viewer and approver, plus an admin in a second org):
   - admin: list 200 with an empty trail; verify `valid: true, checked: 0`, with the guarantee text;
   - the other org's admin: its own empty trail;
   - operator, viewer and approver: 403 on both routes; no token: 401;
   - `org_id`, duplicate `action`, unknown action, `per_page=101`, a bad `target_id`: 400, no value echoed;
   - a hostile `X-Request-Id` is never echoed;
   - verify rate limited (429) per org; the other org unaffected;
   - existing `/v1/audit` and `/v1/audit/verify` still served to viewers.
5. **TRUNCATE on the real table is refused** (`admin_audit_events is append-only (TRUNCATE refused)`).
6. **Write path:** proven in throwaway databases (§4), not live. 0b has no wiring, and no synthetic event was planted in Dev Org's permanent trail (B0b-8). The first real events arrive with Slice 1.
7. **Cleanup:** the 5 fixture users soft-deleted (refresh tokens removed); `admin_audit_events` has 0 rows.

---

## 7. Limitations and follow-ups

- **Not tamper-proof against a database administrator** (§1); head anchoring folds into B-135.
- **B-299** (minted this slice): the superuser app role removes the tamper-resistance claim for every audit table, 0b's triggers included.
- **B-298** (minted this slice): `audit_log`'s 2028 partition cliff, **High** (§2).
- **B-224:** never-delete conflicts with future erasure obligations (org offboarding, GDPR); needs a documented export-then-purge procedure. Recorded there.
- **`occurred_at`** is the API server's clock; `seq` is the authoritative order.
- **No UI** (Slice 2 or B-224, under the Audit page).
