# B-269 (item 8): Discovery presets — Part A investigation

**Date:** 2026-10-05 · **By:** Claude Code · **Read-only.** No code changed. **No build until the founder approves.**
**Design:** `DISCOVERY_PRESETS_DESIGN.md`. **Read:** the master sequence, `DISCOVERY_ADMIN_INVESTIGATION.md`, `B-293_VERIFICATION.md`, `API_CONVENTION.md` (item 7 not locked; followed anyway), `DESIGN_SYSTEM.md` §7.7/§7.8, and B-269, B-270, B-277 and B-274.
**Sequence check:** 8a is `[x]` and item 8 is next. **Item 7a's gate is open**: the drift hand-off to Architect-EAMI has no recorded acknowledgement. The master sequence says that gate comes before item 8 starts. Design and Part A are fine, but Slice 1's build should wait for the acknowledgement (§7, C1).

---

## 1. Enrollment and endpoint identity today

**How an installed agent first "registers":**
1. An admin runs `collector mint-key --agent-id <id>` on the collector host. That's a CLI only; there is no API.
   - The key is random, stored **SHA-256-hashed** in the collector's own SQLite (`api_keys`: `id`, `key_hash`, `label`, `agent_id`, `created_at`, `revoked_at`).
   - **One active key per `agent_id`** (`RegisterKey`). Reinstalling needs `revoke-key` first.
2. The raw key goes into the installer: MSI property, env var or Jamf parameter.
3. The agent sends reports with `X-API-Key`. The collector's `APIKeyMiddleware` accepts either:
   - a per-agent key, which binds the request to that `agent_id` (ingest and config proxy reject a mismatched `agent_id`, B-073); or
   - a legacy **static fleet-wide key**, with no binding.
4. The collector forwards to the API with **one global service key**. The API resolves **every** ingest and config request to `GetDefaultOrgID`, which is single-org (B-243; the B-236 context).
5. **The endpoint row is created on first ingest:** `endpoints` is unique on `(org_id, agent_id)`.

**What identifies an endpoint:**

| ID | What it is | Trust |
|---|---|---|
| `endpoints.id` | Server UUID, created at first ingest | The only server-issued identity, but **the agent never learns it** |
| `endpoints.agent_id` | **Agent-reported** free text: config `agent.id`, else the hostname | Self-asserted. Two machines with the same hostname **collide onto one endpoint row**. |
| `gateway_agent_id` | Admin-set link to a governed agent | Governance only; after B-269 it no longer drives config |

**How an enrollment key exchange fits.** There are two sides, and the flow crosses both:
- The collector is on-prem and holds per-agent keys in SQLite.
- The API is SaaS and would hold presets and enrollment keys in Postgres.

Proposed flow:
1. The agent presents the enrollment key once, to a new collector route (`POST /v1/enroll`).
2. The collector forwards it to a new API route (service key) **with the agent's machine fingerprint**.
3. The API validates the key's hash, expiry, uses, revocation and org. It creates (or matches) the endpoint in **the key's org**, writes the assignment (`source=enrollment`), and returns a **server-issued endpoint identity**.
4. The collector mints a per-endpoint key bound to that identity, which needs a programmatic version of `mint-key`.
5. The agent stores the credential in its **B-293 secured state directory**, never in the YAML or registry, and uses it from then on.

**Findings that need decisions:**
- **E1:** enrollment should issue the endpoint identity (an `endpoints.id`-derived `agent_id`) instead of trusting the hostname. Otherwise a hostname collision silently merges two machines, and a re-install hits "one active key per agent_id".
- **E2:** the org binding from the enrollment key reaches the API only at enrollment time. **Ingest and config delivery still resolve the default org.** For enrollment keys to be truly "org-bound", the per-endpoint credential (or the endpoint identity) must carry the org through the collector, which overlaps B-243. Single-org deployments work without it; multi-org on one collector does not.
- **E3:** the per-endpoint credential lives in the state directory, so the **Windows live-service gate** (B-293) now gates **Windows enrollment** too.

## 2. Installer parameters per OS

