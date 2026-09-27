# B-237 Verification Record: approvals referencing another org's agents and policies

Written 2026-09-27 by Claude Code. The issue was found by the org-ownership sweep (`ORG_BRANCH_ASYMMETRY_SWEEP.md`) and reported immediately. The founder issued an urgent fix brief.

**Checkability.** Everything below quotes command output or review reports verbatim.
- **Raw logs** are in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `sweep_approvals_live.log` (pre-fix), `b237_live_fixed.log`, `b237_live_final.log`, `b237_mutation.log`, `b237_api_full2.log`, `b237_gw_approval.log`, `b237_before.txt`, `b237_after.txt`, `b237_after2.txt`.
- **Review transcripts** are in that session's subagent records.

## 1. Part A: does the gateway call `POST /v1/approvals`? No.

- **No caller exists.** A scoped search of `eami-gateway`, `eami-api`, `eami-collector`, `eami-agent`, `eami-policy`, `eami-ui/src` and `scripts` found no caller of `POST /v1/approvals` (the UI only lists, gets and decides).
- **The comment was wrong.** The handler's comment "called by the gateway on ESCALATE" was false, and it has been corrected.
- **How the gateway creates escalations:** it inserts them **directly into the database**, in `eami-gateway/internal/approval/router.go` `Submit`. The `approval.Request` is built in `cmd/gateway/dispatcher.go` from `ac.OrgID`/`ac.AgentUUID`, the **server-resolved agent identity** hardened by B-128/B-141, and it never sets `policy_id`.
- **So the fix cannot touch the gateway's path.** Nothing in `eami-gateway` changed (`git diff` is empty). The gateway's real-Postgres approval suite passes, 26/0/0, including `TestSubmit_WritesAllNotNullColumns`. It was also live-verified end to end (§4).

## 2. The fix: two independent layers, each separately tested

| Layer | Change |
|---|---|
| **1: handler** (`approvals.go` `CreateApproval`) | `GetAgent(agent_id, uc.OrgID)`, then, when `policy_rule_id` is given, `GetPolicy(id, uc.OrgID)`. Each returns 404 ("agent not found" / "policy not found"), **identical for a foreign and a nonexistent id**. DB errors return a generic 500 plus `slog`, with no more `err.Error()` echo. An unparseable `policy_rule_id` now returns 400 instead of being silently dropped (a review Low). |
| **2: SQL** (`store/approvals.sql.go`, mirrored in `store/query/approvals.sql`) | `INSERT … SELECT … FROM gateway_agents a WHERE a.id=$2 AND a.org_id=$1 AND ($13::uuid IS NULL OR EXISTS (SELECT 1 FROM policies p WHERE p.id=$13 AND p.org_id=$1))`. A foreign or nonexistent reference writes nothing and returns `ErrNoRows`, never an FK error. |

**Why layer 2 has its own 404 message ("approval references not found"):**
- It keeps each layer's removal **separately detectable**, the lesson from B-233's discipline.
- It is reachable only if layer 1 is bypassed, or in a same-org race such as a concurrent delete.
- Both reviews confirmed it is not an oracle: layer 1 and layer 2 use identical predicates, so a foreign or nonexistent ID can never reach layer 2 through the handler.

## 3. Automated verification

**Commands and results**
```
eami-api:     go build ./... ; go vet ./internal/api ./internal/store → ok; gofmt diff counts unchanged vs HEAD (pre-existing) / new test file clean
eami-api:     POSTGRES_PASSWORD=… go test -count=1 -v ./... → every package ok; PASS=486 FAIL=0 SKIP=0
eami-gateway: go test -count=1 -v ./internal/approval/... → ok; PASS=26 FAIL=0 SKIP=0
```

**New tests** (`approval_org_pg_test.go`, real Postgres):
- **Cross-org** (operator and admin attackers):
  - A foreign agent and a nonexistent agent give 404s **byte-identical to each other**, and pinned to "agent not found".
  - The same holds for a foreign vs a nonexistent policy ("policy not found").
  - A foreign agent plus a foreign policy is also rejected.
  - 0 rows reference the victim afterwards, and **the victim can still delete its own policy and agent** (204).
  - An unparseable `policy_rule_id` returns 400.
  - A `t.Cleanup` registered after the org seeds removes any cross-org rows on a failure path (a review Low).
