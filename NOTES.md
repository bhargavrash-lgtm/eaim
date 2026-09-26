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
