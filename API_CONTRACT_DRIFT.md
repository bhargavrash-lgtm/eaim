# API contract drift: `api/openapi.yaml` vs the real API

**Owner of the fix:** Architect-EAMI (`api/openapi.yaml` is theirs per `BOUNDARIES.md`). Code records drift here; Code doesn't edit the contract.
**Hand-off rule (master sequence):** this list goes to Architect-EAMI **before item 8 starts**.
**Consolidated:** 2026-10-05 by Claude Code, from NOTES.md, BACKLOG.md and every `*_VERIFICATION.md`. This is now the single drift log: new drift is added here, not to NOTES.md (`API_CONVENTION.md` §7, §9).
**Method:**
- §A and §B were **generated** by diffing every `r.Get/Post/Put/Patch/Delete` route in `eami-api/internal/api/router.go` against the `paths:` operations in `api/openapi.yaml` (path parameters normalised).
- §C entries were each **re-checked against the current `openapi.yaml`** on 2026-10-05. Entries already fixed in the spec are dropped, not carried.
- **Order:** §A comes first because a generated client calling those operations fails outright (404 or 405), which is worse than a missing entry.
- Routes served by `eami-gateway` (`/v1/gateway/tokens`, `/v1/gateway/tokens/{jti}/revoke`, `/v1/mcp/sse`, `/v1/mcp/messages`) are documented in the same spec and are not drift.

**Counts:** 118 eami-api routes vs 71 documented operations.
- §A: 3 operations are documented but don't exist as documented.
- §B: **54 routes are served but undocumented** (53 plus `/health`).
- §C: 23 field, parameter or response mismatches (C18–C20 added by B-293, C21–C23 by B-269 Slice 0; C18, C21 and C22 sit on routes that are themselves undocumented, §B).

---

## A. Documented operations that don't exist as documented (3) — fix or remove first

| # | Spec says | Reality | Note |
|---|---|---|---|
| A1 | `GET /v1/gateway/tools/{toolId}` | No such route in eami-api | B-252 C5 (Tool Detail) needs a single-tool read. Decide whether to build it or drop it from the spec. |
| A2 | `GET /v1/gateway/nodes/{nodeId}` | No such route (only list and delete) | `gateway_nodes` has no real writer (B-080). |
| A3 | `POST /v1/memory/episodes/search` | Served as **`GET`** (and the `/v1/gateway/episodes/search` alias) | Method mismatch. A generated-client call would 405. |

## B. Routes served by eami-api but absent from `openapi.yaml` (54)

