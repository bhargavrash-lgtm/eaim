# B-253 Verification Record — RBAC split: "operators contain; admins expand or destroy"

Written 2026-09-29 by Claude Code, at founder direction (Brief 2). Roadmap: **Horizon 1, CMDB completion / Horizon 0 hardening.** The decisions are in `RBAC_SPLIT_PART_A.md` §7, committed as Part 0 (`ca9d197`).

Raw logs are in session `b3cde494-55e6-4fd7-b83d-5f55f606e27f`'s scratchpad:
- `b253_mutation.log`;
- `b253_live.log`, plus `b253_live_run1_scriptorder.log`;
- `b253_api_full.log`;
- `b253_before.txt` and `b253_after.txt`.

## 1. Field classification (step 3; completion-report table)
| Entity | Field | Read by (gateway) | Class | Rule for non-admins |
|---|---|---|---|---|
| Agent | `status` → suspended / revoked | issuance gate | containment | allowed |
| Agent | `status` → active (from suspended/revoked) | issuance gate | **expansion** | **admin only** |
| Agent | `scope` | drift detection (`mcp/handler.go`, `workflow/http.go`), token claims (`identity/tokens.go`) | expansion | **admin only** |
| Agent | `risk_tier` | API-key lookup, token claims (`identity/apikey.go`) | expansion | **admin only** |
| Agent | `token_ttl_seconds` | token issuance (`identity/issue_http.go`) | expansion | **admin only** |
| Agent | `name`, `model`, `owner` | — | — | not PATCH-able at all (no rename path) |
| Tool | `name` | tool resolution (`toolrouter`, `aiprovider` `WHERE name=`), policy `tool_names`, audit | expansion (rename evades name policies) | **admin only** |
| Tool | `base_url` | REST dispatch, approval config hash | expansion | **admin only** |
| Tool | `credentials` (any write) | dispatch | expansion | **admin only** |
| Tool | `action_paths` | dispatch method/path, approval config hash | expansion | **admin only** (founder, 2026-09-29) |
| Tool | `provider` | adapter selection (`aiprovider`) | expansion | **admin only** |
| Tool | `audit_mode` | audit parameter logging | expansion | **admin only** |
| Tool | `data_handling_designation` | snapshotted into audit | expansion | **admin only** |
| Tool | `redaction_rules` | outbound redaction | expansion | **admin only** |
| Tool | `mcp_command`, `mcp_args` | nothing today (not executed) | unsure → default | **admin only** (founder, 2026-09-29) |
| Tool | `data_handling_note` | nothing | descriptive | allowed |
| Node | — | — | — | no PATCH/create route exists; nothing writes `gateway_nodes` |

**Rename evasion:**
- Agent names aren't PATCH-able, and agent create and delete are now admin-only.
- Tool rename is now admin-only.
- Workflow steps reference tools by ID, and the gateway resolves the name itself (security review §4).
- **So no rename path remains for an operator.** Pattern *syntax* validation is separate (B-260).

**One founder question was asked mid-build:** admin-only for `action_paths` and `mcp_command` breaks operator flows that exist today. **Answer: both admin-only.** Operators keep OpenAPI discover as a read-only preview.

## 2. The change (server only)
**`router.go`:**
- **New admin-only group:** `POST /v1/auth/api-keys`, `POST /v1/gateway/agents`, `DELETE /v1/gateway/agents/{id}`, `PATCH /v1/endpoints/{id}/link-agent` (this also covers *clearing* a link), `POST /v1/gateway/tools`, `DELETE /v1/gateway/tools/{id}`, `DELETE /v1/gateway/nodes/{id}`.
- **No node create or update route exists** (nothing writes `gateway_nodes`), so there is nothing to restrict there.
- **New approver read group:** `GET /v1/gateway/agents`, `/agents/{id}` and `/tools` (tools have no by-id route).

**`rbac_fields.go`** (new): the in-handler field checks for `UpdateAgent` and `UpdateTool`.
- A changed restricted field rejects the whole request, before anything is written, with requireRole's exact 403 (the new `writeRoleForbidden` in `middleware.go`).
- Unchanged echoed fields are **stripped** from a non-admin's write. That makes a UI echo safe, and it closes the race between the load and the write.
- The comparison is semantic: NULL equals `""`; stored NULL redaction rules equal the default; `{}` action paths equal NULL.

