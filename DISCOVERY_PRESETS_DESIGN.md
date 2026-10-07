# Discovery presets and the Agent-Based Discovery Hub page — design record

**Item:** AI ITAM master sequence item 8, **B-269**. **Status:** design record (founder, 2026-10-05). Part A investigation follows (`B-269_PART_A_INVESTIGATION.md`); **no build until the founder approves Part A.**
**Supersedes,** in B-269's original 2026-09-30 entry and in `DISCOVERY_ADMIN_INVESTIGATION.md` Part 1B:
- the separate "Discovery setup" navigation destination. The page is now **Admin › Discovery Hub › Agent-Based**, the placeholder built in B-252 C2.
- "`discovery_preset_id` NULL means the org default". Now **every endpoint is bound to exactly one preset**, the default is only a package-builder pre-selection, and no assignment means no config.

The design below is recorded verbatim as given.

---

## 1. PRINCIPLES
- Every endpoint is bound to exactly one preset. Installed endpoints get it through the enrollment key in their installer. The org default (star) is only what the package builder pre-selects, never a fallback.
- Two kinds of configuration. BOOTSTRAP is set before the package is built: control-plane URL, enrollment key, CA cert. PUSHED is set from the GUI after deployment: scanners, interval, model scan paths, model minimum size, report size cap. Signed installers never change.
- "Push" means publish and pull. Agents already fetch before every scan (8a), so a published change lands within one scan interval. No inbound connections to customer networks.
- Config follows the endpoint's preset, not the governed-agent link. The link stays for governance identity only. This ends the "Configure" confusion; B-270 retires that action.

## 2. MODEL (names indicative; Part A confirms)
- preset: id, org_id, name, description, builder_default flag, archived_at, timestamps.
- preset_version: preset_id, version_number (v1, v2...), content (scanners list, interval, model_scan_paths, model_file_size_mb, max_report_size_bytes), config_version hash (same canonical hash the agent verifies), status draft | published | superseded, published_by, published_at, note. Published versions are immutable. At most one draft per preset.
- endpoint_preset_assignment: endpoint_id, preset_id, source (enrollment | adopt | change), enrollment_key_id nullable, assigned_by, assigned_at. Keep history.
- enrollment_key: id, org_id, preset_id, name, hash, prefix, expires_at, max_uses nullable, uses, revoked_at, created_by.
- Config delivery resolves endpoint, then preset, then the published version. A draft is never served. No assignment means no config (404), which the agent handles.

## 3. LIFECYCLE
- Edit changes the draft only. Concurrent edits: optimistic lock, a second admin gets a conflict, never a silent overwrite.
- Publish first shows an impact panel: how many endpoints are bound, how many are on agents 1.3.0 or newer (apply whole config) versus older (merge only: can narrow scanners but cannot re-enable or clear paths), a diff against the published version, and warnings (all scanners off, interval at the floor, paths added). Publish requires confirmation and an optional note.
- Revert means republishing an old version as a NEW version number. History stays linear.
- Server-side validation uses the same bounds as the agent (the B-293 and B-277 slice). Share test vectors. Reject with specific codes.
- Archive hides a preset from the package builder; blocked from deletion while endpoints are bound.

## 4. PAGE: Admin › Discovery Hub › Agent-Based
- Strip: "N endpoints are unmanaged (installed before presets) [Adopt]".
- Presets table: name (star = builder default), scanners x of 10, interval, version (draft badge if unpublished edits), endpoints, rollout, last published (date, who). Never empty: seed "Standard" with all 10 scanners, normal interval, EMPTY model scan paths (avoids the B-194 over-collection of the old default row).
- Rollout column: applied / behind / rejected / unknown, from each endpoint's latest report (server-time latest per B-284) and its config_version, config_source and config_error. Clicking a bucket opens Assets filtered to those endpoints. Unknown will be large for a long time (agents older than 1.3.0); say so in the UI.
- Sections: Presets | Deployments (enrollment keys) | Package builder.

## 5. EDITOR
- Name, description. 10 scanner toggles with honest notes from the audit: heavier scanners marked; Linux network_activity "not functional yet" (B-272); user-profile scanners may be blind under SYSTEM/root (B-193).
- Interval with floor and ceiling and quick picks.
- Models parameters appear only when models is on: scan paths with B-277 limits enforced from day one (absolute only, no UNC, count cap, filesystem roots rejected), minimum size, and the plain statement that only file name, path, size and modified time leave the machine.
- Privacy note on ai_processes and mcp_servers: command lines and arguments may contain secrets and are sent every cycle (B-279).
- Report size cap with the explanation of the too_large marker.

## 6. DEPLOYMENTS AND PACKAGE BUILDER
- A deployment = one enrollment key: name, preset, expiry (default 30 days), optional max installs, revoke. Raw key shown once, then prefix only. The list shows uses, expiry, status and the endpoints it enrolled.
- Builder: choose preset (star preselected), platform (Windows MSI, .deb, .rpm, macOS .pkg), and tool (Intune/SCCM, Jamf, Ansible, manual silent install). Output is a bundle: the existing signed installer UNCHANGED plus parameter files and command snippets for that tool, with control-plane URL, enrollment key and CA cert. Never rebuild or re-sign per preset.
- The enrollment key is exchanged once at first registration for a per-endpoint credential. Part A confirms how the collector's existing per-agent keys fit.
- macOS: mark "unverified" in the builder until B-274 is resolved.

