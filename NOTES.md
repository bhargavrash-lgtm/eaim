# NOTES.md — out-of-scope suggestions logged during task work

Per CLAUDE.md's hard rules: no refactoring outside an assigned task's
scope; suggestions belong here instead of being silently made.

## 2026-07-24 — `toolcreds.go`'s `Decrypt` doc comment is now stale

**File:** `eami-api/internal/toolcreds/toolcreds.go`, `Decrypt`'s doc comment
("Not called from any production HTTP path -- credentials are write-only
from the API's perspective...").

**Why it's stale:** B-023 (`eami-api/internal/api/tool_connectivity.go`)
is now `Decrypt`'s first production caller — `TestTool` decrypts stored
credentials to run a real connectivity check. The comment was accurate
when written (B-022) but not anymore.

**Why not fixed now:** `toolcreds.go` is B-022's frozen file and wasn't in
B-023's `MAY MODIFY` scope (`eami-api/internal/api/tools.go` + a new
connectivity helper file only).

**Suggested fix:** update the comment to describe both callers — B-022's
retrieval-proof tests (`toolcreds_test.go`) and B-023's `TestTool` — or
just soften it to "credentials are write-only from the general API's
perspective; the one exception is TestTool's own connectivity check,
which decrypts to attempt a real connection and never returns or logs
the result."

## 2026-08-19 — `api/openapi.yaml` doesn't document `POST /v1/gateway/agents`'s new 409

**File:** `api/openapi.yaml`, the `POST /v1/gateway/agents` path's
`responses:` block (currently only documents `201`).