**`tools.go`:** credential encryption was moved **after** the role check, so a non-admin credential write is refused before any credential handling.

**`agents_test.go`:** `TestCreateAgent_OperatorRole_Succeeds` is **deliberately converted** to `…_Forbidden`, with a comment explaining the rule change.

## 3. Tests
```
eami-api: go build ./... ; go vet ./... → clean; gofmt clean on every new/changed file
eami-api: go test -count=1 -v ./... → every package ok; top-level PASS=504 FAIL=0 SKIP=0 (498 + 6 new)
```

**`rbac_split_pg_test.go`** (real Postgres):
- **Admin-only routes:** operator, approver and viewer get requireRole's exact 403 and **nothing is written** (re-read). This includes five shapes of tool create. Admin succeeds, with the effect verified. Operator key list and revoke, scanner config, tool test and OpenAPI discover still work.
- **Agent fields:** each expansion is refused whole with nothing written, including a mixed allowed-plus-restricted request. Echo passes. Admin can expand. Operator revoke works.
- **Tool fields:** every restricted field is refused whole with nothing written, including a mixed note-plus-rename request, a reset of custom redaction to default, and dropping a custom pattern. The descriptive edit passes even when every field is echoed. An empty `credentials` object isn't a write.
- **Approver read surface:** a `chi.Walk` over **every GET route in the real router**. Approver gets 403 everywhere except agents (list and by id), tools, approvals, self-profile and workspace membership, which are unchanged. Viewer reads are unchanged.

**`rbac_fields_test.go`:** the classifiers, including the provider case (unreachable over HTTP today because "claude" is the only valid provider), the normalisation, and echo-stripping for both entities.

**Mutation checks:** 15, each run separately (`b253_mutation.log`, verbatim summary):
```
=== G1: key minting moved back to admin+operator
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.19s)
=== G2: agent create moved back
--- FAIL: TestCreateAgent_OperatorRole_Forbidden (0.04s)
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.17s)
=== G3: agent delete moved back
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.20s)
=== G4: endpoint link moved back
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.19s)
=== G5: tool create moved back
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.30s)
=== G6: tool delete moved back
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.24s)
=== G7: node delete moved back
--- FAIL: TestRBACSplit_AdminOnlyRoutes_RealDB (0.34s)
=== G8: approver removed from the agent/tool read group
--- FAIL: TestRBACSplit_ApproverReadSurface_RealDB (0.34s)
=== G9: approver added to the broad read group (over-grant)
--- FAIL: TestRBACSplit_ApproverReadSurface_RealDB (0.39s)
=== H1: agent in-handler check removed
--- FAIL: TestRBACSplit_AgentFields_RealDB (0.19s)
=== H2: reactivation rule removed only
--- FAIL: TestAgentAdminOnlyChange (0.00s)
--- FAIL: TestRBACSplit_AgentFields_RealDB (0.20s)
=== H3: tool in-handler check removed
--- FAIL: TestRBACSplit_ToolFields_RealDB (0.13s)
=== H4: credential rule removed only
--- FAIL: TestToolAdminOnlyChange (0.00s)
--- FAIL: TestRBACSplit_ToolFields_RealDB (0.13s)
=== H5: action_paths rule removed only
--- FAIL: TestToolAdminOnlyChange (0.00s)
--- FAIL: TestRBACSplit_ToolFields_RealDB (0.11s)
=== H6: echo stripping removed (agent status)
--- FAIL: TestAgentAdminOnlyChange (0.00s)
=== control: sources restored
ok  	github.com/eami/api/internal/api	1.840s
```

**Service callers:** a repo-wide grep of eami-gateway, eami-collector, eami-agent, eami-policy and scripts found **no call** to any of these routes. The only hit is a comment in `eami-gateway/internal/identity/revoke_http.go`.

