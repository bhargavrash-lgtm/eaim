# IA Consolidation Investigation: Discover / Agents / Tools vs Assets (CMDB), Step-up Auth, Config-Form Quality

**Date:** 2026-09-26. **Author:** Claude Code. This is an investigation only: no application code changed, and no B-ID was minted.

**Roadmap mapping** (per the CLAUDE.md rule), flagged rather than forced:
- **Parts A and B** are closest to Horizon 1, "CMDB completion". That item names the endpoint-centered asset view but not a navigation consolidation.
- **Part D** is Horizon-0-adjacent maturity work, like `MATURITY_AUDIT.md`. It is not itemized in the roadmap.
- **Part C (step-up authentication)** maps to no named roadmap item. Any build brief for it needs founder confirmation of placement first.

**Method**
- **Source:** direct reads of every file cited, as file:line.
- **Backend:** each claim that depends on backend behaviour was traced to its handler, store and SQL.
- **Live check:** one claim was checked against the running stack (§A1). The fixture admin was created and deleted, and the DB snapshot diff before and after was identical.
- **Design rules:** DESIGN_SYSTEM.md §0 (all four pages are Admin mode), §7.1 and §7.2, and the CLAUDE.md one-spine rule.

---

## Part A: Real content audit

### A4. Current navigation (`Navigation.tsx:35-73`, rendered by `Sidebar.tsx`)

These four concepts occupy **4 distinct sidebar entries in 2 groups**:
- **Overview:** Discover (`/discover`) and Assets (`/assets`).
- **Gateway:** Agents (`/gateway/agents`) and Tools (`/gateway/tools`).

There is also one non-nav route, Agent Detail (`/gateway/agents/:id`, `router.tsx:42`). The sidebar has 16 items in total (15 plus the conditional My Workspaces).

Of the four, only **Agents and Tools** are among CLAUDE.md's six core pages. Discover predates that rule. Assets is a documented one-spine exception (`Navigation.tsx:38-49`).

**The real fragmentation is wider than four nav entries.** One endpoint and its governed agent are configured in **four different places**:

| What | Where | Evidence |
|---|---|---|
| Endpoint ↔ governed-agent link | Discover's endpoint drawer only | `DiscoverPage.tsx:70-111`; the code comment says it is "the only place that link is ever set or cleared" |
| Endpoint scanner settings (scan interval, model paths, enabled scanners) | Agents page → "Configure" | `AgentsPage.tsx:47-186`. Stored in `agent_configs`, keyed to `gateway_agents` (`schema.sql:586`). The linked endpoint's scanner polls it via `GET /v1/agents/{agent_id}/config` (B-165, `agent_config_remote.go`) |
| Agent credentials (agent-bound API keys) | Settings → API Keys tab | `SettingsPage.tsx:544+`, `auth.go` `CreateAPIKey` |
| CI classification | Assets | `AssetsPage.tsx:93-97` |

### A1. Discover vs the Assets endpoint panel

**What the Assets page shows for an endpoint today**
- **Table:** name (hostname), kind, classification, status (always the literal `discovered`), risk *tier* (derived from `risk_score` ≥70/≥40, `store/cmdb.sql.go:206-208`), and workspace. The hidden `detail` field is `agent_version`.
- **Row click:** opens `AssetClassificationPanel` (`AssetsPage.tsx:93-97`). It shows **only** the resolved classification, its source, and the type selector.

**Present in Discover but absent from Assets** (the exact merge gap):

| Field or action | Discover location |
|---|---|
| List columns: OS, AI-app count, local-model count, MCP count, GPU count, last seen | `DiscoverPage.tsx:337-346` |
| OS/platform filter (client-side only, over the fetched page) | `:377-386, 333-335` |
| Drawer summary: OS, agent version, **agent ID** (discovery identity), **last seen**, **numeric risk score** (Assets shows only the tier) | `:139-152` |
| **Linked-governed-agent control**, the only UI that writes `endpoints.gateway_agent_id` | `:70-111, 160` |
| MCP servers (name, source, port, active) | `:163-178` |
| AI apps (name, version) | `:181-194` |
| Local models (name, source, size) | `:197-212` |
| Cloud clients (provider, configured, 7-char key prefix) | `:215-231` |
| GPUs (name, VRAM, driver) | `:234-249` |
| Network activity (remote host:port, process, PID, state) | `:252-265` |
| Python environments plus AI packages | `:268-290` |
| Node.js AI projects plus AI packages | `:293-314` |

