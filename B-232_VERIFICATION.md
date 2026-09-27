# B-232 Verification Record: cross-org agent-config overwrite (H-1)

Written 2026-09-27 by Claude Code, as an urgent founder brief. H-1 was found by the Agent Detail Actions-tab security review; see `AGENT_ACTIONS_TAB_VERIFICATION.md` §3b.

**Checkability.** Everything below quotes command output or review reports verbatim.
- **Raw logs** are in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `b232_attack_prefix.log`, `b232_attack_fixed.log`, `b232_ui.log`, `b232_mutation.log`, `b232_api_full2.log`, `b232_before.txt`, `b232_after.txt`.
- **Review transcripts** are in that session's subagent records.

## 1. Part A: the confirmed mechanism

- **The existing code** (`eami-api/internal/api/agents.go` `UpdateAgentConfig`):
  - It called `GetAgentConfig(agentID)` first. That store query is `WHERE agent_id = $1`, with no org filter.
  - It ran the org check `GetAgent(agentID, uc.OrgID)` **only if that returned an error**, i.e. only when no config row existed.
  - The trigger `trg_agent_configs_default` seeds a config row for every agent. So for any real agent the org check never ran, and `UpsertAgentConfig` wrote by `agent_id` alone.
- **Root cause, as the brief described,** with one nuance: `agent_configs` has **no `org_id` column**, so the usual `WHERE id=$1 AND org_id=$2` cannot be applied to it directly.
- **The GET handler** was already correct: its org check runs first.

## 2. The fix: two independent layers

1. **Handler.** `GetAgent(agentID, uc.OrgID)` now runs **unconditionally, before any read or write**.
   - `ErrNoRows` returns 404 "agent not found", byte-identical to the response for a nonexistent ID.
   - Other errors return a generic 500 and are logged with `slog`.
   - Only a missing config row falls back to defaults. Any other load error returns 500 instead of silently resetting fields the request didn't send.
   - The upsert's 500 no longer echoes `err.Error()`.
2. **SQL.** `store.UpsertAgentConfig` is org-scoped inside the statement: `INSERT … SELECT $1..$5, NOW() FROM gateway_agents WHERE id = $1 AND org_id = $6 ON CONFLICT (agent_id) DO UPDATE …`.
   - For another org's agent, the SELECT yields no row, so nothing is written or returned, and the handler maps that to 404.
   - The sqlc source `store/query/agent_configs.sql` was updated to match, after the code review, so a future `sqlc generate` can't silently revert this layer.

## 3. Automated verification

**Commands and results**
```
eami-api: go build ./... ; go vet ./internal/api ./internal/store → ok ; gofmt (touched files) → clean, no change vs HEAD's baseline
eami-api: POSTGRES_PASSWORD=… go test -count=1 -v ./... → every package ok; PASS=479 FAIL=0 SKIP=0
```

**New tests** (`eami-api/internal/api/agent_config_pg_test.go`). They mirror B-141's adversarial shape: a real attacker org and a real victim org. The attempt must be rejected, **and** the victim row, read directly from Postgres including `updated_at`, must be unchanged.
- **`TestAgentConfig_CrossOrgWriteRejected_RealDB`:**
  - admin and operator attackers, each against a victim agent with a config row and one without;
  - every attempt gets a 404 whose body is byte-identical to a nonexistent ID's;
  - no victim values appear in any response;
  - the row-exists victim is unchanged, and no row is created for the no-row victim;
  - a cross-org GET returns 404.
- **`TestAgentConfig_SameOrgConfigureStillWorks_RealDB`:**
  - a full PUT is persisted;
  - a partial PUT merges onto the stored row;
  - a PUT with no config row creates it from defaults;
  - a viewer gets 403 on PUT and 200 on GET.
- **`TestUpsertAgentConfig_StoreLevelOrgScoping_RealDB`,** added after the security review's L-1:
  - calls the store directly with the wrong org, for both the row and no-row cases;
  - expects `pgx.ErrNoRows`, nothing written and no row created;
  - the right org still upserts.

