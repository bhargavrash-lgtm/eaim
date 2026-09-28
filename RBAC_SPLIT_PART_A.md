# B-253 RBAC Split — Part A (investigation only)

**Date:** 2026-09-28. **Author:** Claude Code. **Report only:** no code changed. Server routes only; UI gating follows in B-252 C0(c).

**Roadmap:** Horizon 1, "CMDB completion" / Horizon 0 hardening (founder mapping).

**Decision being implemented** (`IA_CONSOLIDATION_MIGRATION_PLAN.md`, "Founder decisions", Q2): *operators contain; admins expand or destroy.*
- **Stay admin + operator:** suspend and API-key revoke.
- **Become admin-only:** reactivate, delete, key minting, tool credential or `base_url` changes, the endpoint link, and every create or import path that sets a credential or `base_url`.
- **`approver`:** read on agents, tools and assets only.

## 1. Every route in scope (`router.go:331-369`, the admin + operator group)

| Route | Handler | What it can change | Credential or identity bearing? | Decision → proposed |
|---|---|---|---|---|
| `GET /v1/auth/api-keys` | ListAPIKeys | — (returns prefix only, never the key) | no | **admin + operator** (needed to find a key to revoke) |
| `POST /v1/auth/api-keys` | CreateAPIKey | **mints an agent credential** (raw key returned once) | **YES** | **admin** |
| `DELETE /v1/auth/api-keys/{keyId}` | RevokeAPIKey | revokes a key | no (containment) | **admin + operator** |
| `POST /v1/gateway/agents` | CreateAgent | creates a new governed identity (sets no credential or `base_url`) | identity-creating, but **not named in the decision** | **Q-A below** |
| `PATCH /v1/gateway/agents/{agentId}` | UpdateAgent | `status` (suspend **or reactivate**), `scope`, `risk_tier`, `token_ttl_seconds` | **mixed in one route**: reactivate is an expansion; suspend is containment | **route stays admin + operator; in-handler check: a transition to `active` requires admin** |
| `DELETE /v1/gateway/agents/{agentId}` | DeleteAgent | destroys an agent | destroy | **admin** |
| `PUT /v1/gateway/agents/{agentId}/config` | UpdateAgentConfig | endpoint scanner settings | no | **admin + operator** |
| `PATCH /v1/endpoints/{endpointId}/link-agent` | LinkEndpointAgent | binds an endpoint to a governed identity | **YES** (named) | **admin** |
| `POST /v1/gateway/tools` | CreateTool | `base_url`, `credentials` (api_key / oauth client id and secret / connection_string), `mcp_command`/`args`, `provider`, `action_paths`, `audit_mode`, `redaction_rules`, data handling | **YES**: sets a credential and/or `base_url` in every REST and AI-provider create. An MCP create may set neither. | **admin** (Q-B: whole route vs. only when a credential or `base_url` is set) |
| `PATCH /v1/gateway/tools/{toolId}` | UpdateTool | the same fields minus type and auth | **mixed in one route**: `credentials`/`base_url` are credential-bearing; `name`, `action_paths` (a path under the same host) and data-handling note are not | **route stays admin + operator; in-handler check: `credentials` (via the existing `credentialsProvided`) or `base_url` present requires admin**. Also Q-C: `provider`, `audit_mode`, `redaction_rules` |
| `DELETE /v1/gateway/tools/{toolId}` | DeleteTool | destroys a tool | destroy | **admin** (Q-D confirms: "destroy" covers tool delete) |
| `POST /v1/gateway/tools/{toolId}/test` | TestTool | tests connectivity; writes only status and latency | no | **admin + operator** |
| `POST /v1/gateway/openapi/discover` | DiscoverOpenAPI | **writes nothing** (stateless preview; its `spec_url` fetch is SSRF-guarded). Generated actions reach a tool only through the `action_paths` PATCH. | no (an "import" in name only: it sets no credential or `base_url`) | **admin + operator** |
| `DELETE /v1/gateway/nodes/{nodeId}` | DeleteNode | removes a gateway node registration | destroy, but not named | **Q-D** |
| policies, workflows, workflow-step params, alert rules (same group) | — | — | outside this decision's scope | **unchanged** |

**Import and bulk paths:**
- **No bulk or import write route exists** for agents, tools or keys.
- The only "import" is `DiscoverOpenAPI`, which writes nothing.
- Nothing else in eami-api writes `gateway_tools.base_url` or credentials: the only writers are `CreateTool` and `UpdateTool`.
- `reseed.sql` and `seed-db.sh` are operator-run SQL scripts, not API routes.

