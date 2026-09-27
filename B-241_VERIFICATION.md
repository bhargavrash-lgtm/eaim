# B-241 Verification Record — dead `POST /v1/approvals` removed

Written 2026-09-27 by Claude Code, at founder direction ("proceed with the removal now, confirmed it doesn't touch the Architect-owned contract"). Raw logs are in session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad: `b241_live.log`, `b241_mutation.log`, `b241_api_full.log`, `b241_before.txt` and `b241_after.txt`.

## 1. What was removed, and why it was safe
- **Why it could go:** the route had **no caller** (B-237 Part A): no service, no UI, no script. The gateway inserts its own approvals directly (`eami-gateway/internal/approval/router.go` `Submit`), using server-resolved identity.
- **Contract:** `api/openapi.yaml` defines only `GET /v1/approvals`, so there is no contract change.
- **Removed:**
  - the route (`router.go`);
  - `CreateApproval` and `CreateApprovalRequest` (`approvals.go`);
  - `queriesAdapter.CreateApproval`;
  - `MockStore.CreateApproval`, `MockCreateApprovalParams`, the `Store` interface method, and the mock's `CreateApprovalErr`/`CreateApprovalCalls`;
  - `store.CreateApprovalParams`, `createApprovalQuery` and `Queries.CreateApproval`, plus the `query/approvals.sql` block;
  - `approval_org_pg_test.go` (B-237's tests of this route);
  - the two mock create tests.
- **Why keep nothing of the store insert:** it had no remaining caller once the route was gone, and the gateway has its own insert. B-237's org-scoped SQL existed only to protect this route.
- **Added:** `TestCreateApprovalRoute_Removed` asserts that POST on the list path returns 405 for admin, operator and viewer.
- **Unchanged:** list, get and decide; the gateway insert and the notification path; `eami-gateway`, which had no diff.

## 2. Automated verification
```
eami-api: go build ./... ; go vet ./... → clean (the three touched files were already unformatted at HEAD, so they were left as they were)
eami-api: go test -count=1 -v ./... → every package ok; top-level PASS=495 FAIL=0 SKIP=0 (499 − 3 B-237 route tests − 2 mock create tests + 1 removal test)
eami-gateway: go build ./... ; go test ./internal/approval/... → ok
```
**Mutation check:** re-registering a POST handler on `/v1/approvals` fails the new test (`b241_mutation.log`, verbatim):
```
--- FAIL: TestCreateApprovalRoute_Removed (0.10s)
    approvals_test.go:88: admin POST /v1/approvals = 201, want 405 (route removed)
FAIL
FAIL	github.com/eami/api/internal/api	0.347s
```

## 3. Live verification (rebuilt API container, created 2026-09-27T17:17:37Z; `b241_live.log`, verbatim)
```
── (1) the removed route
ADMIN    POST /v1/approvals                                                : 405 
OPERATOR POST /v1/approvals                                                : 405 
approval_requests rows before / after                                      : 32 / 32
ADMIN    GET  /v1/approvals (list still served)                            : 200 {"data":[{"id":"2d508a9f-e2cb-4631-a435-53c995adc452","agent_id":"34d58bef-05d9-4185-8f2f-773b179b515c","agent_name":"b167-redaction-liveverify","tool_name":"claude","action":"messages","parameters":{
── (2) gateway escalation path, end to end
create agent API key                                                       : 201
gateway token issue                                                        : 200 (token received)
open MCP SSE session                                                       : 200
POST tool_call manual_verify_tool.read (matches the escalate policy)       : 202
gateway-inserted approval row                                              : 84721b72-9192-426f-87f1-1f312b5e6825|node=7654598e4a32|status=pending
ADMIN GET /v1/approvals/{id}                                               : 200 {"id":"84721b72-9192-426f-87f1-1f312b5e6825","agent_id":"5f17a751-dcce-421e-bc3c-29324f7773c6","agent_name":"b241-gw-agent","tool_name":"manual_verify_tool","action":"read","parameters":{},"justificat
ADMIN approves via POST /v1/approvals/{id}/decide                          : 200 {"id":"84721b72-9192-426f-87f1-1f312b5e6825","agent_id":"5f17a751-dcce-421e-bc3c-29324f7773c6","agent_name":"b241-gw-agent","tool_name":"manual_verify_tool","action":"read","parameters":{},"justificat
SSE result delivered to the agent                                          : {"jsonrpc":"2.0","id":1,"result":{"args":{},"data":{"tool":"manual_verify_tool","action":"read","session_id":"f2acc207eb55da94dccf7869b0a1b836"},"files":{},"form":{},"headers":{"host":"postman-echo.com","user-agent":"Go-
```
- POST returns 405 for admin and operator, and the approval count is unchanged (32 → 32). Listing still works.
- The gateway's own escalation path works end to end:
  1. The agent's key gets a token and opens an MCP SSE session.
  2. A `tool_call` matching Dev Org's active "manual verify" escalate policy makes the gateway insert its own approval.
  3. The approval is read and approved through the API.
  4. The result is delivered over SSE.
- **Disclosed:** approving, rather than denying as B-237's run did, made the gateway execute Dev Org's existing `manual_verify_tool`, whose configured upstream is the public `postman-echo.com`. One real outbound request went to that echo service.

**Cleanup.** In one transaction I deleted the fixture approval, the gateway-recorded episode, the token-issue event, the fixture API key, the fixture agent (its config cascades), the fixture users and their refresh tokens. The fixture password file was removed.

`diff b241_before.txt b241_after.txt` is identical **except `audit_log` 1667 → 1669**. Those two rows are the escalation and its approval. They were left deliberately: the audit log is hash-chained and tamper-evident, and leaving such rows is B-099's precedent.

## 4. Reviews (both mandatory passes; quoted verbatim)

After the reviews, the code review's Low (a stale "approval creation" in BUILT.md's role matrix) was fixed.

