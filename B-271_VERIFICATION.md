# B-271 — Verification record

The linked-endpoint scanner-disable regression, fixed and live-verified on 2026-09-30 by Claude Code. This is item 2 of `AI_ITAM_EPIC_MASTER_SEQUENCE.md` (epic B-280).

## Part A (investigation, reported before building)

**Where the 6-name list came from.** One list, copied into four places:
- the `agent_configs.enabled_scanners` column default (`migrations-v2/000001_baseline`; live default confirmed, schema v24);
- the `schema.sql` mirror;
- `store.AgentConfigDefaults`;
- the UI's `VALID_SCANNERS`, which also **drops any stored name outside its list on load**, so any save would strip the other 4.

**Linking doesn't create the row.** `LinkEndpointAgent` only sets `endpoints.gateway_agent_id`. The row comes from `trg_agent_configs_default` when the governed agent is created, and linking lets it reach the endpoint.

**Fix now, not with B-269.** B-269 is item 8, behind items 3–7. Its planned migration turns `agent_configs` rows into presets, so corrected rows carry forward.

**Founder decisions:**
- D1: backfill existing rows;
- D2: keep the demo row's `models` exclusion (B-194 is unfixed);
- D3: leave the default `model_scan_paths` alone (B-194/B-277).

**Is the 400 safe?**
- The only runtime writer is the UI, which sends its own list. Tests use valid names. The live DB holds only valid names.
- The agent matches names **exactly and case-sensitively**, so an unknown or differently-cased name was never "tolerated": it silently disabled that scanner.
- Nothing depends on the permissive behaviour.

## Fix

| File | Change |
|---|---|
| `schema/migrations-v2/000025_agent_configs_all_scanners.up.sql` | Default becomes all 10. Backfill appends whichever of `ai_processes`, `gpu`, `python_envs`, `nodejs_ai` a row lacks, in that order, keeping all other choices; rows that already have all 4 are untouched. |
| `…000025….down.sql` | Reverts the default only; the backfill is deliberately kept |
| `eami-api/internal/store/agent_configs.sql.go` | `AllScanners` (10 names, the single Go source), `IsKnownScanner`; `AgentConfigDefaults.EnabledScanners` is a copy |
| `eami-api/internal/api/agents.go` | `UpdateAgentConfig`: 400 for an unknown name (exact match) |
| `eami-ui/src/components/agents/AgentConfigPanel.tsx` | `VALID_SCANNERS` = 10 |
| `schema/schema.sql` | Mirror of the new default |

## Tests

- **`schema/migrationtest/b271_agent_configs_test.go`**, on a throwaway DB:
  - migrate to 24, then seed four row shapes: the old default, the demo shape without `models`, a partial `gpu|browser`, and all 4 in a custom order;
  - migrate to 25, then assert the exact arrays, that `models` stays off, that the untouched row keeps its `updated_at`, and that a new agent gets all 10;
  - migrate down to 24: the default reverts and the backfill is kept.
  - Passes, along with the other 6 migration tests, including fresh-vs-incremental schema parity.
- **`eami-api/internal/api/agent_config_scanners_pg_test.go`** (real Postgres):
  - the trigger row, the no-row GET fallback and a no-row PUT all give all 10;
  - `GPU`, `gpu␠`, `scheduled_tasks`, a bad name mixed with a good one, and `""` all return 400 and leave the row unchanged;
  - each of the 10 alone, and all 10 together, return 200;
  - the defaults don't alias `AllScanners`.
- **Full `eami-api` suite:** pass. `go build` and `go vet` are clean.
- **Mutations:** M1 (validation removed) failed the rejection test; M2 (defaults back to 6) failed the default test. Both were restored.
- **UI:** `tsc --noEmit` and `vite build` pass. The UI container hot-reloads `src`.

## Live verification (real stack, real packaged agent, planted fixtures)

**Why fixtures:** the agent marshals an empty scanner result as `null`, exactly like a skipped scanner, so `null` alone proves nothing. The WSL Ubuntu endpoint `Bhargavtej` ran the real B-273 `.deb` (1.0.6, systemd, root), with one fixture per scanner:
- a `python3` process whose command line contains `ollama-serve-b271-fixture` (for `ai_processes`);
- `/root/b271-proj/.venv` with an `openai` dist-info (for `python_envs`);
- `/root/b271-app/package.json` depending on `openai` (for `nodejs_ai`).

A fixture admin in the Dev Org, logging in for real, created governed agents through `POST /v1/gateway/agents`, linked them through `PATCH /v1/endpoints/{id}/link-agent`, and set a 60 s interval.

| Report | Config in force | `ai_processes` | `python_envs` | `node_projects` |
|---|---|---|---|---|
| **Before the fix:** 06:41:40 | first scan, local defaults (all) | null (fixture not yet started) | **1** | **1** |
| 06:42:40, 06:43:41 … 06:46:42 | the fresh `b271-before` row, **6 names** | null | **null** | **null** |
| **Migration 000025 applied** (backfilled `b271-before` to 10) | | | | |
| 06:47:42 → 06:52:43 (6 cycles) | the backfilled row, 10 names, pulled with no agent restart | **1** | **1** | **1** |
| **Relinked to a fresh `b271-after`** (trigger default, created on the fixed code) | | | | |
| 06:53:43, 06:54:44 | `b271-after` | **1** | **1** | **1** |