## 4. Live verification
The rebuilt API container was created 2026-09-29T04:10:39Z. It used **real login tokens** for admin, operator, approver and viewer in a throwaway org (`b253_live.log`, verbatim):
```
── operator: admin-only routes refused, nothing written
PASS operator mint API key :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator create agent :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator delete agent :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator link endpoint :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator create tool :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator delete tool :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator refusals wrote nothing (keys/agents/tools/links) :: 0/1/2/0 -> 0/1/2/0
── operator: containment and descriptive edits still work; expansions refused
PASS operator suspend agent :: 200 {"id":"0026dee1-3961-4b63-96d5-b628712b32a9","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"b253-live
PASS operator reactivate agent :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator change scope :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS agent still suspended with original scope :: suspended|test
PASS operator rename tool :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator change tool base_url :: 403 {"code":"forbidden","message":"your role (operator) does not have access to this resource"}
PASS operator edit tool data_handling_note :: 200 {"id":"084657a5-d93e-4ac0-a933-ac855e7622f1","name":"b253-live-ai","type":"ai_provider","auth_type":"api_key",
PASS operator scanner config :: 200 {"agent_id":"0026dee1-3961-4b63-96d5-b628712b32a9","scan_interval_seconds":120,"model_scan_paths":["/home","/U
── admin: expansions succeed
PASS admin reactivate agent :: 200 {"id":"0026dee1-3961-4b63-96d5-b628712b32a9","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"b253-live
PASS admin mint API key :: 201 {"key":"eami_k_aee5e4dd218a15f8dba5d2442704ba36098ba33a7a32d304","meta":{"id":"3dd6c39f-0fdd-4770-8b01-6b88398
PASS operator revoke the admin-minted key (containment) :: 204 
PASS admin link endpoint :: 200 {"id":"c68367e8-038c-4fd5-881e-6d98eafc53bb","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","agent_id":"b253-
PASS admin create agent :: 201 {"id":"73d2aa6c-fc59-4c74-8183-75efd8c43db7","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"b253-admi
PASS admin delete agent :: 204 
PASS admin create tool :: 201 {"id":"7a04fedb-b1c7-4200-ba3a-addf01da6535","name":"b253-admin-tool","type":"rest_api","auth_type":"api_key",
PASS admin delete tool :: 204 
── approver: agents and tools only; approvals/self/workspaces unchanged
PASS approver list agents :: 200 {"data":[{"id":"0026dee1-3961-4b63-96d5-b628712b32a9","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"
PASS approver get agent :: 200 {"id":"0026dee1-3961-4b63-96d5-b628712b32a9","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"b253-live
PASS approver list tools :: 200 {"data":[{"id":"084657a5-d93e-4ac0-a933-ac855e7622f1","name":"b253-live-ai","type":"ai_provider","auth_type":"
PASS approver GET /v1/gateway/agents/0026dee1-3961-4b63-96d5-b628712b32a9/config :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/gateway/agents/0026dee1-3961-4b63-96d5-b628712b32a9/connections :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/cmdb/assets :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/auth/api-keys :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/gateway/policies :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/endpoints :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/audit :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
PASS approver GET /v1/approvals (unchanged) :: 200 {"data":[],"meta":{"total":0,"page":1,"per_page":25}}
PASS approver GET /v1/users/me (unchanged) :: 200 {"id":"3e0476d0-c602-4f74-8fdc-172e6e3e0df3","email":"b253-approver@example.test","name":"B253 approver","role
PASS approver GET /v1/workspaces/mine (unchanged) :: 200 {"data":[]}
PASS approver suspend agent :: 403 {"code":"forbidden","message":"your role (approver) does not have access to this resource"}
── viewer: unchanged
PASS viewer GET /v1/gateway/agents :: 200 {"data":[{"id":"0026dee1-3961-4b63-96d5-b628712b32a9","org_id":"6cbc4bed-a3df-45c5-9319-9ea042abc891","name":"
PASS viewer GET /v1/gateway/agents/0026dee1-3961-4b63-96d5-b628712b32a9/config :: 200 {"agent_id":"0026dee1-3961-4b63-96d5-b628712b32a9","scan_interval_seconds":120,"model_scan_paths":["/home","/U
PASS viewer GET /v1/gateway/tools :: 200 {"data":[{"id":"084657a5-d93e-4ac0-a933-ac855e7622f1","name":"b253-live-ai","type":"ai_provider","auth_type":"
PASS viewer GET /v1/gateway/policies :: 200 {"data":[]}
PASS viewer suspend agent :: 403 {"code":"forbidden","message":"your role (viewer) does not have access to this resource"}
ALL PASS
```
**Run 1** (`b253_live_run1_scriptorder.log`) had 2 FAILs caused by **my script's ordering, not the product**. The admin minted a key for the agent the operator had just suspended, and the API correctly refuses keys for non-active agents (an existing rule). Reordering fixed it. All other 40 checks passed in both runs.