- **Same-org:** 201 with and without the org's own policy, both persisted; a viewer gets 403.
- **Store level** (layer 2 alone): a foreign agent, a nonexistent agent, the org's own agent with a foreign policy, and the org's own agent with a missing policy each return `ErrNoRows` and create no row. The org's own agent and policy succeed.

**Mutation check** (`b237_mutation.log`, verbatim). Each part is broken separately:
```
=== M1: original HEAD handler + store (the B-237 bug itself)
--- FAIL: TestCreateApproval_CrossOrgReferencesRejected_RealDB
    operator: approval for another org's agent = 201 want 404: {"id":"ac370d0f-…","agent_id":"cddbecc4-…",…}
--- FAIL: TestCreateApprovalStore_OrgScopedInsert_RealDB
    store insert with foreign agent: err=<nil>, want pgx.ErrNoRows (no row, no FK error)
=== M2: handler AGENT ownership check removed only
--- FAIL: TestCreateApproval_CrossOrgReferencesRejected_RealDB
    operator: foreign-agent response "{\"code\":\"not_found\",\"message\":\"approval references not found\"}\n", want the ownership check's "agent not found"
=== M3: handler POLICY ownership check removed only
--- FAIL: TestCreateApproval_CrossOrgReferencesRejected_RealDB
    operator: foreign-policy 404 "…approval references not found…" / nonexistent-policy 404 "…approval references not found…", want both the ownership check's "policy not found"
=== M4: SQL org scoping removed only (handler checks kept)
--- FAIL: TestCreateApprovalStore_OrgScopedInsert_RealDB
    store insert with foreign agent: err=<nil>, want pgx.ErrNoRows (no row, no FK error)
sources restored OK
```

**An incident during the mutation runs, disclosed.** The M1 and M4 runs used the old, unscoped code, so they really created cross-org approval rows *in the test orgs*. That **blocked the victim test orgs' cleanup delete**: three `b237-*` orgs leaked. This is B-237's own delete-blocking effect, reproduced by accident.
- The attacker orgs were deleted afterwards, and their rows cascaded away.
- The three leaked orgs then had no remaining references and were deleted.
- The org count is back to the baseline 6 (pre-existing orgs only).
- The test now registers a `t.Cleanup` that removes cross-org rows first, so a future regression can't leak orgs this way.

## 4. Live verification (shared stack)

**Before the fix** (from the sweep, on the same pre-fix container; `sweep_approvals_live.log`, verbatim):
```
ATTACKER (other org, operator) POST /v1/approvals with victim agent+policy : 201 {"id":"2ff40758-…","agent_id":"60f254bc-…","status":"pending","policy_id":"58ddd3ec-…",…}
ATTACKER POST /v1/approvals with a NONEXISTENT agent id                     : 500 {"code":"internal_error","message":"ERROR: insert or update on table \"approval_requests\" violates foreign key constraint \"approval_requests_agent_id_fkey\" (SQLSTATE 23503)"}
VICTIM OWNER DELETE own agent                                               : 409 {"code":"conflict","message":"cannot delete an agent with existing episode, approval, or workflow-run history -- suspend it instead (this preserves its audit trail)"}
VICTIM OWNER DELETE own policy                                              : 500 {"code":"internal_error","message":"ERROR: update or delete on table \"policies\" violates foreign key constraint \"approval_requests_policy_id_fkey\" …"}
```

**On the final fixed build** (API started 2026-09-27T12:26:15Z; `b237_live_final.log`, verbatim):
```
── (1) the exact attack from the sweep, on the FIXED build
ATTACKER POST /v1/approvals with victim agent+policy                           : 404 {"code":"not_found","message":"agent not found"}
ATTACKER POST /v1/approvals with victim agent only                             : 404 {"code":"not_found","message":"agent not found"}
ATTACKER POST /v1/approvals with a NONEXISTENT agent id                        : 404 {"code":"not_found","message":"agent not found"}
rows referencing victim agent/policy                                           : 0
── (2) same-org approval creation on the FIXED build
OWNER POST /v1/approvals with own agent+policy                                 : 201 {"id":"d070b7b0-…",…,"status":"pending",…}
── (3) gateway real escalation path, end to end
create agent API key                                                           : 201
gateway token issue                                                            : 200 (token received)
open MCP SSE session                                                           : 200
SSE endpoint event                                                             : /v1/mcp/messages?sessionId=38934aff…
POST tool_call manual_verify_tool.read (matches the escalate policy)           : 202
gateway-inserted approval row (direct DB insert, not via eami-api)             : c18f581f-…|org=4142489b-…|agent=0eeeccce-…|node=7654598e4a32|status=pending (rows before=0)
OWNER denies the gateway approval via POST /v1/approvals/{id}/decide           : 200 {"id":"c18f581f-…","agent_name":"gwfix-agent","tool_name":"manual_verify_tool","action":"read",…}
SSE result delivered to the agent                                              : {"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"approval denied: b237 live verification"}}
```