## 7. ASSIGNMENT AND ADOPTION
- Unmanaged endpoints (deployed before presets): receive no config and change nothing until an admin adopts them.
- Bulk adopt from the unmanaged strip: multi-select, choose preset, confirmation ("this changes what N endpoints collect"), one audited event with the count.
- Single-endpoint change of preset lives on Endpoint Detail (B-270).
- Unassign: server answers no config; agent reverts to local after two consecutive 404s (D-c).
- Migration: each existing agent_configs row becomes a preset ("Migrated: <agent>"); identical contents merge into one preset. Endpoints already linked and receiving config are assigned to it, so behavior does not change at migration. Carried paths that B-277 would reject are kept as-is but flagged "needs review" on the preset.

## 8. SECURITY
- Admin-only to create, edit, publish, archive, issue keys, assign (expansion under B-253). Server-side enforcement.
- Every endpoint is org-scoped; cross-org adversarial tests (B-232, B-233 standard) on every route, including assignment and key exchange.
- Enrollment key: hashed, expiring, rate-limited, revocable, org-bound.
- All mutations audited. Part A checks how admin actions are audited today (B-224 is queued); do not invent a parallel trail.
- Step-up (B-231) is not built; publish and key issuance are named candidates when it exists.
- Standing checks apply: no raw error text, orphaned-link check, drift rows in the same commit.

## 9. API (per API_CONVENTION.md; names indicative)
Presets CRUD with rollout summary; draft GET/PUT; publish; versions list; revert; archive; enrollment keys create/list/revoke; bundle generation; assignment (bulk); extend the existing CMDB endpoint filter with preset and managed filters rather than add a parallel endpoint list.

## 10. BUILD SLICES (each shippable, in order)
- Slice 0 prerequisite: B-277 path allowlist and depth limit.
- Slice 1 backend: schema, migration, endpoint-keyed config delivery, assignment, validation, API, tests. Highest risk.
- Slice 2 UI: preset list, editor, draft/publish/revert, rollout summary.
- Slice 3: enrollment keys, deployments, package builder, bundles.
- Slice 4: bulk adopt and unmanaged strip, then B-270 (Endpoint Detail) and retiring Configure on Agent Detail.

## 11. DEFERRED
Group-based assignment (item 6), canary or percentage rollout, scheduled publish, per-endpoint overrides, two-person publish approval, reusing the versioning pattern for probe settings (item 12): reuse the approach, do not abstract until the second use.

---

## 12. FOUNDER DECISIONS ON PART A (2026-10-05)

Part A (`B-269_PART_A_INVESTIGATION.md`) was approved, with D1–D10 as follows. Where these differ from §1–§11, they win.

- **D1, the 7a gate:** acknowledgement of the drift hand-off is **waived for Slices 0–1** and **required before Slice 2**. The hand-off itself is the founder's action.
- **D2, admin audit:** a new **Slice 0b**, before Slice 1. It is a **minimal, generic, append-only admin audit trail**:
  - fields: org, actor, action code, target, change summary, time;
  - wired **only** to presets, enrollment keys and assignment;
  - it **never records key values or secrets**;
  - it reuses the existing hash chain if that's cheap; otherwise the docs say plainly that it is **not tamper-evident**.
  - B-224 adopts the rest later.
- **D3, org binding:** single-org through Slice 3. **Key issuance is refused for any org other than the default one until B-243 is fixed**, with a test.
- **D4, endpoint identity** (Slice 3; **security review required**):
  - a **server-issued endpoint identity**;
  - reports are attributed by the **minted credential**, not by the ID in the payload;
  - the credential is stored where the existing key is stored, with the same permissions.
  - **Endpoints already merged by a hostname collision can't be un-merged** (B-295).
- **D5, enrollment key:** the `eami_e_` prefix in the **existing key field**. It is accepted **only on the exchange route** and **rejected for ingest**.
- **D6, Configure:** **disable** Agent Detail's Configure **in Slice 1**, with a message pointing at presets. This is a **deliberate removal of a reachable action**, and the Slice 1 commit says so.
- **D7, rollout data:**
  - The latest config fields are stored **on the endpoint row**, in the **same transaction as the report insert**.
  - They are updated **only if the report is the latest by receive time** (B-284).
  - They are **backfilled in the migration**.
  - A **stored-versus-computed equality test** checks them.
- **D8, migration:**
  - Migrate **only rows with linked endpoints**, plus a **Standard** preset in **every** org.
  - Migrated presets **keep today's behaviour**, flagged **"needs review"** with the B-194 warning.
  - **New presets never default to those paths.**
