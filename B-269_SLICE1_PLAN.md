# B-269 Slice 1 (discovery presets backend): plan

**Date:** 2026-10-07 · **By:** Claude Code · **Plan only. No code changed. No build until the founder approves.**
**Read:** `DISCOVERY_PRESETS_DESIGN.md` (§2, §3, §7–§10, §12), `B-269_PART_A_INVESTIGATION.md`, `B-293_VERIFICATION.md`, `API_CONVENTION.md` (LOCKED v1; §11 open), the Slice 0b admin audit code (`eami-api/internal/store/admin_audit.go`, `internal/api/admin_audit.go`), and the current delivery, config, ingest and CMDB code.
**Sequence:** master-sequence item 8, Slice 1. Slices 0 and 0b are done; 7a is acknowledged. **CI has been red on master since `111fc8e`, so under the 2026-10-07 rule Slice 1 can't be ticked while CI is red.**

**Dev database, measured 2026-10-07 (read-only):**
- 7 orgs.
- `agent_configs`: 25 rows, in 5 orgs.
- `endpoints`: 9 rows. 3 are linked to a governed agent: 2 in Dev Org and 1 in the leaked B-301 fixture org.
- The 3 linked endpoints have 3 distinct contents.
- 2 orgs already hold admin audit events: Dev Org (9 events) and the fixture org (1).

---

## 1. Schema (migration `000031_discovery_presets`)

### Tables

Names follow the design's names, prefixed `discovery_` because `preset` alone is ambiguous next to the gateway's tables.

**`discovery_presets`** (mutable metadata):
- `id` UUID PK.
- `org_id` UUID NOT NULL → `orgs(id)` ON DELETE CASCADE.
- `name` TEXT NOT NULL, 1–100 characters.
- `description` TEXT NOT NULL DEFAULT '', at most 1000 characters.
- `origin` TEXT CHECK IN (`standard`, `migrated`, `admin`).
- `builder_default` BOOLEAN NOT NULL DEFAULT false.
- `current_version` INT NULL: the published version that is served; NULL means never published.
- `latest_version` INT NOT NULL DEFAULT 0: the allocation counter.
- `archived_at` TIMESTAMPTZ NULL.
- `created_by` UUID NULL.
- `created_at` and `updated_at`.

Constraints and indexes:
- `UNIQUE (org_id, id)`, the target for composite FKs.
- A unique index on `(org_id, lower(name))`, so names are unique in an org (archived ones included, to avoid look-alikes).
- A partial unique index on `(org_id) WHERE builder_default`: at most one star per org.
- `CHECK (current_version IS NULL OR current_version BETWEEN 1 AND latest_version)`.
- `FOREIGN KEY (id, current_version) REFERENCES discovery_preset_versions (preset_id, version_number) DEFERRABLE INITIALLY DEFERRED`.

**`discovery_preset_versions`** (published versions only, immutable):
- `preset_id` and `org_id`, with `FOREIGN KEY (org_id, preset_id) REFERENCES discovery_presets (org_id, id) ON DELETE CASCADE`.
- `version_number` INT ≥ 1.
- The five content columns.
- `reverted_from` INT NULL.
- `note` TEXT NULL, at most 500 characters.
- `published_by` UUID NULL: a snapshot with **no FK**. NULL means the system (migration or seed). An FK with SET NULL would be an UPDATE, and the immutability trigger would then block user deletion.
- `published_at`.
- `PRIMARY KEY (preset_id, version_number)`.

**`discovery_preset_drafts`** (at most one per preset):
- `preset_id` PRIMARY KEY.
- `org_id`, with the same composite FK, ON DELETE CASCADE.
- The five content columns.
- `revision` INT NOT NULL: the optimistic lock.
- `based_on_version` INT NULL.
- `updated_by` and `updated_at`.

**`endpoint_preset_assignments`** (history):
- `id`, `org_id`, `endpoint_id` and `preset_id`.
- `source` CHECK IN (`migration`, `enrollment`, `adopt`, `change`).
- `enrollment_key_id` UUID NULL, with no FK until Slice 3.
- `assigned_by` and `assigned_at`.
- `ended_at` NULL and `ended_by` NULL.
- `FOREIGN KEY (org_id, endpoint_id) REFERENCES endpoints (org_id, id) ON DELETE CASCADE`. This needs a new `UNIQUE (org_id, id)` on `endpoints`.
- `FOREIGN KEY (org_id, preset_id) REFERENCES discovery_presets (org_id, id) ON DELETE CASCADE`.
- A partial unique index on `(endpoint_id) WHERE ended_at IS NULL`: one current assignment.
- An index on `(org_id, preset_id) WHERE ended_at IS NULL`, for counts and rollout.

