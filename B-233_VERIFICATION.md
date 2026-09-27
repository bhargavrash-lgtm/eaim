# B-233 Verification Record: cross-org workspace-membership mutation

Written 2026-09-27 by Claude Code, as an urgent founder brief. The issue was found by B-232's security-review sweep; see `B-232_VERIFICATION.md` §5b.

**Checkability.** Everything below quotes command output or review reports verbatim.
- **Raw logs** are in Claude Code session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `b233_attack_prefix.log`, `b233_attack_fixed.log`, `b233_sameorg.log`, `b233_attack_final.log`, `b233_sameorg_final.log`, `b233_mutation.log`, `b233_api_full2.log`, `b233_before.txt`, `b233_after.txt`, `b233_after2.txt`.
- **Review transcripts** are in that session's subagent records.

## 1. Part A: the confirmed mechanism, and how it differs from B-232

- **Layer 1 lives in the middleware, not a handler.**
  - `requireWorkspaceRole` (`middleware.go`) returned early for `uc.Role == "admin"` before any org check.
  - Its own membership query is org-joined (`w.org_id = $3`), but only non-admins reached it.
  - The fix belongs in the admin branch, which puts the ownership check in front of **all 9 routes** in both workspace route groups.
- **Two different response conventions.** Non-admins already got **403** for a foreign or nonexistent workspace; that is established and tested. For the admin attacker the brief requires a non-revealing **404**, consistent with the admin handlers' own "not found" usage. So:
  - admin + foreign or nonexistent workspace → 404 "workspace not found";
  - non-admin → unchanged 403.

  Each is uniform within its role, so neither reveals whether a workspace exists.
- **Layer 2, the SQL.** The membership `UPDATE`/`DELETE` filtered only `user_id` + `workspace_id`, and `workspace_memberships` has no `org_id`. As in B-232, the SQL now scopes through the parent table (`AND workspace_id IN (SELECT id FROM workspaces WHERE org_id = $n)`).

## 2. The fix

| Layer | Change |
|---|---|
| **1: middleware** | The admin branch checks `EXISTS (SELECT 1 FROM workspaces WHERE id=$1 AND org_id=$2)`. It returns 404 "workspace not found" if false, and a generic 500 plus `slog` on a DB error. A `requireQueries` nil guard was added after the code review. The doc comment is corrected. |
| **2: SQL** | New `store.UpdateWorkspaceMemberRole` and `store.RemoveWorkspaceMember` (`store/workspace_memberships.sql.go`), org-scoped in-statement. The handlers use them, and no longer echo `err.Error()` on 500. |

## 3. Automated verification

**Commands and results**
```
eami-api: go build ./... ; go vet ./internal/api ./internal/store → ok ; gofmt → clean (touched files unchanged vs HEAD baseline; new files clean)
eami-api: POSTGRES_PASSWORD=… go test -count=1 -v ./... → every package ok; PASS=483 FAIL=0 SKIP=0
```

These are real-Postgres runs, with the credential injected from the container environment. The **code reviewer's** own run had no credential set, so its real-Postgres tests skipped (it disclosed this, §5a). The results above are mine.

**New tests.** They follow B-141/B-232's adversarial shape, and each layer has its **own** test:
- **`TestWorkspaceMember_CrossOrgMutationRejected_RealDB`** (HTTP): an org-A admin PATCHes and DELETEs org B's workspace memberships (promote a member, demote a workspace_admin, a nonexistent user, remove members).
  - Every case returns 404 byte-identical to a **nonexistent workspace's** 404. That baseline is pinned to the ownership check's own "workspace not found".
  - The victims' roles, read straight from Postgres, are unchanged.
  - After the security review, the test was extended to **all 9 guarded routes**: workspace PATCH, members POST/GET, and policies POST/PATCH/DELETE/GET. Org B's workspace name and policies are unchanged.
