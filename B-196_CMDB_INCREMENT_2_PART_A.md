# B-196 CMDB Completion — Increment 2, Part A

**Status:** investigation and implementation planning complete; awaiting founder scope approval. No application code, migration, API contract, or UI was changed.

**Roadmap mapping:** Horizon 1, **CMDB completion**. B-217 delivered the first asset-inventory increment. This proposed increment adds tenant-configurable classification and the first endpoint-centered relationship view while preserving the existing Identity → Action → Tool → Policy → Cost/Audit spine.

## 1. Verified current state

### Stored asset and CI-like records

| Record | Authority and writer | API/UI | Current local data (2026-09-25) | CMDB status |
|---|---|---|---:|---|
| Discovered endpoint | `endpoints`; collector batch ingest upserts it (`eami-api/internal/api/ingest.go:95-144`, `:199-204`) and paste-event ingestion can perform a non-clobbering upsert | `GET /v1/endpoints`, `GET /v1/endpoints/{id}` (`eami-api/internal/api/discover.go:57-141`); `useEndpoints`; `AssetsPage`; `EndpointDrawer` | 5 | Real top-level asset with a production writer |
| Governed agent | `gateway_agents` (`schema/schema.sql:149-166`); admin/operator CRUD (`eami-api/internal/api/router.go:319-340`) | `GET /v1/gateway/agents`; `useAgents`; `AssetsPage`; Agent detail | 12 | Real top-level asset with an administrative writer |
| Gateway tool / connector | `gateway_tools` (`schema/schema.sql:213-236`); admin/operator CRUD (`eami-api/internal/api/router.go:319-340`) | `GET /v1/gateway/tools`; `useTools`; `AssetsPage`; tool panel | 4 | Real top-level asset with an administrative writer |
| Gateway node | `gateway_nodes` (`schema/schema.sql:241-265`); list and delete exist, but no create/upsert/heartbeat writer exists in current application code | `GET /v1/gateway/nodes`; Nodes UI, absent from `AssetsPage` | 0 | Dormant inventory; do not add to CMDB yet |
| HTTP-observed endpoint | `discovered_endpoints` (`schema/schema.sql:559-582`); service-key `POST /v1/reports` can write it (`eami-api/internal/api/reports.go:39-113`) | `/v1/discover/endpoints*`; separate Discover surface | 0 | Write-capable but no real producer found; distinct from endpoint agents and excluded |

The endpoint ingest path keeps the complete original report JSON in `endpoint_reports.report`, while parsing only selected fields into normalized child tables (`eami-api/internal/api/ingest.go:29-59`, `:142-147`). The normalized tables are:

- `endpoint_ai_apps` (`schema/schema.sql:110-119`) — 3 current rows.
- `endpoint_model_files` (`schema/schema.sql:121-132`) — 0 current rows.
- `endpoint_mcp_servers` (`schema/schema.sql:134-144`) — 0 current rows.

They are latest-snapshot children, not durable independent assets: every scan deletes the endpoint's prior normalized rows and reinserts the new report's rows (`eami-api/internal/store/endpoints.sql.go:322-330`, `:343-396`). Their UUIDs can therefore change after every scan.

The agent actually emits ten wired detection domains: local models, cloud clients, network activity, AI processes, AI apps, MCP servers, GPUs, Python environments, Node projects, and browser extensions (`eami-agent/internal/payload/builder.go:32-50`, `:92-174`). Only AI apps, local models, and MCP servers become normalized rows. Cloud clients, network activity, AI processes, GPUs, Python environments, Node projects, and browser extensions are available only through the latest raw `endpoint_reports.report` JSON. `EndpointDrawer` reads those raw arrays directly (`eami-ui/src/pages/discover/DiscoverPage.tsx:119-305`). On the current live stack only `ai_apps` is populated among those latest-report arrays (3 items); the other queried arrays contain zero current items.

### Existing classification

`AssetsPage` owns a closed TypeScript union, three hardcoded labels, and four hardcoded filter tabs (`eami-ui/src/pages/cmdb/AssetsPage.tsx:39-65`). It maps every endpoint to `End-user compute`, every gateway agent to `AI Agent`, and every tool to `Connector` (`:97-136`). These strings are presentation labels only: there is no classification table, no asset-level classification field, no category hierarchy, and no administration API. The page client-merges three list endpoints and caps endpoints at 200, surfacing truncation but offering no way to page the complete inventory (`:71-95`, `:203-206`).