**The composite FKs make a cross-org assignment impossible in the database**, even with a handler bug.

### How the rules are enforced
- **Published versions are immutable:** a `BEFORE UPDATE` trigger on `discovery_preset_versions` raises an exception.
  - No code path deletes a version. DELETE is allowed only so that an org's cascade still works; a DELETE trigger would block every test that deletes its org.
  - "Superseded" is **derived** (`version_number < current_version`), never stored, so no published row ever changes.
  - **Limit:** the app's role is a superuser that owns the table (B-299), so this stops application bugs, not a database administrator.
- **At most one draft per preset:** the drafts table's primary key is `preset_id`.
- **No race on version numbers** when two admins publish at once. Publish runs in `RunAudited`'s transaction:
  1. `SELECT … FROM discovery_presets WHERE id=$1 AND org_id=$2 FOR UPDATE`. The row lock serialises publishers of that preset.
  2. `next = latest_version + 1`.
  3. Insert the version, then set `latest_version = next, current_version = next`.
  4. Delete the draft (only if its `revision` matches the request, else 409).
  5. `UNIQUE (preset_id, version_number)` is the backstop.
  - The second publisher waits on the row lock, then finds the draft gone or its revision changed, and gets a 409.
  - **Lock order:** the change function's row locks come first. `RunAudited` takes the org's chain advisory lock last, in `AppendAdminAuditEvent`. So two different presets can't deadlock.
  - The change function sets `SET LOCAL lock_timeout = '5s'` itself. Today that timeout is set only inside Append, so a wait on a row lock would be unbounded.

### Content mapping

All five content fields map one-to-one onto existing `agent_configs` columns, with the same types and the same limits:

| Field | `agent_configs` column | Type |
|---|---|---|
| scanners | `enabled_scanners` | TEXT[] |
| interval | `scan_interval_seconds` | INT |
| model scan paths | `model_scan_paths` | TEXT[] |
| model minimum size | `model_file_size_mb` | INT, `CHECK 1..100000`, as in 000027 |
| report size cap | `max_report_size_bytes` | INT |

- **New:** version number, note, `reverted_from`, published by and published at, revision, `based_on_version`, origin, the builder flag, archiving, and the assignment history.
- **"Needs review" is derived, not stored.** It is `store.PathWarnings(paths)` being non-empty, the same rule today's route returns as `path_warnings`.

## 2. Hash: one function

- **The function used today:** `store.AgentConfig.Version()` (`eami-api/internal/store/agent_config_version.go`).
  - It canonicalises the content, then computes `"c1:" + sha256`.
  - It is pinned to the agent's canonical form by the shared `testdata/agent_config_vectors.json`.
  - The delivery route reaches it through `agentConfigToResp(c)` → `c.Version()`.
- **Slice 1 serves a version by building the same `store.AgentConfig` value from the version's five columns and calling the same `agentConfigToResp`.** It is the same method on the same type, so there is no second canonicaliser.
- **Recommendation: don't store `config_version` as a column; compute it from the immutable content.**
  - The design lists the hash as a column. But migrated rows are created by SQL, and SQL can't call Go.
  - A SQL re-implementation would be a second canonicaliser. It would differ on edge cases such as JSON escaping of control characters and U+2028, which is the drift the shared vectors exist to prevent.
  - Content can't change (§1), so a computed hash can't drift either.
  - **Rollout** still needs the hash in SQL comparisons. Go computes each preset's current hash (one per preset), passes `(preset_id[], hash[])` as arrays to one grouped query, and classifies the small result in Go.
  - **If you want it stored anyway,** Go writes it at publish, and migrated rows need a Go backfill at API startup. That is the riskier option.
- **Tests:**
  - For every version in a seeded database, the served `config_version` equals `AgentConfig{…}.Version()` over the stored columns.
  - The existing golden-vector tests keep pinning API and agent equality.

## 3. Delivery switch: `GET /v1/agents/{agent_id}/config`

**Resolution:** one org-scoped SQL statement.
1. The org is `GetDefaultOrgID`, unchanged (D3: single-org until B-243).
2. Find the endpoint `(org, agent_id)`. If there is none: **404** `not_found` "endpoint not registered" (as today).
3. Find the current assignment (`ended_at IS NULL`). If there is none: **404** `not_found` "endpoint has no preset".
4. Read the preset's `current_version`. If it is NULL: **404**. This is defensive only; assigning to an unpublished preset is refused.
5. Load that version row and return it through `agentConfigToResp`.

**What doesn't change:**
- The 404 contract is what both agent generations already handle. Agents 1.3.0+ revert to local config after two consecutive 404s (D-c); older agents keep their config.