**Where the drawer's data comes from**
- Every drawer section reads the raw `latest_report` JSON (`useEndpoint`, `:120-121`).
- Only **3 of those 8 domains** have normalized tables: `endpoint_ai_apps`, `endpoint_mcp_servers` and `endpoint_model_files`.
- Cloud clients, GPUs, network activity, Python environments and Node projects exist **only** in the raw report.

**Discover is weaker than recorded.** This corrects `MATURITY_AUDIT.md:31, 51`.
- **Hostname search does nothing.** The UI sends `search` (`DiscoverPage.tsx:328-331`), and `api/openapi.yaml:1234` documents it. But `ListAgentEndpoints` (`discover.go:60-72`) reads only `page`/`per_page`. `ListAgentEndpointsParams` has no filter field (`store/endpoints.sql.go:402-406`).
  - **Live-confirmed 2026-09-26:** `GET /v1/endpoints?search=zz-no-such-host-zz` returned all 5 of 5 endpoints. The Assets page's `q=zz-no-such-host-zz` returned 0.
- **Only the first 25 endpoints are ever reachable.** The page always requests `per_page: 25` and never sends `page`, and `DataTable pageSize={1000}` shows no pager (`:328-331, 396`). Server paging itself works: `?per_page=1&page=2` returned 1 row of 5.
- **The OS filter is client-side over those 25 rows**, while the count shown is the server total (`:333-335, 387`).

The Assets page already has working search (name and detail), server pagination and filters. **As a browse surface, Discover is strictly weaker today.**

**Coupling:** `EndpointDrawer` is **exported from `DiscoverPage.tsx`** and imported by `AgentDetailPage.tsx:14, 266` (the endpoint node in the relationship graph). It was also imported by the pre-B-196-Brief-1 Assets page.

### A2. Agents vs the Assets agent panel

**Actions on the Agents list page** (`AgentsPage.tsx`):
- **+ Add agent** (`:412-421`): creates name, model, owner, scope, risk tier and token TTL (`:198-297`).
- **Configure** (`:377-382`): endpoint scanner settings, see A4.
- **Suspend/Reactivate** (`:318-325, 383-389`): no confirmation.
- **Delete** (`:390-395, 472-495`): ConfirmDialog. A 409 appears when history exists ("suspend it instead").
- **Row click** navigates to Agent Detail (`:453`).
- **`?highlight=` deep link** (`:306-307`).
- Columns: name, model, risk, status, owner.

**Agent Detail** (`AgentDetailPage.tsx`, B-200) is **entirely read-only**:
- relationship graph and read-only policy, workflow, tool and endpoint panels;
- owner and scope grid;
- a **disabled** "More actions — not built yet" button (`:139-147`).

None of the four actions exists there.

**The Assets agent panel links to neither Agent Detail nor its own detail view.**
- Row click opens only `AssetClassificationPanel`.
- B-217 had built `AgentAssetPanel`, which **duplicated** a simpler view: a plain list of the same `/connections` data, with no link to Agent Detail.
- B-196 Brief 1 (`6924729`) replaced the Assets page's row click. `components/cmdb/AgentAssetPanel.tsx` now has **zero importers** (it is orphaned), confirmed by `git show 6924729^:…AssetsPage.tsx:10-12, 229-235`.
- **Today the Assets page neither hands off nor duplicates.** The agent detail content it showed in B-217 was lost in Brief 1, without being recorded as a loss.

### A3. Tools vs the Assets tool panel

**Actions on the Tools page** (`ToolsPage.tsx`):
- **Add tool** (`:398-628`), covering:
  - type and auth type;
  - MCP command and args;
  - REST base URL, with OpenAPI action discovery (`:264-394`) and per-action path mapping (`:200-255`);
  - AI provider settings: provider, audit mode, data-handling designation and note, redaction rules;
  - an encrypted API key or connection string.
- **Edit** (`:632-878`): the same fields; type and auth type are immutable.
- **Test connection** (`:948-960, 975-995`).
- **Remove** (`:1045-1056`): ConfirmDialog.
- A **"data handling unknown" warning badge** in the list (`:916-922`).
- Columns: endpoint/command and last used.

**There is no per-tool destination.**
- No route and no row click (`DataTable` has no `onRowClick`, `:1015-1034`).
- No `?highlight=` support.
- The only single-tool view anywhere is Agent Detail's read-only `ToolDetailPanel` (`AgentDetailPage.tsx:90-119`).

**On the Assets page:** row click opens only the classification panel. The B-217 `ToolAssetPanel` (type, auth, provider, data handling, last used) is **also orphaned** (0 importers).