**Cleanup:** after each run, deleted the throwaway org (cascading users, agents, tools, endpoint, configs), its API keys, lifecycle events and refresh tokens, and the fixture password file. `diff b253_before.txt b253_after.txt` is **identical**, including the audit_log count.

## 5. Reviews (both mandatory passes completed; quoted verbatim)

**After the reviews:**
- **Test gaps from the code review, closed:**
  - the viewer's exact 403 body;
  - admin effects verified;
  - operator tool test and OpenAPI discover;
  - an empty-credentials non-write;
  - redaction reset and custom-pattern drop refused;
  - tool echo-stripping unit assertions;
  - the discarded `NewService` error.
- All tests and 15/15 mutations were re-run on the final code.
- The remaining items are recorded in §6.

### 5a. Code review
> ## B-253 RBAC split: independent review
>
> **Verdict:** I found no High or Medium issues. The change matches §7. The key design choice holds up: an unchanged restricted field is **stripped**, never written. So an equality check that is wrong can only drop a non-admin's no-op. It can never let an expansion through. The only values a non-admin can still write are:
> - agent `status` set to `suspended` or `revoked`;
> - tool `data_handling_note`.
>
> That also makes any timing gap between the load and the write harmless.
>
> **Commands run** (from `eami-api`, with `export PATH="/c/Program Files/Go/bin:$PATH"`):
> - `go build ./...` passed.
> - `go vet ./...` passed.
> - `go test ./internal/api/ -run AdminOnlyChange -v` passed: `TestAgentAdminOnlyChange` and `TestToolAdminOnlyChange`.
> - Read-only `git diff` and greps across `eami-api`, `eami-gateway`, `eami-collector`, `eami-ui` and `schema`.
> - I did not run the DB tests, so `rbac_split_pg_test.go` was reviewed statically, not executed.
>
> ### 1. Correctness and completeness against §7
> - **Routes:** everything in §7 is present in `router.go:338-349`:
>   - Q-A agent create is admin-only.
>   - Q-B tool create is admin-only as a whole route.
>   - Q-D tool delete and node delete are admin-only. No node create or update route exists: the only `gateway_nodes` write anywhere is the `DELETE` in `store/nodes.sql.go:49`.
>   - Q-E: the approver group at `router.go:400-405` covers agents list and by id, and tools list. Tools have no by-id route.
> - **No duplicate method+pattern registrations** (checked with `uniq -d` over `router.go`). Splitting one path's methods across groups is fine in chi. Registration order doesn't matter here.
> - **Other write paths to the same columns:** none that operators can reach.
>   - `gateway_agents` and `gateway_tools`: the only other writer is `SetCMDBAssetType` (`cmdb.sql.go:311-313`, `ci_type_id`). Its route is in the admin-only group (`router.go:277`).
>   - `MarkToolTested` (TestTool, still operator) writes `status` and `test_latency_ms`. The gateway never reads tool `status`.
>   - `gateway_agents.workspace_id` has no API writer. It is only set to NULL by an FK cascade when an admin deletes a workspace.
>   - Workspace and workflow routes don't touch agents or tools.
>   - `api_keys`: the only insert is `auth.go:268` (CreateAPIKey, now admin-only).
>   - `endpoints.gateway_agent_id`: the only writer is `discover.go:301` (LinkEndpointAgent, now admin-only).
> - **Q-F field coverage:**
>   - `AgentUpdateRequest` (`types.go:118-123`) has only scope, risk_tier, status and TTL, and all of them are covered. Agent name, owner and model can't be PATCHed at all.
>   - The gateway never reads tool `data_handling_note` (grep of `eami-gateway`: no hits). `data_handling_designation` is read and is correctly admin-only. So note-only for operators matches the Q-F wording.
>
> ### 2. Echo comparison
> The helpers are in `rbac_fields.go`. I checked each case against what the real UI sends (`ToolsPage.tsx:686-730`) and against what the gateway interprets.
>
> **Safe cases:**
> - **`NULL` vs `""`** for `mcp_command`, `base_url` and `provider` (`textEq`): matches the UI, which sends `x || undefined` for base_url and mcp_command. Provider can't be NULL on an ai_provider tool (CreateTool requires it).
> - **redaction_rules:**
>   - The normalisation (`Enabled == nil` → true, NULL or `null` → default) matches `eami-gateway/internal/redaction/redaction.go:104` exactly.
>   - Stripping an explicit `null` when the stored value is SQL NULL, or equivalent to the default, loses nothing: the result is identical.
>   - `null` against a stored non-default value (custom patterns, or `enabled:false`) is correctly treated as restricted.
>   - The UI's echo, `{enabled, custom_patterns?, disabled_patterns?}`, compares equal. The custom-pattern round trip goes through `JSON.stringify`/`parse` and preserves the keys.
> - **action_paths:** the request is normalised by `validateActionPaths` (uppercase method, POST default), which is the same function that produced the stored bytes since B-046 (`839e8ae`). `jsonObjEq` compares parsed maps, so jsonb key reordering is harmless. `{}` vs NULL is handled.
> - **mcp_args:** an ordered compare is correct, because argument order matters. `[]` vs NULL is handled. The UI never sends mcp_args unless the user types them, because ListTools doesn't return them.
>
> **False positives** (a no-op save gets a 403; they never let anything through):
> - **Low**, `ToolsPage.tsx:647` and `:195` (UI, pre-existing): the edit panel builds rows as `{action, path, method}` and drops `inputSchema`. So a REST tool whose stored action_paths carry `input_schema` (from B-075 discovery) always compares "changed", and an operator's no-op save gets a 403.
>   - More importantly, an admin's save silently strips `input_schema`. That is a pre-existing data-loss bug; log it to `NOTES.md` or `BACKLOG.md`, since it is out of scope here.
>   - The UI also trims path strings, which the server doesn't. A stored untrimmed path would also produce a false 403.
>   - Practical impact is small: operators can't change anything on REST tools anyway, and C0(c) will hide the control.
>
> ### 3. TOCTOU
> - **Info:** no exploitable window.
>   - Agents: stripped fields are never written. The surviving writes are containment only, so a concurrent admin reactivation racing an operator's suspend or revoke simply resolves to containment.
>   - Tools: after the check, only `data_handling_note` goes through the `UpdateTool` COALESCE, so an admin's concurrent change is preserved.
> - **Info:** UpdateTool now does up to three reads (GetToolForTest, loadToolAdminFields, then the write). That is acceptable.
>
> ### 4. Route order and duplicates
> - No conflicts.
> - **Info:** `viewerReadOnly` on the GET-only approver group (`router.go:401`) has no effect, but it is harmless and consistent.
>
> ### 5. Tests
> - **CLAUDE.md pool rule is satisfied.**
>   - `newWorkspaceTestEnv` (`workspaces_pg_test.go:61`) registers `t.Cleanup(pool.Close)` first. `ts.Close` comes next, and then `seedTestOrg`'s DELETE, so LIFO order is correct.
>   - No plain `defer` appears in `rbac_split_pg_test.go`.
>   - Every table seeded there has `org_id … ON DELETE CASCADE` (checked `schema.sql`), so the org DELETE cleans everything.
> - **Good:**
>   - Exact-body 403 checks, with a "nothing written" re-read.
>   - The mixed-field whole-reject cases.
>   - The `chi.Walk` over every GET route, with a `checked < 30` sanity floor. I verified the only non-JWT GETs are `/health`, `/v1/setup/status` and the service-key `/v1/agents/{agent_id}/config`, all listed in `public`.
>   - The `credentials` case implicitly pins the moved encryption: the env has `toolCreds == nil`, so the request would be a 500 if encryption still ran before the check.
> - **Low:** gaps worth closing.
>   - **`rbac_split_pg_test.go:124`:** the viewer's 403 body is not asserted (`role.name != "viewer"`). Viewer hits the same `requireRole("admin")`, so the check could include it.
>   - **Admin success checks (`:131-138`) only assert status**, although §5 says "2xx plus the effect verified".
>   - **Parts of the §5 plan are not covered:**
>     - operator tool test succeeding;
>     - operator OpenAPI discover succeeding;
>     - a credential PATCH that doesn't count as a write (`credentials: {}` or `null`) passing;
>     - a custom_patterns change, or a `null` reset against a stored non-default value, being refused.
>   - **`rbac_fields_test.go`** checks stripping for agents but never for tools. It doesn't assert that `u.*` fields are nil after an echo, and that stripping is the property the TOCTOU safety relies on.
>   - **`:268`** discards the `auth.NewService` error.
> - **Info:** `agents_test.go:218` was correctly converted to `…_Forbidden` and asserts `CreateAgentCalls == 0`, as §7 requires.
>
> ### 6. Idiom and scope
> - **Low, `agents.go:227` vs `tools.go:~660`:** the ordering is inconsistent.
>   - UpdateAgent runs the role check **before** validation, so an operator sending a changed, invalid `risk_tier` gets a 403 where an admin gets a 400.
>   - UpdateTool checks after validation, which its comment says is deliberate.
> - **Low, `rbac_fields.go:84-93`:** `loadToolAdminFields` is raw SQL via `q.DB()` inside package `api`, bypassing both the `toolStore` interface and the sqlc-style `store` convention. There is precedent (`toolStoreWithConnectivity`).
>   - The resulting `s.queries == nil` fallback (`tools.go`) fails closed, which is good, but it is test-only code on the production path.
>   - Note too that with `toolStoreOverride` set, the check reads `s.queries` while the write goes through the override.
> - **Low (pre-existing, now more visible), `agents.go:246-248`:** `status` is not validated. An operator's `"Active"` or `""` passes the classifier and reaches the DB CHECK, which returns a 500 whose body contains `err.Error()`, leaking the constraint text. It can't be used to bypass anything: the CHECK is case-sensitive and allows only active, suspended and revoked.
> - **Low / decision:** an operator can move `revoked` → `suspended`, an "un-revoke". That isn't an expansion, since only an admin can then reactivate. Still, if `revoked` is meant to be terminal, that belongs with B-230.
> - **Info:**
>   - `LinkEndpointAgent` also handles **clearing** a link, so unlinking is now admin-only too. That is consistent with §7 ("the endpoint link") but worth a one-liner in `BUILT.md`.
>   - Policies, workflows, step params and alert rules stay admin + operator, as §7 says. Under the stated rule, though, deleting or loosening a deny policy is an expansion. Flag it for a future decision rather than changing it here.
>   - The `UpdateAgent` mock-store path dereferences `s.queries`. That is pre-existing: the write already did.
> - **Scope and hygiene:** the diff is confined to the B-253 files.
>   - The working tree also has unrelated untracked files: `AGENTS.md`, `CODEX_NEXT_BRIEF.md`, `JEV_ORCHESTRATION_ROADMAP.md`, `.local-test-agent/`, the license_checker and npm_audit JSON/txt files. They must not be swept into the B-253 commit.
>   - `BUILT.md` and `BACKLOG.md` are not yet updated. CLAUDE.md requires that at session end.
>
> **Files reviewed:** C:\AI\EAIM\eaim\eami-api\internal\api\{router.go, middleware.go, agents.go, tools.go, rbac_fields.go, rbac_fields_test.go, rbac_split_pg_test.go, agents_test.go}, C:\AI\EAIM\eaim\RBAC_SPLIT_PART_A.md, C:\AI\EAIM\eaim\eami-ui\src\pages\gateway\ToolsPage.tsx, C:\AI\EAIM\eaim\eami-gateway\internal\redaction\redaction.go