- **D9, path rules:**
  - **Server-side:** reject relative paths, UNC paths, **filesystem roots** and **whole-profile parents** (`/home`, `/Users`, `C:\Users`).
  - **Agent-side:** a **fixed walk-depth limit** and **true-root rejection**.
- **D10, error codes:**
  - **Stable codes in a JSON error body**, with **one shared fixture file** for both Go modules.
  - A code may name a field from a fixed list; it **never echoes a value**.
  - The codes are added to `API_CONTRACT_DRIFT.md`.
- **Minted: B-295.** A hostname collision merges two machines into one endpoint.
- **Slice order is now 0, 0b, 1, 2, 3, 4.** **Slice 0** is B-277's path allowlist and depth limit, **plus the B-194 file-type filter**. Its plan is `B-269_SLICE0_PLAN.md`.

### Slice 0 decisions S1–S7 (founder, 2026-10-05), and the D4 correction

**D4 correction (supersedes D4's "stored where the existing key is stored"):**
- The minted per-endpoint credential does **NOT** go in the existing key field (MSI property / registry value, `EAMI_COLLECTOR_API_KEY`) or in the admin's YAML (`/etc/eami/agent.yaml`). The agent never rewrites the YAML (B-278 #4, B-293).
- It goes in its **own credential file in the B-293 state directory**:
  - the same ancestor-chain and junction/symlink checks as the state file;
  - mode **0600** (Unix) or a **restricted protected ACL** (Windows: SYSTEM, Administrators, the agent's own user);
  - an **atomic write**.
- The agent **never logs it, reports it, or returns it in any API response.**
- Windows enrollment stays under the 8a live-service gate.

**S1–S7:**
- **S1:** keep `.bin` in the extra-path allowlist. Editor help text must say that **`.bin` can match non-model files**.
- **S2:** a new `source` value, **`scan_path`** ("Configured path"), for extra-path hits. **Every** consumer of `source` must handle it: the UI label map, Lineage, CMDB queries and Endpoint Detail. The orphaned-link check applies.
- **S3:** walk depth limit **8**. On a hit, record a short **`depth_limited`** reason. **Never silent.**
- **S4:** **empty** default model paths for newly created agents. Follow-up minted (**B-296**) to clean stored configs still carrying `/home`, `/Users`, `C:\Users`, using the `B-269_PART_A_INVESTIGATION.md` / `B-293_VERIFICATION.md` §6 query.
- **S5:** the new path rules apply on the old Configure route **only when the paths change**. Legacy paths stay accepted and **flagged**.
- **S6:** see the D4 correction above.
- **S7:** verify on a **disposable VM** whether `HKLM\SOFTWARE\EAMI\Agent` (which holds the collector key) is readable by local users. The consequence is recorded on B-293: if it is, **a standard user can forge reports as that agent**. If confirmed, it is rated **Medium** and minted separately.
- **Untouched by Slice 0:** B-282 (blank agent version) and B-294 (MSI state folder).

### Path rules: who is the authority (founder, 2026-10-06)

**Server-side path rules are a first filter only. The agent is the authority on what it walks.**
- The server checks a path's *shape*: relative, UNC, roots, non-normal forms, whole-profile parents. That lets it refuse obviously bad config early, with a clear code for the admin.
- **The server cannot resolve paths on a remote machine.** It can't know that `/srv/models` is a symlink to `/`, that a folder is a junction to `C:\Users`, or that a path is an NFS mount.
- **The agent resolves links before walking** (B-269 Slice 0, `models.resolveScanRoot`) and refuses a target that is a root or a network share. It also enforces the depth limit, the file-type filter and the deadline on the machine itself.
- **Design consequence for Slices 1–4:** never present a server-side "accepted" as a guarantee of what an endpoint will walk. The preset editor and impact panel describe paths as *requested*. What an endpoint actually did comes from its reports (`scanner_notes`, `config_error`).

### Enrollment keys bind to a preset, never a package (founder, 2026-10-07)

An enrollment key binds a machine to a preset, never to a package type, so the same key works from the standalone discovery installer and from the chat bundle. Nothing in Slices 1-4 may assume the package type. (Context: B-130, `CHAT_APP_AND_DISCOVERY_BUNDLE_DESIGN.md`; B-305 merged into B-130.)

### Slice 1 decisions (founder, 2026-10-08)

Plan: `B-269_SLICE1_PLAN.md`. K2, K3, K4 and K11 are accepted, with conditions:
- **K2, hash:** computed in Go and cached per version.
- **K3, versions:**
  - published versions are blocked from update or delete at the database level;
  - unique (preset, version), and one draft per preset;
  - the version number is allocated under a per-preset lock inside the publish transaction.
- **K4, the seed writes no audit events:** the migration's presets and assignments, and the Standard preset seeded for every new org, are system actions recorded in the migration and `BUILT.md`, not in `admin_audit_events`. Otherwise every org, including test orgs, would become undeletable (B-224). A test checks it.
- **K11, legacy paths:** reverting to a legacy-path version is a path change, so it's refused. Editing a migrated preset's other fields with its legacy paths unchanged stays allowed and flagged (S5).
