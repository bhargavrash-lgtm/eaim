# B-301 + B-302: verification record

**Date:** 2026-10-07 (build started 2026-10-06) · **Author:** Claude Code · **Plan:** `B-301_LIVE_TEST_AND_FIX_PLAN.md` §4 (approved). **Design:** `AGENT_IDENTITY_DESIGN.md` Slice A1.
**Founder decisions:** revoking an API key also revokes every live token issued with it, in one transaction with the key change, written to the admin audit trail; if that can't be written, the key revocation fails with a stable error. Conditions 1–7 (§3).

---

## 1. What suspend does and does not guarantee (plain statement)

**Suspending, revoking or deleting a governed agent, or revoking its token or the API key the token was issued with, guarantees:**
- **Its next call is refused**, on every gateway node, whether or not any notification arrives. Every tool call and every workflow step goes through `Dispatcher.Dispatch`, which checks the agent's status, the token and the issuing key against the database first. The refusal is audited as `denied` (nothing is dispatched), and the MCP session ends with a fixed error (`-32001 unauthorized: session ended`).
- **`tools/list`, new MCP sessions and episode reads are refused too**, by the same database check.
- **An approved escalation that waited for approval is re-checked before it runs**; if the agent or token is no longer live it is not executed (`resume_outcome = agent_not_active`).
- **Open sessions usually end at once.** A notification (`agent_status`, `token_revoked`) closes them on every node within a round trip. That's an optimisation: a node that misses it still refuses the next call, re-checks its sessions on every listener reconnect, and every 60 seconds.
- **Tokens are bound to the exact governed agent row and key** they were issued for: deleting an agent and re-creating one with the same name never revives an old token.
- **It fails closed:** if the database can't be reached, the call is refused and the session ends.

**It does not guarantee:**
- **A call already running when the suspension lands is not stopped.** A call that passed the check finishes; the longest it can run is **30 seconds** (the downstream timeout of the static forward, the tool router and the Claude adapter). Later calls, and later workflow steps, are refused.
- **It does not take effect on an open session before that session's next call** if the notification is lost and the 60-second catch-up hasn't run yet: the session stays open but can't do anything.
- **A database outage ends sessions** (fail closed): agents reconnect once it's back.
- **It doesn't revoke anything outside the gateway**: credentials the agent already obtained from a tool, or results already delivered.

## 2. What was built

**eami-gateway**
- `internal/registry/liveness.go` (new): `CheckLive(agent, org, jti, key)`, one primary-key query: agent `status` (by id **and** org), token in `revoked_ai_tokens`, issuing key `revoked`.
- `cmd/gateway/dispatcher.go`: `AgentLiveness` is a required `NewDispatcher` argument; `Dispatch` checks it first (`rejectOnNotLive`: audit row `denied`, hooks, `mcp.ErrNotLive`); nil checker or error refuses; approved-call refusals map to `ErrNotLive` (`notLiveOr`).
- `internal/approval/router.go`: resume-time re-check (database-backed by default, fails closed), `approval.ErrAgentNotLive`, `resume_outcome = agent_not_active` (**migration 000030**).
- `internal/identity`: tokens gain server-set `agent_uuid` and `api_key_id` claims (`Claims.BoundTo`); a revocation write also notifies `token_revoked` in the same statement; `IsRevoked`/`MarkRevoked`/`ReloadRevoked`; fixed `MsgUnauthorized`/`MsgForbidden` (B-302); the revoke route's 403 no longer echoes errors.
- `internal/mcp`: `ActionContext.TokenID`/`APIKeyID`; binding and live check at SSE open; per-message in-memory pre-check (expiry, local revoked set) after decoding, with the real request id; `tools/list` live check; sessions end on `ErrNotLive`; queued events drain before the end-of-session event; `SessionManager.CloseWhere`/`Snapshot`; `LivenessListener` (new): `agent_status`/`token_revoked`, payloads validated and confirmed in the database, catch-up on every reconnect and every 60 s, queries outside the session lock with a 2 s timeout.
- `internal/workflow/http.go`, `internal/episode/http.go`: binding (and, for episodes, the live check); fixed 401/403.