**Why two decisions need in-handler checks, not route groups:** reactivate shares `PATCH /agents/{id}` with suspend, and credential and `base_url` changes share `PATCH /tools/{id}` with non-secret edits. Proposal:
- a tiny helper `requireAdmin(w, uc, what) bool` that returns the **same 403 body** as `requireRole`;
- called only on the specific branch;
- for agents, "transition to `active`" is judged against the row's current status (read in the same handler). This ties into B-230's pending transition enforcement.

## 2. What relies on operator access to these routes today
- **UI** (to be fixed in C0(c), not here):
  - `AgentsPage.tsx` (Add, Suspend/Reactivate, Delete and Configure buttons; no role checks);
  - `AgentActionsTab.tsx` (`WRITE_ROLES = ['admin','operator']`, which would show Reactivate and Delete to operators and then 403);
  - `ToolsPage.tsx` (Add, Edit with credentials and `base_url`, Remove; no role checks);
  - `DiscoverPage.tsx` `LinkedAgentControl` (no role check);
  - `SettingsPage.tsx` API Keys tab (Create).
  - **After the server change, operators see these controls and get 403 until C0(c) ships.** Recommend shipping C0(c) immediately after B-253.
- **Tests:**
  - `agents_test.go` `TestCreateAgent_OperatorRole_Succeeds` (`:213`) asserts that an operator can create an agent. Keep it if Q-A says operator, otherwise invert it.
  - `agent_config_pg_test.go` uses operator tokens for config PUT (stays operator) and for the B-232 cross-org attacker (role-independent). No change.
  - **No existing test exercises tool, key or link writes as an operator.**
- **eami-gateway:** its token-revoke endpoint (`internal/identity/revoke_http.go`) is service-key authenticated, not role-based. Its comment anticipates a future eami-api proxy "requireRole admin/operator": containment, consistent with the decision. **No gateway code calls these eami-api routes.**
- **eami-collector, eami-agent, scripts:** none call these routes (grep across all modules and `scripts/`).

## 3. Credential material in read responses: confirmed none
- **Tools** (`ToolResp`, `tools.go:295-316`, used by list, create and update responses): id, name, type, auth_type, `mcp_command`, `base_url`, status, action_paths, provider, audit and data-handling fields, redaction_rules.
  - **No `credentials` or `credentials_encrypted` field.** Credentials are write-only (the B-022 design).
  - Test Connection returns only `success`, `latency_ms` and an `error` whose text is documented as never derived from decrypted material (`tools.go:767-771`).
- **API keys** (`APIKeyResp`, `types.go:66-75`): prefix, scopes, dates and `agent_id`. **Never the key or its hash.** The raw key appears exactly once, in `CreateAPIKeyResponse`.
- **Agents** (`AgentResp`, `types.go:85-96`), **agent config** (`agents.go:599-604`) and **connections** (`agents.go:384-387`): no secrets.
- **Adjacent, informational (not tools or agents):**
  - An endpoint's raw `latest_report` (`GET /v1/endpoints/{id}`) includes the **7-character prefix** of discovered cloud-client API keys (Discover renders it). That's a prefix, not a usable secret. Noted because B-253 would extend endpoint reads to `approver` (see Q-E).
  - `mcp_command` is returned verbatim. If an admin ever put a secret inline in it, readers would see it. The UI has no args field that invites that, and `mcp_args` are not returned.

## 4. Proposed route groups

```
admin + operator  (containment + operational)
  GET    /v1/auth/api-keys
  DELETE /v1/auth/api-keys/{keyId}
  PATCH  /v1/gateway/agents/{agentId}        + in-handler: →active requires admin
  PUT    /v1/gateway/agents/{agentId}/config
  PATCH  /v1/gateway/tools/{toolId}          + in-handler: credentials or base_url (and Q-C fields) require admin
  POST   /v1/gateway/tools/{toolId}/test
  POST   /v1/gateway/openapi/discover
  (policies, workflows, alert rules: unchanged)
  [POST /v1/gateway/agents, DELETE /v1/gateway/nodes/{id}: here or admin, per Q-A / Q-D]

admin only  (new group)
  POST   /v1/auth/api-keys
  DELETE /v1/gateway/agents/{agentId}
  PATCH  /v1/endpoints/{endpointId}/link-agent
  POST   /v1/gateway/tools                    (per Q-B)
  DELETE /v1/gateway/tools/{toolId}

admin + operator + approver + viewer, read-only  (new group, split out of today's read group)
  GET /v1/gateway/agents, /{agentId}, /{agentId}/connections, /{agentId}/config
  GET /v1/gateway/tools
  GET /v1/cmdb/classifications, /v1/cmdb/assets
  GET /v1/endpoints, /v1/endpoints/{endpointId}     (nested requireModuleLicensed("discovery"), per Q-E)
  — every OTHER route in today's admin/operator/viewer read group stays WITHOUT approver
    (policies, workflows, nodes, audit, finops, memory/episodes, alerts, model pricing,
     workspaces, paste-events, /v1/discover/endpoints)
```