### 4a. Code review
> ## B-241 review: removal of `POST /v1/approvals`
>
> **Verdict: clean.** I found no High or Medium issues. There is one stale doc line (Low) and a few Info notes.
>
> ### Commands run
> - `git diff HEAD --stat -- eami-api`, the full `git diff HEAD` of all 8 eami-api files, `git diff HEAD -- BACKLOG.md CONTEXT.md`, `git status --short`, and `git show HEAD:eami-api/internal/api/approval_org_pg_test.go` to see what the deleted file contained.
> - `go build ./... && go vet ./...` in `eami-api`: clean. `go vet` also compiles the `_test.go` files, so `MockStore` still satisfies `Store` and no test references a removed symbol.
> - `go build ./... && go vet ./...` in `eami-gateway`: clean.
> - Read-only greps across the whole repo, excluding node_modules, for `CreateApproval|createApprovalQuery|CreateApprovalRequest|MockCreateApprovalParams|CreateApprovalErr|CreateApprovalCalls|approval_org_pg_test`, for `POST /v1/approvals` and for `'/v1/approvals'`. I also grepped the router, `api/openapi.yaml` and chi's version (`go-chi/chi/v5 v5.3.0`).
> - I did not run `go test`, which is outside the allowed commands. No DB tests were run.
>
> ### Check 1: nothing still references the removed code
> - **Go (all 5 modules):** the only remaining `CreateApproval` hit is the new test name `TestCreateApprovalRoute_Removed` (`eami-api/internal/api/approvals_test.go:78`). Nothing in eami-gateway, eami-collector, eami-agent or eami-policy references it; the gateway inserts its own approvals.
> - **UI:** `eami-ui/src/hooks/useApprovals.ts:31` and `useDashboard.ts:26` only call `api.GET('/v1/approvals')`. Nothing POSTs to it.
> - **OpenAPI:** `api/openapi.yaml:1792` defines `/v1/approvals` with GET only; `/{approvalId}` and `/{approvalId}/decide` are unchanged. No contract change.
> - **Scripts:** no caller.
> - **Docs:** see the Low finding below. Every other mention is a historical record, not a description of live API: `B-237_VERIFICATION.md`, `ORG_BRANCH_ASYMMETRY_SWEEP.md`, the `BUILT.md` B-237 entry (lines 45-51), `CONTEXT.md` and `tasks/TASK-044`/`TASK-059`.
>
> ### Check 2: list, get, decide and the gateway flow are unaffected
> - In `router.go`, `GET /v1/approvals` and `GET /v1/approvals/{approvalId}` (lines 457-458) and `POST /v1/approvals/{approvalId}/decide` (line 375) are still registered with the same role groups.
> - In `approvals.go`, the diff is removals only. `ListApprovals`, `GetApproval` and `DecideApproval` are unchanged. The shared helpers `approvalToResp`, `storeApprovalToResp` and `decodeJSON` are still used (approvals.go:105/126/152/160/206/224).
> - In `store/approvals.sql.go`, `scanApproval`, `approvalCols`, `GetApproval`, `ListApprovals`, `CountApprovals` and `DecideApproval` are intact.
> - There is no eami-gateway diff, so the gateway's own insert in `internal/approval/router.go` and the notification and dispatch path are untouched. The removed handler never called any notifier, so the notification path loses nothing.
>
> ### Check 3: the removal is complete
> - The handler, request type, `queriesAdapter` method, `Store` interface method, mock method and params, the mock `Err`/`Calls` fields, the store params, query and method, and the `.sql` block are all gone. The only imports dropped were `errors` and `log/slog`; the build confirms the remaining ones are still used.
> - The deleted `approval_org_pg_test.go` had three helpers local to that file (`approvalBody`, `seedApprovalPolicy`, `approvalsReferencing`). They are gone with it, and `go vet` shows nothing else needed them.
> - `validApprovalPayload` and `approvalOperatorID` remain, and the new test uses both (approvals_test.go:28, 33, 83-85).
> - No dead helpers are left.
>
> ### Check 4: the regression test is meaningful
> - In chi v5, `r.Group` registers on the same routing tree. `/v1/approvals` still has a GET endpoint, so a POST there resolves to the tree's 405 handler before any group middleware runs, including JWT and `requireRole`.
> - The test builds its server through `NewHandler(...).Router()`, and `Router()` just returns `Handler()` (router.go:166). So it exercises the real route table.
> - If `r.Post("/v1/approvals", …)` is re-added in any group, admin would get 201 or 400 and viewer would get 403, not 405, so the test would fail. It guards the removal correctly.
>
> ### Check 5: nothing out of scope changed
> - The eami-api diff is limited to the 8 files listed in the brief.
> - Other working-tree changes:
>   - `BACKLOG.md` only changes the B-243 heading and adds a founder flag. That is B-243 bookkeeping, not B-241.
>   - `CONTEXT.md` only changes the ACTIVE AGENT marker.
> - The untracked files (`.local-test-agent/`, `AGENTS.md`, `CODEX_NEXT_BRIEF.md`, `JEV_ORCHESTRATION_ROADMAP.md`, the license and npm audit JSON/TXT files) were already there at session start. They are not part of this change and should not be swept into its commit.
>
> ### Findings
>
> **Low: `BUILT.md:808` still lists the removed route as live.**
> - The role-matrix line reads "JWT + admin/operator: … alert-rule CRUD, **approval creation**".
> - Scenario: a reader, or a later agent scoping work from BUILT.md, assumes an admin/operator approval-create endpoint exists.
> - Fix: drop "approval creation" in the B-241 BUILT.md entry at session end. BUILT.md is not modified yet, which the CLAUDE.md hard rule requires before the commit.
>
> **Low (process): the B-241 docs are not written yet.**
> - In `BACKLOG.md`, B-241 (line 2696) is still marked "QUEUED".
> - `BUILT.md` has no B-241 entry.
> - Both need updating before the commit.
> - The BACKLOG B-241 entry (lines 2702-2708) also lists the planned verification, including a live check that the gateway escalate→approve/deny round trip still works. That live check has not been run. Neither has `go test` for the new test; I could not run it under this review's constraints.
>
> **Info: unauthenticated POST now gets 405, not 401.**
> - Chi resolves method-not-allowed before the JWT middleware, so an unauthenticated POST gets 405.
> - It reveals only that GET exists on a path the public OpenAPI spec already documents. No action needed.
> - Because of this, the loop over admin/operator/viewer adds no coverage today: the 405 does not depend on role. It is still harmless and documents the intent.
>
> **Info: the historical docs are correct to keep as they are.**
> - `B-237_VERIFICATION.md`, `ORG_BRANCH_ASYMMETRY_SWEEP.md` rows 23/47 and the BUILT.md B-237 entry describe the route as it was.
> - Optionally, add a one-line "route removed in B-241" pointer in the sweep ledger or B-237 entry.
>
> **Info:** a background repo-wide `grep -rnE` I started timed out and was moved to the background. It was superseded by the equivalent ripgrep searches above, whose results this report uses.