- **`TestWorkspaceMember_SameOrgManagementStillWorks_RealDB`:** an org admin and a non-admin workspace_admin promote, demote and remove members, each change checked in the DB; a nonexistent own member returns 404.
- **`TestWorkspaceMemberStore_OrgScopedWrites_RealDB`** (layer 2 alone): wrong-org store writes change 0 rows and leave the membership unchanged; right-org writes change 1 row.
- **`TestRequireWorkspaceRole_AdminOwnershipCheck_RealDB`** (layer 1 alone, package-internal): a sentinel handler wrapped by `requireWorkspaceRole` is **never reached** for a foreign or nonexistent workspace, for both minimum roles. The admin's own workspace does reach it.
- **The six pre-existing workspace/RBAC tests still pass**, including `TestRequireWorkspaceRole_RealDB_CrossOrgWorkspaceID_Rejected`, which accepts 404 or 403.

**Mutation check.** Each part was broken **separately**, as the brief requires (`b233_mutation.log`, verbatim):
```
=== M1: original HEAD middleware + handlers (the bug itself; both layers absent)
--- FAIL: TestRequireWorkspaceRole_AdminOwnershipCheck_RealDB
    workspace_admin: org-A admin on org B's workspace: status=204 handlerReached=true, want 404 and never reached
--- FAIL: TestWorkspaceMember_CrossOrgMutationRejected_RealDB
    nonexistent-workspace 404 body "{\"code\":\"not_found\",\"message\":\"membership not found\"}\n", want requireWorkspaceRole's "workspace not found"
=== M2: LAYER 1 removed only (admin ownership check in requireWorkspaceRole), SQL scoping kept
--- FAIL: TestRequireWorkspaceRole_AdminOwnershipCheck_RealDB
    workspace_admin: org-A admin on org B's workspace: status=204 handlerReached=true, want 404 and never reached
--- FAIL: TestWorkspaceMember_CrossOrgMutationRejected_RealDB
    nonexistent-workspace 404 body "{\"code\":\"not_found\",\"message\":\"membership not found\"}\n", want requireWorkspaceRole's "workspace not found"
=== M3: LAYER 2 removed only (SQL org scoping in both store writes), middleware check kept
--- FAIL: TestWorkspaceMemberStore_OrgScopedWrites_RealDB
    wrong-org role update: n=1 err=<nil>, want 0 rows
sources restored OK
```

**What the mutations show:**
- **Layer 1's removal alone** is caught by the direct middleware test and by the HTTP test.
- **Layer 2's removal alone** is caught by the store test.
- Unlike B-232, where the HTTP tests could not detect removal of one layer alone, **each part here has a test that fails when only that part is removed**.
- **One weakness in my first draft, found and fixed before running:** its baseline-equality check would *not* have caught the removal of layer 1. With the middleware check gone, both the foreign and the nonexistent workspace fall through to the handler and produce the same "membership not found". Pinning the baseline to "workspace not found" closed that gap.

## 4. Live verification (shared stack)

**Fixtures:**
- a throwaway attacker org and its admin;
- in Dev Org, a fixture workspace (group + workspace) with a `workspace_member` and a `workspace_admin`, plus a Dev Org admin for the same-org check.

Passwords were hashed in-DB via `pgcrypto`, for the new rows only.

**On the pre-fix API that was running** (started 2026-09-27T04:13:48Z), the bug was reproduced live (`b233_attack_prefix.log`, verbatim):
```
memberships BEFORE                 : b233fix-member@example.test=workspace_member, b233fix-wsadmin@example.test=workspace_admin
PATCH promote member               : 204
PATCH demote workspace_admin       : 204
PATCH nonexistent user             : 404 {"code":"not_found","message":"membership not found"}
DELETE remove member               : 204
DELETE nonexistent user            : 404 {"code":"not_found","message":"membership not found"}
PATCH on a nonexistent workspace   : 404 {"code":"not_found","message":"membership not found"}
GET members list                   : 200 {"data":[]}
memberships AFTER                  : b233fix-wsadmin@example.test=workspace_member
```

**On the final fixed build** (started 2026-09-27T11:13:26Z), the fixtures were re-seeded and the identical attack repeated (`b233_attack_final.log`, verbatim):
```
memberships BEFORE                 : b233fix-member@example.test=workspace_member, b233fix-wsadmin@example.test=workspace_admin
PATCH promote member               : 404 {"code":"not_found","message":"workspace not found"}
PATCH demote workspace_admin       : 404 {"code":"not_found","message":"workspace not found"}
PATCH nonexistent user             : 404 {"code":"not_found","message":"workspace not found"}
DELETE remove member               : 404 {"code":"not_found","message":"workspace not found"}
DELETE nonexistent user            : 404 {"code":"not_found","message":"workspace not found"}
PATCH on a nonexistent workspace   : 404 {"code":"not_found","message":"workspace not found"}
GET members list                   : 404 {"code":"not_found","message":"workspace not found"}
memberships AFTER                  : b233fix-member@example.test=workspace_member, b233fix-wsadmin@example.test=workspace_admin
```