**Mutation check** (`b232_mutation.log`, verbatim):
```
=== M1: original HEAD handler + store (the H-1 bug itself)
--- FAIL: TestAgentConfig_CrossOrgWriteRejected_RealDB
    cross-org PUT config: admin, victim HAS a config row (the broken branch) = 200 want 404: {"agent_id":"5b8b1b72-…","scan_interval_seconds":61,"model_scan_paths":["/attacker"],…}
=== M2: handler ownership check removed, org-scoped upsert kept (SQL layer alone)
ok
=== M3: SQL org scoping removed, handler check kept (handler layer alone)
ok
=== M4: both layers removed
--- FAIL: TestAgentConfig_CrossOrgWriteRejected_RealDB
    … = 200 want 404 …
=== M5: SQL org scoping removed — store-level test must now fail
--- FAIL: TestUpsertAgentConfig_StoreLevelOrgScoping_RealDB
    store upsert with the wrong org for agent c1ba3297-…: err=<nil>, want pgx.ErrNoRows
sources restored OK / restored OK
```

**What the mutations show:**
- M1 reproduces H-1.
- M2 and M3 prove **each layer independently** blocks the write.
- M5 proves the store test detects removal of the SQL layer.
- **Honest note:** the no-row HTTP cases would also have passed on the old code, since its no-row branch was already checked. Only the row-exists cases catch the original bug; the security review makes the same point.

## 4. Live verification (shared stack)

**Fixtures:**
- a throwaway attacker org and its admin;
- Dev Org fixture victims: `b232fix-victim` (config set to `4321|/victim|models`) and `b232fix-victim-norow` (row deleted);
- a Dev Org admin and `b232fix-own` for the same-org check.

Passwords were hashed in-DB via `pgcrypto`, for the new rows only.

**On the pre-fix API that was running** (container started 2026-09-27T02:52:46Z), H-1 was reproduced live (`b232_attack_prefix.log`, verbatim):
```
victim row BEFORE        : 4321|/victim|models
victim-norow row BEFORE  : <no row>
PUT victim (has row)     : 200 {"agent_id":"d8124927-…","scan_interval_seconds":61,"model_scan_paths":["/attacker"],"max_report_size_bytes":1048576,"enabled_scanners":["browser"],…}
PUT victim-norow (no row): 404 {"code":"not_found","message":"agent not found"}
PUT nonexistent id       : 404 {"code":"not_found","message":"agent not found"}
victim row AFTER         : 61|/attacker|browser
victim-norow row AFTER   : <no row>
```

Then the victim was restored, the API was rebuilt with the fix (container started 2026-09-27T04:13:48Z), and **the identical attack was repeated** (`b232_attack_fixed.log`, verbatim):
```
victim row BEFORE        : 4321|/victim|models
victim-norow row BEFORE  : <no row>
PUT victim (has row)     : 404 {"code":"not_found","message":"agent not found"}
PUT victim-norow (no row): 404 {"code":"not_found","message":"agent not found"}
PUT nonexistent id       : 404 {"code":"not_found","message":"agent not found"}
victim row AFTER         : 4321|/victim|models
victim-norow row AFTER   : <no row>
```

**Same-org Configure through the real UI still works on the fixed API** (`b232_ui.log`, verbatim):
```
PASS Agent Detail Configure loads the own agent's real config (GET, org-checked) :: form scan interval=300
PASS Agent Detail Configure saves through the fixed, org-scoped PUT :: db=777
PASS Agents list Configure saves through the fixed PUT :: db=888
PASS zero console/page errors :: []
4 checks, 4 passed, 0 failed
```

**Cleanup:**
- One transaction: `DELETE 3` fixture agents (configs cascade), `DELETE 1` user, `DELETE 1` attacker org (its user cascades), then `COMMIT`.
- `diff b232_before.txt b232_after.txt` was **identical**. The snapshot covers counts plus every agent's status and config row, so it also proves no real agent's config was touched.
- The residual scan found **0**.