**Why it's stale:** B-074 added a real, live `409 conflict` response
(`eami-api/internal/api/agents.go`'s `CreateAgent`) when a caller submits
a duplicate `(org_id, name)` — `gateway_agents`' pre-existing `UNIQUE
(org_id, name)` constraint now surfaces cleanly instead of a 500. Every
other handler that returns 409 in this codebase documents it in the spec
(e.g. the rules endpoint's "Rule name already exists" at ~line 1804) —
this is the one new exception, found by this session's own mandatory
code-review pass.

**Why not fixed now:** `api/openapi.yaml` is Architect-EAMI-owned per
`BOUNDARIES.md` ("Only Architect writes it" — any change requires
Architect to update the file AND notify FE-Dashboard to regenerate the
client). B-074's `MAY MODIFY` scope was the agent-creation error handling
itself, not the contract file. Matches this repo's own B-045 precedent
(a new response shipped undocumented in the spec, flagged rather than
silently edited by the wrong role).

**Suggested fix:** Architect-EAMI adds a `409` response entry to `POST
/v1/gateway/agents` in `api/openapi.yaml` (schema: the existing
`ErrorResponse`/equivalent shape, `code: "conflict"`), then notifies
FE-Dashboard to regenerate `eami-ui/src/api/schema.ts` so the typed
client knows about it.

## 2026-09-26 — B-196 Brief 1 fix-up: L-3 residuals and an ungated endpoint write (not fixed; out of scope)

**L-3 residuals.** `cmdb.go`'s `normalizeCMDBName` now trims with Go's Unicode-aware `TrimSpace`, but only on the API path. Three residual cases remain:
- The `normalize_ci_name` trigger (migration 000024) still uses ASCII-only `btrim`. A writer that bypasses the API (direct SQL, a future importer) can still store `"Connector\t"` and get normalized name `"connector "`.
- Zero-width characters such as U+200B are not whitespace to Go or Postgres. `"Connector​"` is therefore not treated as a duplicate of `"Connector"`.
- Whether the trigger's internal `regexp_replace('\s+', ' ')` collapses NBSP depends on the database locale.

The live DB has 0 rows with trailing tab, NBSP or newline (security review, 2026-09-26). **Suggested fix:** a new migration that makes `normalize_ci_name` trim and collapse Unicode whitespace, and strips zero-width format characters, before lowercasing. Then renormalize existing rows with a conflict check.

**`PATCH /v1/endpoints/{endpointId}/link-agent` (`LinkEndpointAgent`) has no Discovery-license gate.** It is admin-only and stays within one org. The B-196 fix-up gated only `SetCMDBAssetClassification`; this route was noted in both B-196 security reviews as pre-existing precedent. **Suggested fix:** the same `discoveryLicensed` check and 403 `module_not_licensed`, when a brief covers the endpoint-link surface.

## 2026-09-28 — `AgentConfigPanel.tsx`: two pre-existing UI-rule deviations (fix opportunistically, no B-ID)

**File:** `eami-ui/src/components/agents/AgentConfigPanel.tsx`

Found by the B-236 reviews. They predate B-236 and sit outside its diff. At founder direction (2026-09-28) there is **no separate B-ID**: fix them the next time this file is touched for any reason.

1. **Page-local success toast.** The `toast` state is set with `setToast('Config saved')` plus `setTimeout(..., 3000)` and rendered in a green box. CLAUDE.md's B-182 rule requires the shared `useToast()`: `showToast('Config saved', { type: 'success', durationMs: 3000 })`.
2. **Raw Tailwind red on the save-error box.** `saveError` renders with `bg-red-50 border-red-200 text-red-700`. Use the design-system Danger tokens that B-236's load-error panel in the same file already uses: `bg-status-danger` / `text-status-danger-text`, `DESIGN_SYSTEM.md` §2. Alternatively, route the save error through `useToast()` with `type: 'error'` as well.

## 2026-09-28 — B-254 code-review follow-ups (not fixed; out of scope)

- **Conditions column truncation is inconsistent** (`components/policies/PolicyBadges.tsx` `ConditionSummary`).
  - `truncate max-w-xs` on a plain inline `<span>` does nothing: inline elements ignore `max-width`, so non-semantic rows never truncate.
  - Inside B-254's `inline-flex` wrapper the same span *does* truncate. So semantic-rule rows cap at about 20rem while others widen the `whitespace-nowrap` cell.
  - Fix: make the summary `inline-block` (or `block`) in both branches.
- **"Never fires" uses the same warning pill colour as the `Escalate` ActionBadge.** A row can show two identical amber pills meaning different things. A product call (danger colour, or a distinct icon) for whoever next touches the badges.
- **`PolicyPanel.tsx`: the semantic-rule `<label>` has no `htmlFor`/`id` pairing** with its textarea. This existed before; B-254 added `aria-describedby` for the note only.
- **`eami-policy/semantic.go:15`'s comment says the stub "always returns ESCALATE".** Wrong: it returns `(false, nil)`, the rule is skipped, and the evaluator's default applies. Fix the comment with B-007 or B-258.

## 2026-09-29 — B-253 follow-ups (not fixed; out of scope)

- **`api/openapi.yaml` (Architect-EAMI, not edited): no per-route role requirements are documented at all.**
  - B-253 made key minting, agent create and delete, the endpoint link, tool create and delete, and node delete admin-only.
  - It added field-level admin-only rules on `PATCH /v1/gateway/agents/{id}` and `PATCH /v1/gateway/tools/{id}`.
  - It gave `approver` read access to `GET /v1/gateway/agents`, `/agents/{id}` and `/tools`.
  - The spec should document the required role(s) and the 403 response per route.
- **`eami-ui/src/pages/gateway/ToolsPage.tsx` edit panel drops `input_schema` from action paths on save** (rows are built as `{action, path, method}`). An admin's save silently strips OpenAPI-discovered input schemas. It is pre-existing data loss. It also makes an operator's no-op REST save look "changed" (403), which C0(c) will hide.
- **`UpdateAgent` doesn't validate `status`:** only the DB CHECK does, so a bad value gives a 500 echoing the constraint text (B-234 class). It also role-checks before validation, while `UpdateTool` validates first. Minor inconsistency.

## 2026-09-29 — B-252 C0 review follow-ups (not fixed; out of scope)

- **`eami-ui/src/stores/authStore.ts:9`: `User.role` is typed `'admin' | 'operator' | 'viewer'`.** Real values also include `approver` (and `platform_admin`).
  - It's harmless today, because `lib/rbac.ts` types the role as `string`.
  - Widen the union so future code can't narrow on a wrong one (code review CR-3, security review SR-3).
- **`ToolsPage.tsx` `EditToolPanel`: clicking the backdrop closes the panel mid-save**, even though Cancel is disabled. It's the same behaviour C0 fixed in its own `EditToolNotePanel` (CR-1).
- **The operator note panel is last-write-wins:** a stale panel can overwrite an admin's newer note. Only the note is affected. Add optimistic concurrency if it ever matters (SR-2).

## 2026-09-29 — Agent Lineage review follow-ups (not fixed; out of scope)

- **`store/agent_connections.sql.go` `listAgentPolicyConnections` joins `policies p ON p.id = a.policy_id` with no `p.org_id = a.org_id` match.**
  - `audit_log.policy_id` has no org-matched FK, so a mis-attributed `policy_id` would surface another org's policy name in `/connections`.
  - Lineage's equivalent query has the match, pinned by mutation M14.
  - This is defence in depth; fix it the same way (security review SR-4).
- **Per-tool cost is keyed by `tool_name`.** A deleted and re-created ai_provider connector with the same name inherits the old usage cost. It stays within one org (SR-5).
- **`schema/schema.sql`'s `gateway_tools.type` CHECK doesn't list `ai_provider`.** The migrations do, so the reference schema has drifted (SR-6).
- **There is no general per-route rate limit on authenticated read routes.** Only the audit export has one (`auditExportLimiter`).
  - Lineage runs 6 queries concurrently per request.
  - B-265's index is the main fix; a per-org limiter is optional (SR-1/SR-7).
- **The gateway records a dispatch failure (upstream error, SSRF-guard refusal) as `denied` with no `policy_id`,** the same word as a policy denial (B-121's vocabulary).
  - Lineage and Audit can't tell them apart except by `policy_id` being NULL.
  - Separating them needs new audit vocabulary. That is a founder decision, not a UI fix.