### Existing relationships

Direct, durable relationships that are safe to display:

- Endpoint → normalized AI app/local model/MCP server through child-table `endpoint_id` foreign keys (`schema/schema.sql:110-144`).
- Endpoint → explicit gateway agent through nullable `endpoints.gateway_agent_id`; the only write path is the admin/operator link endpoint, and no automatic identity inference occurs (`eami-api/internal/api/discover.go:242-308`; `schema/migrations-v2/000013_endpoint_gateway_agent_link.up.sql:9-23`).
- Endpoint and gateway agent → workspace through their nullable `workspace_id` foreign keys, guarded against cross-org assignment by triggers (`schema/migrations-v2/000021_groups_workspaces.up.sql:100-172`).
- Workflow run → agent and workflow; workflow step → gateway tool through foreign keys (`schema/migrations-v2/000006_workflows.up.sql:32-52`, `000007_workflow_execution.up.sql:36-66`).

Relationships supported by reliable runtime history, which must be labeled **observed** rather than structural:

- Agent → tool from `audit_log.tool_name`, with an optional name-based join back to a current tool (`eami-api/internal/store/agent_connections.sql.go:17-47`). Rename/delete can leave a historical dangling node.
- Agent → policy from audit decisions whose `policy_id` actually matched (`:68-108`). This is “policy observed applying,” not assignment.
- Agent → workflow from real `workflow_runs` history (`:111-144`). This proves participation in a run, not permanent membership.

Unsupported edges that must remain absent include raw cloud-client → provider/model identity, network destination → managed tool, detected MCP server → configured gateway tool, and gateway node → agent/tool. Names and URLs are not sufficient identity evidence.

### Stale or conflicting documentation/code assumptions

1. B-196's heading and final status still say “investigation not started” / “zero investigation,” although B-217 already completed Increment 1 and this Part A now completes Increment 2 investigation.
2. B-196's older extension text says normalized AI apps/MCP servers/local models were populated. The current live database has 3 AI apps but 0 MCP servers and 0 model files. The tables and writers are real; “currently populated” is stale.
3. B-200's store comment says the endpoint-agent link is at most one endpoint per agent (`agent_connections.sql.go:147-162`). Migration 000013 adds a plain non-unique index, not a uniqueness constraint (`000013_endpoint_gateway_agent_link.up.sql:17-24`). Multiple endpoints may therefore reference the same agent, while `GetAgentEndpointConnection` silently returns an arbitrary one via `LIMIT 1`. Increment 2 should not repeat this singular assumption. The endpoint-centered direction is unaffected because one endpoint row contains at most one `gateway_agent_id`.
4. `api/openapi.yaml` does not describe B-217's workspace fields or B-200's connections route (`eami-ui/src/hooks/useAgents.ts:9-17`, `:50-58`). New CMDB endpoints would compound this drift unless Architect-EAMI updates the contract first.

## 2. Recommended smallest coherent Increment 2

Deliver two sequential build briefs under B-196 Increment 2:

1. **Classification foundation and AssetsPage classification UI.** Add reusable org-scoped category/type definitions, assign one resolved type to each of the three current top-level asset kinds, and replace the client-merged/capped list with one server-paginated CMDB read model.
2. **Endpoint-centered relationship view.** Add a truthful connections endpoint using normalized child rows plus the explicit gateway-agent link, then generalize B-200's visual mechanism to render that endpoint graph on a full-width detail route.

This is the smallest increment that covers both requested capabilities without coupling a security-sensitive schema/RBAC change to a graph refactor in one review unit. It remains one product increment but should be two implementation briefs and commits. The Architect-owned OpenAPI change should precede generated-client/frontend work for each brief.

This increment deliberately remains an asset inventory plus a focused relationship view. It does **not** claim full CMDB completion or true arbitrary multi-hop traversal. A direct endpoint graph is defensible now; semantic app → service → model → host edges are not.

## 3. Proposed classification model

Classification belongs on each authoritative asset row, backed by reusable definitions:

```text
ci_categories
  id, org_id, name, description, sort_order, created_at, updated_at
  UNIQUE(org_id, normalized_name)

ci_types
  id, org_id, category_id, asset_kind, name, description,
  is_default, created_at, updated_at
  asset_kind IN ('endpoint','agent','tool')
  UNIQUE(org_id, normalized_name)
  one default per (org_id, asset_kind)

endpoints.ci_type_id         NULLABLE FK -> ci_types
gateway_agents.ci_type_id    NULLABLE FK -> ci_types
gateway_tools.ci_type_id     NULLABLE FK -> ci_types
```