**What changes:**
- **The link to a governed agent stops affecting config.** Today, linking an endpoint starts config delivery; after Slice 1 it doesn't. That is a behaviour change to `PATCH /v1/endpoints/{id}/link-agent`, and it needs a drift row.

**A draft is never served:** the delivery query never reads `discovery_preset_drafts`. A test proves it: publish v1, edit the draft, fetch, and get v1.

**Response body:**
- `agent_id` is today the gateway agent's UUID. Neither agent generation reads it (checked: the 1.3.x `remoteconfig` struct and the pre-1.3.0 `collector/sender.go` struct). It becomes the agent-reported ID from the path.
- `updated_at` becomes the version's `published_at`.
- New additive fields `preset_id` and `preset_version`.
- Both changes are allowed under §9, since every in-repo consumer ignores them. Each gets a drift row.

**Identity (B-295):**
- Until Slice 3 the key is still the **agent-reported** `agent_id`, the config's ID or else the hostname.
- Two machines reporting the same ID share one endpoint row. They therefore share one assignment and get the same config.
- Their reports interleave, so that endpoint's rollout state reflects whichever machine reported last.
- Assigning a preset to that one endpoint changes both machines.
- Rows already merged by a collision can't be un-merged (D4). Slice 3's server-issued identity fixes this only for newly enrolled machines.

**Also switches in Slice 1:** `GET /v1/endpoints/{id}`'s `expected_config_version` reads `agent_configs` today (`discover.go`). It must read the assigned preset's current version. Otherwise Endpoint Detail would compare against a table nothing serves.

## 4. Migration (`000031`)

**Today's numbers (dev):**

| Org | Endpoints | Linked | `agent_configs` rows | Admin audit events |
|---|---|---|---|---|
| Dev Org | 8 | 2 | 20 | 9 |
| b121 / b124 / B-169 Live Verify | 0 | 0 | 1 each | 0 |
| B-169 Expired License Org / Avula | 0 | 0 | 0 | 0 |
| TEST FIXTURE (leaked by B-301) | 1 | 1 | 2 | 1 |

**What it creates (D8):**
- **Standard ×7**, one per org:
  - all 10 scanners, 300 s, empty paths, 100 MB, 5 MiB;
  - v1 published by the system, `builder_default`.
- **Migrated presets,** only for rows with a linked endpoint in the same org, with identical contents merged per org:
  - **Dev Org ×2:** content B (60 s, 9 scanners without `models`, legacy paths) for `Bhargav_tej`, and content A (300 s, all 10, legacy paths) for `b164-b165-live-agent`.
  - **The fixture org ×1:** 120 s, all 10, `{}`.
  - Each is named "Migrated: <agent name>" (plus "(+N)" when merged), v1 = the `agent_configs` content exactly, and is flagged "needs review" through `path_warnings` where the legacy `/home`, `/Users`, `C:\\Users` paths are present.
- **Assignments ×3** (`source = migration`), for the 3 linked endpoints.
- **The other 6 endpoints stay unmanaged.** All 6 already get 404 today.
- **A trigger `AFTER INSERT ON orgs`** seeds Standard for every new org, following the existing `trg_agent_configs_default` pattern, so the "never empty" rule holds for every org-creation path.
- **The D7 columns are backfilled** (§6).
- **`agent_configs` isn't dropped or altered,** and neither is its trigger.

**The migration writes no admin audit events.** That is a decision for you:
- An event would make every org permanently undeletable (B-224/B-304), including the three fixture orgs and every test org that the Standard trigger seeds.
- So the system seed and the migration are recorded in the migration file and `BUILT.md`, not in the chain.
- This is a deliberate exception to D2's "all mutations audited". Every admin action is still audited.

**Byte-identical test** (throwaway database):
1. Apply 000001–000030.
2. Seed orgs, governed agents, `agent_configs` and endpoints that mirror the dev data: contents A, B and the fixture content, including the literal doubled-backslash `C:\\Users`, plus unlinked rows, a second org, and an unlinked endpoint.
3. Capture **before**: for each endpoint, `agentConfigToResp(GetAgentConfig(link))` (both kept), or 404.
4. Apply 000031.
5. Call the new handler.
6. **Assert:**
   - the five content fields' canonical JSON bytes are identical;
   - `config_version` is identical;
   - `path_warnings` is identical;
   - every 404 stays a 404.
7. **Live, too:** capture the real dev responses for the linked Dev Org endpoints before and after the migration (service key read in process, never printed) and diff them.