**The gateway path in step (3), end to end:**
1. A fixture agent's API key was exchanged for a gateway token.
2. The agent opened a real MCP SSE session.
3. It sent a real `tool_call` that matched Dev Org's existing, active "manual verify" escalate policy (priority 1, tool `manual_verify_tool`, any agent).
4. The **gateway itself** inserted the approval, with its own node hostname.
5. The approval was denied through the real API, and the denial reached the agent over SSE.

A first run of the same script on an earlier fixed build, before the review-Low follow-ups, gave identical results (`b237_live_fixed.log`).

**Cleanup (both cycles):**
- One transaction removed the fixture approvals, the gateway-recorded episode, the fixture API key and its token-issue event, the fixture agents and policy, the fixture user, and the attacker org.
- `diff b237_before.txt b237_after.txt` and `diff b237_before.txt b237_after2.txt` were both **identical**. The snapshot covers org, user, agent, config, lifecycle, approval, endpoint and tool counts; every agent's status and config row; every workspace membership; and api_keys, episodes and ai_token_events counts.
- **The one deliberate residue:** **+2 `audit_log` rows per E2E cycle** (the escalation and its denial). The audit log is **hash-chained and tamper-evident**, so deleting rows would break chain integrity. Leaving them follows B-099's established precedent for live gateway verification.

## 5. Reviews (both mandatory passes completed; quoted verbatim)