`NULL` means “use this org's default for the row's asset kind.” That preserves all existing and future writers without adding a fragile dynamic database default. Reads always return the resolved type plus `classification_source: default|explicit`. A cross-table trigger, following `check_workspace_org_match`, must reject a type from another org or a type whose `asset_kind` does not match the table being classified.

Migration seeds these defaults for every existing org and an org-insert trigger/function seeds them for future orgs:

| Asset kind | Default category | Default type | Existing display migrated from |
|---|---|---|---|
| endpoint | End-user compute | Endpoint | End-user compute |
| agent | AI systems | Governed agent | AI Agent |
| tool | Integrations | Connector | Connector |

Admins may add categories and types without a migration. `asset_kind` is immutable after creation. Each asset has exactly one resolved type in this increment; multi-label tags are a separate concern and should not be smuggled into classification.

Deletion behavior:

- Category deletion is `RESTRICT` while types exist.
- Type deletion is `RESTRICT` while explicitly assigned or while it is the default.
- Replacing a default is a transaction that sets the new default before releasing the old one.
- Deleting an asset naturally removes the assignment because the assignment is its own column, avoiding an un-FK-able polymorphic mapping table and orphan cleanup.

This avoids a canonical duplicate `configuration_items` identity table. The existing endpoint/agent/tool rows remain authoritative, so the design stays on the one spine and does not introduce a parallel identity or audit system.

## 4. Proposed relationships

The first supported center should be **endpoint** because it has the strongest current evidence: three normalized child tables with real foreign keys and one explicit gateway-agent foreign key. It also satisfies the original asset-perspective request without inventing tool/node relationships.

Add `GET /v1/endpoints/{endpointId}/connections`, returning:

```text
center: endpoint summary
relationships:
  - type: contains_ai_app       evidence: direct   targets: normalized AI-app rows
  - type: contains_local_model  evidence: direct   targets: normalized model-file rows
  - type: contains_mcp_server   evidence: direct   targets: normalized MCP-server rows
  - type: linked_to_agent       evidence: explicit targets: zero or one gateway agent
```

Every target includes kind, current row ID, display label, relevant subtitle, and `observed_at`/report timestamp where available. The response must not include raw cloud clients or other raw-only fields in v1: they have no durable row identity and no queryable foreign-key relationship. Since normalized child rows are replaced per scan, their IDs are response-local node identifiers and must not become durable deep links.

`RelationshipGraph.tsx` is currently coupled to `AgentConnections` and four hardcoded junctions (`eami-ui/src/pages/gateway/RelationshipGraph.tsx:20-48`, `:96-172`). Reuse its SVG bezier, layout, pan/zoom, selection, and reset mechanisms by extracting a generic focused-graph input (`center`, labeled relationship groups, target nodes). Keep the existing agent adapter so B-200 behavior remains unchanged; add an endpoint adapter for the new response. Node clicks continue to open `SlideOverPanel`.

The endpoint graph should live on a full-width Admin-mode route such as `/assets/endpoints/{id}`. B-200 already proved the 480px drawer is too narrow for the graph. `AssetsPage` can continue using `EndpointDrawer` for quick inspection and add a real “View relationships” action to the full page.

Observed second-hop agent relationships can be added later with explicit provenance. They should not be included in the first relationship brief merely to claim “multi-hop”: tool/policy/workflow links express historical behavior, and the current graph/layout is one-hop. Arbitrary first-class relationship persistence, reconciliation, lifecycle state, and semantic app→model→host traversal remain later B-196 increments.

## 5. Workspace behavior

- Classification definitions are org-scoped, not workspace-scoped. A category name should mean the same thing across the tenant.
- Asset classification remains on the underlying shared asset row. Moving an endpoint/agent between workspaces does not clone or change its classification.
- Increment 2's `AssetsPage` and endpoint relationship detail are **Admin mode**, org-wide, matching the current `/assets` surface.
- No Workspace-mode Assets page or workspace relationship route is added in this increment. When that surface is built, it must use `requireWorkspaceRole` and filter the center asset by its real `workspace_id`.
- B-218 is not required for coherence. Tools remain honestly labeled “Not workspace-scoped.” The endpoint graph does not expose unrelated org tools. If observed second-hop tools are later exposed inside Workspace mode, B-218 must be resolved or the UI must continue to state that those tools have no workspace assignment.