## 5. Reviews (both mandatory passes completed; quoted verbatim)

After both reviews, two follow-ups were made. Neither went back to a reviewer, and both are covered above:
- **The sqlc source mirror.** It is not compiled, and it matches the reviewed `.go` SQL exactly.
- **The store-level test.** It is proven by mutation M5.

### 5a. Code review

> **Verdict: approve.** The B-232 fix is correct. I found no High or Medium issues: the cross-org overwrite is closed by two independent checks, and the same-org flow behaves as before. There are two Low items and some Info notes below.
>
> I ran `go build ./...` and `go vet ./internal/api ./internal/store` in eami-api; both passed. As instructed, I did not run the real-Postgres tests.
>
> **Low: the sqlc query source was not updated** — `eami-api/internal/store/query/agent_configs.sql:7-17`
> - `sqlc.yaml` points sqlc at `internal/store/query` and writes to `internal/store`. That query file still holds the old unscoped `VALUES ($1..$5)` UpsertAgentConfig.
> - **Scenario:** someone runs `sqlc generate`. It would regenerate `agent_configs.sql.go` from the old SQL, and the params struct would lose `OrgID`.
> - The failure would be loud, not silent: `agents.go:717` sets `OrgID`, so the build would break. But the SQL layer of the defence could get "fixed back" to the unscoped version.
> - **Fix:** mirror the `INSERT ... SELECT ... FROM gateway_agents WHERE id=$1 AND org_id=$6` change into `query/agent_configs.sql`. (The .go file calls itself "hand-written", but its query-file twin exists and matches sqlc's naming.)
>
> **Low: GET paths treat any DB error as "no config row" and return defaults** — pre-existing, out of scope
> - Locations: `agents.go:630-636` (admin `GetAgentConfig`) and `agent_config_remote.go:83-93` (`AgentRemoteConfig`).
> - **Scenario:** a transient DB error on the remote-config route. The endpoint agent receives the server defaults (all 6 scanners, default paths) as its config, which fails open. There is no cross-org exposure here.
> - **Fix:** handle this the way the new `UpdateAgentConfig` does: only `pgx.ErrNoRows` falls back to defaults, anything else returns 500. Worth a NOTES/BACKLOG entry rather than this change.
>
> **Info: the tests skip when no database is set**
> - `toolsUpdateTestDSN` calls `t.Skip` when neither `TEST_DATABASE_URL` nor `POSTGRES_PASSWORD` is set, so without a DB these tests pass without testing anything. That is the repo-wide pattern.
> - The mutation evidence you described (the old code returns 200 and overwrites the victim; each layer alone blocks it) covers this in practice.
>
> **Info: the handler now does three round trips** (GetAgent, GetAgentConfig, Upsert) instead of two. This is negligible on an admin write path.
>
> **Verified clean**
>
> **SQL semantics**
> - When the `SELECT ... FROM gateway_agents WHERE id=$1 AND org_id=$6` returns no rows, nothing is inserted. With no candidate row there is no conflict, so `DO UPDATE` never runs and `RETURNING` produces nothing. `QueryRow().Scan` then returns `pgx.ErrNoRows`, which the handler maps to 404 at `agents.go:739-742`.
> - Because `agent_id` is the primary key and `gateway_agents.id` is unique, the SELECT yields at most one row, so there is no "affect row a second time" error.
> - **Parameter types:** `$1` appears in both the insert list and `WHERE id = $1`; both are uuid, so its type is consistent. `$2`–`$5` get their types from the target columns. Postgres doesn't force unknown SELECT-list outputs to text inside INSERT...SELECT. The new same-org tests exercise this statement successfully, per your run.
> - **Argument order:** `$6` is `toPgtypeUUID(p.OrgID)`, passed last, which matches the numbering.
>
> **Handler flow**
> - `GetAgent(agentID, uc.OrgID)` now runs unconditionally, before any config read or write.
> - **Error paths:** `ErrNoRows` from `GetAgent` returns 404 "agent not found", the same body as for a nonexistent id, so no existence oracle. Any other `GetAgent` error returns a generic 500 and logs via `slog`. A non-`ErrNoRows` error from `GetAgentConfig` returns 500 instead of silently resetting fields the request didn't send. A missing config row falls back to defaults merged with the request, and the upsert creates the row. An upsert error no longer echoes `err.Error()`.
> - Validation still runs before any DB access.
> - The `storeIface` test-path branch is unchanged, never writes, and already checked the org.
> - Removing `SeedAgentConfig` from this path is safe: the upsert inserts the row itself.
>
> **`GetAgent` is org-scoped:** its query is `WHERE id = $1 AND org_id = $2` (`store/agents.sql.go:43-49`).
>
> **Other callers and paths**
> - `UpsertAgentConfig` has exactly one caller, `agents.go:738`; nothing else in the repo calls it.
> - `SeedAgentConfig` is called only from `CreateAgent` (`agents.go:184`), with the id of the agent just created under `uc.OrgID`, so it is safe. The trigger `create_default_agent_config` is `ON CONFLICT DO NOTHING`.
> - `GetAgentConfig` (store) is called by: the admin GET, which does an org-checked `GetAgent` first; the new PUT; and `AgentRemoteConfig`. That route is service-key only and single-tenant: it takes the org from `GetDefaultOrgID` and resolves the agent through `endpoints.gateway_agent_id`. `LinkEndpointToGatewayAgent` (`store/endpoints.sql.go:597-621`) checks that the gateway agent belongs to the same org before linking, and `ResolveEndpointGatewayAgent` filters by org. No cross-org read path.
> - **Other `{agentId}` routes are all org-scoped:** `UpdateAgent` and `DeleteAgent` use SQL `WHERE id=$1 AND org_id=$2`, and `DeleteAgent` also calls `GetAgent` first. `GetAgent` uses an org-scoped query, as above. `GetAgentConnections` calls `GetAgent` with the org first.
>
> **Route roles:** PUT config is in the admin+operator group (`router.go:329,337`). GET is in the admin/operator/viewer group, so the test's viewer 403 on PUT and 200 on GET match.
>
> **Tests**
> - Pool lifecycle follows the CLAUDE.md rule: `newWorkspaceTestEnv` registers `t.Cleanup(pool.Close)` before the per-org `DELETE FROM orgs` cleanups that `seedTestOrg` registers.
> - Cleanup cascades: `DELETE FROM orgs` removes `gateway_agents` (`ON DELETE CASCADE`), which removes `agent_configs` (`ON DELETE CASCADE`). No leaks.
> - `expectCMDBStatus` calls `t.Fatalf` on a status mismatch.
> - The victim row comparison includes `updated_at::text`, so even an overwrite with identical values would be caught.
> - The no-row victim case asserts that no row was created.
> - Each cross-org 404 body is compared with the 404 body for a nonexistent id.
> - Same-org coverage: a full PUT with persistence checked, a partial PUT merged onto the stored row, and a no-row PUT that creates the row with defaults (`max_report_size_bytes` 5242880).
>
> **Same-org behaviour:** `AgentConfigPanel.tsx` sends a full body through `mutateAsync` and shows `err.message` on failure. For a same-org agent the only behavioural change is that DB errors now produce generic messages. Success responses are unchanged.
>
> **Diff scope:** `git diff HEAD --stat` shows only `CONTEXT.md` (2 lines), `agents.go`, and `agent_configs.sql.go`, plus the untracked test file.

**Disposition:**
- **Low 1:** fixed (the sqlc source mirrored).
- **Low 2:** pre-existing. It fails open on DB errors but is not cross-tenant, so it is recorded (§6).
- **Info items:** accepted.

### 5b. Security review

> **Verdict: B-232 fully closes H-1. I found no new issue introduced by the fix. Looking for the same bug class elsewhere turned up one real, separate cross-tenant write (Medium) in the workspace-membership handlers. Everything else I checked is scoped correctly.**
>
> **M-1 (Medium, real, pre-existing, not in this diff): an org admin can change or remove memberships in another org's workspace.**
> - Where:
>   - `eami-api/internal/api/middleware.go:168-172`: `requireWorkspaceRole` lets `uc.Role == "admin"` through with no check that the workspace belongs to `uc.OrgID`.
>   - `eami-api/internal/api/workspaces.go:538-540` (`UpdateWorkspaceMemberRole`) and `workspaces.go:569-571` (`RemoveWorkspaceMember`): both run `... WHERE user_id=$2 AND workspace_id=$3` with no org predicate. Neither handler reads `uc` at all.
> - Why the database doesn't catch it: `workspace_memberships` has no `org_id` column, and it has no workspace/org-match trigger. I checked `pg_trigger`: only `gateway_agents`, `endpoints` and `policies` have one.
> - Scenario: an admin in org A who knows org B's workspace UUID and a member's user UUID can PATCH that member's role to `workspace_admin` (escalating a B user inside B's workspace) or demote one, or DELETE the membership, locking B's users out.
> - Limits: it needs two UUIDs from the other org, and the response gives no read-back of B's data. Hence Medium, not High.
> - Siblings behind the same bypass are safe: `AddWorkspaceMember` checks `w.org_id=$3` (workspaces.go:477-483). `ListWorkspaceMembers` filters `w.org_id=$2`. `UpdateWorkspace`, `UpdateWorkspacePolicy` and `DeleteWorkspacePolicy` all include `org_id`. `CreateWorkspacePolicy` inserts with the caller's `org_id` plus the foreign `workspace_id`; the DB trigger `trg_policies_workspace_org_match` rejects that combination. `ListWorkspacePolicies` filters `p.org_id=$1`, so it returns only A's floor policies.
> - Existing tests don't cover it: `TestWorkspaceMembers_RealDB_AddUpdateRoleRemove` has no cross-org case.
> - Fix: in the admin branch of `requireWorkspaceRole`, first confirm `EXISTS(SELECT 1 FROM workspaces WHERE id=$1 AND org_id=$2)` and return 403 otherwise. As defence in depth, add `AND workspace_id IN (SELECT id FROM workspaces WHERE org_id=$4)` to both statements. Add an adversarial real-database test. This needs a new B-ID, which needs founder confirmation per your memory rule. The backlog counter reads B-232, which this fix already uses.
>
> **L-1 (Low, test gap): the SQL-level scoping is never exercised directly.**
> - `agent_config_pg_test.go` goes through HTTP, so every cross-org request stops at the new `GetAgent` check before `UpsertAgentConfig` runs. The `INSERT ... SELECT ... WHERE org_id=$6` guard is untested.
> - Fix: add a store-level call `UpsertAgentConfig{OrgID: orgA, AgentID: victimWithRow}`, assert `pgx.ErrNoRows`, and re-read the victim row directly.
>
> **Info:**
> - **The no-row cases don't catch a regression.** The pre-fix code already checked org on the no-row path, so the four no-row cases would have passed before the fix too. They cover the branch. Only the two row-exists cases (admin and operator) catch the original bug. That's fine, just worth knowing.
> - **`SeedAgentConfig` isn't org-scoped** (`agent_configs.sql.go`, `INSERT ... VALUES ($1) ON CONFLICT DO NOTHING`). Its only caller is `CreateAgent` (agents.go:184), with an id it just created in the caller's org, and it can't overwrite anything. Safe today, but it's a trap if someone reuses it.
> - **Pre-existing, outside this diff, in `GetAgentConfig` (GET):** any `GetAgent` error, including a database error, becomes a 404, and any `GetAgentConfig` error falls back to the defaults. There's no leak, but real DB failures are masked.
> - **Other 500s still echo `err.Error()`:** `CreateAgent` (agents.go:180), `UpdatePolicy` and the workspace handlers. That's outside B-232's scope, which only covered this handler.
>
> **H-1 closure: evidence**
> - **Every path through `UpdateAgentConfig` goes through the new check.** `GetAgent(agentID, uc.OrgID)` (SQL `WHERE id=$1 AND org_id=$2`, agents.sql.go) runs before any read or write. The only code before it is JSON decoding and field validation, which don't depend on the agent. The fallback path (`s.queries == nil`) keeps its own `sa.OrgID != uc.OrgID` check. The old unscoped `SeedAgentConfig` call on the no-row branch is gone. Only `ErrNoRows` now falls back to defaults; other errors return a generic 500, so a transient error can no longer silently reset fields.
> - **Race between the check and the write: effectively closed.** `gateway_agents.org_id` is immutable in application code: the only `UPDATE gateway_agents` statements are `UpdateAgent` (sets scope, risk_tier, status and token TTL only) and the CMDB `ci_type_id` update, both `WHERE id AND org_id`, and neither sets `org_id`. A grep for `SET ... org_id =` across eami-api and eami-gateway found nothing. The upsert re-checks `org_id` inside the same statement anyway. If the agent is deleted between check and write, the cascade FK (`agent_configs_agent_id_fkey ON DELETE CASCADE`) plus the empty SELECT give `ErrNoRows` and a 404. Neither table has row-level security (`relrowsecurity = f`), so nothing interferes there.
> - **The `ON CONFLICT DO UPDATE` branch cannot fire on a zero-row SELECT.** Postgres only evaluates the conflict arbiter for rows actually proposed for insertion. No candidate row means no conflict, no update and no `RETURNING` row, so `Scan` returns `pgx.ErrNoRows`, which the handler maps to 404. `$1` is used both as a value and in the `WHERE` clause; both are sent as the same `pgtype.UUID`, and `go vet` compiles the package cleanly. The sqlc source `query/agent_configs.sql` was updated to match.
> - **The 404 gives no existence oracle.** Status and body are identical for a nonexistent agent and another org's agent (`"not_found"`, `"agent not found"`), and the test asserts the bodies are byte-equal. Timing is the same for both: one indexed lookup on `id AND org_id` returning no rows, no further queries. Body validation runs before the ownership check and is independent of the agent, so its 400s reveal nothing either.
> - **The fix doesn't open anything new.** The PUT route (router.go:337) keeps its role gating; the test confirms a viewer gets 403. Logging goes through `slog` on the server side only.
>
> **Same bug class elsewhere: checked and clean**
>
> I listed every table without an `org_id` column from the catalog: `agent_configs`, `endpoint_ai_apps`, `endpoint_mcp_servers`, `endpoint_model_files`, `policy_conditions`, `workflow_steps`, `workflow_step_params`, `workflow_run_steps`, `workspace_memberships`, `group_memberships`, and the token tables. I traced every writer.
> - **`agent_config_remote.go` (service-key route)** resolves through `ResolveEndpointGatewayAgent`, scoped by `org_id` and `agent_id`, with the org taken from `GetDefaultOrgID`. The endpoint link is org-validated when it is written: `LinkEndpointToGatewayAgent` does an `EXISTS` check on `gateway_agents` by `id` and `org_id`, then `UPDATE endpoints ... AND org_id`. Because `org_id` is immutable, the later unscoped `GetAgentConfig(*gatewayAgentID)` read is safe. Caveat: the service key is global, and "default org" only holds up while the deployment is single-tenant. It needs revisiting before multi-tenant collectors.
> - **`policy_conditions`:** in `UpdatePolicy`, `UpsertPolicyCondition` runs only after `UpdatePolicy ... WHERE id AND org_id` succeeds, using the returned `pol.ID`. `UpdateWorkspacePolicy` does the same with `idOut`, scoped by `id`, `org_id` and `workspace_id`. On create, the condition uses the id of the policy just inserted.
> - **`workflow_steps` and `workflow_step_params`:** `UpdateWorkflow` calls `GetWorkflow(id, orgID)` first, then `UpdateWorkflow` with `org_id` inside the transaction, before `DeleteWorkflowSteps` and `InsertWorkflowStep`. Step tools pass through `ToolBelongsToOrg`, and step-params reads and writes are org-joined. `UpsertWorkflowStepParams` already uses the same atomic `INSERT ... SELECT ... WHERE EXISTS` pattern as this fix.
> - **`endpoint_*` children** are written only by ingest, using an `endpoint_id` returned from `UpsertAgentEndpoint` in the resolved org.
> - **Id-only `WHERE` clauses without `org_id` elsewhere in eami-api** are all acceptable: self-scoped to the caller (`UpdateUserPasswordHash(uc.UserID)`); internal ids that are never taken from the client (token consumption and alert notification); or parent already checked (`ListWorkflowSteps`).
> - **eami-gateway:** `approval_requests`, `workflow_runs` and `usage_dispatch_inflight` updates use ids the server generated internally. The user-facing decision path, eami-api's `DecideApproval`, is `WHERE id AND org_id AND status='pending'`. eami-gateway doesn't touch `agent_configs`.
>
> **The new test** (`eami-api/internal/api/agent_config_pg_test.go`)
> - **Branches:** it covers the row-exists branch (victim given non-default values 4321 and `/victim`) and the no-row branch (trigger-seeded row deleted), for both admin and operator attackers.
> - **Victim checked in the database:** it re-reads the victim rows straight from Postgres, including `updated_at`. It asserts the row-exists victim is unchanged and that no row was created for the no-row victim.
> - **Other assertions:** the 404 body must equal the nonexistent-agent body, the response must not leak victim values, cross-org GET must 404, and the same-org test covers full update, partial merge, the no-row create with defaults, and viewer 403 on PUT with 200 on GET.
> - **Pool lifecycle:** the `newWorkspaceTestEnv` pool uses `t.Cleanup(pool.Close)` registered first, and `seedTestOrg` cleans up with `t.Cleanup`, matching the mandatory pattern.
> - **Verification run:** `go vet ./internal/api/ ./internal/store/` passes. Per the brief, I did not run the real-Postgres tests.

