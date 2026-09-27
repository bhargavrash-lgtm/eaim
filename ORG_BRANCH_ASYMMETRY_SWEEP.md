# Org-Ownership Branch-Asymmetry Sweep — COMPLETE (69/69)

Started 2026-09-27 by Claude Code, from a founder brief. **Completed 2026-09-27.**

## Result

| | Count | Endpoints |
|---|---|---|
| **PASS** | 67 | everything not listed below |
| **FAIL** | 2 | **B-237** (`CreateApproval`: cross-org references), **FIXED** 2026-09-27; **B-238** (webhook SSRF: the dial guard on one path is missing on its sibling), **FIXED** 2026-09-27 |

The two FAILs are counted by finding, not by row: B-238's source is shared between rows 54 and 55. Rows 24–26 PASS only because of B-232/B-233, which were fixed before this sweep.

**No other org check was found that runs on one branch only.**

**Info items recorded, not minted.** Each is a founder or design decision, not a branch asymmetry:
- **#46 InviteUser:** `users.email` is globally unique, which tells an admin whether an email has an account in any org.
- **#67/#69:** the single global service key trusts a body `org_id`.
- **#23 (post-fix):** `POST /v1/approvals` has no legitimate caller.

**Question asked of every mutation endpoint in eami-api:** is org-ownership validation guaranteed on **every** code path that can reach a DB write? Or can a branch skip it — a "no existing row" case, a route bypassing shared middleware, or a fallback? That is the shape B-232 and B-233 shared.

**Scope interpretation.** A write that stores a caller-supplied **reference** to another org's row (a foreign key that isn't org-validated) counts as the same failure. The org check covers the row's own `org_id` but not the references it writes, so another tenant's data is reachable through a path the check doesn't cover.

**The inventory is complete: 69 mutation endpoints**, all in `eami-api/internal/api/router.go`:
- 65 `r.Post/Put/Patch/Delete` routes;
- login;
- 3 service-key ingest routes.

**How each is audited:**
- read the handler;
- trace every branch to each DB write;
- read the SQL for its WHERE / org predicate;
- check how any caller-supplied foreign ID is validated.

## Findings (reported immediately, per the brief)

> **Status note (2026-09-27):**
> - **B-237 is FIXED and verified.** Handler ownership checks plus an org-scoped insert. Each layer's removal is caught by its own test. It was live-verified on the final build, including the gateway's own escalation path. See `B-237_VERIFICATION.md`.
> - **The sweep resumed at endpoint 27.**
> - **B-238 is FIXED and verified (2026-09-27).**
>   - Both webhook senders now use one guarded client, with save-time validation and a single failure reason.
>   - The shared guard is hardened.
>   - It was live-verified on both the route and the real alert-engine path.
>   - See `B-238_VERIFICATION.md`.

### B-237 — `POST /v1/approvals` (`CreateApproval`) stores unvalidated cross-org agent and policy references — **FAIL, live-confirmed → FIXED 2026-09-27**

**The problem**
- The handler writes the approval with the caller's `org_id`.
- It inserts the body's `agent_id` and `policy_rule_id` **as given**, with no check that they belong to the caller's org.
- `approval_requests.agent_id` and `policy_id` are FKs with **NO ACTION** on delete, so they reference other orgs' rows unchecked.

**Live on the running stack** (`sweep_approvals_live.log`, verbatim). The attacker is an operator in a separate throwaway org; the victim is a Dev Org fixture agent and policy.
```
ATTACKER (other org, operator) POST /v1/approvals with victim agent+policy : 201 {"id":"2ff40758-…","agent_id":"60f254bc-…","status":"pending","policy_id":"58ddd3ec-…",…}
ATTACKER POST /v1/approvals with a NONEXISTENT agent id                     : 500 {"code":"internal_error","message":"ERROR: insert or update on table \"approval_requests\" violates foreign key constraint \"approval_requests_agent_id_fkey\" (SQLSTATE 23503)"}
VICTIM OWNER DELETE own agent                                               : 409 {"code":"conflict","message":"cannot delete an agent with existing episode, approval, or workflow-run history -- suspend it instead (this preserves its audit trail)"}
VICTIM OWNER DELETE own policy                                              : 500 {"code":"internal_error","message":"ERROR: update or delete on table \"policies\" violates foreign key constraint \"approval_requests_policy_id_fkey\" on table \"approval_requests\" (SQLSTATE 23503)"}
```