**Same-org management on the final build** (`b233_sameorg_final.log`, verbatim):
```
GET members list (own workspace)   : 200 {"data":[{"user_id":"434a3060-…
PATCH promote own member           : 204
  after promote                    : b233fix-member@example.test=workspace_admin, b233fix-wsadmin@example.test=workspace_admin
PATCH demote back                  : 204
PATCH own ws, nonexistent user     : 404 {"code":"not_found","message":"membership not found"}
DELETE own member                  : 204
memberships AFTER                  : b233fix-wsadmin@example.test=workspace_admin
```

A first fixed build, before the code review's nil guard and comment fixes, produced identical results (`b233_attack_fixed.log`, `b233_sameorg.log`). The whole cycle was rerun on the final build so the evidence matches the committed code.

**UI.** `eami-ui` only *lists* members (`useWorkspaces.ts`, a GET). Role changes and removals are API-only today, so same-org management was verified through the real HTTP API.

**Cleanup, both cycles.**
- Each cycle ran in one transaction: memberships, workspace, group, 3 Dev Org users, the attacker org.
- `diff b233_before.txt b233_after.txt` and `diff b233_before.txt b233_after2.txt` were both **identical**. The snapshot includes every `workspace_memberships` row, plus group, workspace and membership counts.
- The residual scan found 0.

## 5. Reviews (both mandatory passes completed; quoted verbatim)

After both reviews, three things changed:
- the all-routes test cases (security Low);
- the nil guard and comment fixes (code review).

Both are covered by the rerun suite (483/0/0) and the final-build live cycle above. They were not sent back to a reviewer.

### 5a. Code review

