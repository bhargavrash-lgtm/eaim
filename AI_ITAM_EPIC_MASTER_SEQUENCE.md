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

Related detailed design records, referenced by name, never pasted
inline: `AI_LLM_SERVICE_MAPPING_DESIGN.md`,
`DYNAMIC_ASSET_GROUPING_EPIC.md`, `DISCOVERY_ADMIN_INVESTIGATION.md`.
(`DYNAMIC_ASSET_GROUPING_EPIC.md` is not in the repo yet as of
2026-09-30.)

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
- [ ] 4. Honest-state gap — "0" renders identically for "reported, found
      nothing" vs. "never reported."
- [ ] 5. Windows endpoint governed-agent link trace.

---

## Phase 0 — Foundational decisions (must resolve before Phase 2 building starts)

- [ ] 6. **Dynamic Asset Grouping — schema resolution.** Extend B-207's
      Groups primitive with an optional rule definition. Full design in
      `DYNAMIC_ASSET_GROUPING_EPIC.md`.
- [ ] 7. **API design convention — lock, don't assume.** Confirm B-137's
      real state, trace CMDB's exact endpoint shape, confirm real
      versioning practice. Deliverable: one written convention every
      Phase 2 item follows.

---

## Phase 2 — Discovery admin design (build in this order, each satisfying items 6 and 7)

- [ ] 8. **B-269** — Discovery presets, group-based assignment,
      multi-group precedence resolved before build.
- [ ] 9. **B-270** — Endpoint Detail read-only effective-config view.
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

---

**One active thread at a time, in this exact order. Nothing new gets
introduced outside this file until the current item closes.**