---

## Part B: Honest gap check (what removing each standalone page would actually lose)

**Proposed model:** CMDB (the Assets page) becomes the canonical browse-and-classify surface. Discover's detection data merges into CMDB's endpoint detail. Agents and Tools remain configuration destinations that CMDB hands off to.

**Verdict: the direction is correct, but three parts of the model as stated are wrong or incomplete.**

**1. Discover → merge into the CMDB endpoint detail. Correct, provided the merge carries all of the following.**
- **The link control, not only the data.** Removing Discover without moving `LinkedAgentControl` removes the **only** way to set `endpoints.gateway_agent_id`. That silently breaks two things built on the link:
  - B-165 remote scanner config: the scanner gets a 404 when unlinked;
  - Agent Detail's endpoint node.
- **All 8 detail domains.** Five of them exist only in the raw report, so the merged view must keep rendering `latest_report`. It cannot be built solely on the normalized tables planned for B-196 Brief 2.
- **Summary fields the Assets page lacks:** agent ID, last seen, the numeric risk score, and the per-category counts and OS columns, if they're wanted in the table.
- **Relocating `EndpointDrawer`.** It lives in `DiscoverPage.tsx`, and Agent Detail imports it from there.

Nothing else would be lost. Discover's search is non-functional, and its list is capped at 25 rows, so the Assets page's list already supersedes it. **Honest caveat:** Paste Detection is also endpoint-sourced and sits under another nav entry. It is outside this brief, but it is the same "endpoint data scattered" pattern.

