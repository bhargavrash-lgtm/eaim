# IA Consolidation — Touchpoint Audit, Target State, Ordered Migration Plan

**Date:** 2026-09-28. **Author:** Claude Code. **Investigation only:** no application code changed and no B-IDs minted. Every future consolidation brief should be scoped from this document.

**Roadmap mapping:** Horizon 1, **"CMDB completion"** (`rheoARC_Roadmap_Enterprise_AI_Platform.md:41`), plus Horizon 0 maturity (making the shipped Discovery and Gateway surfaces coherent).

**Inputs read:**
- CLAUDE.md, CONTEXT.md and `DESIGN_SYSTEM.md` (read in full this session);
- `IA_CONSOLIDATION_INVESTIGATION.md`, used only as a cross-check; every Part A fact below was re-derived fresh;
- the design canvas (read-only): Layer 7 "Unified Entity Detail" and Layer 8 "Collapsible Nav Group Template";
- `eami-ui/src` in full (every route, page, component and hook) and `eami-api/internal/api/router.go`'s role groups.

## ⚠ Two discrepancies with the brief, flagged first

1. **`DESIGN_SYSTEM.md` has no §7.8.** It ends at §7.7. No commit on any branch has ever contained a §7.8. Nothing in the repo defines "placeholder navigation entries", and the codebase has no placeholder nav items; the sidebar's 16 entries are all real pages.
   - What exists is **canvas Layer 8** ("Collapsible Nav Group Template"). It defines the *mechanism*: force-open the active group, hide a group that is empty for the viewer's role, and keep Settings and profile outside the groups at the bottom.
   - It explicitly says its group content is **"placeholder — the real, final grouping is still pending IA_CONSOLIDATION_MIGRATION_PLAN.md's own findings."**
   - The five placeholder entries (Guardrails, Automations, Models, Observability, Chat) are therefore placed in Part C from their real roadmap and backlog sources. Their rendering rule is proposed from §7.4 (honest pending state), not from a written spec, because none exists.
   - **Recommendation:** once the founder approves Part C's grouping, write §7.8 into `DESIGN_SYSTEM.md` as a docs-only change.
2. **CLAUDE.md vs `DESIGN_SYSTEM.md` §7.7 conflict** on whether Agents and Tools stay as pages. See Part D, Q1. It blocks Part C step C9 only.

---

## Part A — Exhaustive touchpoint audit (fresh, file by file)

**Method:**
- grep of every `.ts`/`.tsx` under `eami-ui/src` (excluding the generated `api/schema.ts`) for each entity's hooks (`useEndpoint*`, `useAgent*`, `useTools`/`useTool*`, `useCMDBAssets`, `useApiKeys`), API paths, routes (`/discover`, `/gateway/agents`, `/gateway/tools`, `/assets`), and field names (`hostname`, `endpoint_id`, `agent_id`, `agent_name`, `tool_id`, `tool_name`, `gateway_tool_id`, `asset_kind`);
- each hit was then read in context.

Legend: **D** display · **L** link or navigation · **W** write action · **R** reference (a picker, filter or free-text condition).

### A.1 Endpoint

| # | File | Touchpoint | Kind |
|---|---|---|---|
| E1 | `pages/discover/DiscoverPage.tsx` (route `/discover`) | Endpoint list. Server search and paging (B-226/B-227, `:351-356`). An OS filter that is **client-side, current page only** (`:413-415`). Row click opens the drawer. | D, L |
| E2 | `DiscoverPage.tsx:120` `EndpointDrawer` (exported) | Summary (OS, version, agent ID, last seen, risk score) plus **8 detection sections** (`:164-315`). 5 of the 8 come only from the raw `latest_report`. | D |
| E3 | `DiscoverPage.tsx:71` `LinkedAgentControl` | **The only UI that sets or clears `endpoints.gateway_agent_id`**: a `<select>` of all agents via `useAgents` and `useLinkEndpointAgent`. | W, R |
| E4 | `pages/cmdb/AssetsPage.tsx` (route `/assets`) | The unified inventory lists endpoints, with kind, classification, status (the literal `discovered`), risk tier and workspace. Search, server pagination and a classification tree. **Row click opens `AssetClassificationPanel` only** (`:73, :95-100`). No link onward. A licence banner shows when the Discovery module is off (`:67`). | D, W (classification, admin only) |
| E5 | `pages/gateway/AgentDetailPage.tsx:14, :300` | The Connections-graph endpoint node opens **`EndpointDrawer` imported from `DiscoverPage`**, a page-to-page coupling. | L, D |
| E6 | `pages/gateway/RelationshipGraph.tsx:165-172` | Endpoint node (hostname) in an agent's graph. | D, L |
| E7 | `components/cmdb/AgentAssetPanel.tsx:63` | Linked endpoint hostname. **Orphaned: 0 importers.** | D |
| E8 | `pages/dashboard/DashboardPage.tsx:53, :104` | "Endpoints Monitored" metric (`useEndpoints({per_page:1})` total). **Not a link.** | D |
| E9 | `pages/ops/AlertsPage.tsx:37, :85` | The alert metric `new_endpoints_count` ("New Endpoints"). Rule configuration only; no endpoint link. | R |
| E10 | `pages/ops/PasteEventsPage.tsx` (route `/paste-events`) | Endpoint-sourced data (browser-extension pastes relayed by the endpoint agent). Its columns (`:131-135`) show domain, length, hash and OS user, and **no endpoint at all**. The UI hook's type (`hooks/usePasteEvents.ts`) carries no endpoint field. | D (implicit) |
| E11 | `components/layout/Navigation.tsx:37` | Nav: "Discover" (Overview group). | L |
| E12 | `router.tsx:39` | `/discover`. **There is no endpoint detail route.** | — |