**Impact.** Any admin or operator in any org (the route's role group) can:
1. **Permanently block another org from deleting its own agents and policies** (cross-tenant denial of service). The victim cannot remove the blocking row, because it lives in the attacker's org.
2. **Probe whether a foreign agent ID exists**: a real one returns 201, a nonexistent one returns 500 plus the FK error text.

It does **not** read or modify the victim's rows. Proposed severity: **Medium**, the same class as B-233.

**Fixture cleanup:** the forged approval row was removed along with all fixtures. The snapshot diff (`sweep_before.txt` vs `sweep_after.txt`) is **identical**, and the residual scan found 0.

**Proposed fix, following B-232/B-233:**
- **Handler:** verify `agent_id` belongs to `uc.OrgID` (`GetAgent`), and `policy_rule_id` too when it is given. Return a non-revealing 404 that is identical for a foreign and a nonexistent ID.
- **SQL:** the insert selects from `gateway_agents WHERE id=$agent AND org_id=$org`, so a foreign reference writes nothing.
- **Tests:** a cross-org adversarial test (real-Postgres) and per-layer mutation checks.
- **Open question:** whether this JWT route should exist at all. No gateway code calls it; the gateway creates approvals itself.

### B-238 — Slack webhook sends bypass the SSRF dial guard — **FAIL (guard-on-one-path shape), live-confirmed → FIXED 2026-09-27**

**The problem**
- `safeDialContext` protects `TestTool` and `DiscoverOpenAPI`.
- `TestNotificationChannel` and the alert engine's `SendSlack` use a bare `http.Post`, with no dial guard and no timeout, to an admin-set URL that is never validated.

**Live:** a throwaway-org admin made eami-api POST to loopback, compose-internal hosts and the metadata IP. The error `reason` is a port-scan oracle (verbatim, `ssrf_live.log`):
```
webhook=http://127.0.0.1:8081/health                       save=200 test=200 21ms {"sent":false,"reason":"slack_webhook_non_200"}
webhook=http://127.0.0.1:1/                                save=200 test=200 15ms {"sent":false,"reason":"Post \"http://127.0.0.1:1/\": dial tcp 127.0.0.1:1: connect: connection refused"}
webhook=http://postgres:5432/                              save=200 test=200 11ms {"sent":false,"reason":"Post \"http://postgres:5432/\": EOF"}
webhook=http://eami-gateway:8080/health                    save=200 test=200 20ms {"sent":false,"reason":"slack_webhook_non_200"}
webhook=http://169.254.169.254/latest/meta-data/           save=200 test=200 9ms {"sent":false,"reason":"Post \"http://169.254.169.254/latest/meta-data/\": dial tcp 169.254.169.254:80: connect: connection refused"}
```
**Cleanup:** the fixture org was deleted and the snapshot (`ssrf_before.txt` vs `ssrf_after.txt`, which also covers `notification_config`) is **identical**.

Full detail and the proposed fix are in BACKLOG.md B-238. The severity proposed is **Medium**.

## Endpoint ledger

| # | Endpoint | Handler | Verdict | Evidence |
|---|---|---|---|---|
| 1 | POST /v1/gateway/tools | CreateTool | PASS | inserts `org_id=uc.OrgID` (`tools.go`, `store/tools.sql.go:96`) |
| 2 | PATCH /v1/gateway/tools/{toolId} | UpdateTool | PASS | the pre-read `GetToolForTest(uc.OrgID)` on the ai_provider branch; the single write `UpdateTool … WHERE id=$1 AND org_id=$2` runs on every path |
| 3 | DELETE /v1/gateway/tools/{toolId} | DeleteTool | PASS | `DELETE … WHERE id=$1 AND org_id=$2` |
| 4 | POST /v1/gateway/tools/{toolId}/test | TestTool | PASS | the read `GetToolForTest(uc.OrgID)`; the write `MarkToolTested … WHERE id=$1 AND org_id=$2` |
| 5 | POST /v1/gateway/policies | CreatePolicy | PASS | inserts `org_id=uc.OrgID`; the condition uses the just-created `pol.ID` |
| 6 | PATCH /v1/gateway/policies/{policyId} | UpdatePolicy | PASS | `UPDATE … WHERE id AND org_id` runs unconditionally; on failure it returns before the conditions upsert, which uses the returned `pol.ID` |
| 7 | DELETE /v1/gateway/policies/{policyId} | DeletePolicy | PASS | `DELETE … WHERE id=$1 AND org_id=$2` |
| 8 | PUT /v1/gateway/policies/reorder | ReorderPolicies | PASS | `UPDATE policies AS p … WHERE p.id=v.id AND p.org_id=$3` |
| 9 | POST /v1/gateway/policies/reorder | ReorderPolicies | PASS | the same handler |
| 10 | POST /v1/workspaces/{id}/policies | CreateWorkspacePolicy | PASS | inserts `org_id=uc.OrgID`; the trigger `trg_policies_workspace_org_match` rejects a foreign workspace; it is behind the B-233 middleware |
| 11 | PATCH /v1/workspaces/{id}/policies/{policyId} | UpdateWorkspacePolicy | PASS | `WHERE id AND org_id AND workspace_id`; the conditions upsert uses the returned id |
| 12 | DELETE /v1/workspaces/{id}/policies/{policyId} | DeleteWorkspacePolicy | PASS | `WHERE id AND org_id AND workspace_id` |
| 13 | POST /v1/gateway/workflows | CreateWorkflow | PASS | `validateWorkflowSteps(uc.OrgID)`, which covers step tool references; the workflow is inserted with `uc.OrgID` |
| 14 | PATCH /v1/gateway/workflows/{workflowId} | UpdateWorkflow | PASS | an unconditional `GetWorkflow(id, org)`, then an org-scoped update in the transaction before the step replace; step tools are org-validated |
| 15 | DELETE /v1/gateway/workflows/{workflowId} | DeleteWorkflow | PASS | `DeleteWorkflow(uc.OrgID, id)` |
| 16 | PUT /v1/gateway/workflow-steps/{stepId}/params | PutWorkflowStepParams | PASS | an atomic `INSERT … SELECT … WHERE EXISTS(step→workflow.org_id)`; `ON CONFLICT` can only fire on an org-matched row |
| 17 | POST /v1/alerts/rules | CreateAlertRule | PASS | inserts `org_id=uc.OrgID` |
| 18 | PUT /v1/alerts/rules/{ruleId} | UpdateAlertRule | PASS | an org read, then `UPDATE … WHERE id AND org_id` |
| 19 | DELETE /v1/alerts/rules/{ruleId} | DeleteAlertRule | PASS | `WHERE id AND org_id` |
| 20 | POST /v1/alerts/rules/{ruleId}/test | TestAlertRule | PASS | an org-scoped read, then a dry run only (no write) |
| 21 | POST /v1/alerts/{alertId}/acknowledge | AcknowledgeAlert | PASS | `UpdateAlertStatus … WHERE id AND org_id` |
| 22 | POST /v1/alerts/{alertId}/resolve | ResolveAlert | PASS | the same |
| 23 | POST /v1/approvals | CreateApproval | **FAIL → B-237 (FIXED 2026-09-27)** | see above; `B-237_VERIFICATION.md` |
| 24 | PUT /v1/gateway/agents/{agentId}/config | UpdateAgentConfig | PASS (fixed by B-232) | `B-232_VERIFICATION.md` |
| 25 | PATCH /v1/workspaces/{id}/members/{userId} | UpdateWorkspaceMemberRole | PASS (fixed by B-233) | `B-233_VERIFICATION.md` |
| 26 | DELETE /v1/workspaces/{id}/members/{userId} | RemoveWorkspaceMember | PASS (fixed by B-233) | `B-233_VERIFICATION.md` |
| 27 | POST /v1/approvals/{approvalId}/decide | DecideApproval | PASS | `UPDATE … WHERE id AND org_id AND status='pending'`; foreign, missing and decided rows all get an identical 409; `approved_by=uc.UserID`; the notify uses the org-scoped row's id; the mock branch is test-only. (500 echoes `err.Error()`, logged under B-234) |
| 28 | DELETE /v1/gateway/nodes/{nodeId} | DeleteNode | PASS | `DELETE FROM gateway_nodes WHERE id AND org_id` |
| 29 | POST /v1/auth/api-keys | CreateAPIKey | PASS | when `agent_id` is given, an unconditional `GetAgent(id, uc.OrgID)`; the same 400 for foreign and missing; inserted with `org_id=uc.OrgID` |
| 30 | DELETE /v1/auth/api-keys/{keyId} | RevokeAPIKey | PASS | `UPDATE api_keys … WHERE id AND org_id`. Note: an Exec never yields `ErrNoRows`, so the 404 branch is dead code and a foreign or missing key gets a uniform no-op 204, which is not an oracle |
| 31 | POST /v1/gateway/agents | CreateAgent | PASS | inserted with `org_id=uc.OrgID`; config seed and lifecycle event use the returned `a.ID`; the `storeIface` branch is test-only |
| 32 | PATCH /v1/gateway/agents/{agentId} | UpdateAgent | PASS | `UPDATE gateway_agents … WHERE id AND org_id`; the lifecycle event runs only after success |
| 33 | DELETE /v1/gateway/agents/{agentId} | DeleteAgent | PASS | an unconditional `GetAgent(id, org)`, then `DELETE … WHERE id AND org_id` |
| 34 | PATCH /v1/endpoints/{endpointId}/link-agent | LinkEndpointAgent | PASS | set branch: `EXISTS(gateway_agents id AND org)`; both the set and clear branches: `UPDATE endpoints … WHERE id AND org_id`; `org_id` is immutable, so there is no TOCTOU |
| 35 | POST /v1/cmdb/categories | CreateCMDBCategory | PASS | inserted with `org_id` |
| 36 | PATCH /v1/cmdb/categories/{categoryId} | UpdateCMDBCategory | PASS | `WHERE id AND org_id` |
| 37 | DELETE /v1/cmdb/categories/{categoryId} | DeleteCMDBCategory | PASS | `WHERE id AND org_id` |
| 38 | POST /v1/cmdb/types | CreateCMDBType | PASS | both branches (default and non-default) insert with `org_id`; the default-clear is org-scoped inside the transaction; `category_id` goes through the composite FK `(category_id, org_id)`, so foreign and missing fail identically |
| 39 | PATCH /v1/cmdb/types/{typeId} | UpdateCMDBType | PASS | an org pre-read; the transaction's default-clear is org-scoped and rolled back if the `WHERE id AND org_id` update finds no row; the composite category FK |
| 40 | DELETE /v1/cmdb/types/{typeId} | DeleteCMDBType | PASS | `WHERE id AND org_id AND NOT is_default` |
| 41 | PATCH /v1/cmdb/assets/{kind}/{id}/classification | SetCMDBAssetClassification | PASS | non-nil type: `EXISTS(ci_types id AND org AND kind)`; both branches: `UPDATE … WHERE id AND org_id`; the composite FK `(ci_type_id, org_id)` |
| 42 | POST /v1/workspaces | CreateWorkspace | PASS | group, workspace and creator membership are all server-generated ids, with `org_id=uc.OrgID`, in one transaction |
| 43 | DELETE /v1/workspaces/{workspaceId} | DeleteWorkspace | PASS | `DELETE FROM groups WHERE id=(SELECT group_id FROM workspaces WHERE id AND org_id)` |
| 44 | PATCH /v1/workspaces/{workspaceId} | UpdateWorkspace | PASS | `UPDATE workspaces … WHERE id AND org_id`; also behind the B-233 middleware |
| 45 | POST /v1/workspaces/{workspaceId}/members | AddWorkspaceMember | PASS | an unconditional user↔workspace↔`uc.OrgID` join before the insert; the B-233 middleware |
| 46 | POST /v1/users/invite | InviteUser | PASS | user and invite token are created with `org_id=uc.OrgID` / the returned `u.ID`. **Info:** `users.email` is *globally* UNIQUE, so inviting an email that exists in another org returns a 500 echoing the unique-violation text. That tells an admin whether an email has an account anywhere. The raw text is B-234's class; the existence signal comes from global email uniqueness by design (founder's call) |
| 47 | PUT /v1/users/{userId}/role | UpdateUserRole | PASS | `UPDATE users … WHERE id AND org_id AND deleted_at IS NULL`; platform_admin excluded |
| 48 | DELETE /v1/users/{userId} | DeleteUser | PASS | `UPDATE users SET deleted_at … WHERE id AND org_id` |
| 49 | POST /v1/users/{userId}/reset-link | AdminGenerateResetLink | PASS | `GetUserByID`, then an unconditional `u.OrgID != uc.OrgID` → the same 404 as missing, before any token write |
| 50 | PATCH /v1/users/me | UpdateMe | PASS | `WHERE id=uc.UserID AND org_id=uc.OrgID` |
| 51 | POST /v1/users/me/change-password | ChangeMyPassword | PASS | self only (`uc.UserID`); the current password is checked first |
| 52 | PUT /v1/settings/org | UpdateOrgSettings | PASS | keyed on `uc.OrgID` only |
| 53 | POST /v1/settings/license | UploadLicense | PASS | the signed license's `org_id` must equal `uc.OrgID`; the transaction is locked and scoped to `uc.OrgID` |
| 54 | PUT /v1/settings/notifications | UpdateNotificationConfig | PASS (org) | the upsert is keyed on `uc.OrgID`. It stores `slack_webhook_url` unvalidated, which feeds B-238 |
| 55 | POST /v1/settings/notifications/test | TestNotificationChannel | PASS (org) / **FAIL → B-238 (FIXED 2026-09-27)** | the config is read by `uc.OrgID`. The unguarded `http.Post` to the stored URL is an SSRF (see above) |
| 56 | POST /v1/admin/model-pricing | CreateModelPricing | PASS (N/A: global) | `model_pricing` is a deliberately global table with no `org_id`, gated to `platform_admin`. That role cannot be granted through invite or role-update (#46/#47), only at provisioning |
| 57 | PATCH /v1/admin/model-pricing/{model} | UpdateModelPricing | PASS (N/A: global) | the same |
| 58 | DELETE /v1/admin/model-pricing/{model} | DeleteModelPricing | PASS (N/A: global) | the same |
| 59 | POST /v1/gateway/openapi/discover | DiscoverOpenAPI | PASS | no DB write (parse only); the `spec_url` fetch uses `safeDialContext` |
| 60 | POST /v1/auth/login | Login | PASS | org is taken from the user row (`GetUserByEmail … deleted_at IS NULL`), never from the request; the refresh token is keyed on that user; the `storeIface` branch is test-only |
| 61 | POST /v1/auth/refresh | Refresh | PASS | the token is looked up by sha256 hash; org is re-derived from the DB user; the old token is revoked by its own id |
| 62 | POST /v1/auth/accept-invite | AcceptInvite | PASS | in a transaction, the token row is locked (`FOR UPDATE`), so the user comes from the token; the password write and token consume are keyed on those ids; deleted users are refused |
| 63 | POST /v1/auth/request-reset | RequestPasswordReset | PASS | no DB write; log only |
| 64 | POST /v1/auth/reset-password | ResetPassword | PASS | the same token→user pattern as #62 |
| 65 | POST /v1/setup/token/validate | ValidateSetupToken | PASS | read only |
| 66 | POST /v1/setup/bootstrap | Bootstrap | PASS | an advisory lock, then a locked setup-token row; it runs only when `count(orgs)=0` and creates its own org and admin |
| 67 | POST /v1/reports | IngestReports | PASS (by design) | `requireServiceKey` runs on every path, so there is no branch asymmetry. **Info:** `org_id` comes from the event body, and one global service key authorizes any org. There is no current caller (the collector uses #68). This is safe only while every key holder is platform-operated |
| 68 | POST /v1/ingest/batch | IngestBatch | PASS | the service key; org is server-resolved (`GetDefaultOrgID`), and no item field can influence it (B-033/B-034 note) |
| 69 | POST /v1/internal/token-usage | IngestTokenUsage | PASS (by design) | the service key; body `org_id`/`agent_id`; the only caller is eami-gateway, with its server-resolved org. **Info:** the same global-key trust as #67 (also noted in B-237's security review) |

**Original bucket list** (all now audited; kept for reference):
- **Approvals, nodes, API keys:** DecideApproval, DeleteNode, CreateAPIKey, RevokeAPIKey.
- **Agents:** CreateAgent, UpdateAgent, DeleteAgent.
- **Endpoints, CMDB, workspaces:**
  - LinkEndpointAgent;
  - the 7 CMDB routes;
  - CreateWorkspace, DeleteWorkspace, UpdateWorkspace, AddWorkspaceMember.
- **Users:** InviteUser, UpdateUserRole, DeleteUser, AdminGenerateResetLink, UpdateMe, ChangeMyPassword.
- **Settings and admin:**
  - UpdateOrgSettings, UploadLicense, UpdateNotificationConfig, TestNotificationChannel;
  - the 3 model-pricing routes (a platform_admin global table);
  - DiscoverOpenAPI.
- **Pre-auth:** Login, Refresh, AcceptInvite, RequestPasswordReset, ResetPassword, ValidateSetupToken, Bootstrap.
- **Service-key:** IngestReports, IngestBatch, IngestTokenUsage.