**Disposition:**
- **H-1 closure:** confirmed.
- **L-1:** fixed (the store-level test, mutation M5).
- **M-1 (workspace-membership cross-org write):** **real, confirmed in source by Claude Code** (`middleware.go:167-171`, `workspaces.go:539`, `:570`). It is outside B-232 and **needs a founder-confirmed B-ID**; recommended as the next urgent fix (§6).
- **Info items:** recorded.

## 6. Recorded, not fixed here (each needs a founder-confirmed B-ID)

1. **Workspace-membership cross-org write (Medium).** A security-review finding, confirmed in source.
   - **Cause:** `requireWorkspaceRole` lets any org `admin` through before its org-scoped query runs. `UpdateWorkspaceMemberRole` and `RemoveWorkspaceMember` then filter only by `user_id` and `workspace_id`.
   - **Impact:** an org-A admin holding org-B UUIDs can escalate, demote or remove org-B workspace members.
   - **Recommended next:** the same two-layer shape as this fix, plus an adversarial test.
2. **Config GET paths fail open on DB errors (Low, not cross-tenant).**
   - `GetAgentConfig` (admin) turns any DB error into a 404, or into defaults.
   - `AgentRemoteConfig` serves defaults (all scanners) on any DB error.
3. **Carried over from earlier:**
   - clickjacking headers in the Actions-tab record (its M-1);
   - the app-wide raw `err.Error()` echo in 500s, whose remaining instances this review names: `CreateAgent`, `UpdatePolicy`, and the workspace handlers.