### A.2 Agent (governed `gateway_agents`)

| # | File | Touchpoint | Kind |
|---|---|---|---|
| G1 | `pages/gateway/AgentsPage.tsx` (route `/gateway/agents`) | Agent list: name, model, risk, status and owner (`:184-202`). Row click goes to Agent Detail (`:285`). `?highlight=` deep link (`:136-139`). **+ Add agent** (`:251, :272`). Row actions **Configure, Suspend/Reactivate and Delete** (`:205-226`). **No role gating: 0 role checks in the file,** so viewers see every write button and get a server 403. | D, L, W |
| G2 | `pages/gateway/AgentDetailPage.tsx` (route `/gateway/agents/:id`) | **Tabs: Overview, Connections and Actions** (`:131-218`, `?tab=`, WAI-ARIA). Overview shows the metadata grid. Connections shows the graph plus read-only Policy, Workflow and Tool panels (`:31, :60, :91`). | D, L |
| G3 | `components/agents/AgentActionsTab.tsx` | Configure, Suspend/Reactivate and Delete. **Role-gated to admin/operator** (`:19-29`); viewers get a read-only notice (`:62-65`). | W |
| G4 | `components/agents/AgentConfigPanel.tsx` | Endpoint **scanner** settings (`agent_configs`), keyed to the agent. Opened from G1 and G3. B-236 error state. | W |
| G5 | `pages/cmdb/AssetsPage.tsx` | Agents appear as inventory rows (kind "Agent"). **Row click is classification-only, with no link to Agent Detail.** | D, W (classification) |
| G6 | `DiscoverPage.tsx:71-111` (E3) | The agent picker used to link an endpoint. | R, W |
| G7 | `pages/settings/SettingsPage.tsx:540-595, :688` | **API Keys tab**: create a key, optionally bound to an agent (`agent_id` `<select>` from `useAgents`); the list shows the bound agent's name or "Org-wide". **Agent credentials live here, not on the agent.** | D, W, R |
| G8 | `pages/dashboard/DashboardPage.tsx:126-131` | Agent sessions table. **Row click goes to `/audit?agent_name=…`, not Agent Detail.** | D, L |
| G9 | `pages/ops/AuditPage.tsx:95, :143-145, :215` | `agent_name` column plus a free-text `agent_name` filter. No link. | D, R |
| G10 | `pages/ops/AuditEntryDetailPanel.tsx:260-261` | Agent name plus a copyable `agent_id`. No link. | D |
| G11 | `pages/ops/ApprovalsPage.tsx:66, :165-168` | `agent_name` in the list and the card. No link. The response carries `agent_id` (`approvals.go:28`). | D |
| G12 | `pages/finops/FinOpsPage.tsx:268, :314, :386, :459` | Per-agent spend table, "top agent" metric and CSV export. `getRowId` uses `agent_id`. No link. | D |
| G13 | `pages/ops/MemoryPage.tsx:24-25, :92` | Episode list shows `agent_name`. No link. | D |
| G14 | `components/policies/PolicyPanel.tsx:48, :60` / `PolicyBadges.tsx:65` | **Free-text `agent_name_pattern` glob** in the policy conditions. No picker and no preview of matches. | R |
| G15 | `pages/gateway/RelationshipGraph.tsx:97-102, :379` | Centre node (agent name). | D |
| G16 | `components/cmdb/AgentAssetPanel.tsx` | B-217 agent panel. **Orphaned: 0 importers.** | — |
| G17 | `hooks/useDashboard.ts`, `hooks/useEndpoints.ts` | Agent data consumed through the dashboard and link hooks (no UI of their own). | — |
| G18 | `Navigation.tsx:50` / `router.tsx:41-42` | Nav "Agents" (Gateway group); routes for the list and detail. | L |