**2. Agents → Assets hands off, no duplication. The direction is correct, but the model assumes a destination that doesn't exist yet.**
- **The handoff is not built.** The Assets page currently offers no route to Agent Detail at all (see A2). Adding it is small.
- **Agent Detail is not a configuration destination today.** Add, Configure, Suspend/Reactivate and Delete exist **only as row buttons on the Agents list** (A2). If Assets hands off to Agent Detail, those actions must move to Agent Detail (the currently disabled "More actions" slot is the natural home). Otherwise the Agents list page stays the only place to act.
- **Removing the Agents list page would lose today:**
  - all four actions;
  - `?highlight=` deep links;
  - the owner column (the Assets page doesn't show owner).
- **Assets and a separate Agents list would duplicate the browse function** unless the Agents list is deliberately kept as the Gateway-scoped view.
- **Agent credentials live in Settings, not with the agent.** This is a real IA gap the model doesn't mention. The same goes for the scanner-config/endpoint-link split in A4.

**3. Tools → Assets hands off, no duplication. The direction is correct, but the model is incomplete: there's no per-tool destination to hand off to.**
- **The only destination is the whole Tools page.** The handoff therefore needs a new per-tool target: a `?highlight=`-plus-open-edit-panel deep link, or a tool detail route.
- **Removing the Tools page would lose all tool configuration** (A3). It must stay a destination, as the model says.
- **No real relationship data exists for tools**, beyond Agent Detail's audit-derived per-agent tool list. This was already recorded by B-217 and is still true.

**4. An unrecorded regression to log (not a new B-ID; noted here for B-196).** B-196 Brief 1 replaced B-217's agent and tool detail panels with the classification panel, leaving two orphaned components. Whether to delete them, or re-home their content into the Asset panel as links, is a Brief-2-or-consolidation decision.

---

## Part C: Step-up authentication for sensitive actions

### C2. Does any step-up mechanism exist? **No.**

**What exists today**
- **Password re-entry:** the only one anywhere is the user's own password change, `ChangeMyPassword` (`users.go:80, 349-350`, `current_password`).
- **No re-auth pattern anywhere:** a grep across `eami-api`, `eami-gateway` and `eami-ui` for step-up, reauth, `auth_time`, `recent_auth`, sudo and elevate found none.
- **Session lifetime:** access JWTs last 1 h and refresh tokens **30 days** (`config.go:308-309`). An unattended browser session can therefore perform any admin action for up to 30 days.

**Facts that constrain any design**
- **Admin routes accept only the user JWT** (`middleware.go:34-43`). API keys are agent-bound credentials used at the gateway (`auth.go` `CreateAPIKey`, B-098); they cannot call admin routes. A step-up check on the JWT path therefore has **no API-key bypass**.
- **SSO users have no password.** `users.password_hash` is **NULL for SSO-only users** (`schema.sql:35`), and SSO (B-138) is planned. A password-only re-prompt would lock SSO admins out of every stepped-up action. The mechanism must allow an IdP re-authentication path later.

### C1. Genuinely sensitive actions, with evidence

| Action | Why it's sensitive (verified) |
|---|---|
| **Rotating a tool credential** (`PATCH /v1/gateway/tools/{id}` with `credentials`) | Replaces the secret the gateway uses. **Viewing a stored credential is not a real action:** credentials are write-only and never returned by any API (`ToolsPage.tsx:640-645`, B-022). It should stay that way. |
| **Changing `base_url` on an existing credentialed REST tool** (found during this audit) | REST dispatch sends the stored API key as `Authorization: Bearer` to `base_url` (`eami-gateway/internal/toolrouter/router.go:235`). Editing only the URL, with the credential field left blank ("keep current"), re-points the existing secret at any host. This is the highest-value credential-exfiltration path found. |
| **Deleting an agent** (`DELETE /v1/gateway/agents/{id}`) | Irreversible. |
| **Reactivating a suspended agent** | Re-grants a contained identity's access. |
| **Minting an agent API key** (`POST /v1/auth/api-keys`) | Creates a new agent credential; the raw key is shown once. |
| **Deleting a policy** (`DELETE /v1/gateway/policies/{id}`) | Removes a control. The ConfirmDialog says it is permanent. |
| **Disabling a policy, or changing its action to `allow`** | Weakens a control without deleting it. |
| **Other candidates found:** user role change to admin (`PUT /v1/users/{id}/role`), admin-generated password-reset link (`POST /v1/users/{id}/reset-link`, which yields an account-takeover-capable link), and license upload | Privilege escalation, or entitlement changes. |

**Deliberately excluded** (adding friction here would be wrong):
- suspending an agent and revoking an API key: these are protective incident-response actions;
- test connection, create policy (drafts by default), classification edits and all reads.

**A lower risk than it looks:** `mcp_command` is stored but **never executed by the gateway**. There is no `os/exec` in `eami-gateway`.

### C3. Recommendation (not a build proposal)

**The mechanism: server-enforced "recent authentication", following sudo's timeout convention.** It must not be a UI-only modal, which a direct API call bypasses.
- **Re-auth endpoint.** A new `POST /v1/auth/step-up` verifies the password, using the same `CheckPassword` path as `users.go:349` plus a rate limit. It returns a short-lived step-up proof bound to the user and session: either a re-minted access token carrying an `auth_time`-style claim, or a separate signed token.
- **Absolute 5-minute window.** Matching sudo's default `timestamp_timeout`, it is **not** extended by activity.
- **Route middleware.** A `requireRecentAuth` middleware on **only** the routes above returns `403 step_up_required`.
- **UI.** A global client handler shows a re-prompt modal and retries the original request once.
- **Audit and SSO.** Step-up successes and failures should be audit events (this ties to B-224). For SSO users, the same `step_up_required` response later maps to an IdP re-auth (OIDC `prompt=login`/`max_age`) instead of a password.

**Which actions, in phases:**
- **Phase 1**, route-level and simple: tool credential rotation, tool `base_url` change on a credentialed tool, agent delete, agent reactivate, agent API-key minting, policy delete, user role elevation, admin reset-link generation.
- **Phase 2**, which needs server-side diffing: policy disable, and policy action → `allow`.

**Alternative for the base_URL case:** require the credential to be re-entered whenever the destination changes. That closes the path even without step-up. It is worth the founder's choice.

---

## Part D: Configuration (create/edit) form quality

### D1. Tools

**Credential entry and rotation**
- **Secure but thin.** It is a password field whose "Leave blank to keep the current value" placeholder is the only rotation affordance (`ToolsPage.tsx:859-866`).
- **No status shown.** There is no indicator of whether a credential is currently set, no last-rotated time and no expiry.
- **OAuth2 and Basic auth have no credential input at all.** Both are selectable (`:515-518`), but only `api_key` and `db_connection_string` render a field, in both Add (`:600-616`) and Edit (`:667-670, 859`). A tool with either auth type cannot be given credentials through the UI. Backend support for storing them was not verified.

**Connection test**
- **Hard to find and hard to use:**
  - it is a small row-level link (`:948-960`), unavailable inside Add or Edit, so you cannot test before saving or right after rotating a credential;
  - the result auto-clears after 6 s (`:994`);
  - the failure *reason* is shown only as a hover tooltip (`title`, `:951`);
  - MCP tools always report "misconfigured".
- **SSRF protection is real** (`tool_connectivity.go:19-60`, refusing loopback, link-local and private targets). But the gateway's REST dispatch (`toolrouter`) has **no equivalent guard** (grep found `safeDialContext` only in `aiprovider` and the API's test). For an on-prem internal REST tool, the test may therefore report failure while real dispatch succeeds. Verify this before any redesign relies on "test" as a health signal.