## 6. API and authorization

Proposed classification surface:

- `GET /v1/cmdb/classifications` — categories, types, and counts; admin/operator/viewer.
- `POST /v1/cmdb/categories` — admin only.
- `PATCH /v1/cmdb/categories/{categoryId}` — admin only.
- `DELETE /v1/cmdb/categories/{categoryId}` — admin only.
- `POST /v1/cmdb/types` — admin only.
- `PATCH /v1/cmdb/types/{typeId}` — admin only.
- `DELETE /v1/cmdb/types/{typeId}` — admin only.
- `PATCH /v1/cmdb/assets/{assetKind}/{assetId}/classification` — admin only; body `{ "ci_type_id": <uuid|null> }`, where null restores the default.
- `GET /v1/cmdb/assets` — server-paginated/filterable org inventory; admin/operator/viewer. Filters: `kind`, `category_id`, `type_id`, `workspace_id`, `q`, `page`, `per_page`.
- `GET /v1/endpoints/{endpointId}/connections` — admin/operator/viewer plus Discovery entitlement, matching endpoint reads.

“Admin-configurable” is applied literally: only org admins alter taxonomy or assignments. Operators and viewers retain read access. Workspace admins do not administer org-wide taxonomy through workspace RBAC.

All reads and writes derive `org_id` from JWT claims. Category/type IDs, asset IDs, and workspace filters must be joined back to the same org. Cross-org IDs return 404/validation failure without disclosing existence. `assetKind` is an allow-listed dispatch key, never interpolated into SQL. Classification mutations should write through the platform's existing administrative event/audit convention chosen during implementation; they must not create a separate CMDB audit trail.

`api/openapi.yaml` is Architect-EAMI-owned (`BOUNDARIES.md:32-40`, `:328-334`). The Architect must add the schemas/routes and regenerate the client before frontend work. B-217's workspace response fields and B-200's agent-connections route should be aligned in the same contract pass if ownership permits, but that correction is not a reason to broaden application scope.

## 7. UI shape and states

Both proposed surfaces are Admin mode.

`AssetsPage` keeps `AppTopBar` and `DataTable`, replacing the tab strip with DESIGN_SYSTEM §7.2's left classification panel. Category/type counts come from the server. The selected classification controls the heading, a category-scoped search field, and the paginated table query. A top-bar “Manage classifications” action opens a `SlideOverPanel` for category/type CRUD. Per-row classification editing uses the same panel or a focused asset panel.

All mutation buttons use `Button.isLoading`; Cancel is disabled during mutation; success/error feedback uses `useToast`. Loading shows existing spinners/skeleton behavior, fetch errors preserve the selected filter and offer retry, and empty states distinguish “no assets in this classification” from “no assets exist.” Server pagination replaces the 200-endpoint truncation. Category counts and table totals must come from the same applied filters so the UI never presents stale or partial counts as complete.

The endpoint detail route uses `AppTopBar`, a compact metadata grid, the generic focused `RelationshipGraph`, and `SlideOverPanel` node details. Empty relationship groups are omitted. A fully empty graph states that no normalized relationships were found in the latest report. Large results retain B-205's 700px viewport, pan/zoom, reset, and click-vs-drag behavior; the API should cap each child collection with total/truncated metadata rather than sending an unbounded graph.

## 8. Security, tenancy, migration, and compatibility

Required adversarial guarantees:

- Cross-org category/type IDs cannot be read, mutated, assigned, counted, or used as filters.
- Cross-org asset IDs cannot be classified or queried for relationships.
- A type for one asset kind cannot be assigned to another kind.
- Viewer/operator taxonomy writes and workspace-admin org-taxonomy writes fail closed.
- Discovery licensing still gates endpoint inventory and endpoint relationships.
- A workspace filter never broadens access; in this Admin-only increment it is an org-wide filter, not authorization.

Migration risks:

- Existing rows resolve to seeded defaults without destructive rewrites because `ci_type_id NULL` means default.
- Seed creation must be deterministic and idempotent for every existing and future org.
- Renaming a category/type changes display everywhere immediately; API responses should carry IDs and names, while saved filters use IDs.
- Normalized endpoint child IDs churn after scans. UI state must tolerate a selected node disappearing after refetch.
- The new unified asset endpoint changes `AssetsPage` data flow but should leave existing agents/endpoints/tools APIs and consumers intact.
- No B-218 columns are added. No raw report JSON is rewritten.