| Area | # | Routes |
|---|---|---|
| Infrastructure | 1 | `GET /health` |
| Model pricing (FinOps) | 4 | `GET /v1/admin/model-pricing`<br>`POST /v1/admin/model-pricing`<br>`DELETE /v1/admin/model-pricing/{model}`<br>`PATCH /v1/admin/model-pricing/{model}` |
| Agent config (B-271) | 3 | `GET /v1/agents/{agent_id}/config`<br>`GET /v1/gateway/agents/{agentId}/config`<br>`PUT /v1/gateway/agents/{agentId}/config` |
| Audit | 1 | `GET /v1/audit/verify` |
| Auth: invite, reset (B-212) | 3 | `POST /v1/auth/accept-invite`<br>`POST /v1/auth/request-reset`<br>`POST /v1/auth/reset-password` |
| Agent Detail reads (B-200, Agent Lineage) | 2 | `GET /v1/gateway/agents/{agentId}/connections`<br>`GET /v1/gateway/agents/{agentId}/lineage` |
| Memory / episodes | 4 | `GET /v1/gateway/episodes`<br>`GET /v1/gateway/episodes/search`<br>`GET /v1/gateway/episodes/{episodeId}`<br>`GET /v1/memory/episodes/search` |
| Tools | 2 | `POST /v1/gateway/openapi/discover`<br>`PATCH /v1/gateway/tools/{toolId}` |
| Policies | 1 | `PUT /v1/gateway/policies/reorder` |
| Workflows | 7 | `GET /v1/gateway/workflow-steps/{stepId}/params`<br>`PUT /v1/gateway/workflow-steps/{stepId}/params`<br>`GET /v1/gateway/workflows`<br>`POST /v1/gateway/workflows`<br>`DELETE /v1/gateway/workflows/{workflowId}`<br>`GET /v1/gateway/workflows/{workflowId}`<br>`PATCH /v1/gateway/workflows/{workflowId}` |
| Internal ingest (service key) | 1 | `POST /v1/internal/token-usage` |
| Paste events (B-033) | 2 | `GET /v1/paste-events`<br>`GET /v1/paste-events/timeseries` |
| Licence | 2 | `GET /v1/settings/license`<br>`POST /v1/settings/license` |
| Setup / bootstrap | 3 | `POST /v1/setup/bootstrap`<br>`GET /v1/setup/status`<br>`POST /v1/setup/token/validate` |
| Users: self-profile, reset link (B-212) | 4 | `GET /v1/users/me`<br>`PATCH /v1/users/me`<br>`POST /v1/users/me/change-password`<br>`POST /v1/users/{userId}/reset-link` |
| Workspaces (B-214/B-215/B-216) | 14 | `GET /v1/workspaces`<br>`POST /v1/workspaces`<br>`GET /v1/workspaces/mine`<br>`DELETE /v1/workspaces/{workspaceId}`<br>`GET /v1/workspaces/{workspaceId}`<br>`PATCH /v1/workspaces/{workspaceId}`<br>`GET /v1/workspaces/{workspaceId}/members`<br>`POST /v1/workspaces/{workspaceId}/members`<br>`DELETE /v1/workspaces/{workspaceId}/members/{userId}`<br>`PATCH /v1/workspaces/{workspaceId}/members/{userId}`<br>`GET /v1/workspaces/{workspaceId}/policies`<br>`POST /v1/workspaces/{workspaceId}/policies`<br>`DELETE /v1/workspaces/{workspaceId}/policies/{policyId}`<br>`PATCH /v1/workspaces/{workspaceId}/policies/{policyId}` |

Each group's origin, where one is recorded:
- **Agent config:** B-271 noted "no schema for agent config".
- **Lineage:** `AGENT_LINEAGE_VERIFICATION.md` §2.
- **Paste events GETs:** open item B-033. Its `POST /v1/reports/paste-events` part is moot: that route was removed 2026-09-19 (`router.go:224`).
- **Workspaces:** B-214/B-215/B-216 "known limitations".
- **Auth and users:** B-212.
- **Model pricing:** the B-106-class note in BACKLOG.
- **Reorder:** the `PUT` alias is undocumented; `POST` is documented (see C10).
- **`GET /v1/memory/episodes/search`:** see A3.

## C. Field, parameter and response mismatches on documented routes (23)