**Down migration:**
```sql
DROP TRIGGER IF EXISTS trg_seed_standard_preset ON orgs;
DROP FUNCTION IF EXISTS seed_standard_preset();
DROP TABLE IF EXISTS endpoint_preset_assignments;
DROP TABLE IF EXISTS discovery_preset_drafts;
ALTER TABLE discovery_presets DROP CONSTRAINT IF EXISTS discovery_presets_current_version_fkey;
DROP TABLE IF EXISTS discovery_preset_versions;
DROP TABLE IF EXISTS discovery_presets;
ALTER TABLE endpoints
  DROP CONSTRAINT IF EXISTS endpoints_org_id_id_key,
  DROP COLUMN IF EXISTS applied_config_version,
  DROP COLUMN IF EXISTS config_source,
  DROP COLUMN IF EXISTS config_error,
  DROP COLUMN IF EXISTS config_report_received_at,
  DROP COLUMN IF EXISTS config_report_id;
```
- `agent_configs` is untouched in both directions, so running down **together with the previous API binary** restores today's delivery exactly.
- **What down loses:** every preset edit, publish and assignment made after the migration.
- **What remains:** admin audit events that name presets stay (append-only), pointing at deleted targets.
- Down needs the code rolled back with it, and that is stated in the migration file.

## 5. The old route and Configure

**`PUT /v1/gateway/agents/{id}/config`:** I agree with your lean.
- It returns **410** `config_moved_to_presets`, with a fixed message.
- No write, no audit event, never a silent accept.
- 410 rather than 409, because the resource has moved for good.

**`GET` on the same route:** also **410** `config_moved_to_presets`.
- Its only UI consumer, `AgentConfigPanel`, becomes unreachable.
- Serving `agent_configs` would show a stale config as if it were live.
- This is allowed as a breaking change under §9, because every in-repo consumer is updated in the same commit.

**The trigger `trg_agent_configs_default`:** keep it.
- `agent_configs` stays a frozen record and the down-migration fallback.
- New agents still get a row, whose default paths have been `{}` since 000028. Harmless.

**Configure (D6):**
- It exists in **two** places, and D6 names only Agent Detail:
  - the Agents list's Actions column (`AgentsPage.tsx`);
  - Agent Detail's Actions tab (`AgentActionsTab.tsx`).
- Both get disabled, with: "Discovery settings moved to presets (Admin › Discovery Hub › Agent-Based)".
- `AgentConfigPanel` and its hooks are deleted, since they would be unreachable code.
- The Slice 1 commit says this is a deliberate removal of a reachable action (orphaned-action check).

**Gap until Slice 2:** there is no UI for editing presets until then, so between Slices 1 and 2 discovery config is editable through the API only. Ship 1 and 2 close together, or make the message say editing returns with the presets page.

**Drift rows:**
- `PUT` and `GET /v1/gateway/agents/{id}/config` → 410 `config_moved_to_presets`.
- `GET /v1/agents/{agent_id}/config`: the source becomes the preset; the meaning of `agent_id` and `updated_at` changes; `preset_id` and `preset_version` are added.
- `GET /v1/endpoints/{id}`: the source of `expected_config_version` changes.
- `PATCH /v1/endpoints/{id}/link-agent`: no longer affects config.
- Every new route in §7.

## 6. D7: the latest config fields on the endpoint row (belongs in Slice 1)

**New `endpoints` columns:**
- `applied_config_version`, `config_source`, `config_error`: sanitised by the same `parseAppliedConfig` allowlist the read path uses today.
- `config_report_received_at` and `config_report_id`.

**Code reality, flagged:** ingest **isn't transactional today**. `UpsertAgentEndpoint`, `DeleteEndpointNormalizedData` and `InsertEndpointReport` are separate pool statements. Slice 1 adds one transaction around the report insert and the endpoint update.
- `InsertEndpointReport` returns `received_at` with its `id`.
- The update is conditional, `WHERE id=$1 AND (config_report_received_at IS NULL OR (config_report_received_at, config_report_id) < ($recv, $id))`. That matches `latestReportJoinSQL`'s `ORDER BY received_at DESC, id DESC` exactly, so a slower concurrent ingest can never overwrite a newer report's values.
- A report from an old agent, with no config keys, still writes NULLs, because the latest report says "not known".

**Backfill:**
- The migration sets the columns from each endpoint's latest report.
- It uses SQL versions of the allowlists: a version must match `^c1:[0-9a-f]{64}$` or be `''`; the source and error must be in the known lists, else `unrecognised`.
- That is a second copy of the allowlists, which is a risk (§11). The equality test is what catches it.

**Equality test:** for every endpoint in a seeded throwaway database, the stored columns equal `parseAppliedConfig(latest report)`. The seed covers every known code, junk values, a missing version, an empty version, ties on `received_at`, and paste-only endpoints with no report.
- It runs after the backfill and again after concurrent ingests.
- It also runs once, read-only, against the dev database after migrating.

**Why it belongs in Slice 1:** Slice 1's rollout summary is the first reader, and without these columns it would de-TOAST whole reports (Part A, C7).