## 9. Test and live-verification strategy

Classification brief:

1. Real-Postgres migration tests for default seeding on existing/new orgs, uniqueness, cross-org/type-kind trigger rejection, deletion restrictions, and reset-to-default behavior. Use the repository's `t.Cleanup`-only pool lifecycle.
2. HTTP tests for all role tiers, cross-org IDs, asset-kind allow-listing, server pagination/filter/count consistency, and classification CRUD/assignment.
3. Frontend `npx tsc --noEmit` and `npx vite build`.
4. Playwright against the rebuilt shared stack: create category/type, assign one endpoint/agent/tool, filter/search/page, verify toast/loading behavior, verify a blocked delete, reset to default, and prove a second org's taxonomy/assets never appear.

Relationship brief:

1. Real-Postgres HTTP tests proving only the requested org's endpoint and child rows return; include a foreign endpoint ID and deliberately similar child names in two orgs.
2. Prove raw-only cloud clients are absent, deleted/replaced child rows disappear, and a cross-org linked-agent corruption is rejected by existing invariants.
3. Component/regression verification that the existing agent graph still renders identically after generic extraction.
4. Playwright with a seeded endpoint containing multiple AI apps, model files, MCP servers, and an explicit linked agent; verify node panels, empty state, truncation metadata, pan/zoom/reset, and no unsupported edge labels.
5. Rebuild/restart the real API/UI containers before claiming shared-stack completion; B-200's disposable-instance deployment gap must not recur.

## 10. Ordered implementation plan and acceptance criteria

### Brief 1 — classification foundation

1. Architect updates OpenAPI for classification and unified asset-list routes.
2. Add migration for `ci_categories`, `ci_types`, nullable `ci_type_id` columns, default seeding, indexes, and tenant/type-kind enforcement.
3. Add org-scoped store/API handlers and role routing.
4. Add the unified paginated assets read model.
5. Rebuild `AssetsPage` around the classification panel, server search/filter/pagination, and admin CRUD/assignment panels.
6. Run adversarial tests, mandatory code/security review, real build, shared-stack deployment, and live browser acceptance.

Acceptance: an admin can create reusable types and classify endpoint/agent/tool rows; existing assets resolve to defaults; operators/viewers can read but cannot mutate; no cross-org assignment/read succeeds; all assets can be paged without the current 200-row ceiling; B-218 remains honestly visible.

### Brief 2 — endpoint-centered relationships

1. Architect adds the endpoint connections contract.
2. Add the org-scoped, entitlement-gated endpoint connections query/handler with bounded collections and provenance.
3. Extract generic graph mechanics while retaining an adapter for B-200's existing agent graph.
4. Add the full-width endpoint relationship route and node `SlideOverPanel` details; link it from the existing endpoint asset flow.
5. Run real-Postgres isolation tests, graph regression checks, frontend builds, mandatory reviews, shared-stack deployment, and Playwright acceptance.

Acceptance: a requested endpoint renders only real normalized AI-app/model/MCP children and its explicit agent link; unsupported/raw-only relationships are absent; empty/large/error states are honest; cross-org IDs do not disclose data; the existing agent graph and pan/zoom behavior do not regress.

## 11. Expected files

Classification brief: new `schema/migrations-v2/000024_*` up/down files; Architect-owned `api/openapi.yaml`; new `eami-api/internal/api/cmdb*.go`, store/query files, router wiring, and real-Postgres tests; `eami-ui/src/pages/cmdb/AssetsPage.tsx`; new CMDB hooks/components; generated API schema; `BUILT.md`, `BACKLOG.md`, and `CONTEXT.md`.

Relationship brief: Architect-owned `api/openapi.yaml`; `eami-api/internal/api/discover.go` or a focused `endpoint_connections.go`; store/query/test files; `eami-ui/src/pages/gateway/RelationshipGraph.tsx` (generic extraction or a shared replacement), new endpoint asset detail page/panels, CMDB routing/hooks; `BUILT.md`, `BACKLOG.md`, and `CONTEXT.md`.

No `gateway_tools.workspace_id`, `gateway_nodes.workspace_id`, CI lifecycle/reconciliation tables, arbitrary relationship table, raw-report normalization expansion, new sidebar section, or separate audit mechanism belongs in Increment 2.