### A.3 Tool (`gateway_tools`)

| # | File | Touchpoint | Kind |
|---|---|---|---|
| T1 | `pages/gateway/ToolsPage.tsx` (route `/gateway/tools`) | Tool list. **Add tool** (`:398-628`: type, auth, MCP command, REST base URL with OpenAPI discovery and action paths, AI-provider data handling and redaction, credential). **Edit** (`:632-878`). **Test connection** (row-level, `:898-903`). **Remove.** **No row click, no detail route, no `?highlight=`.** **0 role checks,** so viewers see every write control. | D, W |
| T2 | `pages/gateway/AgentDetailPage.tsx:91-119` `ToolDetailPanel` | The only single-tool view anywhere: read-only, reached from an agent's Connections graph. It finds the tool by scanning the full list (`:93`). | D |
| T3 | `pages/gateway/RelationshipGraph.tsx:133-142` | Tool nodes; clickable only when `tool_id` is present. | D, L |
| T4 | `pages/cmdb/AssetsPage.tsx` | Tools appear as inventory rows. **Row click is classification-only.** | D, W (classification) |
| T5 | `pages/gateway/WorkflowsPage.tsx:290-299` | Step editor: **"Connector" `<select>`** of tools (`useTools`), then an action `<select>`. | R, W |
| T6 | `pages/gateway/WorkflowCanvasPage.tsx:48, :95-101` / `workflowCanvasAdapter.ts:33-71` | Canvas toolbox and step labels (`tool.action`); a deleted tool renders as "(deleted connector)". | D, R |
| T7 | `pages/gateway/AgentDetailPage.tsx:80` | A workflow step's tool name in the read-only workflow panel. | D |
| T8 | `components/policies/PolicyPanel.tsx:49, :61, :179` / `PolicyBadges.tsx:66` | **Free-text comma-separated `tool_names`** in policy conditions, with no picker against real tools. | R |
| T9 | `pages/ops/AuditPage.tsx:96, :148-150, :222` / `AuditEntryDetailPanel.tsx:109, :264` | `tool_name` column, filter and detail. **Name only**: the audit response has no tool ID. | D, R |
| T10 | `pages/ops/ApprovalsPage.tsx:67, :172-174` | `tool_name`. Name only in the response, even though `approval_requests.resolved_tool_id` exists in the DB. | D |
| T11 | `pages/dashboard/DashboardPage.tsx:169` | Recent activity: `tool_name · action`. | D |
| T12 | `pages/ops/MemoryPage.tsx:13, :111` | Episode steps `tool_name/action`. | D |
| T13 | `components/cmdb/ToolAssetPanel.tsx` | B-217 tool panel. **Orphaned: 0 importers.** | — |
| T14 | `hooks/usePolicies.ts`, `useAudit.ts`, `useLicense.ts`, `settings/ProfilePage.tsx` | These matched only incidental uses of the word "tool" (types or licence module names). **Not tool touchpoints**; listed so the sweep is literal. | — |
| T15 | `Navigation.tsx:52` / `router.tsx:44` | Nav "Tools" (Gateway group); `/gateway/tools` only. | L |

### A.4 Cross-cutting facts found during the audit
- **Global search is chrome only:** `AppTopBar.tsx:66` says "Global search — not built yet". Assets' search is the only working find-anything surface.
- **The sidebar has no role filtering.** `Sidebar.tsx:39-40` filters only `conditional: 'hasWorkspaces'`, so every role sees every entry.
- **The Workspace mode** (`/workspace/:id`) has no asset view today (`WorkspaceOverviewPage` shows members only). Canvas Layer 4b's "Our AI Assets" is unbuilt, and outside this plan's scope.

---

## Part B — Current vs target, per entity (against `DESIGN_SYSTEM.md` §7.7)

The §7.7 target: **one canonical detail page per entity**; Discover, Agents and Tools **retire as standalone browse destinations**; Assets is the one place to find anything, and a row opens the entity's detail page.

### B.1 Endpoint → Endpoint Detail (§7.7: Overview, Connections, Agent Link, Classification; Spend later)