### 5b. Security review
> ## B-253 RBAC split: independent security review
>
> **Verdict: PASS for the change as scoped.** I found no path for an operator, approver or viewer to reactivate an agent, rotate or alter a tool credential, change `base_url`, `action_paths`, `provider`, `audit_mode`, `redaction_rules`, `mcp_*` or a tool name, mint a key, create or delete an agent or tool, or link an endpoint. One Medium finding sits outside this change's scope (org policy writes). The rest are Low or Info.
>
> **What I ran** (read-only; no DB tests, no file changes):
> - `go build ./...` in eami-api and eami-gateway: OK.
> - `go vet ./...` in eami-api: OK.
> - `go test ./internal/api/ -run AdminOnlyChange -v -count=1`: `TestAgentAdminOnlyChange` and `TestToolAdminOnlyChange` PASS.
> - `git diff HEAD -- eami-api`, plus reads of `rbac_fields.go`, `rbac_split_pg_test.go`, `RBAC_SPLIT_PART_A.md` §1–7, the store SQL, the schema CHECKs, the gateway status checks, the workflow connector, the policy loader and evaluator, and the route groups.
> - Greps for every writer of `gateway_agents`, `gateway_tools`, `api_keys` and `endpoints.gateway_agent_id` across eami-api, eami-gateway and eami-collector.
>
> ### Attack results
>
> **1. Paths to an expansion: none found.**
> - **Writer inventory.** Every SQL writer of these tables is reached only from:
>   - admin-group routes: CreateAgent, DeleteAgent, CreateAPIKey (`auth.go:268`), LinkEndpointAgent (`discover.go:301`), CreateTool, DeleteTool, and CMDB classification (the admin group at `router.go:241`);
>   - UpdateAgent and UpdateTool, which have the in-handler check;
>   - operations that only contain or are neutral: RevokeAPIKey (sets `revoked=TRUE` only) and MarkToolTested (status and latency only).
> - **Other write paths.** The collector and paste-event endpoint upserts never touch `gateway_agent_id`. The gateway writes none of these tables. DiscoverOpenAPI writes nothing. UpdateAgentConfig writes only scanner settings (`agent_configs`), which the gateway doesn't read.
> - **Reactivation.**
>   - Direct `{"status":"active"}` from a non-active status is rejected (`rbac_fields.go:52-53`).
>   - Revoked→active is covered by the same branch.
>   - DELETE and create are admin-only (`router.go:339-351`).
>   - Other spellings such as "Active" or "ACTIVE" hit the DB CHECK `status IN ('active','suspended','revoked')` (`schema/schema.sql:157`) and fail with a 500. The gateway's deny-list check (`registry.go:177`, `issue_http.go:308`) is therefore safe.
> - **JSON tricks.** The check and the write read the same struct from a single `json.Decode`, so there is no parser differential:
>   - duplicate keys resolve last-wins;
>   - `"Status"`, and the Unicode case-fold `"ſtatus"`, both map to `status`.
>   - Caveat: a front proxy or WAF would see these differently. That's Info only.
>
> **2. Echo-stripping: sound.** Every "equal" branch sets the field to nil, so it is never written, and every "not equal" branch returns 403. A false "equal" can only drop a write; it can never change stored state. I checked each edge case against the COALESCE SQL (`store/tools.sql.go:179-192`, `store/agents.sql.go:110-116`):
> - NULL vs `""` in `textEq`;
> - `redaction_rules` sent as `null` or the explicit default vs a stored NULL;
> - `action_paths` `{}` vs NULL;
> - malformed JSON, which normalises to empty on both sides and is then stripped.
>
> Another edge case: `token_ttl_seconds` values that overflow int32 (e.g. 2^32+900) truncate to the stored value and are stripped, so they are harmless for non-admins. After stripping, a non-admin's write contains only `data_handling_note` (tools) or `status` = suspended or revoked (agents). The gateway doesn't read `data_handling_note`: no hits in eami-gateway, which is consistent with Q-F.
>
> **3. TOCTOU: no expansion window.** Unchanged restricted fields are stripped rather than rewritten, so a stale echo can never overwrite a concurrent admin change. For example, an operator echoing `"active"` right after an admin suspends is stripped and nothing is written. The only fields a non-admin still writes are containment or descriptive, and last-writer-wins is acceptable for them.
>
> **4. Rename evasion: none.**
> - Agent names can't be PATCHed; create and delete are admin-only.
> - Tool names are admin-only.
> - Workflow steps reference `gateway_tool_id`, and the gateway resolves `ToolName` from `gateway_tools.name` (`eami-gateway/internal/workflow/connector.go:59-62`, `executor.go:176,188`). A workflow can't supply its own tool name.
>
> **5. Approver read group** (`router.go:400-405`): exactly `GET /v1/gateway/agents`, `/agents/{id}` and `/tools`. Agent config and connections, endpoints, CMDB, keys and policies stay excluded. No other approver-inclusive group grants writes beyond approvals and alerts (pre-existing). This matches Q-E. See L2 for what the tools list exposes.
>
> **6. 403/404 ordering: consistent.** Cross-org and nonexistent IDs return the same 404 through org-scoped reads (`GetAgent`, `GetToolForTest`, `loadToolAdminFields`) before any 403 is possible. The in-handler 403 body is byte-identical to `requireRole`'s (`middleware.go` `writeRoleForbidden`).
>
> ### Findings
>
> **Medium (outside B-253's scope)**
> - **M1. Operators keep full org policy writes: create, update, delete and reorder** (`router.go:360-364`). An operator can delete or disable a deny or escalate policy, or insert a higher-priority allow. That widens what every agent may do, which is a bigger expansion than any of the fields this change locks. Part A §1 marked policies "outside this decision's scope", but it conflicts with the spirit of "admins expand" and Q-F ("any field that policy evaluation … reads is admin-only").
>   - Recommendation: a founder decision and a new B-ID, e.g. operators may add or tighten deny and escalate rules, while deleting, disabling, loosening or reordering requires admin.
>   - Related Info: workspace admins of any org role can create workspace `allow` policies (`router.go` workspace group). Floor-first ordering and the evaluator's default ALLOW (`eami-policy/evaluator.go:76,158-164`) make that non-expanding today. It would matter if an org ever configures a default-deny.
>
> **Low**
> - **L1. Operators can move an agent from revoked back to suspended** (`rbac_fields.go:48-55` only restricts `→active`). This isn't an expansion, since the gateway denies both states, but it removes "revoked" as a terminal marker and writes no lifecycle event. It also isn't in the test matrix. It ties into B-230's pending transition enforcement; consider refusing `revoked→*` for non-admins.
> - **L2. The approver tools list returns `base_url`, `mcp_command` (verbatim), `action_paths`, `redaction_rules` and the data-handling fields** (`tools.go` `toolToResp` ~319-345). This is permitted by Q-E, and no credentials are returned. Part A §3 already noted the risk that an admin puts a secret inline in `mcp_command`, and approvers are now a wider audience for that. Consider a trimmed approver projection.
> - **L3. Equality oracle on the unreturned `mcp_args`.** An operator sending `{"mcp_args":[guess]}` gets 200 (match, stripped) or 403 (differs). It's whole-array exact match only, so it's impractical against high-entropy values, but it does leak a field that responses deliberately omit. `credentials` has no oracle, because any credential write is a 403.
>
> **Info**
> - **I1.** The handler never validates `status`; only the DB CHECK does. A bad value gives a 500 that echoes the raw DB error, and admin writes of `risk_tier "critical"` also hit a CHECK (`schema.sql:156` allows only low/medium/high). Both are pre-existing; neither is a bypass.
> - **I2.** The `s.queries == nil` branch in UpdateTool (`tools.go:667`) is test scaffolding inside the production handler. It fails closed, but it means the fake-store unit tests never exercise the real stripping path. Only `rbac_split_pg_test.go` does, and I didn't run it.
> - **I3.** Validation 400s (empty name at `tools.go:584`; the "not an ai_provider tool" gate at `tools.go:604`) fire before the role 403. They reveal only the tool type, which is readable anyway.
> - **I4.** Operators can still decide approvals and trigger TestTool egress to the stored target (pre-existing, by design, containment or operational).
>
> ### Relevant files
> - `C:\AI\EAIM\eaim\eami-api\internal\api\router.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\rbac_fields.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\agents.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\tools.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\middleware.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\store\agents.sql.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\store\tools.sql.go`
> - `C:\AI\EAIM\eaim\eami-api\internal\api\rbac_split_pg_test.go` (not run; needs a DB)

## 6. Follow-ups recorded (not fixed here; not minted — founder's call)
- **Operators keep full org policy writes** (security review M1): create, update, delete, reorder. Under "admins expand", deleting or loosening a deny or escalate policy, or adding a higher-priority allow, is an expansion. This needs a founder decision; it was outside this brief's scope.
- **Operator can move an agent from revoked to suspended** (both reviews, Low). This is not an expansion, but it breaks "revoked" as terminal and writes no lifecycle event. Folds into **B-230** (transition enforcement).
- **The approver tools list returns `base_url`, `mcp_command`, `action_paths`, redaction and data-handling fields** (security L2). Permitted by Q-E, and no credentials are returned. Consider a trimmed approver projection.
- **Equality oracle on the unreturned `mcp_args`** (security L3): whole-array exact match only, so impractical, but noted.
- **The tool edit UI drops `input_schema` from action paths on save**, a pre-existing data-loss bug (code review Low). It also causes a false 403 on an operator's no-op REST save; C0(c) hides that control anyway. Logged in NOTES.md.
- **`status` isn't validated in `UpdateAgent`:** a bad value returns 500 with the raw DB text (B-234 class). `UpdateAgent` also role-checks before validation, while `UpdateTool` validates first (a code review Low).
- **`openapi.yaml` documents no per-route role requirements** (Architect-EAMI; not edited). Logged in NOTES.md.
- **UI gating follows in B-252 C0(c):** the Agents list, Tools, Discover's link control, and Agent Detail Overview-only for approvers.