### 4b. Security review
> ## B-241 security review: removal of `POST /v1/approvals`
>
> **Verdict: PASS.** I found nothing at High or Medium severity. Removing the route closes the same-org spoofing and FK-blocking vector. Nothing else can create approvals from a client, and nothing depended on the removed insert. The approvals area has a few Low and Info items, none caused by this change.
>
> ### What I ran
> - `git diff HEAD --stat -- eami-api` and a read of the diffs to `router.go`, `approvals.go`, `query/approvals.sql`, `store_adapter.go` and `store_mock.go`. 8 files changed, +16/−493. `git diff HEAD --stat -- eami-gateway` is empty, so the gateway is unchanged.
> - `go build ./...` in eami-api: OK. `go vet ./...` in eami-api: OK. `go build ./...` in eami-gateway: OK. I ran no tests, per the instructions.
> - Read-only greps across eami-api, eami-gateway, eami-collector, eami-policy, eami-ui/src, `schema/` (triggers and functions), `api/openapi.yaml`, `docker/caddy`, `eami-ui/nginx.conf` and the collector mux.
>
> ### 1. Remaining write paths into `approval_requests`
> Production code has exactly four:
>
> | Write | Who can reach it | Where org, agent and policy come from |
> |---|---|---|
> | `eami-api/internal/store/approvals.sql.go:131` `DecideApproval` UPDATE | JWT roles admin, operator or approver via `POST /v1/approvals/{id}/decide` (`router.go:373-375`) | `org_id` is the JWT's `uc.OrgID` and `approved_by` is the JWT's `uc.UserID` (`approvals.go:187-193`). `WHERE id=$1 AND org_id=$2 AND status='pending'`. It only changes status, approver and reason; it cannot create a row or change agent, policy or descriptive fields. |
> | `eami-gateway/internal/approval/router.go:433` `Submit` INSERT | Only the gateway's escalate branch (`cmd/gateway/dispatcher.go:632-673`, the only `Submit` caller) | Server-derived (see §2). The row has no `policy_id` column. |
> | `eami-gateway/internal/approval/router.go:550` expire UPDATE | Gateway only, when the hold times out | Keyed by an approval ID the gateway generated itself. `WHERE status='pending'`. |
> | `eami-gateway/internal/approval/router.go:878` `resume_outcome` UPDATE | Gateway only, after dispatch | Keyed by an approval ID the gateway generated itself. |
>
> - All other `UPDATE approval_requests` hits are in `_test.go` files.
> - There are no DB triggers or functions that insert into the table.
> - The collector mux (`eami-collector/internal/api/query.go:69-72`) never touches approvals.
> - The OpenAPI spec (`api/openapi.yaml:1792`) and the UI's generated `schema.ts` only define GET on `/v1/approvals`.
> - The UI only calls GET on the list and item routes, plus POST on `.../decide` (`useApprovals.ts:31,51`).
>
> ### 2. The gateway's `Submit` is unchanged and derives identity on the server
> - `approval.Request` is built from `ac.OrgID`, `ac.AgentUUID` and `ac.AgentName` (`dispatcher.go:632-640`).
> - For MCP calls, those values come from `sess.Agent`, the registered agent record behind the validated bearer JWT (`internal/mcp/handler.go:399-420`).
> - For workflow runs, they come from `LookupByNameAndOrg(claims.Subject, claims.OrgID)` (`internal/workflow/http.go:65-92`).
> - `justification`, `risk_level`, `expires_at` and `gateway_node_address` are set by the server (`router.go:397-420`). Tool, action and parameters are the agent's real governed call, which is what approvers need to see.
> - On approval, the gateway dispatches the in-memory `entry.req` (`router.go:702-743`), not the DB row's contents. A spoofed DB row could never have caused a dispatch.
>
> ### 3. No hole from removing the org-scoped store insert
> - `store.CreateApproval`, `CreateApprovalParams`, the `Store` interface's `CreateApproval`, `MockCreateApprovalParams` and the adapter are all gone.
> - A repo-wide grep finds `CreateApproval` only in the new negative test (`approvals_test.go:78`), and the builds pass.
> - The gateway never called that insert; it has its own.
> - The org-scoped `INSERT…SELECT` protected only the removed handler.
> - The removed mock/adapter path (`storeIface.CreateApproval`) had no org check on references. It is gone too, which is a small net gain.
>
> ### 4. Router behaviour
> - chi v5.3.0 (`eami-api/go.mod:7`). `GET /v1/approvals` is still registered (`router.go:457`), so `POST /v1/approvals` matches the path but not the method, and chi returns 405. `TestCreateApprovalRoute_Removed` asserts this for admin, operator and viewer.
> - `router.go` has no `Mount`, no `/*` wildcard, no custom `NotFound` or `MethodNotAllowed`, and no proxy. `Group` only wraps middleware inline.
> - Edge routing:
>   - Caddy sends :443 to eami-ui (`docker/caddy/docker-entrypoint.sh:114-117`), whose nginx sends `/v1/` to `eami-api:8081` (`eami-ui/nginx.conf:7-8`). A POST ends at the api's 405.
>   - Caddy sends :8443 to the gateway. The gateway mux (`cmd/gateway/main.go:282-309`) has no `/v1/approvals` pattern, so a POST there gets 404 and is not forwarded.
>   - Caddy sends :8888 to the collector. It only proxies `GET /v1/agent-config/{id}`.
>
> ### 5. Other findings in the approvals area
>
> **Low**
> - **L1. Decide does not check expiry, and approvals can be left pending.**
>   - `approvals.sql.go:131-137` only filters `status='pending'`, not `expires_at > now()`.
>   - The gateway marks a row expired only on a genuine hold timeout. On a cancelled context (client disconnect, `router.go:529-539`) or a gateway restart, the row stays `pending` forever. No sweeper exists: a grep for `'expired'` or `expires_at <` finds nothing.
>   - An approver can later mark such a row `approved`. Nothing is dispatched, because `resolve()` returns early with no pending hold (`router.go:702-706`). But the stored record says "approved" for an action that never ran, and the list keeps showing stale pending items.
>   - This is an integrity and UX problem, not an execution risk. Suggested fix: add `AND expires_at > now()` to the decide query and/or add an expiry sweeper.
> - **L2. Decide's 500 path leaks raw DB errors.** `approvals.go:202` (and `:221` on the mock path) return `err.Error()` to the client. This is inconsistent with the B-237/B-238 non-leaking style.
>
> **Info**
> - **I1.** `approvals.go:198` and `:217` compare errors with `==` rather than `errors.Is`. It works today because `scanApproval` returns the error unwrapped (`approvals.sql.go:~30`).
> - **I2.** The `decided_by` field in the request body is ignored on the real path, where `approved_by` is the JWT's UserID. It is used only by the mock path (`approvals.go:182-185, 209-214`). Harmless, but the mock and real paths diverge.
> - **I3.** 405 is returned before `jwtMiddleware` runs, because chi's method-not-allowed handler bypasses group middleware. An unauthenticated POST therefore gets 405, not 401. The route's existence is already visible, so this is not material.
> - **I4. Notification path is fine.**
>   - `NotifyApprovalDecision` (`approvals.sql.go:38-42`) sends only `{"approval_id"}` over `pg_notify`. The gateway re-reads the status from the DB and acts only on its own in-memory holds.
>   - Gateway Slack notification (`router.go:1067-1120`): the webhook comes from operator config or env (`config.go:217`), not from any API user, so B-238's SSRF class does not apply. It uses a bare `http.Client` without the netguard guard, but that is acceptable for operator-trusted config. Text fields are mrkdwn-escaped. `uiBaseURL` and `approvalID` are server-controlled.
> - **I5. Spoofed rows from before the removal are not cleaned up.** Any same-org rows already created through the old route stay in the DB and still block agent/policy deletion through the NO ACTION FKs. Removing the route stops new ones but does not remove existing ones. If any production deployment exposed the route, consider a one-off query for rows whose `gateway_node_address` or justification don't match the gateway's `"Escalated by policy: tool.action"` pattern.
> - **I6.** `B-237_VERIFICATION.md` still documents the route as live, which is historical and fine. `BACKLOG.md:2615` and BUILT.md should record the B-241 removal when the session closes.
>
> ### Relevant files
> - `C:\AI\EAIM\eaim\eami-api\internal\api\router.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\approvals.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\approvals_test.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\store\approvals.sql.go`
> - `C:\AI\EAIM\eaim\eami-gateway\internal\approval\router.go`
> - `C:\AI\EAIM\eaim\eami-gateway\cmd\gateway\dispatcher.go`
> - `C:\AI\EAIM\eaim\eami-ui\nginx.conf`
> - `C:\AI\EAIM\eaim\docker\caddy\docker-entrypoint.sh`

## 5. Review follow-ups
- **Security I5 (spoofed rows left behind), checked on the shared DB, read-only:** all 32 `approval_requests` rows have the gateway's `"Escalated by policy:"` justification. **0** rows came from the removed route, so there is nothing to clean up.
- **Security L1** (decide doesn't check `expires_at`; rows orphaned by a gateway restart or client disconnect stay pending forever): this existed before B-241 and isn't caused by it. It is recorded here as a proposed follow-up, **not minted**, pending the founder.
- **Security L2** (`DecideApproval`'s 500 echoes `err.Error()`) is already covered by B-234.