| Platform | Today | Enrollment-key need |
|---|---|---|
| **MSI** | Public properties `COLLECTOR_URL` (default `http://localhost:8888`), `COLLECTOR_API_KEY`, `COLLECTOR_CA_CERT_PATH`, written to `HKLM\SOFTWARE\EAMI\Agent`. Intune/SCCM pass them on the `msiexec` line. | **No installer change** if the enrollment key travels in the existing key field with a distinct prefix (`eami_e_…` vs `eami_k_…`) and the agent detects it. Otherwise a new `ENROLLMENT_KEY` property means one rebuild and re-sign of the MSI (once, not per preset). |
| **deb / rpm** | `EAMI_COLLECTOR_URL`, `EAMI_COLLECTOR_API_KEY`, `EAMI_COLLECTOR_CA_CERT_PATH` env vars at install. `postinstall.sh` writes `/etc/eami/agent.yaml` (root, with CA-path ownership checks). | Same: the prefix in the existing variable means no package change. Ansible: env lines. |
| **pkg (macOS)** | Jamf `$4`/`$5`/`$6`, or env vars. **B-274 open:** values may never reach `postinstall` (`sudo VAR=… installer` drops env). | Mark it "unverified" in the builder, as the design says. |

- **Recommendation:** reuse the existing key field with a prefix. Zero installer changes on all four platforms.
- **Not changed by the bundle:** the MSI's `localhost` default URL. Linux had the same fallback and it was removed in B-273; the MSI default stays. Every bundle must always set the URL. Flagged, not fixed.

## 3. Config delivery today and the `agent_configs` migration

**Delivery:** agent → collector `GET /v1/agent-config/{agent_id}` → API `GET /v1/agents/{agent_id}/config` → **default org** → `endpoints(org, agent_id)` → `gateway_agent_id` → `agent_configs`. Unlinked endpoints get 404.

**The data, measured on the dev DB:**

| | |
|---|---|
| `agent_configs` rows | **13**, in **4 orgs** |
| Distinct contents | **2** |
| Dev Org | 10 rows; 2 contents; **2 rows with a linked endpoint** |
| 3 other orgs (leftover test fixtures: b121, b124, b169) | 1 row each; **no endpoints** |
| Endpoints | 8, all in Dev Org; **2 linked** |

The two contents:
- **A:** 300 s, all 10 scanners, paths `/home`, `/Users`, `C:\\Users` (doubled backslash, B-277), 100 MB, 5 MiB. 12 rows.
- **B:** 60 s, 9 scanners without `models`, the same paths, 100 MB, 5 MiB. 1 row (`bhargav-demo-endpoint`).

**What the migration as designed would create** (merging per org, since presets are org-scoped):

| Org | Presets created | Endpoints assigned |
|---|---|---|
| Dev Org | "Migrated: …" ×2 (A, B) + "Standard" | `Bhargav_tej` (agent_id `Bhargav_tej`) → B; `Bhargav_tej` (agent_id `b164-b165-live-agent`, last seen 2026-09-04) → A |
| b121 / b124 / b169 | 1 each + "Standard" | none |

**No behavior change for the linked endpoints, confirmed:**
- Each assigned preset's v1 content equals its `agent_configs` row, so the served config, and so its `c1` hash, is identical.
- Both linked endpoints run an **agent older than 1.3.0** (their latest reports carry no `config_source`). They merge anyway, so they see no difference.
- The 6 unlinked endpoints get 404 before and after ("unmanaged").

**Things to decide (§7):**
- **M1:** 8 of Dev Org's 10 rows, and all 3 other orgs' rows, have **no linked endpoint**. "Each row becomes a preset" would create unused "Migrated" presets, including in fixture orgs.
  - **Recommendation:** migrate only rows with at least one linked endpoint (here, 2 presets in Dev Org).
  - Seed "Standard" in every org.
- **M2:** both migrated contents carry `/home`, `/Users`, `C:\Users`. None of these is a filesystem root, so a "roots rejected" rule would **not** flag them, yet they are the B-194 over-collection default. "Needs review" should be driven by the B-277 rules Slice 0 actually adopts (§6), plus a specific check for this exact legacy default.

