# AI ITAM Epic — Master Sequence

**This is a living document. Code maintains it directly, the same way
CONTEXT.md's ACTIVE AGENT marker is self-maintained — never re-pasted
from chat again after this initial commit.**

**Rule:** before starting any item below, confirm the one directly
above it is `[x]`. When an item completes, change its own checkbox to
`[x]` and add a one-line date + commit hash, as part of that item's own
completion commit — not a separate housekeeping step. No new item gets
added to this file without being placed in the correct phase, matching
its real dependency.
An item marked DEFERRED does not block the item below it, but must be
resolved before the deferred condition changes (e.g., before Linux is
claimed customer-ready).

Related detailed design records, referenced by name, never pasted
inline: `AI_LLM_SERVICE_MAPPING_DESIGN.md`,
`DYNAMIC_ASSET_GROUPING_EPIC.md`, `DISCOVERY_ADMIN_INVESTIGATION.md`.

**Epic B-ID:** **B-280** — the AI ITAM program as a whole (`BACKLOG.md`).

---

## The standing, cross-cutting rule

**Every new data type this epic produces gets a real, general,
filterable, versioned API endpoint — not a narrow endpoint built only
for whatever UI screen currently needs it.** This directly expands
B-137's real scope. Reuse CMDB's own established convention
(`GET /v1/cmdb/assets`'s real filter/pagination shape) as the reference
pattern. Item 7 below locks the exact convention before Phase 2 builds
anything.

---

## Phase 1 — Live bugs (in progress, no dependency on anything below)

- [x] 1. **B-273** — packaged Linux/macOS agents never read their config
      file. Done 2026-09-30 — `6d64aaa` (first fix `fa4a221`; reopened for
      RPM upgrades, re-closed in `6d64aaa`).
- [x] 2. **B-271** — linking an endpoint silently disables 4 of 10
      scanners. Done 2026-09-30 — `b138ff7` (migration 000025,
      live-verified; `B-271_VERIFICATION.md`).
- [ ] 3. **B-272** — `network_activity` non-functional on Linux.
      **DEFERRED** (2026-09-30): no active Linux customer deployment yet -
      revisit and close before Linux is presented as customer-ready in any
      external context. Does not block item 4.
- [x] 4. Honest-state gap — "0" renders identically for "reported, found
      nothing" vs. "never reported." Done 2026-09-30 — `2fa8b0a`
      (`scanner_status` + `has_report`; `ITEM4_HONEST_STATE_VERIFICATION.md`).
      **Then, immediately after item 4 (founder, 2026-09-30):** the B-252
      Admin rename + Endpoint Detail brief, with a minimal C3 folded in
      (Assets' endpoint rows gain OS, last seen and the per-domain counts).
      Discover stays live until that brief ships, and is retired only once
      C3's parity is confirmed live. Decisions are recorded in `CONTEXT.md`
      and B-252.
      **Handoff requirement (founder, 2026-09-30):** that brief must carry
      item 4's `scanner_status` (per-scanner ok / disabled / error in each
      report) and `has_report` into Endpoint Detail's design from the start:
      Never reported / Disabled / Scan failed / real count / Not known.
      It must not rebuild the "None detected" / bare-"0" ambiguity on the
      new page. **Also (founder, 2026-09-30):** show the agent-link control on
      an endpoint with no scan report. Don't hide it as the current drawer
      does; that's where manual linking is most useful.
      **Handoff brief done 2026-10-05** — `9e5dc02` (`B-252_C2_VERIFICATION.md`):
      - Endpoint Detail carries all five honest states.
      - The link control is shown on report-less endpoints.
      - Minimal C3 parity was confirmed live, and then Discover was retired.
      - Settings was renamed Admin.
      - The `GET /v1/cmdb/assets` extension is item 7's first real
        implementation (precedent in §6 of the verification record). Item 7
        itself stays open until its written convention is locked.
- [x] 5. Windows endpoint governed-agent link trace. Done 2026-10-05 — `429d791`
      (closed at founder direction). **No link bug:**
      - Both `Bhargav_tej` endpoints are linked in the database (`db2e7ea8…` → `bhargav-demo-endpoint`, `0392716a…` → `b164-b165-governed-liveverify`).
      - The Agent Link tab reads that link (`GET /v1/endpoints/{id}` → `gateway_agent_id`, rendered against the unpaginated agent list).
      - The original concern was the always-on "No automatic match exists…" note under a populated control. That copy fix is now in B-291.
      - Verified by a database and code trace, not a live browser.

---

## Phase 0 — Foundational decisions (must resolve before Phase 2 building starts)

- [ ] 6. **Dynamic Asset Grouping — schema resolution.** Extend B-207's
      Groups primitive with an optional rule definition. Full design in
      `DYNAMIC_ASSET_GROUPING_EPIC.md` (repo root; §2 is the schema
      decision this item resolves, and §5 is why it must precede B-269).
- [ ] 7. **API design convention — lock, don't assume.** Confirm B-137's
      real state, trace CMDB's exact endpoint shape, confirm real
      versioning practice. Deliverable: one written convention every
      Phase 2 item follows.
      **Convention drafted, awaiting founder lock (2026-10-05):** `API_CONVENTION.md`.
      - Principles 1–7 are copied from B-252 C2's §6.
      - Added: §8 sorting, §9 breaking changes.
      - Open at lock: OpenAPI-first or same-epic; deprecation length (deferred to B-137).
      - B-137 is a scoping placeholder with no design.
      - **Not ticked.**
- [ ] 7a. **Contract drift hand-off.** `API_CONTRACT_DRIFT.md` (3 documented operations that don't exist as documented, 54 undocumented routes, 17 field/response mismatches) **goes to Architect-EAMI before item 8 starts.**
      **The gate is hand-off plus acknowledgement, not completion.**
      - Item 8 may start once the file has been handed to Architect-EAMI *and* Architect-EAMI has acknowledged receiving it.
      - Fixing the drift in `api/openapi.yaml` is **not** a precondition for item 8. It proceeds on Architect-EAMI's own schedule.
      - New drift keeps being appended under `API_CONVENTION.md` §10.

---

## Phase 2 — Discovery admin design (build in this order, each satisfying items 6 and 7)

- [x] 8a. **B-293** — Agent applies remote config correctly: replace not
      merge, persisted, fetched before first scan, applied config reported
      back. Prerequisite for B-269's preset content and for B-270's
      "applied config" display. No dependency on item 6 (Groups).
      Done 2026-10-05 — `50f6a90` (agent 1.3.1; `B-293_VERIFICATION.md`).
      Rollout: agents older than 1.3.0 keep merging until they update.
      **Windows gate:** live verification of persisted config on a real
      Windows service is required before any Windows deployment relies on
      it; 8a's Windows path was verified by native unit tests only. Track
      on B-293 / B-294 (disposable Windows VM).
- [ ] 8. **B-269** — Discovery presets, group-based assignment,
      multi-group precedence resolved before build.
      Splitting: preset DEFINITION (name, version, scanner content,
      package generation) can be designed and built now - it has no
      dependency on item 6. Preset ASSIGNMENT to a fleet via Groups still
      waits on item 6's schema resolution. Do not build group-based
      assignment before item 6 closes.
      **Depends on 8a (B-293):** a preset is only real if the agent can
      apply it (replace semantics, persistence, first-scan fetch).
      **Design (founder, 2026-10-05):** `DISCOVERY_PRESETS_DESIGN.md`.
      Decisions D1–D10 (founder, 2026-10-05): design §12. Slices, each
      shippable, in order 0, 0b, 1, 2, 3, 4. Tick item 8 only when all
      are done:
      - [ ] Slice 0 (prerequisite): B-277 path allowlist and depth limit,
            plus the B-194 file-type filter (`B-269_SLICE0_PLAN.md`).
      - [ ] Slice 0b: minimal append-only admin audit trail (D2), for
            presets, keys and assignment only.
      Gate: the 7a drift hand-off acknowledgement is waived for Slices
      0–1 and **required before Slice 2** (D1).
      - [ ] Slice 1 (backend): schema, migration, endpoint-keyed config
            delivery, assignment, validation, API, tests.
      - [ ] Slice 2 (UI): preset list, editor, draft/publish/revert,
            rollout summary.
      - [ ] Slice 3: enrollment keys, deployments, package builder,
            bundles.
      - [ ] Slice 4: bulk adopt and unmanaged strip, then B-270 and
            retiring Configure on Agent Detail.
      **Sequencing (founder, 2026-10-05): B-277's path allowlist and
      walk-depth limit must land before item 8's preset content, or inside
      B-269's first slice.** Why:
      - B-269 puts a UI on `model_scan_paths`;
      - `/` is still accepted as a scan path (8a bounds only check shape);
      - since 8a, remote config persists across restarts, so a bad root
        walk would survive a restart too.
- [ ] 9. **B-270** — Endpoint Detail read-only effective-config view.
      **Depends on 8a (B-293):** "last config actually applied" needs the
      agent to report the config it is running.
- [ ] 10. Shadow-agent surfacing + "Onboard as Governed Agent" flow.
- [ ] 11. Model characterization (Ollama extension).
- [ ] 12. **B-267** — Discovery Probe + real Discovery Setup page.

---

## Phase 3 — Agent tab's own unresolved pieces

- [ ] 13. Resolve "Configure" placement (reconfirm the Q3 decision before
      Phase 2 touches this area).
- [ ] 14. **B-255** — real `scope`/`risk_tier`/`token_ttl_seconds` edit
      surface.

---

## Phase 4 — Only once Phase 2 and 3 are fully closed

- [ ] 15. CI-reconciliation identity keys investigation.
- [ ] 16. **AI/LLM Service Mapping** — the four-tier model, begins only
      once B-267 exists.
- [ ] 17. **B-279** — report data minimisation (redact command-line and
      MCP-arg secrets) plus an `endpoint_reports` retention decision.
      Prioritized within its own scope (since B-271, this data arrives
      every cycle and nothing limits retention), but placed here by founder
      decision 2026-09-30: it's a data-minimisation/retention concern,
      not a Phase 1 report-integrity gap.

---

**One active thread at a time, in this exact order. Nothing new gets
introduced outside this file until the current item closes.**