**eami-api**
- `internal/store/agent_liveness.go` (new): `NotifyAgentStatus`; `RevokeAPIKeyAndTokens` (key revoke, `revoked_ai_tokens` + `ai_token_events` + `token_revoked` for that key's tokens from the last 4 h, org-scoped).
- `RevokeAPIKey` runs inside `store.RunAudited` (Slice 0b) with action `api_key.revoked` (refs `agent_id`, `count`); 404 `not_found`, 500 `key_revocation_failed`, 500 `audit_write_failed`; never raw text. The dead `store.RevokeAPIKey` was removed.
- `UpdateAgent`/`DeleteAgent` notify `agent_status` (best-effort).

**Docs:** drift rows **C25** (gateway 401/403, `-32001`, end-of-session text, token claims and cutover) and **C26** (key revocation); `CHANGELOG.md` release notes.

## 3. The founder's conditions

| # | Condition | Where it's met |
|---|---|---|
| 1 | Per-call check fails closed on an unreachable database, with a test | `notLiveReason` (error or nil checker refuses); `TestB301_FailClosed_DatabaseUnreachable` (closed pool, and no checker); mutations M9, M9b |
| 2 | Notifications are an optimisation; a test where one is dropped and the next call is still refused | Documented in `LivenessListener`, `AgentLiveness`, §1; `TestB301_NotificationDropped_NextCallStillRefused` and `T3b` (second node, no listener); mutation M1 |
| 3 | In-flight limit stated | §1: a call past the check finishes, **at most 30 s**; later calls and workflow steps refused; escalations re-checked at resume |
| 4 | Open sessions at deploy, in the release notes | They **end** (sessions live in gateway memory); and every token minted before the deploy is refused (no `agent_uuid`), so agents fetch a new token. `CHANGELOG.md` |
| 5 | Per-call cost re-measured inside Docker | §5 |
| 6 | Leftover fixture agents marked | §6 |
| 7 | B-302 fixed in the same change, with tests | `TestB302_FixedUnauthorizedAndForbiddenBodies` (SSE, workflow-run, episode; unknown vs suspended byte-identical 403; malformed, revoked, pre-cutover byte-identical 401); mutation M11 |

## 4. Tests and deliberate breakages

**Gateway (`cmd/gateway/b301_liveness_pg_test.go`, throwaway database per test, counting downstream):** T1 suspend → next call refused; T2 notification closes the stream; T3 revocation reaches a second node; T3b second node without notification; condition 2; T4 workflow step after suspend (Dispatch called as the executor calls it); T5 expired token dispatches nothing; T6 reactivation works; T7 same-name agent in another org untouched; T8 hostile and forged notifications ignored (barrier-synchronised); condition 1; approved call not executed after suspend, and after token revocation; B-302 bodies; delete-and-re-create; revoked key with no event rows; `tools/list`; episode read with a database-only revocation; in-memory pre-check; reconnect catch-up; unbound (pre-cutover) token on SSE, workflow-run and episode routes (fixed 403, same as an unknown agent). **22 tests**, plus `TestRateLimitRunMiddleware_UnboundTokenDoesNotConsumeQuota` in `internal/workflow`.

**eami-api (`internal/api/b301_key_revocation_test.go`):** T9 key revocation revokes exactly the live tokens (not older than 4 h, other keys, deleted agents, or another org's), audit event and chain, notifications; T9 fail-closed (unknown key and other org's key 404; blocked token write → 500 `key_revocation_failed`, key unrevoked, nothing recorded).

**Deliberate breakages: 26 of 26 caught.**

| Mutation | Caught by |
|---|---|
| M1 no per-call check in Dispatch | T1, T3b, T4, T6, condition 1, condition 2, revoked key |
| M2 `agent_status` ignored | T2, T7 |
| M3a revocation not notified | T3, T8 |
| M3b per-call check ignores revoked tokens | T3b, episode read, resume (token) |
| M5 no session expiry (both layers) | T5 |
| M6 suspension made sticky | T6 |
| M7 sessions closed by agent name | T7 |
| M8 `token_revoked` payload not validated | T8 |
| M9 / M9b database error / no checker treated as live | condition 1 |
| M10 no resume-time re-check | resume (suspend), resume (token) |
| M11 403 echoes the error | B-302 |
| M12 key revocation skips its tokens | T9, T9 fail-closed |
| M13 key revoked outside the transaction | T9 fail-closed |
| M14 SSE token not bound to the agent row | delete-and-re-create, unbound token |
| M15 per-call check ignores a revoked key | revoked key |
| M16 `tools/list` not checked | `tools/list` |
| M17 episode reads not checked | episode read |
| M18 pre-check ignores the local revoked set | pre-check |
| M19 no catch-up on reconnect | reconnect catch-up |
| M20 resume refusal not mapped to `ErrNotLive` | resume (token) |
| M21 `agent_status` not confirmed in the database | T8 |
| M22 workflow-run token not bound | unbound token |
| M23 SSE open skips the live check | revoked key |
| M24 rate limiter counts unbound tokens | `TestRateLimitRunMiddleware_UnboundTokenDoesNotConsumeQuota` |

(M21's first mutation didn't compile; a corrected one was caught.)

**A shared-database test was moved:** `TestRBACSplit_*` revoked a key on the shared dev database, which now writes a permanent admin audit event and makes its org undeletable. It now runs in a throwaway database (`newThrowawayWorkspaceTestEnv`). One org had already leaked from an earlier full-suite run; it's marked as a fixture (§6).

**Suites (final code):** `go build`, `go vet` clean in both modules. eami-gateway `go test ./...` **379 passed, 0 failed** (re-run green after the re-review fixes, plus the new rate-limit test); eami-api **633 passed, 0 failed** (real Postgres); `schema/migrationtest` (fresh vs incremental schema equality, including 000030) **pass**. No test org leaked from the final run.

## 5. Measured cost of the per-call check

| Where | p50 | p95 | p99 |
|---|---|---|---|
| The exact query, from a container on the compose network next to Postgres (2000 runs) | **0.27 ms** | **0.37 ms** | **0.47 ms** |
| The same query from the host (through Docker Desktop's port forward) | 1.13 ms | 1.74 ms | 2.07 ms |
| In-memory pre-check (expiry, revoked set) | ~7–9 ns | | |
| End to end through the built gateway, policy-denied path (`audit_log.latency_ms`, 40 calls) | 4 ms | 5 ms | |

- For scale: before the change the same path measured 3–7 ms, and the gateway's own work is small next to any real downstream call.
- The container measurement ran while the mutation suite was using CPU, so it's slightly pessimistic.
- The reviewed build adds one indexed `EXISTS` on `api_keys` to the same query.

## 6. Live verification (real stack, fixture data only)

**The fixture was fixed first.** Calls use `b301tool.read` (the correct `<tool>.<action>` separator), and a fixture deny policy matches tool `b301tool`. The baseline call's audit row must carry that policy's id, or the script aborts before any further call. **Proven on every run.**

| | Before (old build) | After (new build, re-run on the reviewed build) |
|---|---|---|
| Suspended agent, first call on its open session | **processed** by policy (audit row with the policy id) | **refused**: the session had already ended (notification), so the call got 404, nothing dispatched, no audit row |
| Revoked key: call on the token's open session | **processed** | **refused** (session ended) |
| Revoked key: new session with that token | **200** | **401** (fixed text) |
| Revoked key: token in `revoked_ai_tokens` | 0 | 1 |
| Second node: new session with a token revoked on node 1 | **200** | **401** |
| Second node: call on a session opened before the revocation | **processed** | **refused** (session ended) |

**Deploy:** backup `eami_20261006T135307Z.dump`; migration 000030 applied on the real database (version 30); eami-api and eami-gateway rebuilt and healthy; the gateway logs `mcp/liveness: LISTEN agent_status, token_revoked active`.

**Fixtures and cleanup (condition 6):**
- 10 governed agents `b301-*` remain, because their episode history blocks deletion. All suspended or revoked, `owner` = "TEST FIXTURE (B-301 live test, 2026-10-06): inert, do not use", `scope` = "TEST FIXTURE: kept only because its episode history blocks deletion".
- All `b301-*` keys are revoked, fixture policies deleted, fixture admins soft-deleted, the second node removed.
- **Admin audit trail:** 9 real `api_key.revoked` events in Dev Org from the live runs' key revocations (gap-free chain). They are real admin actions and stay.
- **Leaked test org:** `b253-rbac-9723bccb` (from the full-suite run before the RBAC test was moved) holds 1 admin audit event and can't be deleted; its name now reads "TEST FIXTURE: leaked by B-301 suite run …".
- Audit rows from the tests stay.

## 7. Reviews

**Security review** (required). Two Highs, both fixed:
- **High 1:** an agent deleted and re-created under the same name revived old tokens → tokens bound to the agent row (`agent_uuid`), checked everywhere a governed agent is resolved; pre-change tokens refused (hard cutover); an unbound token gets the same fixed 403 as an unknown agent.
- **High 2:** key revocation depended on asynchronous `ai_token_events` rows → tokens carry `api_key_id`, and the per-call check refuses a revoked key's tokens directly.
- **Medium 3:** the catch-up queried the database under the session lock → fixed (snapshot, 2 s timeout).
- **Medium 4:** `tools/list`, episode reads and new sessions relied on memory only; a silently deaf listener → live checks added, 60 s periodic catch-up.
- **Low 5:** resume refusal not `ErrNotLive`, and the nil checker failed open → fixed.
- **Low 6:** raw error text in pre-existing gateway replies → the revoke route's 403 fixed; the rest proposed as a follow-up (§8).
- **Low 7:** forged notifications → confirmed in the database before acting.
- **Low 8:** a database outage ends sessions → documented.

**Code review:**
- **Medium 1:** gofmt → fixed.
- **Medium 2:** the catch-up lock → fixed, as security Medium 3.
- **Medium 3:** key revocation via events only → fixed, as security High 2.
- **L1:** null JSON-RPC id → fixed.
- **L2:** `tools/list` → fixed.
- **L3:** test gaps → tests added (pre-check, catch-up, resume with a revoked token, barrier in T8).
- **L4:** comment order → fixed.
- **L5:** the revoke 403 → fixed.
- **L6:** dead code → removed.
- **L7:** the 4 h constant duplicated → cross-referenced on both sides.

Standing checks: orphans none; drift rows C25 and C26 present.

**Security re-review of the fixes:** no Critical or High; **all seven original findings resolved.** New:
- **Low A:** the workflow rate limiter counted unbound tokens → fixed (`BoundTo` before counting) with a test.
- **Low B:** the key check cast to text and couldn't use the index → fixed (nullable uuid parameter).
- **Info 3:** unbound token (401) vs unknown agent (403) revealed a re-created name → unbound is now the same fixed 403.
- **Low C**, the catch-up checks sessions one at a time (bounded by the 2 s timeout; the per-call check still guards every call), is recorded, not changed.
- **Info 1**, an empty `api_key_id` skips the key check, which no production token can hit: `HandleIssue` always sets it, and older tokens fail the `agent_uuid` cutover. Recorded as hardening for any future second issuance path.
- **Info 4:** deploy all gateway nodes together (a rolling deploy would briefly refuse tokens minted by old nodes).

## 8. Limitations and follow-ups

- **In-flight calls finish** (at most 30 s).
- **Pre-existing raw error text** reaches agents in some gateway replies:
  - JSON-RPC `-32000` dispatch and `tools/list` errors;
  - JSON parse errors;
  - workflow-run and episode non-auth errors.

  **Minted as B-303** (2026-10-07).
- **`revoked_ai_tokens` still cascades on agent delete.** That's harmless now that tokens are bound to the agent row, but the revocation record is lost with the agent.
- **No UI** for the new audit action or resume outcome.
- **Live:** the notification path was exercised; the dropped-notification path is proven by tests, not live.