After both reviews, two things changed:
- **the `policy_rule_id` 400** (both reviews' Low);
- **`t.Cleanup` hardening in the tests** (the code review's Lows).

Both are covered by the rerun suite (486/0/0) and the final-build live cycle above. They were not sent back to a reviewer.

### 5a. Code review

> **Verdict: approve.** I found no High or Medium issues. The B-237 fix is correct and the tests are real mutation-catchers. There are a few Low/Info items, mostly around test cleanup and error-path gaps that existed before this change.
>
> **What I ran:** `go build ./...` and `go vet ./internal/api ./internal/store` in eami-api, both clean. I did not run any `go test`, so no real-Postgres test ran and the SQL was not executed by me. Everything I say about SQL runtime behaviour comes from reading the code plus Postgres semantics.
>
> ## Findings
>
> **Low: same-org test cleanup is fragile**
> - File: `eami-api/internal/api/approval_org_pg_test.go` around lines 111–118 (same-org test) and 155–157 (store test).
> - Problem: the `DELETE FROM approval_requests` is a plain trailing statement, not a `t.Cleanup`. If any `t.Fatalf` fires before it (count≠2, the viewer-403 check, the store success insert), it never runs. Cleanup then depends on `seedTestOrg`'s org-delete cascade, and `seedTestOrg` ignores errors.
> - Fix: register `t.Cleanup(func(){ pool.Exec(ctx, "DELETE FROM approval_requests WHERE org_id=$1", orgID) })` right after `seedTestOrg`. It then runs before the org-delete cleanup (LIFO), and the pool is still open because `newWorkspaceTestEnv` registered `pool.Close` first.
> - Pool lifecycle itself is fine: no plain `defer pool.Close()`, it is `t.Cleanup`-only via `newWorkspaceTestEnv`, so this complies with the CLAUDE.md rule.
>
> **Low: the cross-org test leaks if the fix regresses**
> - File: `approval_org_pg_test.go`, cross-org test.
> - Scenario: if B-237 regresses, org A's approval rows point at org B's agent/policy. `t.Fatalf` fires, and org B's cleanup delete can then fail on the NO ACTION FK from a row in a different org. Org A's delete doesn't reach those rows by cascade in time, and the error is swallowed.
> - Result: a leaked org, only on the failure path.
> - Fix: a `t.Cleanup` that deletes `approval_requests WHERE agent_id=$victimAgent OR policy_id=$victimPolicy`.
>
> **Low / Info: claim that "NO ACTION FKs would otherwise block seedTestOrg's org delete" is likely overstated**
> - `approval_requests.org_id` is `ON DELETE CASCADE`, and NO ACTION is checked at end of statement. So an org delete that cascades both the approval rows and the agent/policy rows in the same org should normally succeed.
> - The test's own comment ("so seedTestOrg's cleanup … is not the only path") is worded softly and is fine; the claim as stated in your brief is the overstated part.
> - I did not verify this at runtime, and the order of nested RI triggers adds some uncertainty. Either way, the explicit delete is harmless.
>
> **Low (existed before this change): unparseable `policy_rule_id` is silently dropped**
> - File: `approvals.go` around lines 241–246.
> - A malformed id gives 201 with `policy_id` NULL. There's no security impact now, since nothing gets stored for it.
> - Fix: return 400 "invalid policy_rule_id", matching the `agent_id` handling.
>
> **Low (existed before this change): bad input returns 500 instead of 400**
> - An invalid `risk_level` (including an empty string, which the handler allows) or `environment` violates a table CHECK constraint.
> - That now correctly returns a generic 500 without leaking the error text (an improvement), but it should be a 400.
> - Separately, `int32(*req.EstimatedRecords)` can silently overflow.
> - Out of scope; could be logged in NOTES.md.
>
> **Info: the store-interface fallback still echoes `err.Error()`**
> - File: `approvals.go` around lines 293–306.
> - This branch only runs when `s.queries == nil`, i.e. `NewHandler` mock tests. `NewServer` always sets `queries`, so this is not a production path.
> - `queriesAdapter.CreateApproval` (`store_adapter.go:255`) is effectively dead in production. It never passes `PolicyID`, and it still gets org scoping from the SQL (layer 2), so it can't create a cross-org reference.
>
> **Info: same-org users can still make their own agent/policy undeletable**
> - Any same-org operator can make an agent or policy undeletable by creating approvals that reference it: the FK is NO ACTION and there is no approval-delete route.
> - This is intended and outside B-237's cross-org scope, but worth noting as a product decision.
>
> **Info: the distinct layer-2 message is not an oracle**
> - "approval references not found" is returned only when layer 1 has already confirmed both ids belong to the caller's org and the insert then finds no row. That only happens if the org's own agent/policy is deleted concurrently.
> - The layer-1 and layer-2 predicates are identical (`id` + `org_id` on the same tables: `GetAgent` → `gateway_agents`, `GetPolicy` → `policies`), so a foreign or nonexistent id can never reach layer 2 through the handler.
> - The only thing it can reveal is the caller's own org's race. The comment describing this is accurate.
>
> ## Verified clean
>
> **SQL (`approvals.sql.go:141–154`, mirrored in `query/approvals.sql`)**
> - `gateway_agents.id` is the primary key, so the INSERT…SELECT inserts at most one row.
> - RETURNING can only see the target table, not the `FROM` alias `a` or subquery `p`, so `approvalCols` (`id`, `org_id`, …) resolve to `approval_requests` without ambiguity.
> - Parameter types:
>   - `$1`, `$2` and `$13` are fixed to uuid by the predicates and by the explicit `$13::uuid` cast.
>   - The params that only appear in the select list are coerced to the target column types by Postgres's special handling for unknown params in INSERT…SELECT.
>   - The same param used both in the select list and in the WHERE resolves to one type (uuid), so there's no "inconsistent types" error.
> - This is based on my reading of Postgres behaviour, not an execution; the author's reported real-DB runs are what would confirm it.
> - `$13` NULL (`pgtype.UUID{Valid:false}`) → `$13::uuid IS NULL` is true, so no policy check applies, which is correct.
> - `policies.org_id` is NOT NULL, so there are no global policies that the EXISTS check would wrongly reject.
> - The `.go` string and the `.sql` file match (the `.sql` file adds a comment only).
>
> **Handler error paths**
> - Both `GetAgent` and `GetPolicy` use `QueryRow`/`Scan` and return the raw `pgx.ErrNoRows`, so `errors.Is` works.
> - `GetPolicy`'s LEFT JOIN with a policy that has no conditions scans NULLs into `pgtype`/`[]string`/COALESCE'd bool fields, so a seeded policy with no conditions does not produce a 500.
> - Foreign and nonexistent ids give byte-identical 404s. Other DB errors give a generic 500 plus `slog`. The old `err.Error()` leak on the queries path is gone.
>
> **Callers of `store.CreateApproval`**
> - Only `approvals.go:280`, `store_adapter.go:256` (above), and the new test.
> - `MockStore.CreateApproval` is separate. The existing `approvals_test.go` mock tests use `NewHandler`, so they don't touch `GetAgent`/`GetPolicy`.
>
> **Other writers of `approval_requests`**
> - Only `eami-gateway/internal/approval/router.go:433`. `org_id`/`agent_id` there come from `ac.OrgID`/`ac.AgentUUID`, set server-side in `cmd/gateway/dispatcher.go:632–640`.
> - No Go code outside eami-api's handler (gateway, collector, agent) calls `/v1/approvals`, so the corrected handler comment is true.
> - `DecideApproval` is an UPDATE scoped by `id` + `org_id` that sets only status/`approved_by` (the caller)/reason, so it writes no user-supplied agent/policy reference.
>
> **Mutation validity (from reading the tests)**
> - HEAD code → 201 for a foreign agent → the cross-org test fails.
> - Removing the handler's agent check → layer 2 returns "approval references not found" instead of the pinned "agent not found" → caught.
> - Removing the handler's policy check → caught the same way.
> - Removing the SQL's agent scoping → store test "foreign agent" returns nil error; "nonexistent agent" returns an FK error, not `ErrNoRows` → caught.
> - Removing only the policy EXISTS clause → store test "own agent, foreign/missing policy" → caught.
> - The same-org test guards against over-blocking (201 with and without a policy) and checks viewer → 403.
> - Routes `DELETE /v1/gateway/agents/{id}` and `DELETE /v1/gateway/policies/{id}` exist (`router.go:336,345`).

**Disposition:**
- **Test-cleanup Lows:** fixed.
- **Overstated-claim note:** accepted. The mutation-run leak in §3 shows the cross-org case does block cleanup; the same-org case likely doesn't.
- **`policy_rule_id` Low:** fixed (400).
- **Bad-input 500s and the int32 overflow:** recorded (§6).
- **Info items:** recorded.

### 5b. Security review

> **Verdict: B-237 is closed.** I found no High or Medium issues in the diff. I also swept every eami-api write that stores a reference id and found no other cross-org foreign-key write open. `go build ./...` and `go vet ./internal/api/ ./internal/store/` pass. As instructed, I did not run the real-Postgres tests. The SQL behaviour therefore rests on reading the code plus the new test file.
>
> ## Findings
>
> **Low — unparseable `policy_rule_id` is dropped silently** (eami-api/internal/api/approvals.go, the `if req.PolicyRuleID != nil` block, about lines 241-246)
> - **Scenario:** `"policy_rule_id":"garbage"` parses with an error, the error is ignored, and the approval is stored with policy_id NULL and a 201. No cross-org reference is possible this way, so this is not a hole in B-237. It is silent data loss and differs from `agent_id`, which returns 400.
> - **Fix:** return 400 "invalid policy_rule_id".
>
> **Low / Info — the route has no legitimate caller** (router.go:363, admin and operator)
> - `grep` finds no POST /v1/approvals caller in eami-ui/src, eami-gateway or eami-collector. The gateway inserts its own rows (router.go:433).
> - **Scenario:** an operator can still create fake "pending" approvals inside their own org. `agent_name`, `tool_name`, `action`, `justification`, `risk_level` and `gateway_session_id` are free text and not checked against the real agent. For example, agent_id=X can be shown with agent_name "Y", which misleads approvers.
> - These rows are inert (see the gateway section below), so this is spoofing within one org only, not cross-org.
> - **Fix:** remove the route, or restrict it and derive `agent_name` from the GetAgent row.
>
> **Low (existing, not in this diff) — raw DB error text in DecideApproval**
> - approvals.go:348 `writeError(..., err.Error())` on the 500 path, plus the mock-store branches (about line 305, `CreateApproval` mock, and line 369).
> - This is the same class as B-234. It is not an oracle between orgs because the UPDATE is org-scoped.
>
> **Info — IngestTokenUsage trusts `org_id`/`agent_id` from the body** (reports.go:306-363, `/v1/internal/token-usage`)
> - It does not check that the pair belongs together, and token_usage has no foreign key. Only a holder of `X-Service-Key` can reach it, so this is the service trust boundary working as designed. The risk is only FinOps misattribution if that key leaks.
>
> **Info — timing difference between foreign and nonexistent agent**
> - `GetAgent` looks up by primary key and then filters on org_id. A foreign id visits one heap row; a nonexistent id stops at the index.
> - The gap is microseconds and sits behind network jitter. I do not consider it exploitable.
>
> ## B-237 closure detail
>
> **All paths closed:**
> - Layer 1 is the handler. `GetAgent(id, org)` and, when a policy is given, `GetPolicy(id, org)` return the same 404 for foreign and nonexistent ids ("agent not found" / "policy not found"). Other errors return a generic 500 with the DB error logged, not echoed.
> - Layer 2 is the store. `INSERT…SELECT FROM gateway_agents a WHERE a.id=$2 AND a.org_id=$1 AND ($13 IS NULL OR EXISTS policy in org)` writes nothing for a cross-org reference, so no foreign-key error text can come back.
> - `.sql` and `.sql.go` are kept in sync.
> - Production always has `queries` set (router.go:81, main.go:60), so the mock branch without checks is test-only.
>
> **Race between check and insert:**
> - An `org_id` on gateway_agents or policies never changes. `UpdateAgent` sets only scope, risk_tier, status and ttl; the policy UPDATE and reorder are org-predicated. So a race cannot turn a reference into a cross-org one.
> - An agent deleted after the check either gives 0 rows (404 "approval references not found") or a 23503 foreign-key error mapped to a generic 500. Both concern the caller's own org only.
> - The layer-2 message leaks nothing: it is identical for foreign and nonexistent references and only reachable in a race or when layer 1 is bypassed.
>
> **Existing damage:** a live read-only query found 0 approval_requests rows whose agent, policy, resolved_tool or approved_by belongs to a different org. There are no leftover rows blocking deletes.
>
> **Tests:** approval_org_pg_test.go covers foreign vs nonexistent agent and policy at the handler and store layers, plus the same-org happy path. One note: the store test deletes its approval rows only at the end, so a mid-test `Fatal` leaves them. That is harmless because approval_requests.org_id cascades on org delete.
>
> ## Gateway approval insert is independent
> - `Submit` (eami-gateway/internal/approval/router.go:380-460) builds its row from `ac.OrgID`/`ac.AgentUUID`. These come from `sess.Agent` (mcp/handler.go:404-420) or from `LookupByNameAndOrg(agentName, claims.OrgID)` (workflow/http.go:73-90). The resolved tool comes from org-scoped `Resolve(ctx, orgID, name)`.
> - Nothing there comes from a client body, and it does not call this API route.
> - The live DB has 0 approval_requests rows whose resolved_tool_id belongs to another org.
>
> ## DecideApproval and the notification path
> - The decide query (approvals.sql.go:196-203) is `WHERE id=$1 AND org_id=$2 AND status='pending'`. Foreign, nonexistent and already-decided approvals all return the same 409.
> - `approved_by` is the caller's own user id. The live DB has 0 rows where approved_by belongs to another org.
> - `NotifyApprovalDecision` sends only `a.ID` from that org-scoped row.
> - The gateway's `resolve` (router.go:700+) acts only on `r.pending.Load(approvalID)`, and entries exist only for ids the gateway generated in `Submit`. API-created approval ids come from the DB default `uuid_generate_v4()`, so they cannot match a pending hold.
> - A forged `gateway_session_id` is stored but never used to find or resume a session; `grep` shows it is only read and written in approvals.go and approvals.sql.go.
> - Conclusion: one org cannot decide, or resume held sessions for, another org's approvals.
>
> ## Sweep of the same bug shape in eami-api (all pass)
> I listed every non-org foreign key from pg_constraint and enumerated every INSERT/UPDATE in api/ and store/.
>
> | Write | Result | Evidence |
> |---|---|---|
> | api_keys.agent_id (auth.go:231-256) | PASS | `GetAgent(id, uc.OrgID)`, 400 on miss. The 500 path echoes `err.Error()`, a B-234-class nit. |
> | endpoints.gateway_agent_id (endpoints.sql.go:597-620) | PASS | EXISTS check on id+org before the UPDATE; only write path (discover.go:272). |
> | endpoints / gateway_agents / gateway_tools .ci_type_id (cmdb.sql.go:309-335) | PASS | EXISTS on id+org+kind, and the foreign key is composite `(ci_type_id, org_id)` RESTRICT. |
> | ci_types.category_id | PASS | Composite foreign key with org_id; 23503 gives a generic 409 (cmdb.go:212). |
> | policies.workspace_id (workspace_policies.go:83) | PASS | workspace_id comes from the URL only; `requireWorkspaceRole` checks org for admins (middleware.go:180-196) and joins `w.org_id` for members (:200-207). |
> | policy_conditions.policy_id (policies.go:169,232; workspace_policies.go:104,184) | PASS | The id always comes from the org-scoped policy row just inserted or updated. |
> | workflow_steps.gateway_tool_id (workflows.go:260-270) | PASS | `ToolBelongsToOrg` checked per step. |
> | input_mapping.from_step | PASS | Resolved only from this run's own prior results (gateway workflow/params.go:48). |
> | workflow_step_params.workflow_step_id (workflow_step_params.sql.go:54-63) | PASS | Single INSERT…SELECT WHERE EXISTS joined to workflows.org_id. |
> | workspace_memberships.user_id (workspaces.go:485-499) | PASS | User and workspace org joined (B-233). Workspace creation: group and membership ids are server-generated. |
> | agent_configs.agent_id | PASS | B-232, already committed. |
> | alerts.rule_id (alerting/engine.go:176) | PASS | The rule comes from the DB, server-side. alert_rules has no foreign-key id columns. |
> | license_events.license_id (license.go:186,226) | PASS | Server-derived `row.ID`. |
> | agent_lifecycle_events | PASS | Built from the org-scoped agent row. |
> | users.invited_by, *.created_by, approved_by | PASS | Set from `uc.UserID`. |
> | episodes.agent_id, workflow_runs.agent_id | PASS | Gateway-only, server-resolved identity. |
> | group_memberships | PASS | No API write path exists. |
> | Collector/agent ingest (endpoints, endpoint_reports, paste_events and their children) | PASS | org comes from auth; child ids come from the upsert itself. |
>
> A live DB read-only check found 0 cross-org rows for: approval agent/policy/resolved_tool/approved_by, api_key→agent, endpoint→agent, endpoint→workspace, agent→workspace, policy→workspace, workflow_step→tool, workflow_run→agent, episode→agent, workspace_member user↔workspace, and workspace→group.
>
> **Uncertainty:**
> - I did not run the real-Postgres tests or test-prepare the new INSERT…SELECT (instructions allow SELECT only). My confidence that the bare `$n` parameters in the SELECT list take the target column types rests on the new store test, not on my own run.
> - I did not audit platform-admin cross-org flows.
>
> Files reviewed: C:\AI\EAIM\eaim\eami-api\internal\api\approvals.go, C:\AI\EAIM\eaim\eami-api\internal\store\approvals.sql.go, C:\AI\EAIM\eaim\eami-api\internal\store\query\approvals.sql, C:\AI\EAIM\eaim\eami-api\internal\api\approval_org_pg_test.go, C:\AI\EAIM\eaim\eami-gateway\internal\approval\router.go, C:\AI\EAIM\eaim\eami-gateway\cmd\gateway\dispatcher.go.

**Disposition:**
- **`policy_rule_id` Low:** fixed.
- **No-legitimate-caller Low:** recorded for a founder decision (§6).
- **DecideApproval `err.Error()`:** added to B-234.
- **Info items:** recorded.
- **The reference-shape sweep** is incorporated into `ORG_BRANCH_ASYMMETRY_SWEEP.md` as corroborating evidence. It does not replace the literal per-endpoint ledger, which continues there.

## 6. Recorded (not fixed here)

**Needs a founder decision:**
- **`POST /v1/approvals` has no legitimate caller.** It still lets an admin or operator create inert, spoofed approvals **within their own org**, because the descriptive fields (`agent_name`, `tool_name` and so on) are free text. The options are to remove the route, or to derive the descriptive fields from the real agent row.

**Pre-existing, not fixed here:**
- invalid `risk_level`/`environment` returns 500 instead of 400;
- `int32(*req.EstimatedRecords)` can overflow;
- same-org approvals make the org's own agent or policy undeletable (NO ACTION, and there is no approval-delete route), which is a product decision.

**Added to B-234:** `DecideApproval`'s `err.Error()` echo on the 500 path.