## 7. API (all new routes are admin only in Slice 1, on the existing `requireRole("admin")` group)

| Route | Purpose | Success | Errors |
|---|---|---|---|
| `GET /v1/discovery/presets` | List, with current version, draft flag, endpoint count and rollout counts. Filters: `archived=false\|true\|all`. Sort: `name`, `updated_at`. | 200 list | 400 `invalid_parameter` |
| `POST /v1/discovery/presets` | Create (name, description, initial draft content) | 201 preset | 400 validation codes; 409 `name_taken` |
| `GET /v1/discovery/presets/{presetId}` | One preset, with its rollout counts | 200 | 404 |
| `PATCH /v1/discovery/presets/{presetId}` | Name and description (not versioned) | 200 | 400; 404; 409 `name_taken` |
| `GET /v1/discovery/presets/{presetId}/draft` | The draft (content, revision, `based_on_version`) | 200 | 404 `draft_not_found` |
| `PUT /v1/discovery/presets/{presetId}/draft` | Save the draft. `revision`: 0 creates it from the current version; n updates. | 200, revision n+1 | 400 validation codes; 404; 409 `draft_conflict`; 409 `preset_archived` |
| `GET /v1/discovery/presets/{presetId}/draft/impact` | Impact panel: bound endpoints, 1.3.0+ proxy count (`applied_config_version` not null) versus older, field-level diff against current, warning codes | 200 | 404 |
| `POST /v1/discovery/presets/{presetId}/publish` | Body: `draft_revision`, optional `note` | 201 version | 400 validation codes; 404 `draft_not_found`; 409 `draft_conflict`; 409 `preset_archived` |
| `GET /v1/discovery/presets/{presetId}/versions` | Version list (paginated, newest first) | 200 list | 404 |
| `GET /v1/discovery/presets/{presetId}/versions/{version}` | One version | 200 | 404 |
| `POST /v1/discovery/presets/{presetId}/revert` | Body: `version`, `expected_current_version`, `note`. Creates a new version. | 201 version | 400; 404; 409 `version_conflict`; 409 `preset_archived` |
| `POST /v1/discovery/presets/{presetId}/archive` | Archive | 200 | 404; 409 `preset_in_use`; 409 `preset_is_builder_default` |
| `POST /v1/discovery/presets/{presetId}/unarchive` | Unarchive | 200 | 404 |
| `PUT /v1/endpoints/{endpointId}/preset` | Assign or change one endpoint (`preset_id`) | 200; a no-op if unchanged (no event) | 400; 404 (endpoint or preset, never saying which org); 409 `preset_not_published`; 409 `preset_archived` |
| `DELETE /v1/endpoints/{endpointId}/preset` | Unassign | 204; a no-op if none | 404 |
| `POST /v1/discovery/presets/{presetId}/assignments` | Bulk assign (`endpoint_ids`, at most 500), all or nothing | 200, with `assigned` and `unchanged` counts | 400 `too_many_items`; 404 if any ID is missing in this org (none applied); 409 as above |
| `GET /v1/cmdb/assets` (extended) | New filters: `preset_id`, `managed=true\|false`, `rollout=applied\|behind\|rejected\|unknown` (needs `preset_id`) | as today | 400 `invalid_parameter` |
| `GET /v1/endpoints/{endpointId}` (extended) | Adds `preset_id` and `preset_version`; `expected_config_version` from the preset | as today | as today |

**Request and response shapes** follow §12 below:
- A preset is `{id, name, description, origin, builder_default, current_version, latest_version, has_draft, archived_at, created_at, updated_at, needs_review, endpoint_count, rollout:{applied, behind, rejected, unknown}}`.
- A version is `{preset_id, version, content:{…5 fields}, config_version, path_warnings, reverted_from, note, published_by, published_at}`.

**Roles:**
- Every preset route is admin-only, as you leaned.
- The CMDB filter extension inherits CMDB's read roles (admin, operator, viewer), per principle 6. It exposes IDs and buckets only, never preset content. If you want it strict, the new parameters return 403 for non-admins.

**Waits:**
- **Slice 2:** the UI, and draft discard.
- **Slice 3:** setting the builder default, enrollment keys, and the `source = enrollment` writer.
- **Slice 4:** the unmanaged strip and B-270.

## 8. Audit (`RunAudited`, one event per committed change; failures roll back)