- **Served config:** `GET /v1/agent-config/Bhargavtej` through the collector, with the agent's key, returned `agent_id` = `b271-after` with all 10 names.
- **`gpu` on the real Windows endpoint `Bhargav_tej`** (its demo row was backfilled to 9, `models` still off):
  - earlier today, **0 of 72** reports had `gpus`; after 000025, **7 of 8** (the exception is the first cycle before the pull);
  - every steady-state report now has 1 GPU;
  - `local_models` stays `null`, so the B-194 exclusion is preserved.
- **The 400, live:** `PUT … {"enabled_scanners":["GPU"]}` and `["scheduled_tasks"]` returned 400 with the valid list. `["gpu","browser"]` returned 200.

**Cleanup:**
- Both test agents were deleted through the API (204), and the WSL endpoint is unlinked.
- The WSL package was purged, with its fixtures and process removed.
- The collector test key was revoked, and the scratch password, token and key deleted.
- No orphan `agent_configs` rows remain.
- **Left in place, for the founder:**
  - The fixture user `b271-admin@fixture.local` couldn't be deleted: 4 `agent_lifecycle_events` rows (the create and delete of the two test agents) reference it.
  - Deleting audit-type rows in the real Dev Org wasn't done unilaterally. The user can't be used: its random password was deleted and its refresh tokens are gone.
  - The unlinked `Bhargavtej` test endpoint also remains, like earlier fixtures.

## Found during, not fixed (pre-existing)

- **The default `model_scan_paths` Windows entry is stored as `C:\\Users`** (two backslashes). The SQL literal `'C:\\Users'` in the baseline default is literal under `standard_conforming_strings`, and the API serves it as-is. Windows tolerates the doubled separator, so the walk still happens; this is the B-194 trigger. The Go default is `C:\Users`, so there's also a drift. Belongs with B-194/B-277.
- **`api/openapi.yaml` has no schema for agent config** (`GET`/`PUT /v1/gateway/agents/{id}/config`, the remote route). This is contract drift for Architect-EAMI.
- **On Windows, `null` for `ai_processes`/`python_envs`/`node_projects` is still ambiguous**: there's no command-line capture there, and B-193 applies. That is why the WSL fixtures were the discriminating proof.

## Reviews

The first attempts at both passes were cut off by a rate limit before producing anything; both were rerun in full.

**Code review.** No blocking, HIGH or MEDIUM findings.
- **Verified:**
  - migration correctness: correlated append, canonical order, idempotent, keeps other choices;
  - the down migration is acceptable;
  - `AllScanners` matches `builder.go` exactly;
  - no existence oracle (the 400 depends only on the body; cross-org still gets B-232's 404);
  - no caller mutates `AgentConfigDefaults`;
  - the UI enum and checkboxes;
  - no missed consumer;
  - the `schema.sql` mirror.
- **Fixed:**
  - LOW: an empty-array row (which means "all" to the agent) would have been narrowed to 4 names. The backfill now requires `cardinality(enabled_scanners) > 0`, and a migration-test case pins it. The dev DB had no empty rows (0 of 13), so the version already applied there is equivalent.
  - LOW: the aliasing test's comment was wrong, and its restore of the shared global only ran on success. The restore now runs in `t.Cleanup`.
- **Accepted, LOW or nit:**
  - a NULL element blocks the append (no writer produces one);
  - the UI list is hand-synced (commented; no OpenAPI contract exists);
  - duplicate names are accepted (harmless);
  - `AllScanners` is an exported mutable var (nothing mutates it).

**Security review.** Nothing blocks B-271: no HIGH findings and no injection, tenancy or oracle issues.
- MEDIUM (privacy volume, not a new kind of data): linked endpoints now send `ai_processes` and the others every cycle, instead of once per restart. That matches unlinked endpoints. **Recorded on B-279:**
  - sequence it alongside or right after B-271;
  - pair it with an `endpoint_reports` retention decision (the table has none);
  - fix the pre-first-fetch full scan after every restart, because remote config applies only after the first send.
- The echoed `%q` name in the 400 is safe: JSON-escaped, not logged, and the valid list is already public in the UI bundle.
- The backfill across all orgs is fine (no RLS or `org_id` involved). Governance note: it widens collection for every tenant under founder decision D1, and deserves a release-note line.
- The empty-array edge case and test hygiene are the same two LOW findings as the code review, both fixed.
- **Pre-existing, LOW:** there is no `MaxBytesReader` on `UpdateAgentConfig`'s body.

**After the fixes:** the migration tests (B-271 plus schema parity) pass, and `go vet` plus all agent-config API tests (including B-232's cross-org and oracle tests) pass.