**Other issues**
- **Custom redaction patterns are a raw JSON textarea** (`:96-101`), and `disabled_patterns` has no editor (`:659-665`).
- **Current MCP args can't be shown on edit**, because `ListTools` doesn't return them (`:638`).
- **Remove has no error handling** (`:1053`).
- **Standing UI rules are broken:** local `useState`/`setTimeout` toasts violate CLAUDE.md's `useToast` rule (B-182), and the buttons use raw `indigo-*` classes (`:1005-1007, 1027`).

### D2. Agents

**Creation is complete and clear:** six zod-validated fields (`AgentsPage.tsx:198-297`).

**Editing after creation does not exist in the UI**
- The backend accepts `scope`, `risk_tier` and `token_ttl_seconds` updates (`types.go:118-123`, `AgentUpdateRequest`), but the UI only ever sends `status` (`:318-325`).
- Name, model and owner are not editable even in the API.
- Agent Detail, the read-heavy relationship view, has no edit surface; its action slot is disabled.
- **Scope, the field the governance model is built around, can be set once and never corrected in the product.**

**"Configure Agent" is mislabeled**
- It edits *endpoint scanner* settings (`:47-186`). They only take effect if an endpoint is linked, and the form never says whether one is.
- Several endpoints linked to one agent share the settings.

**Other issues**
- **Suspend** disables every row's button while pending and reports errors in a page banner.
- **The toasts are local state**, violating the `useToast` rule (`:50, 76-77, 215, 225-228`).
- **"+ Add agent" is a raw `<button>`**, not the `Button` component (`:415-420`).

### D3. Policies

**Condition builder: usable for experts, error-prone for everyone else**
- **Free-text lists with no validation against real data.** Tool names and action verbs are comma-separated text (`PolicyPanel.tsx:177-189`), with no pick-list from the real tools list (`/v1/gateway/tools` exists). A typo is a silent non-match.
- **No preview for the agent glob** of which agents it matches (`:171-175`).
- **No way to test a policy** against a sample or past calls.
- **Semantic rules silently never match.** "Semantic rule (LLM-evaluated)" is offered as a normal condition (`:211-216`), but `evaluateSemantic` is a stub that **always returns false** (`eami-policy/semantic.go:22-31`). The evaluator skips the rule when it is false (`evaluator.go:133-140`). A policy with a semantic condition therefore **never matches**: a "deny" written that way silently never fires, and the UI does not disclose this. This conflicts with DESIGN_SYSTEM §7.4's honest-data rule, and it is the most serious Part D finding. `scope_drift`, by contrast, is a real heuristic (`structural.go:143-169`).

**Priority and reordering: works for a handful of policies, degrades as they grow**
- **Two competing mechanisms:**
  - a free numeric priority field in the form (`:130-135`); a collision fails on the unique constraint and surfaces only as a generic "Save failed" toast (`:89-91`);
  - one-step up/down chevrons (`PoliciesPage.tsx:55-61, 79-101`).
- **Every chevron click resubmits the entire order.** Moving policy #20 to #1 takes 19 sequential round-trips, and there is no drag.
- **Org-floor and workspace-scoped policies are interleaved** in one chevron-ordered list (the scope badge is shown, `:116-120`).
- **Reorder failures can return raw driver error text** (the handler's own comment, `policies.go:431-436`).
- **Delete has no error handling** (`PoliciesPage.tsx:218`). A failed delete leaves the dialog open with no message.

---

## Corrections to existing records

1. **`MATURITY_AUDIT.md:31, 51`** say Discover has "real, API-backed hostname search" and count it among the 2 of 11 pages with real search. **That is false** (live-confirmed, §A1). A status note has been added there, leaving the historical text intact.
2. **B-196 Brief 1's records** don't mention that it orphaned B-217's `AgentAssetPanel`/`ToolAssetPanel` and removed the Assets page's agent/tool detail content. This is recorded here and in CONTEXT.md.
3. **`api/openapi.yaml:1234`** documents `search` on `/v1/endpoints`, but the handler ignores it. This is a contract/implementation mismatch for Architect-EAMI, or a handler fix; the decision is the founder's.

No B-IDs were minted. Every item above is investigation output, awaiting the founder's decision on what to scope.