| Route | Code | Target | Summary |
|---|---|---|---|
| Create | `preset.created` | preset | Changed-only entries for `name`, `description` and each content field present; refs `preset_id` |
| PATCH | **new code `preset.updated`** | preset | Changed-only entries for `name` and/or `description` |
| PUT draft | `preset.draft_updated` | preset | Changed-only field names; list fields carry `added`/`removed` **counts**; refs `preset_id` |
| Publish | `preset.published` | preset | Changed field names and counts against the previous version; refs `preset_id`, `preset_version`, `from_version` |
| Revert | `preset.reverted` | preset | Changed field names; refs `preset_version` (new), `from_version` (the source version) |
| Archive / unarchive | `preset.archived` / `preset.unarchived` | preset | refs `preset_id` |
| Assign / change | `endpoint.preset_assigned` | endpoint | refs `endpoint_id`, `preset_id`, and a **new ref `from_preset_id`** when changing |
| Unassign | `endpoint.preset_unassigned` | endpoint | refs `endpoint_id`, `preset_id` |
| Bulk | `preset.bulk_assigned` | preset | refs `preset_id`, `count` |

- **Never recorded:** names, descriptions, notes, paths, scanner names or numeric values.
  - The 0b registry *permits* hashed values and scanner names.
  - Slice 1 uses only field names and counts. The registry already accepts that (`validateChange`): an entry with no old/new hash is valid for every field kind.
  - A short hash of a small-domain number such as an interval is effectively the value.
- **Two registry additions, both small:** the code `preset.updated`, and the ref `from_preset_id`.
- **A no-op writes no event.**
- **A failed audit write rolls the change back:** `ErrAdminAuditWrite` returns 500 `audit_write_failed` (the B-301 code).

## 9. Validation (reuse Slice 0's rules and `testdata/agent_config_vectors.json`)

- **Draft PUT and create:** the basic rules, `ValidateModelScanPaths` plus the limit checks.
- **Publish and revert:** the full rules (`ValidateModelScanPathsFull`) **when the paths differ from the current published version**. Unchanged legacy paths stay accepted and flagged (S5).
  - So republishing a migrated preset with its paths untouched works.
  - Reverting to a legacy-path version when the current paths differ is a path change, so it's **refused with `path_profile_parent`**. That is a consequence of S5 to confirm (§11).
- **Codes publish can return:** the 14 in `adminAuditCodes`:
  - `interval_out_of_range`, `report_size_out_of_range`, `model_size_out_of_range`;
  - `too_many_paths`, `path_empty`, `path_too_long`, `path_invalid_chars`, `path_network`, `path_not_absolute`, `path_root`, `path_not_normalized`, `path_profile_parent`;
  - `too_many_scanners`, `unknown_scanner`;
  - plus the request codes `invalid_body`, `draft_not_found`, `draft_conflict` and `preset_archived`.
- **Body:** `{code, field, message}`, using the existing `writeFieldError`. It never echoes a value.
- **Warnings** (impact panel only, never errors): `all_scanners_off`, `interval_at_floor`, `paths_added`, `path_profile_parent`.

## 10. Tests (every test that calls an audited route uses a throwaway database: `newAdminAuditEnv` / `newThrowawayWorkspaceTestEnv`)

**Cross-org, on every route:** org B's admin addresses org A's preset, version, draft or endpoint, and gets 404, with nothing written and no event. Specifically:
- an endpoint in A with a preset in B, single and bulk (the bulk request applies nothing);
- reverting to another org's version;
- the CMDB `preset_id` filter with another org's preset;
- the composite FK rejecting a cross-org row inserted directly in SQL.

**Roles:** operator, viewer and approver get 403 on every preset route.

**Concurrency:**
- Two simultaneous publishes of one preset: one 201, one 409; versions contiguous; one event.
- Two publishes of different presets: both succeed, no deadlock (with `-race`).
- A stale draft PUT: 409 `draft_conflict`, and the stored draft is unchanged.
- A revert with a stale `expected_current_version`: 409.
- Two ingests for one endpoint, one older and one newer, racing: the D7 columns hold the newer.

**Delivery:**
- Byte-identical before and after migration (§4).
- A draft is never served.
- Unassigned, unregistered and never-published all give 404.
- After publish, the new version is served; after revert, a new number with the old content.
- Served `config_version` equals `Version()`.

**Audit rollback:** a test trigger in the throwaway database blocks inserts into `admin_audit_events`, the same technique as `TestB301_T9_KeyRevocationFailsClosed`. For each audited route, preset, version, draft and assignment are all unchanged, with a 500 `audit_write_failed` and no raw error text.

**Also:**
- D7 equality.
- The immutability trigger: an UPDATE fails.
- Org deletion still cascades for an org with no events.
- The Standard seed trigger fires for a new org.
- Old routes: 410, with no write.
- Validation vectors through publish.
- The orphaned-action check on both Configure entry points (the UI type-check plus a manual click-through).

## 11. Risks and conflicts with code reality (flagged, not worked around)