| Touchpoint | Fate |
|---|---|
| E1 Discover list | **Deleted.** Browse moves to Assets (E4). It first needs **column parity** (OS, per-domain counts, last seen), which the CMDB assets API does not return today, and a **server** OS filter (B-228). |
| E2 `EndpointDrawer` content | **Merges into Endpoint Detail → Overview.** All 8 domains, still rendered from `latest_report` (5 have no normalized table). |
| E3 `LinkedAgentControl` | **Merges into Endpoint Detail → Agent Link.** This must land **before** Discover is removed, or the only way to link breaks (B-165 remote config and Agent Detail's endpoint node both depend on it). |
| E4 Assets endpoint row | **Survives, changed:** row click opens Endpoint Detail. Classification moves into the detail page's **Classification** tab. |
| E5 Agent Detail → `EndpointDrawer` | **Changes:** the node navigates to Endpoint Detail. The drawer import from a page file is removed. |
| E6 graph node | Survives. |
| E7 orphan | **Deleted.** |
| E8 Dashboard metric | **Survives, changed:** becomes a link to Assets filtered to endpoints. |
| E9 Alerts metric | Survives unchanged (rule configuration). |
| E10 Paste Detection | **No home in §7.7 — gap.** Pastes are per-endpoint activity. Proposal: add a future **"Activity" tab** on Endpoint Detail, and keep the org-wide Paste Detection page as the Observe-group aggregate. Needs the endpoint in the paste-events API response. |
| E11/E12 nav, route | Nav entry deleted; `/discover` **redirects** to Assets filtered to endpoints. A new route for Endpoint Detail (Part D, Q4). |
| **§7.7 gap** | **Licensing.** Endpoint data is licence-gated (`requireModuleLicensed("discovery")`). §7.7 doesn't say what Endpoint Detail shows unlicensed. Proposal: the §7.4 honest pending state, reusing Assets' existing banner wording. |

### B.2 Agent → Agent Detail (§7.7: Overview, Connections and Actions built; Orchestration, Autonomy Limits, Memory and Model later)

| Touchpoint | Fate |
|---|---|
| G1 Agents list | **Deleted as a nav destination per §7.7**, but **blocked on Part D, Q1** (a CLAUDE.md conflict). If it stays, it becomes a Gateway-scoped view of Assets filtered to agents. Its actions already exist on the detail page (G3). **"+ Add agent" has no §7.7 home**: it needs a create entry point in Assets (C8). |
| G2/G3/G4 detail and actions | **Survive** (canonical). |
| G5 Assets agent row | **Changes:** row click opens Agent Detail. **§7.7 gap: Agent Detail has no Classification tab**, although Endpoint and Tool do. Agents are classifiable today (B-196), so a Classification tab is proposed. |
| G6 link picker | Moves with E3 into Endpoint Detail → Agent Link. |
| G7 Settings → API Keys | **§7.7 gap: no Credentials tab for agents.** Proposal: a **Credentials tab** on Agent Detail listing, creating and revoking keys bound to that agent. Settings → API Keys stays only for **org-wide** keys (keys without an agent), or becomes a read-only index (Part D, Q5). |
| G8 Dashboard sessions | **Changes:** row opens Agent Detail (it links to Audit today). |
| G9–G13 Audit, Approvals, FinOps, Memory | **Survive** as org-wide Observe pages. **Changes:** agent names become links to Agent Detail. Feasible because every one of these responses carries `agent_id`. |
| G14 policy agent glob | Survives. A matches preview is a Policies-page improvement, not consolidation. |
| G16 orphan | **Deleted.** |
| **§7.7 gap** | **Editing an agent.** The API accepts `scope`, `risk_tier` and `token_ttl_seconds`, but no UI can edit them after creation. §7.7 names no place for it. Proposal: edit-in-place on Overview, role-gated like Actions. |
| **§7.7 gap** | **Scanner config.** "Configure" (G4) edits settings the linked **endpoint** consumes, but they are stored per agent and shared by every endpoint linked to it. The Layer 7 canvas note assigns it to Endpoint Detail; the data model keys it to the agent. A real modelling question (Part D, Q3). |

### B.3 Tool → Tool Detail (§7.7: Overview/Credentials and Classification planned; Model Details and Usage Analytics later)

| Touchpoint | Fate |
|---|---|
| T1 Tools list | Like G1: **deleted as a destination per §7.7**, blocked on Part D, Q1. Its **Add** needs an Assets create entry point (C8). **Edit, Test and Remove merge into Tool Detail** (Overview/Credentials). |
| T2 `ToolDetailPanel` | **Changes:** it gets an "Open tool" link to Tool Detail, or is replaced by navigation. |
| T3 graph node | Survives; navigates to Tool Detail. |
| T4 Assets tool row | **Changes:** row click opens Tool Detail; classification moves into its Classification tab. |
| T5/T6/T7 Workflows | Survive. **Changes:** the chosen connector shows an "open" link to Tool Detail. |
| T8 policy tool names | Survives. A pick-list is a Policies improvement (see the prior investigation's Part D). |
| T9–T12 Audit, Approvals, Dashboard, Memory | Survive, but **cannot link to Tool Detail yet**: the responses carry `tool_name` only. Tool links need the API to expose a tool ID (audit's resolved tool; approvals' existing `resolved_tool_id`). |
| T13 orphan | **Deleted.** |
| **§7.7 gap** | **Test connection has no named home.** It belongs on Overview/Credentials, and it should also be available right after a credential rotation (the prior investigation's D1). |
| **§7.7 gap** | **Tool Detail has no Connections tab**, although Agent Detail has one and §7.7 calls Usage Analytics "maybe redundant with what Connections already shows". A Tool Connections tab (agents that used it, workflows containing it, policies naming it) is proposed. **It needs a new backend endpoint**: nothing returns tool-centric relationships today. |

### B.4 Pages with no home in §7.7 (not entities; placed in the sidebar in Part C)
- **Nodes** (gateway nodes, `/gateway/nodes`): an infrastructure entity with no detail page.
- **Paste Detection, Memory, Audit, FinOps, Alerts, Approvals:** org-wide activity pages. They are not entity pages, and they survive.

---

## Part C — The complete ordered migration sequence

**Principle:** every step ships a coherent, honest UI.
- Nothing is removed until its replacement exists.
- Every interim duplication says which surface is canonical.
- Future tabs are **not** rendered as empty shells. This follows Agent Detail's precedent of showing only built tabs.
- The **B-ID** column records whether a step needs a new ID (**NEW**) or folds into an existing one. Nothing is minted here.

| Step | What gets built or changed | Depends on | Mid-migration UX (what a user sees at this point) | B-ID |
|---|---|---|---|---|
| **C0 Foundations** | (a) Move `EndpointDrawer` and `LinkedAgentControl` out of `DiscoverPage.tsx` into `components/endpoints/`, keeping behaviour identical. (b) Delete the orphaned `AgentAssetPanel` and `ToolAssetPanel`. (c) **UI role gating parity:** hide write controls from roles that the server rejects, on the Agents list (G1), Tools (T1) and Discover's link control (E3), mirroring `AgentActionsTab`'s `WRITE_ROLES`. **(B-253 decision, 2026-09-29:** approvers can read agents and tools only, with no agent config or connections, so **Agent Detail must show approvers the Overview tab only**, hiding Connections and Actions.**)** | — | No visible change for admins and operators. Viewers stop seeing buttons that only 403, which is more honest. | NEW (small; a UI-hygiene brief). **Status 2026-09-29: (a) and (c) DONE**, including approver Overview-only. See `B-252_C0_VERIFICATION.md`. (c) is implemented through `eami-ui/src/lib/rbac.ts` (per-action rules from B-253), not `WRITE_ROLES`, which B-253 made too coarse. **(b) moved to C1** (founder 2026-09-29): C1 deletes the orphaned panels. |
| **C1 Agent handoff from Assets** — **DONE 2026-09-30** (`B-252_C1_VERIFICATION.md`). Agent Detail now lives at `/assets/agents/:id` with a Classification tab, and the old path redirects. Agent names on Audit, FinOps and Memory link to it. The orphaned asset panels are deleted. | Assets agent rows open Agent Detail. Add a **Classification tab** to Agent Detail, reusing `AssetClassificationPanel`'s logic, admin-only edit, as in Assets today. | C0 | Assets → agent → full detail page, with no dead end. Classification is still editable, now in a tab. Endpoint and tool rows still open the classification panel, so the behaviour is mixed for one step: agents go to a detail page, the others to a panel. **Honesty:** the endpoint and tool panels gain a one-line note: "Full detail page coming — classification only for now." | NEW (or folds into B-196 as "Brief 2a"; founder's call) |
| **C2 Endpoint Detail page** | A new route (Part D, Q4) with the tabs **Overview** (summary grid, §7.6a, plus the 8 detection domains from `latest_report`), **Agent Link** (`LinkedAgentControl`) and **Classification**. **Connections is not rendered** until C6. Licence-off gives the §7.4 pending state. Wire-ups: Assets endpoint rows, Agent Detail's endpoint node, and **Discover rows** all open it. | C0 | Discover still exists, but its drawer is replaced by the page, so there is **one** endpoint detail view. Discover shows a banner: "Endpoint details now open on each endpoint's page. This list is moving into Assets." | NEW (Endpoint Detail; relates to the B-196 extension) |
| **C3 Assets parity for endpoints** | The CMDB assets API returns OS, last seen and per-domain counts for endpoint rows. The Assets endpoint view shows those columns. Server-side OS and has-AI filters. | C2 | Assets becomes a strict superset of the Discover list. Discover is still reachable, with the same banner. | **Folds into B-228** (OS filter) **and B-229** (has_ai / has_local_model); column parity is NEW or folds into B-196 |
| **C4 Retire Discover** | Remove the nav entry. `/discover` redirects to Assets filtered to endpoints. The Dashboard "Endpoints Monitored" metric links there too. | C2, C3 | One endpoint browse (Assets) and one endpoint detail page. Bookmarks keep working. | NEW (small) |
| **C5 Tool Detail page** | A new route with the tabs **Overview/Credentials** (edit fields, credential rotation, **inline Test connection** with a persistent result) and **Classification**. Wire-ups: Tools list rows, Assets tool rows, the Agent Detail tool node, and the Workflows connector "open" link. Tools-list Edit and Test open the page instead of the panel. | C0 | The Tools list stays (Add plus browse) and becomes a launcher into detail pages. **Honesty:** list rows say "open"; the edit slide-over is retired rather than duplicated. | NEW |
| **C6 Connections for Endpoint and Tool** | Endpoint Connections: the asset-perspective graph, reusing `RelationshipGraph` (the B-196 extension). Tool Connections: needs a **new tool-connections API**. Each tab appears only once real. | C2, C5; backend for Tool | New tabs appear when their data is real; the §7.4 honest-data rule is respected. | Endpoint graph: **folds into B-196** (its logged extension). Tool connections: NEW |
| **C7 Agent consolidation completion** | Agent Detail gets a **Credentials tab** (keys bound to this agent: list, create, revoke, raw key shown once) and **edit-in-place** for scope, risk tier and TTL. The scanner "Configure" moves or stays per Part D, Q3. Settings → API Keys narrows to org-wide keys, per Q5. | C1 | Agent credentials live with the agent. **Interim:** Settings → API Keys shows agent-bound keys with a link "Manage on the agent's page" until narrowed. | NEW; step-up on key minting stays **B-231** |
| **C8 Create entry points in Assets** | An "Add" action in the Assets top bar (§6's contextual action), kind-aware: Add agent reuses `AgentsPage`'s create form, Add tool reuses `AddToolPanel`. | C1, C5 | Creation is available from the canonical inventory. The old pages still offer it too, so there is interim duplication for one step. | NEW |
| **C9 Retire the Agents and Tools list pages** *(blocked on Part D, Q1)* | Remove the nav entries. `/gateway/agents` and `/gateway/tools` redirect to Assets filtered by kind. `?highlight=<id>` redirects to the entity's detail page. | C1, C5, C7, C8, **plus the founder's decision** | Assets alone is the browse surface, as §7.7 intends. | NEW |
| **C10 Cross-links from activity pages** | Agent names on Dashboard, Audit, Approvals, FinOps and Memory link to Agent Detail (by `agent_id`, already in every response). Tool names link once the API exposes tool IDs (audit's resolved tool; approvals' `resolved_tool_id`). Paste events link to the endpoint once the API returns it. | C1; C5 plus an API change for tools; C2 plus an API change for pastes | Activity pages become entry points into canonical detail pages. Until the tool and paste API changes land, those names stay plain text; they are not faked as links. | NEW (agent links, small); tool and paste links NEW (API) |
| **C11 Sidebar regroup (Layer 8)** | Collapsible groups (see below), role-based hiding, placeholders. The current rail-collapse (B-200) keeps working. | Can land after C4. Final content after C9. | See "Final sidebar". Before C9, Agents and Tools sit inside GOVERN. | NEW |
| **C12 Write §7.8** | Document the final grouping and placeholder rules in `DESIGN_SYSTEM.md`. | C11 approved | — | Docs only |

### Final sidebar (Layer 8 mechanism, content proposed here)

```
Dashboard                                   (ungrouped, top)

GOVERN
  Assets              ← canonical browse for endpoints, agents and tools (C9)
  Policies
  Guardrails          ⟂ planned (B-150, Horizon 2 item 3)   ← moved here by founder decision
  Workflows
  Approvals  [badge]
  (Agents, Tools)     ← only until C9 (Q1 decided: retire)

OBSERVE
  Audit
  FinOps
  Alerts
  Paste Detection
  Memory
  Observability       ⟂ planned (B-135/B-136)

AI INFRASTRUCTURE
  Nodes
  Models              ⟂ planned (B-151, Horizon 2 item 1)

AGENTIC                                      (every item planned today)
  Automations         ⟂ planned (Horizon 2 item 4, Build/Orchestration; B-155 likely subsumed)
  Chat                ⟂ planned (Horizon 2 item 7, the Chat Engine; canvas Layer 5)

──────────────── (outside the groups, at the bottom, per Layer 8)
My Workspaces        (conditional, as today; it is a mode switch, not a page)
Settings
Profile / account menu
```

**Rules**, from Layer 8 plus the proposals marked:
1. **Force-open the active group.** The group containing the current route is always expanded and cannot be collapsed while you're on it. This includes detail routes: Agent, Endpoint and Tool Detail count as "inside" GOVERN.
2. **Hide empty groups per role.** A group whose every item is hidden for the viewer's role disappears entirely.
   - This needs the missing piece from A.4: **role-based item visibility driven by the server's real role groups** (Part D, Q2).
   - Example: `approver` cannot read agents, tools or CMDB today, so GOVERN would show only Policies (if permitted) and Approvals.
3. **Other groups' expanded or collapsed state persists** per user in `localStorage`. Proposed.
4. **Placeholders** (Observability, Models, Guardrails, Automations, Chat). Proposed per §7.4: muted, with a small "Planned" tag, and they **never count as content**. A group containing only placeholders (AGENTIC today) is **collapsed by default**.
   - Clicking one opens a single honest "Not yet available" page naming the roadmap item and one line on why, with no fake metrics.
   - **Whether to show placeholders at all is a founder decision** (Part D, Q6). The mechanism supports hiding them per role or globally.

---

## Part D — Honest gaps and open questions (founder input)

**Q1. CLAUDE.md vs §7.7: do Agents and Tools stay as pages?** *(Blocks C9 only.)*
- CLAUDE.md's one-spine rule names the "existing sidebar's **six core pages** (Agents, Policies, Tools, Workflows, Audit, FinOps)" as the UI surface for new capabilities.
- `DESIGN_SYSTEM.md` §7.7 says Discover, Agents and Tools "retire as standalone browse destinations".
- These directly conflict. **Options:**
  - (a) Keep Agents and Tools as Gateway-scoped *filtered views* of Assets (no separate data or actions; a view of the same inventory);
  - (b) Retire them and amend CLAUDE.md's six to "Assets, Policies, Workflows, Audit, FinOps" plus the detail pages.
- **Recommendation: (b)**, because it matches §7.7's rationale of one list per entity. Either way, CLAUDE.md or §7.7 needs amending.

**Q2. RBAC: should *configuring* require a stricter role than *viewing*?** This is separate from step-up (B-231).

**Current role checks (server, `router.go`):**

| Capability | Roles today |
|---|---|
| Read agents, tools, CMDB, agent config, connections; read endpoints (plus the Discovery licence) | admin, operator, viewer (**not approver**) |
| Create, update, delete agents; agent scanner config; **endpoint↔agent link**; tool CRUD and test; **API keys (list, create, revoke)**; delete nodes | admin, operator |
| CMDB taxonomy and asset classification | admin only |
| Model pricing | platform_admin |

**UI today:**
- `AgentActionsTab` gates on admin/operator.
- Assets gates classification edits to admin.
- **The Agents list, Tools and Discover's link control have no UI gating** (the server 403s).
- **The sidebar shows everything to every role.**

**Observations:**
- **Operator can mint agent credentials, re-point a credentialed tool's `base_url`, and link endpoints.** These are the three highest-impact configuration writes in the prior investigation's Part C.
- **Approver cannot open any agent, tool or asset**, even though approvers decide approvals *about* those agents and tools. This is likely an unintended gap: the approval card shows an agent name the approver cannot inspect.

**Recommendation (not decided):** split configuration by blast radius, without adding a new role.
- **Identity- and credential-bearing writes → admin only:** agent API keys; tool credential and `base_url` changes; agent reactivate and delete; the endpoint link.
- **Operational writes stay admin + operator:** suspend (protective), test connection, non-secret tool fields, agent scope/risk edits, scanner config.
- **Give `approver` read access** to agents, tools and assets, read-only, so approvals are decided with context.
- Implement as server route groups first; then drive UI and sidebar visibility from one shared role map, closing the A.4 gap.
- **Step-up (B-231) layers on top** of the credential-bearing set; it does not replace the role split.

**Q3. Where does the scanner "Configure" belong?**
- It edits `agent_configs`, which is keyed to the **agent**, but it is consumed by the **linked endpoint(s)**. Several endpoints linked to one agent share it.
- The Layer 7 canvas note puts it on Endpoint Detail. **Options:**
  - (a) Keep it on Agent Detail (it follows the data model), and show it read-only on Endpoint Detail's Agent Link tab ("Scanner settings come from agent X");
  - (b) Move the data model to per-endpoint config (a schema change, a bigger brief).
- **Recommendation: (a) now; revisit (b) only if one agent per endpoint stops being the norm.**

**Q4. Detail route shape.**
- Agent Detail lives at `/gateway/agents/:id`. The new pages could follow the same shape (`/gateway/tools/:id`; endpoints have no gateway prefix, so perhaps `/endpoints/:id`), or all move under `/assets/{kind}/:id` to match the "Assets is the one place" model.
- **Recommendation: `/assets/{endpoints|agents|tools}/:id`**, with `/gateway/agents/:id` redirecting.
- This is a naming decision with bookmark implications, so it's the founder's.

**Q5. Settings → API Keys after C7.**
- **Options:**
  - (a) Keep only org-wide keys (those with no `agent_id`);
  - (b) A read-only org index of all keys with links to their agents;
  - (c) Remove it.
- **Recommendation: (a) plus (b) combined:** manage org-wide keys there, and list agent keys read-only with a "manage on agent" link.

**Q6. Placeholder entries: show them at all?**
- §1 "Restraint beats boldness" argues against nav clutter for things that don't exist; the §7.7 roadmap framing argues for showing direction.
- **Recommendation:** show them to **admins only**, collapsed by default and visually muted, each opening an honest "planned" page. Hide them from other roles.

**Q7. Things I'm unsure about (verification owed before building):**
- **Assets column parity (C3) needs new fields on `GET /v1/cmdb/assets`** (OS, last seen, counts). I have not checked the query cost of computing the per-domain counts across the normalized and raw-report sources at Assets' page sizes.
- **Paste events:** I verified only that the UI type carries no endpoint field. Whether the API response already carries `endpoint_id` needs checking before C10's paste links are scoped.
- **The Workspace mode's asset view** (Layer 4b) is out of scope here. When it's built, it should reuse Endpoint, Agent and Tool Detail read-only, not grow a parallel set.
- **B-218** (tools and nodes have no `workspace_id`) will affect how Tool Detail shows workspace scope; not assessed further here.
- **Layer 7's canvas note** (as originally written) listed "Agents list Configure" among the surfaces merged into Endpoint Detail. That argues **against** Q3 option (a), which keeps Configure on Agent Detail; it leans toward moving the edit to Endpoint Detail. *(Corrected 2026-09-28: an earlier version of this bullet wrongly said the note supported option (a). The founder reports the canvas note itself has since been corrected. Q3 is decided below.)*

---

## Founder decisions (2026-09-28)

These supersede the recommendations above wherever they differ.

**Tracking:** umbrella **B-252** (C0–C12 checklist); RBAC split **B-253**.

- **Q1: retire the Agents and Tools list pages.** CLAUDE.md's "six core pages" wording is amended **in the C9 commit**, not before.
- **Q2: "operators contain; admins expand or destroy."**
  - **Stay admin + operator:** suspend and API-key revoke (containment).
  - **Become admin-only:**
    - agent reactivate and delete;
    - API-key minting;
    - tool credential and `base_url` changes;
    - the endpoint↔agent link;
    - **every create or import path that sets a credential or `base_url`**.
  - **`approver` gets read on agents, tools and assets only.**
  - This is a server-side route change first (B-253); UI gating follows in C0(c).
- **Q3: option (a).** Scanner settings stay edited on Agent Detail. Endpoint Detail shows them **read-only, with "shared by N endpoints"** and a link to edit them on the agent.
- **Q4: `/assets/{endpoints|agents|tools}/:id`.** New detail pages are built there. **Agent Detail moves in C1**, with a redirect from `/gateway/agents/:id`.
- **Q5: (a) + (b).** Settings → API Keys manages org-wide keys and lists agent-bound keys read-only, with a "manage on agent" link.
- **Q6: placeholder nav entries sit behind a feature flag**, on in dev and demo builds, off in customer builds. Rendering follows the proposal above (muted, "Planned", an honest page).
- **Q7:**
  - measure the query cost of the endpoint column parity **in C3's Part A**;
  - check whether the paste-events API returns `endpoint_id` **before C10**;
  - Workspace mode reuses the detail pages read-only.
- **Sequence changes:**
  - **The RBAC brief (B-253) comes first, then C0.**
  - **Agent cross-links (the agent half of C10) ship with C1.**
  - **C3 needs Architect-EAMI contract authorization** (new `GET /v1/cmdb/assets` fields and filters) from the founder **before its brief is written**.
  - **Guardrails sits in GOVERN**, not AI INFRASTRUCTURE (sidebar above updated).
- **Related items minted from `IA_CONSOLIDATION_INVESTIGATION.md` Part D:**
  - **B-254:** semantic-policy honesty;
  - **B-255:** agent edit form, which **folds into C7**;
  - **B-256:** tool auth and test gaps, which **partly folds into C5**;
  - **B-257:** Policies page error handling.

## Corrections to existing records
1. **The brief's "§7.8 defines the placeholder navigation entries" is inaccurate:** no §7.8 exists (see the discrepancy at the top). Recommended remedy: write it after C11 is approved (C12).
2. **`IA_CONSOLIDATION_INVESTIGATION.md` A2** said Agent Detail "has no actions". That is now superseded: the Actions tab shipped in `43ddf95`. Its A1 "Discover search does nothing" is also superseded by B-226/B-227. Both remain accurate as the historical record of that date.