**Paths B-277 would reject:** with the current B-293 bounds (shape only), none. With a roots denylist (`/`, `X:\`), none. With an allowlist, it depends on the list (§6).

## 4. Role gating and admin audit today

**Role gating:**
- `/admin` and all its tabs render for **every role**. The UI does no page or tab gating.
- The real gate is server-side: `requireRole("admin")` on `/v1/settings/*` and `/v1/users/*`, and B-253's admin-only writes.
- The Discovery Hub tab must follow the B-252 C0 pattern: `can.*` helpers in `lib/rbac.ts`, with server enforcement as the source of truth.

**Audit of admin actions:**
- There is **no durable admin-write audit trail.** `audit_log` is the gateway tool-call chain.
- CMDB writes emit only `slog.Info("cmdb taxonomy changed", org_id, user_id, action, target_id)`. Users, settings and licence writes don't even log.
- **B-224** ("durable admin-write audit trail") is QUEUED, **not investigated**.
- **Conflict A1:** the design's "all mutations audited; do not invent a parallel trail" can't be met today. Options:
  - (a) Do B-224's Part A and a minimal build **before or inside Slice 1**, so presets are the first writer.
  - (b) Ship Slice 1 with structured `slog` like CMDB, and accept a gap until B-224.
  - **Recommendation: (a).** Publishing, assignment and key issuance change what fleets collect; they need a durable trail on day one.

## 5. Rollout buckets: what can be computed, and what it costs

**Computable from existing data** (the latest report by server `received_at`, B-284, plus the preset's published hash):

| Bucket | Rule |
|---|---|
| **unknown** | No report, or the latest report has **no** `config_version` key (an agent older than 1.3.0) |
| **rejected** | `config_error` is a rejection code (anything except `fetch_failed`) |
| **applied** | `config_version` equals the preset's published hash |
| **behind** | Any other versioned state, including `persisted` with `fetch_failed` (unreachable) and a version that is still the old one |

- **"1.3.0 or newer" for the impact panel:** `agent_version` is blank for every endpoint (**B-282**), so it can't be counted by version. The **presence of `config_version`** in the latest report is a reliable proxy, since only 1.3.0+ agents send it.

**Cost:**
- **Dev scale:** the bucket query (a LATERAL latest report per endpoint, using `idx_reports_endpoint_received`) ran in **0.53 ms** for 8 endpoints (41 buffer hits). There are 5,170 reports, 4.6 MB of JSONB, and the largest is 195 KB.
- **Expected scale: this doesn't scale as written.** `report->>'config_version'` de-TOASTs the **whole** latest report: up to 1.27 MB in the B-293 size test, and tens of KB is typical with many models.
  - For 10k endpoints, one rollout summary could decompress hundreds of MB, and the presets table shows it per preset.
- **Recommendation for Slice 1:** ingest writes the three fields onto the **`endpoints` row** (`applied_config_version`, `config_source`, `config_error`, and their `received_at`). Update only when the incoming report is the latest by server `received_at`, to keep B-284's rule.
  - Rollout then becomes a `GROUP BY` on `endpoints` joined to the assignment and published version, with an index on `(org_id, preset)`.
  - `GET /v1/endpoints/{id}` (B-293) can read the same columns.

## 6. Can B-277's bounds be shared as common test vectors?

**Yes, with two changes:**
- **Today they're duplicated** as constants and as golden-hash literals in two test files (`eami-api/internal/store`, `eami-agent/internal/remoteconfig`).
  - Both Go modules live in one repo, so one fixture file (for example `testdata/agent_config_vectors.json`: a config, plus the expected `c1` version or the expected **reason code**) can be loaded by both test suites through relative paths.
- **API change:** the agent rejects with **reason codes** (`path_network`, …), but the API's PUT returns `bad_request` plus a **message**. To share vectors, and to meet the design's "reject with specific codes", the API should return the same reason code in the error `code`.
  - This is an API change: a drift row for C18, and the UI must map the codes.
- **The UI can't run the vectors:** there is no frontend test framework. The editor's client-side checks would mirror them, with the server as the authority.
- **Slice 0 decisions for B-277:**
  - **B1, the shape of "allowlist":** B-277 proposed an **endpoint-local** allowlist or opt-in (defense even if the server is compromised). The design's editor rules are a **server-side denylist** (absolute, no UNC, a count cap, filesystem roots rejected).
    - A local allowlist is a **bootstrap** setting: installer and bundle, and it touches the package builder. A denylist is pure validation.
    - **Recommendation:** the server-side denylist plus an agent-side root rejection in Slice 0, with an endpoint-local allowlist as an optional bootstrap parameter later.
  - **B2, depth limit:** needs agent code in the `models` scanner's walk. It can be a fixed constant (simplest, no wire change) or a preset field (a new field, and the hash covers it).
    - **Recommendation:** a fixed constant in Slice 0.
  - **B3, which "roots":** `/`, `X:\`, `X:/` for certain. Do `/home`, `/Users`, `C:\Users` count? They are exactly the B-194 default.
    - **Recommendation:** reject true roots, and warn (not reject) on whole-profile trees in the publish impact panel.

## 7. Conflicts with code reality (flagged, not worked around)

| # | Design says | Reality | Needs |
|---|---|---|---|
| **C1** | Item 8 builds now | Master-sequence **7a**: the drift hand-off to Architect-EAMI must be **acknowledged before item 8 starts**; no acknowledgement is recorded | The founder confirms the hand-off is acknowledged, or waives it for Slice 0/1 |
| **C2** | "All mutations audited; no parallel trail" | No admin-write trail exists; **B-224** is uninvestigated | Decision A1 (§4) |
| **C3** | The enrollment key is org-bound | Ingest and config resolve **`GetDefaultOrgID`**; one global service key (**B-243**) | Accept single-org for now, or bring B-243 forward (E2) |
| **C4** | Exchange for a per-endpoint credential | The collector mints keys **only via CLI**, one per **self-asserted `agent_id`**; hostnames collide | A collector enrollment route plus a programmatic mint; a server-issued identity (E1) |
| **C5** | Configure retires in **Slice 4** (with B-270) | Once Slice 1 switches delivery to presets, Agent Detail's **Configure** edits `agent_configs`, which **nothing reads any more**: a misleading action for three slices (the standing orphaned-action check) | Retire or disable Configure **in Slice 1** (or pointer text to presets), not Slice 4 |
| **C6** | Impact panel counts agents ≥ 1.3.0 by version | `agent_version` is blank everywhere (**B-282**) | Use the presence of `config_version` (§5), or fix B-282 first |
| **C7** | Rollout from each latest report | Reads whole TOASTed reports and won't scale (§5) | Denormalize onto `endpoints` in Slice 1 |
| **C8** | Migration: every row becomes a preset | 11 of 13 rows have no linked endpoint, including 3 fixture orgs | Decision M1 (§3) |
| **C9** | "Filesystem roots rejected" fixes B-277 | The migrated defaults `/home`, `/Users`, `C:\Users` aren't roots | Decision B3 (§6) |
| **C10** | Shared vectors with specific codes | The API returns messages, not codes | The API returns reason codes (§6); drift row |
| **C11** | Enrollment on Windows | The credential is persisted in the B-293 state directory, which is under the **Windows live-service gate** | Windows enrollment is gated with it |
| **C12** | "Unknown will be large" | Today **all 8** endpoints are unknown or pre-1.3.0, except the WSL test agent (now removed) | Confirms the design's warning; no change |

**Consistent with code, no change needed:**
- Endpoint-keyed delivery fits the existing route.
- B-293's agent already handles 404, versioned replace, persistence and reporting.
- CMDB-filter extension (`preset_id`, `managed`) follows `API_CONVENTION.md` principle 1.
- The B-252 C2 placeholder is exactly where the page goes.
- "Standard" with empty paths avoids B-194.
- The signed installers stay unchanged, if the prefix approach in §2 is taken.

## Decisions for the founder

| # | Decision | Recommendation |
|---|---|---|
| D1 | 7a gate (C1) | Confirm acknowledged, or waive for Slices 0–1 |
| D2 | Admin audit (C2) | B-224 Part A plus a minimal trail before or inside Slice 1 |
| D3 | Org binding (C3/E2) | Single-org in Slices 1–3; schedule B-243 before multi-org enrollment |
| D4 | Endpoint identity (E1/C4) | Enrollment issues a server-side identity; programmatic collector minting |
| D5 | Installer field (§2) | Reuse the existing key field with an `eami_e_` prefix, so no installer rebuild |
| D6 | Configure (C5) | Retire or disable it in Slice 1 |
| D7 | Rollout data (C7) | Denormalize the latest config fields onto `endpoints` in Slice 1 |
| D8 | Migration scope (M1) | Only rows with linked endpoints, plus "Standard" everywhere |
| D9 | B-277 shape (B1–B3) | Server denylist plus agent root rejection; fixed depth constant; warn on whole-profile trees |
| D10 | API reason codes (C10) | Return the agent's reason codes; shared JSON vectors in `testdata/` |