## 5. Test plan
Real Postgres, a table-driven `rbac_split_pg_test.go`. Pool closed via `t.Cleanup` first, per CLAUDE.md.

**Operator gets an exact-body 403** on every restricted route, including:
- `POST /v1/auth/api-keys`, `DELETE /v1/gateway/agents/{id}`, `PATCH …/link-agent`;
- `POST /v1/gateway/tools` in each shape: REST with `base_url`, with `credentials`, AI provider with an `api_key`, and MCP (per Q-B);
- `DELETE /v1/gateway/tools/{id}`;
- `PATCH /v1/gateway/tools/{id}` with `credentials`, and with `base_url`;
- `PATCH /v1/gateway/agents/{id}` `{"status":"active"}` on a suspended agent.

Each is also asserted to have **written nothing** (a row re-read).

**Operator still succeeds:**
- `PATCH agents` `{"status":"suspended"}` and scope/risk/TTL edits;
- config PUT;
- API-key revoke and list;
- tool test;
- `PATCH tools` with only a name or `action_paths`;
- OpenAPI discover;
- plus any Q-A or Q-D routes that stay operator.

**Admin succeeds** on every restricted route (2xx plus the effect verified).

**Approver:**
- `200` on each new read route;
- `403` on **every** write in the table;
- `403` on reads **not** granted (policies, audit, finops, workflows, episodes, paste-events). This catches accidental over-grant.

**Viewer unchanged:** the same reads as today (`200`), every write `403`.

**Mutation tests**, each separately:
1. move each admin-only route back to admin + operator; its operator row must fail;
2. delete the reactivate in-handler check;
3. delete the tool credential/`base_url` in-handler check;
4. drop `approver` from the new read group;
5. add `approver` to the broad read group; the over-grant rows must fail.

**Live** (rebuilt stack, fixture users per role, snapshot diff): the same matrix via HTTP for a representative subset per group.

## 6. Questions for the founder (the decision doesn't settle these)
- **Q-A: `POST /v1/gateway/agents` (create).** It creates a new governed identity but sets no credential or `base_url`, so the decision's wording doesn't cover it. "Admins expand" suggests **admin-only** (recommended). Note that a new agent is inert until an admin mints its key, which argues it could stay operator. Your call.
- **Q-B: `POST /v1/gateway/tools`.** Admin-only for the **whole route** (recommended: simpler, one route-group move, and a new tool is itself a new egress target), or only when the body sets a credential or `base_url`? The latter leaves credential-less MCP creates to operators and needs an in-handler check.
- **Q-C: other `PATCH /tools` fields that expand exposure.**
  - **`provider`** re-points an AI-provider tool's stored key to a different adapter; only Claude is registered today. That's the same class as `base_url`, so **admin** is recommended.
  - **`audit_mode` → `structural_metadata_only`** and **loosening `redaction_rules`** weaken governance on data leaving the org. Recommend **admin** for any change to these two. Only loosening strictly needs it, but that requires diffing.
- **Q-D: destroys not named.** `DELETE /v1/gateway/tools/{id}` is assumed covered by "destroy" (proposed admin). `DELETE /v1/gateway/nodes/{id}` (a node registration record): recommend **admin** for consistency, but it wasn't named. Confirm both.
- **Q-E: what "assets" means for `approver`.** Proposed: CMDB assets and classifications, plus endpoint reads (`/v1/endpoints*`), since Endpoint Detail becomes an asset page in C2, plus agent connections and config. Excluded: `/v1/discover/endpoints` (HTTP-traffic observations, not assets). Confirm.
- **Q-F: agent `scope`, `risk_tier` and `token_ttl_seconds` edits.** The plan's recommendation kept these operator. Raising `token_ttl_seconds` or widening `scope` arguably *expands*. Recommend: keep operator for now, and revisit with B-255 (agent edit UI), where server-side diffing can make only widening admin-only.

**Side note** (no action): `BACKLOG.md` B-248 item 3 (the invite link documented as expiring after "72 hours" vs the actual 48 h) duplicates the older **B-213**, which is the same doc correction. When B-248 is picked up, that item should defer to B-213.