| # | Route / schema | Drift | Source | Tracked |
|---|---|---|---|---|
| C1 | `GET /v1/cmdb/assets`, `CMDBAsset` | Missing endpoint-only nullable fields: `os`, `last_seen`, `ai_app_count`, `local_model_count`, `mcp_server_count`, `gpu_count`, `has_report`, `scanner_status` | B-252 C3 (`B-252_C2_VERIFICATION.md` L7) | here |
| C2 | `GET /v1/cmdb/assets` parameters | Missing `os` (enum `windows`, `linux`, `darwin`; 400 otherwise) | B-252 C3 / B-228 | here |
| C3 | `GET /v1/cmdb/assets` parameters | Missing `id` (uuid; used with `kind`) | B-252 C1 (`B-252_C1_VERIFICATION.md` SR-1) | here |
| C4 | `CMDBAssetList.counts` | Semantics undocumented: counts ignore `category_id`, `type_id`, `kind`, `id` and `os`, but honour workspace, search and licence | B-196 fix-up N1; extended by C1/C3 | here |
| C5 | `PATCH /v1/cmdb/assets/{assetKind}/{assetId}/classification` | Missing `403 module_not_licensed` for endpoints | B-196 fix-up | here |
| C6 | `Endpoint` schema (`GET /v1/endpoints`, `/v1/endpoints/{id}`) | Missing `has_report` and `scanner_status` | item 4 (`ITEM4_HONEST_STATE_VERIFICATION.md` #8) | here |
| C7 | `Endpoint` schema | Missing `first_seen` (returned by `GET /v1/endpoints/{id}`) | B-252 C2 | here |
| C8 | `POST /v1/reports` report payload | Missing `received_at` (server-set) and `scanner_errors` (reason codes `timeout`, `still_running`, `panic`, `error`) | B-281/B-284/B-285 (`B-281_B-284_B-285_VERIFICATION.md` Info 7) | here |
| C9 | `GET /v1/endpoints` | Documents `has_ai` and `has_local_model`, which the handler ignores. Search is described as "hostname or username", but there is no username column. | B-226 review | **B-229** (the code half) |
| C10 | `POST /v1/gateway/policies/reorder` | Request body documented as `order`; the handler requires `policy_ids` | B-086 | **B-089** |
| C11 | `EpisodeStep` | Documented fields (`step_number`, `tool`, `reasoning`, …) don't match the recorded step JSON | B-002 Brief 3 | **B-017** |
| C12 | `POST /v1/auth/api-keys` and `POST /v1/gateway/tokens` | `CreateAPIKeyRequest` / `APIKeyResp` lack `agent_id` and `expires_at`. `AITokenResponse` lacks `jti`. `ToolSpend` (FinOps) is undocumented. | B-098, B-108 | **B-106** |
| C13 | `POST /v1/gateway/agents` | Missing the `409 conflict` for a duplicate name | B-074 (NOTES 2026-08-19) | here |
| C14 | `POST /v1/users/invite` | Description says the link expires after 72 h; the code uses 48 h. The 201 schema omits `user` and `expires_at`. The 409 description is inaccurate. | B-212, B-242 review | **B-213** (72 h text) + here |
| C15 | `POST /v1/settings/notifications/test` | Spec and UI expect `{success, error}`; the handler returns `{sent, reason}`, so the UI shows every result as an error. **This is a user-visible bug, not just a documentation gap.** | B-238 review I1 | **B-292** (UI/handler fix); the spec half stays here for Architect-EAMI |
| C16 | `GET /v1/audit/export` | Real optional filters (`agent_name`, `tool_name`, `decision`, RFC3339 `from`/`to`) and the bounded-error responses are undocumented | B-221 | **B-222** |
| C17 | All routes | **No per-route role requirements are documented.** B-253 made several routes, and some PATCH fields, admin-only. | B-253 (NOTES 2026-09-29) | here |
| C18 | `GET /v1/agents/{agent_id}/config` (service key), `GET` and `PUT /v1/gateway/agents/{agentId}/config` | B-293: every response carries the **full** config, with `[]` meaning empty and never `null`, plus `config_version` (`c1:` + sha256 of the canonical JSON, a content hash) and `model_file_size_mb` (1–100000, default 100). PUT accepts `[]` for `model_scan_paths` and `enabled_scanners` (`[]` = no scanners); an absent field still means unchanged. New 400s: `model_file_size_mb` out of range; paths: more than 32, empty, over 1024 bytes, control characters, not absolute (POSIX `/`, `X:\` or `X:/`), or a network/UNC/device path (`\\host\x`, `//host/x`, `\\?\…`); more than 32 scanner names. Responses stay well under the agent's 256 KiB cap. The canonical form is specified in `B-293_VERIFICATION.md`. | B-293 | here |
| C19 | `Endpoint` detail (`GET /v1/endpoints/{endpointId}`) | B-293: new nullable `applied_config_version`, `config_source` (`remote`/`persisted`/`local`/`defaults`), `config_error` (a fixed reason-code set, else `unrecognised`), and `expected_config_version` (null when unlinked). Null on reports from older agents. **New failure status:** the route now returns 500 (a fixed message) when the linked agent's config can't be read, rather than silently showing no expected config. | B-293 | here |
| C20 | `POST /v1/reports` / collector ingest report payload | B-293: the report gains `config_version`, `config_source` and optional `config_error`; `scanner_errors` gains the code `too_large` (a section dropped to respect `max_report_size_bytes`). Extends C8. | B-293 | here |
| C21 | `PUT /v1/gateway/agents/{agentId}/config` (and future preset routes) error body | B-269 Slice 0 (D10): `ErrorResponse` gains an optional `field` (one of `scan_interval_seconds`, `max_report_size_bytes`, `model_file_size_mb`, `model_scan_paths`, `enabled_scanners`). Validation 400s now carry **stable codes** instead of `bad_request`: `interval_out_of_range`, `report_size_out_of_range`, `model_size_out_of_range`, `too_many_paths`, `path_empty`, `path_too_long`, `path_invalid_chars`, `path_network`, `path_not_absolute`, `path_not_normalized` (a `.` or `..` part, or a part ending in a dot or space; a `:` after the drive letter is `path_invalid_chars`), `path_root`, `path_profile_parent` (server-only: `/home`, `/Users`, `X:\Users`, and the aliases `X:\Documents and Settings`, `/System/Volumes/Data/Users`, `/var/home`), `too_many_scanners`, `unknown_scanner`. Messages are fixed text and **never echo the submitted value** (the old `unknown scanner %q` echo is gone). `path_profile_parent` (and the full rules) apply on this legacy route **only when `model_scan_paths` changes** (S5). Shared vectors: `testdata/agent_config_vectors.json`. | B-269 | here |
| C22 | Agent config responses (`GET /v1/agents/{agent_id}/config`, `GET`/`PUT /v1/gateway/agents/{agentId}/config`) | B-269 Slice 0 (S5): new `path_warnings: string[]` (never null): codes the stored paths would fail if added now (`path_root`, `path_profile_parent`); legacy paths stay accepted but flagged. **Behaviour:** new agents' default `model_scan_paths` is now `[]` (migration 000028; was `/home`, `/Users`, `C:\\Users`). `config_error` (C19) gains `path_root` and `path_not_normalized` (agent codes); the API allowlist also accepts `path_profile_parent`. | B-269 | here |
| C23 | Report payload: local models and notes | B-269 Slice 0: `local_models[].source` gains `scan_path` (a hit under a configured path; agents ≥ 1.3.2; older agents send `lm_studio`). New optional `scanner_notes: {scanner: [code]}`, codes `depth_limited` (a walk hit the depth limit of 8), `path_root` (a configured path was, or resolved through links to, a root and was skipped) and `path_network` (it resolved to a network share). Stored `endpoint_model_files.source` now accepts `gpt4all` and `scan_path`, and the agent's `lm_studio` is stored as `lmstudio` (previously both became `unknown`). Extends C20. | B-269 | here |

**Unspecified older entries:** B-217 recorded "two changed response shapes" for the CMDB list without naming them. They are superseded by C1–C5 above, which describe the current shape.

## D. How to use this file

- **Adding drift:** a new row in the right section, with source and tracking ID, in the same commit as the code change (`API_CONVENTION.md` §7, §9).
- **When Architect-EAMI fixes a row:** delete it here in the same commit as the `openapi.yaml` change, and regenerate `eami-ui/src/api/schema.ts`. Where a UI hook uses the `apiFetch` escape hatch only because of that row, switch it to the generated client.
- **Re-generate §A and §B:** diff `router.go`'s routes against `openapi.yaml`'s `paths:`. The method is described above; the script is not committed.