| # | Design or decision says | Reality | Needs |
|---|---|---|---|
| K1 | D7: "same transaction as the report insert" | Ingest has no transaction today | Slice 1 adds one; ingest changes and needs its own review |
| K2 | §2: the hash is stored on `preset_version` | SQL migrations can't call the Go canonicaliser | Compute it, don't store it (§2); confirm |
| K3 | §2: status draft / published / superseded | A mutable status breaks immutability | A separate drafts table; superseded derived; confirm |
| K4 | D2: all mutations audited | Seeding Standard and migrated presets with events would make every org undeletable (B-224/B-304) | The system seed and migration are unaudited; confirm |
| K5 | D6: disable Configure on Agent Detail | Configure is also on the Agents list | Disable both |
| K6 | — | `GET /v1/endpoints/{id}` reads `agent_configs` for `expected_config_version` | Switch it in Slice 1 |
| K7 | §1: config follows the preset | Linking an endpoint stops changing config | Drift row and UI wording on the link action |
| K8 | §10: Slice 1 is backend | D6 disables Configure in Slice 1; there's no preset UI until Slice 2 | API-only editing in between; ship close together |
| K9 | §2: assignment source is enrollment / adopt / change | Migration needs its own value | Add `migration` |
| K10 | 0b registry | No code for a name or description edit; no "from" ref for a reassignment | Add `preset.updated` and `from_preset_id` |
| K11 | S5: legacy paths accepted if unchanged | Reverting to a migrated legacy-path version is a path change | It's refused; confirm |
| K12 | "Never empty", Standard in every org | The seed trigger hardcodes the 10 scanner names in SQL | Extend the scanner release rule (CLAUDE.md) to name this trigger, as it already names the column default |
| K13 | D7 backfill | The SQL allowlists duplicate the Go ones | The equality test, plus a test that diffs the migration's lists against `knownConfigSources`/`knownConfigErrors` |
| K14 | Ticking | CI red since `111fc8e` | Slice 1 can't be ticked until CI is green |
| K15 | — | The leaked B-301 fixture org has a linked endpoint, so it gets a migrated preset | Expected; fixture data |
| K16 | — | `endpoint_reports.id` is a random UUID, so the latest-report tie-break is arbitrary (though deterministic) | Keep the same rule in D7 for equality; note it |
| K17 | Brief: "never values" in audit | The 0b registry permits short hashes of values | Slice 1 records names and counts only (§8) |
| K18 | §3: an impact panel counting 1.3.0+ agents | `agent_version` is blank (B-282) | Use `applied_config_version` not null as the proxy (Part A C6) |

## 12. `API_CONVENTION.md` §11: proposed answers

1. **Pagination:**
   - `page` defaults to 1. `per_page` defaults to 25, with a maximum of 100.
   - A non-integer, `< 1`, or `per_page > 100` gives **400** `invalid_parameter` with `field`.
   - A page past the end gives 200, with empty `data` and the real `total`.
   - **Disagrees:** `pagination()` *clamps* (approvals, endpoints, CMDB, alerts). The admin audit route already returns 400.
   - Proposal: new routes return 400. Existing routes keep clamping until next touched, each switch with a drift row, since it narrows accepted input.
2. **Envelope:**
   - A list is `{data: [...], meta: {total, page, per_page}}`, both always present. `counts` appears only where a resource defines navigation counts (CMDB).
   - A single object is returned bare, as today. Created objects return 201 with the object.
   - **Disagrees:** the alert-rule, agent and policy lists return `data` without `meta`, and episodes use a partial `meta`.
3. **Error body:**
   - `{code, message, field?}`. `code` is stable snake_case.
   - `field` is from a fixed per-route list. `message` is fixed per code on the server and never contains submitted values.
   - Every 5xx is `internal_error`, except the documented `audit_write_failed`.
   - **Disagrees:** many older routes use `bad_request` with free-text messages.
4. **Draft, publish and optimistic locking:**
   - A `revision` integer travels in the body: no ETag or If-Match, which have no precedent here.
   - A stale write gives **409** `draft_conflict`; the client refetches.
   - Publish and revert return **201** with the new version.
   - Revert carries `expected_current_version`; stale gives 409 `version_conflict`.
   - Archive while bound gives 409 `preset_in_use`.
5. **Action and bulk routes:**
   - Non-CRUD actions are `POST /v1/<collection>/{id}/<verb>` (publish, revert, archive, unarchive, later revoke).
   - Bulk is a POST to a sub-collection with an array of at most 500, **all or nothing**: validate all, one transaction, one audit event. The response gives counts, `{assigned, unchanged}`.
   - No partial success, so there is no partial-result shape to design.