> **Verdict: approve.** Both layers are correct and I found no High or Medium issues. Four Low/Info items are listed below, and none of them blocks the change.
>
> One disclosure first. I ran `go test -count=1 -run 'B233|WorkspaceMember|RequireWorkspaceRole' ./internal/api`, and the pattern matches the new real-Postgres tests. I then confirmed that `TEST_DATABASE_URL` and `POSTGRES_PASSWORD` were both unset in that shell. Every pg test therefore hit its `t.Skip` (the run took 0.32s) and nothing touched a database. So those tests were compiled but not run for real. I did not verify the mutation evidence by running it; I only checked it by reasoning, below.
>
> **Low: an org admin with a nil `s.queries` now panics instead of getting a clean 500.**
> - Where: `eami-api/internal/api/middleware.go:176`, the new admin branch, which calls `s.queries.DB()`.
> - Before: the admin branch never touched the DB. With a nil `queries`, the handler's `requireQueries` returned a clean 500.
> - Now: `(*Queries)(nil).DB()` dereferences nil, and chi's Recoverer turns that into a generic 500.
> - The non-admin branch already had this behaviour, so it is not new in kind. No non-pg test hits `/v1/workspaces/{id}` routes (grep checked), so nothing breaks.
> - Fix (optional): add `if s.queries == nil { writeError(...500...); return }` at the top of the middleware.
>
> **Low/Info: two comments now say something untrue.**
> - `middleware.go` ~147-151, the `requireWorkspaceRole` doc comment, still says a workspace in a different org "fails closed with 403". That is only true for non-admins; admins now get 404 "workspace not found". It should mention both.
> - `workspaces_pg_test.go:408-411` still says "Org A's own admin bypass short-circuits BEFORE the workspace lookup in the current design". That is no longer true. The assertion there (404 or 403) still passes, so only the comment needs updating (the file is out of scope; log it or fix it in the same commit).
>
> **Info: status-code changes for an org admin on a foreign or nonexistent workspace.** All of these are improvements. No existing test or UI code depends on the old codes: I grepped the pg tests' status assertions and `eami-ui/src` for `status ===` checks.
>
> | Route | Before | Now |
> |---|---|---|
> | `POST /members` | 400 "user does not belong to this workspace's org" | 404 "workspace not found" |
> | `PATCH` / `DELETE /members/{u}` | 404 "membership not found" | 404 "workspace not found" |
> | `GET /members` | 200 with an empty list | 404 |
> | `GET /policies` | 200 with the admin's own org-floor policies (query is `org_id=uc.OrgID AND (workspace_id=$2 OR NULL)`) | 404 |
> | `POST /policies` | Probably a 500 that echoed the DB error text, via FK violation or (my guess) a policies org trigger; I did not open the trigger | 404 |
> | `PATCH /policies/{id}`, `DELETE /policies/{id}` | 404 from the handler | 404 from the middleware |
> | `PATCH` workspace | 404 "workspace not found" | same code and message, now from the middleware |
>
> Two side effects of these changes: for an admin, a malformed body sent to a foreign or nonexistent workspace now gets 404 instead of 400; and admins get 404 while non-admins get 403. Each is uniform within its own role, so neither is an existence oracle, but the convention is now split. That is documented in the code comment and is acceptable. `eami-ui/src/hooks/useWorkspaces.ts` doesn't branch on status codes. `api/openapi.yaml` contains no `/v1/workspaces` paths, so there is no contract drift.
>
> **Info: handlers outside this scope still echo `err.Error()` on 500s.** These are `AddWorkspaceMember`, `ListWorkspaceMembers`, `UpdateWorkspace` and `CreateWorkspacePolicy`. That is the same class of leak this change removes from the two member handlers. Suggest logging it in NOTES.md or BACKLOG rather than widening this change.
>
> **Verified clean**
>
> **Layer 1 (middleware).** The query `SELECT EXISTS(... WHERE id=$1 AND org_id=$2)` binds `workspaceID` then `uc.OrgID`, so the parameter order is correct. `EXISTS` always returns one row, so `Scan` fails only on real DB errors; those get slog plus a generic 500, and `err` is not echoed. A false result returns 404 "workspace not found", and `next` is only reached when the result is true. `workspaceID` is parsed before the branch, so a bad UUID still gets 400.
>
> **Bypass by other roles.** Only the exact string `"admin"` takes the bypass. `platform_admin` (`router.go:322`) and every other role go through the membership lookup. That lookup is joined to `w.org_id = $3` (the caller's org) and fails closed with 403, so it cannot be bypassed the same way. `requireWorkspaceRole` is only used on `router.go` lines 284 and 299. `DELETE /v1/workspaces/{id}` sits in the `requireRole(admin)` group, and `DeleteWorkspace` scopes itself by org.
>
> **Legitimate same-org flows.** An own-org admin gets `EXISTS=true` and passes as before. Non-admin workspace members and admins follow the unchanged path. Workspace org cannot be reassigned through the API, so there is no meaningful race between the middleware check and the handler.
>
> **Layer 2 (store).** Update SQL: `$1=role, $2=user, $3=ws, $4=org` matches the argument order `role, userID, workspaceID, orgID`. Delete SQL: `$1=user, $2=ws, $3=org` matches. The `IN (SELECT id FROM workspaces WHERE org_id=$n)` predicate is correct. Handlers now pass `uc.OrgID`, return 404 on 0 rows, and log plus return a generic 500 on error.
>
> **Build and vet.** `go build ./...` and `go vet ./internal/api ./internal/store` both pass.
>
> **Would each test fail without its layer?** (by reasoning, not by running)
> - **HTTP test without layer 1:** the nonexistent-workspace baseline body would be "membership not found", so the `strings.Contains("workspace not found")` pin fails. Cross-org `GET /members` would return 200 instead of 404. If layer 2 were also removed, the victim-role checks would catch the change.
> - **Byte-identical comparison:** `writeError` produces deterministic JSON (`{code, message}`), so comparing bodies byte for byte is valid.
> - **Store test without layer 2:** the wrong-org calls would affect 1 row instead of 0. The right-org positive control (`n==1`) stops it passing vacuously.
> - **Internal sentinel test without layer 1:** the handler would be reached for `wsB` and for the random UUID, so it fails. The own-org positive control (reached, 204) stops a vacuous pass, and it covers both `minRole` values.
> - **Same-org test:** covers an org admin and a non-admin `workspace_admin` (operator token) doing promote, demote and remove, with each change checked in the DB, plus a 404 for a nonexistent member.
>
> **Pool lifecycle and leaks.** The internal test registers `t.Cleanup(pool.Close)` before the per-org DELETE cleanups, so the pool closes after them, as CLAUDE.md requires; it uses no `defer`. The HTTP and store tests use `newWorkspaceTestEnv`, which follows the same `t.Cleanup` order. Its hand-rolled DSN is justified: `toolsUpdateTestDSN` lives in package `api_test` and can't be imported from package `api`; the fallback matches that helper. Leak risk: groups and workspaces cascade from `orgs` (`ON DELETE CASCADE`, migration 000021), memberships cascade from workspaces and users, and orgs are deleted in cleanup.
>
> **Not covered by tests:** the middleware's DB-error 500 path.

**Disposition:**
- **Nil guard:** added (`requireQueries`).
- **Both comments:** corrected.
- **The runs it couldn't do:** done by Claude Code with credentials (§3), including the mutation runs it had only reasoned about.
- **Status-code changes:** recorded below (§6).
- **`err.Error()` echoes:** recorded under the app-wide item.

### 5b. Security review

> **Verdict: B-233 is closed.** No High or Medium findings. An org-A admin can no longer read or change org-B workspace memberships or workspace policies through any route guarded by `requireWorkspaceRole`. The fix adds nothing new that can be exploited.
>
> Evidence: I read the full diff, the new store file and both new test files. `go build ./...` and `go vet ./internal/api/ ./internal/store/` pass. As instructed, I did not run the real-Postgres tests.
>
> **Findings**
> - **Low (test gap):** `workspace_member_org_pg_test.go:33-82` covers cross-org PATCH/DELETE on members and GET on members. It does not cover these cross-org admin routes: `PATCH /v1/workspaces/{id}`, `POST .../members`, and `POST`, `PATCH`, `DELETE` and `GET .../policies`. All of these are protected by the same middleware branch, which I verified by reading the code. A table-driven case per route in the same test would pin that.
> - **Low (behaviour change, not security):** an org admin now gets 404 for `GET .../members` and `GET .../policies` on a workspace that doesn't exist. Before the fix this returned 200 with an empty list or the org-floor policies. Nothing in the repo depends on the old behaviour; the UI could be checked to be sure.
> - **Info (existing, out of scope):** `jwtMiddleware` (`middleware.go:35-70`) takes `Role` from the JWT and never re-reads the DB. A demoted or soft-deleted admin keeps the admin bypass until the token expires, but only within their own org, so it is not cross-tenant. Separately, org-level `viewer` users holding `workspace_admin` can write to workspace routes because `viewerReadOnly` isn't applied there. This is by design (B-197).
> - **Info (nil DB):** the admin branch now calls `s.queries.DB()` before the handler's `requireQueries` check. With a nil `queries` this panics, `Recoverer` turns it into a 500, and it still fails closed. The non-admin path already behaved this way.
>
> **Checked and clean**
> - **Every route behind the middleware** (`router.go:283-306`, 9 routes: workspace PATCH, members POST/PATCH/DELETE/GET, policies POST/PATCH/DELETE/GET). The `workspaceId` param is always present and parsed before any branch runs. Admin branch (`middleware.go:175-192`): `EXISTS(workspaces WHERE id=$1 AND org_id=$2)` comes before `next`; a foreign workspace gets 404. Every other role (operator, viewer, approver, platform_admin, and anyone with only a workspace role) takes the membership lookup at `middleware.go:194-213`, which joins `workspaces` and requires `w.org_id = uc.OrgID`; no row gives 403, and the 403 path is unchanged. `platform_admin` gets no bypass here.
> - **Admin-only routes outside this middleware:** `DELETE /v1/workspaces/{id}` and `POST /v1/workspaces` are under `requireRole("admin")`. Delete uses `SELECT group_id FROM workspaces WHERE id=$1 AND org_id=$2`, so it is org-scoped. Create inserts group, workspace and the creator's membership in one transaction, all with `uc.OrgID` and `uc.UserID`.
> - **Defense in depth in the handlers:** the new store writes (`workspace_memberships.sql.go`) add `AND workspace_id IN (SELECT id FROM workspaces WHERE org_id=$n)` to UPDATE and DELETE, with parameters bound in the right order. `UpdateWorkspace` is scoped by `org_id` (`workspaces.go:272`). `ListWorkspaceMembers` joins with an `org_id` filter. `AddWorkspaceMember` runs one EXISTS check that the user, the workspace and `uc.OrgID` all belong to the same org, then inserts; the check and insert aren't atomic, but that only matters if an org can change, and it can't (next point). Workspace policies: Update and Delete filter on `id + org_id + workspace_id` (`workspace_policies.go:162,222`); List filters `p.org_id=$1`; Create inserts `org_id=uc.OrgID`, and the DB trigger `trg_policies_workspace_org_match` rejects a foreign `workspace_id` on its own.
> - **Race between the check and the write:** no code path changes `workspaces.org_id`, `users.org_id` or `groups.org_id`. Every `UPDATE users/workspaces/groups` I found touches only name, role, password or `deleted_at`. A workspace deleted between check and write just gives 0 rows and a 404. The store writes re-check the org inside the same statement anyway.
> - **Existence oracles:** for an admin, a foreign workspace and a nonexistent one both return 404 `"workspace not found"` from the same single query, before the handler runs, so the handler's user-id parsing, body/role validation and "membership not found" can't reveal whether a foreign user exists. The timing difference is one primary-key index probe plus an org filter: microseconds, below network noise; I consider it negligible, but it is not strictly constant-time. The non-admin 403 is identical for foreign and nonexistent workspaces. Whether you get 404 or 403 depends on the caller's own role, not on the target, so it reveals nothing. Handler 500s no longer echo `err.Error()` in the two changed handlers; other handlers still do, but those are same-org only.
> - **Other writers to the membership tables:** `workspace_memberships` is written only in `workspaces.go` (Create, Add, Update, Remove) and in tests. `group_memberships` has no writes anywhere in non-test Go code. eami-gateway, the collector and the policy module never write `workspace_memberships`; gateway references to `workspaces` are test fixtures only.
> - **Live dev DB (read-only SELECTs):** 0 memberships where the user's org differs from the workspace's org; 0 workspaces whose group belongs to a different org; the only non-internal trigger on these tables is `trg_workspaces_updated_at`.
> - **Middleware sweep across eami-api (non-test code):** `uc.Role` is compared in exactly three places: `requireRole` (a set lookup with no org branch, and every handler behind it scopes by `uc.OrgID`), `requireWorkspaceRole:175` (now fixed), and `viewerReadOnly` (it only denies). `requireModuleLicensed` checks the license's own org claim against `uc.OrgID`. I found no other shortcut that skips the org check.
> - **Tests:** `workspace_role_org_internal_pg_test.go:42` registers `t.Cleanup(pool.Close)` before the per-org DELETE cleanups, which is the correct order under the CLAUDE.md rule. The cross-org test compares the 404 body for every case against the nonexistent-workspace baseline, then confirms the victim's roles are unchanged.

**Disposition:**
- **Test-gap Low:** fixed; all 9 routes are now covered.
- **Behaviour-change Low:** verified that the UI only requests lists for workspace IDs from the org-scoped list, so nothing depends on the old 200. Recorded in §6.
- **Info items:** recorded, and pre-existing or by design.

## 6. Recorded (behaviour changes and follow-ups)

**Behaviour changes for an org admin on a foreign or nonexistent workspace.** All of these are now 404 "workspace not found", and nothing in the repo depends on the old codes. For comparison, before the fix:
- `POST /members` returned 400;
- `GET /members` returned 200 with an empty list;
- `GET /policies` returned 200 with the org-floor policies;
- `POST /policies` returned a 500 that echoed the DB error.

**Convention now split by role:**
- admin: 404;
- non-admin: 403.

Each is uniform within its role, and this is documented in the code.

**Existing items the reviews re-surfaced:**
- **The app-wide raw `err.Error()` echo in 500s** (needs a founder B-ID). The reviews name, among others: `AddWorkspaceMember`, `ListWorkspaceMembers`, `UpdateWorkspace`, `CreateWorkspacePolicy`, `CreateAgent` and `UpdatePolicy`.
- **JWT roles are not re-validated against the DB** (in-org only), and **viewers holding `workspace_admin` can write** (by design, B-197). Both are Info.
