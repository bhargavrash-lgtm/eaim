# Org-Ownership Branch-Asymmetry Sweep (in progress)

Started 2026-09-27 by Claude Code, from a founder brief.

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

**Remaining, not yet audited:** 43 endpoints.
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