6. **Idempotency:**
   - PUT and DELETE are idempotent, and a no-op writes no event.
   - Actions are guarded by preconditions (`draft_revision`, `expected_current_version`), so a retry after success gets 409, never a double publish.
   - Create isn't idempotent; `name_taken` stops duplicates.
   - No Idempotency-Key header in v1.
7. **IDs, timestamps and naming:**
   - IDs are lowercase UUID strings; version numbers are integers.
   - Timestamps are RFC 3339 in UTC with `Z`.
   - JSON fields are snake_case. Routes are lowercase plural nouns, kebab-case for multiple words (as in `api-keys`). Path parameters are camelCase (`{presetId}`), the majority today.
   - **Disagrees:** the service route `{agent_id}`; the admin audit timestamps use microseconds.
8. **Body-size limits:**
   - Every write route wraps the body in `http.MaxBytesReader`: 64 KiB by default, 128 KiB for bulk. Over the limit gives **413** `body_too_large`.
   - **Disagrees:** today only the OpenAPI-discovery route has a limit, and ingest has none (B-287).
9. **The org comes from the session:**
   - No route accepts an org ID for scoping. New routes decode with `DisallowUnknownFields`, so an `org_id` in the body is a 400 `invalid_body`, and an unknown query parameter is a 400 `invalid_parameter`.
   - Service-key routes keep `GetDefaultOrgID` until B-243.
   - **Disagrees:** `decodeJSON` ignores unknown fields today.

## 13. Isolation (B-304)

**Tests:**
- Every Slice 1 test that calls a preset, assignment or audited route runs in a **throwaway database** (`newAdminAuditEnv` pattern), which is dropped at the end. So no events reach the shared database.
- The migration and equality tests use throwaway databases too.
- Shared-database tests that hit the old Configure routes now get 410: no write, no event.
- **Proposed addition (B-304's guard):** the `api` package's `TestMain` counts `admin_audit_events` in the shared database before and after, and fails the run if the count grew.

**Live verification:**
- It has to use **Dev Org**, because delivery resolves the default org only (D3).
- Every preset action there writes a **permanent** event in Dev Org's chain. Dev Org already has 9, so it is already undeletable; nothing new is lost.
- It runs on the WSL packaged agent (agent_id `Bhargavtej`, 1.3.x, currently unlinked and getting 404):
  1. Create the preset "LIVE VERIFY B-269 S1 2026-10-xx".
  2. Publish it.
  3. Assign it to the WSL endpoint and confirm `applied`.
  4. Publish v2 and confirm it applies.
  5. Revert and confirm.
  6. Unassign; two 404s mean local config.
  7. Archive the preset.
- The Windows MSI endpoint (`Bhargav_tej`) isn't touched. Only its served config is captured before and after migration.

**What stays behind in the dev database:**
- **Dev Org:**
  - Standard and 2 Migrated presets, each with v1;
  - the archived live-verify preset and its versions;
  - the WSL endpoint's assignment history rows;
  - about 10–12 new admin audit events;
  - the D7 columns on every endpoint.
- **Every other org:** a Standard preset (and the fixture org a Migrated one) from the migration, with **no** events, so they stay deletable, except the fixture org, which already wasn't.

---

## Founder decisions on the plan (2026-10-08)

**K2, K3, K4 and K11 are accepted, with these conditions:**
- **K2:** the hash is **computed in Go** (`store.AgentConfig.Version()`) and **cached per version** (a `config_version` column written only by Go).
- **K3:**
  - a **database-level block on updating or deleting published versions**;
  - `UNIQUE (preset, version)`, and **one draft per preset**;
  - the version number is **allocated under a per-preset lock inside the publish transaction**.
- **K4:** the migration and the Standard seed **write no audit events**, with a test, and a note in the design record and on B-224.
- **K11:**
  - reverting to a legacy-path version is a path change, so it's refused;
  - **editing a migrated preset's other fields with its legacy paths unchanged stays allowed, and flagged.**

**Follow-on points for the build, flagged, not decided:**
- **The K2 cache and the K3 update block meet:** rows the migration and the seed trigger create in SQL have no hash (SQL can't call Go).
  - The build fills it from Go: on first read, plus a startup backfill.
  - So the update block needs **one narrow exception**: `config_version` may change from NULL to a value, with every other column unchanged. Anything else raises.
  - Rollout queries treat a NULL hash as "fill first" and never as "applied".
- **The K3 delete block and org deletion:** a plain DELETE block also stops an org's `ON DELETE CASCADE`. Every org will hold a Standard preset with v1, so every org would become undeletable, and every shared-database test that deletes its org would fail.
  - **Proposed:** the trigger refuses a DELETE unless the parent preset row is already gone, which happens only in a cascade from the org's deletion.
  - Direct deletes by the application, or of a single version, still raise.
  - This narrows the condition, so it needs **your confirmation** before the build.
