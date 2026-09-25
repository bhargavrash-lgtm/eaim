# Horizon 0 Usability/Maturity Audit

**Date:** 2026-09-24
**Author:** Claude Code, investigation-only task (no code changes)
**Roadmap mapping:** does not correspond to a specific named Horizon or numbered item in `rheoARC_Roadmap_Enterprise_AI_Platform.md` — flagged explicitly rather than forced. Horizon 0's own text defines its bar as *"shipped, reviewed by mandatory code-review and security passes, and live-verified against real data"* — correctness and security, not feature maturity. This audit tests a dimension the roadmap doesn't itemize anywhere, but is directly Horizon-0-adjacent: it tests the roadmap's own closing claim that "Horizon 0 is the proof the wedge is real," specifically in the feature-maturity sense rather than the correctness sense.

**Scope:** every Horizon 0 page has been adversarially proven correct and secure. None had been evaluated for whether it's genuinely mature and usable — feature-rich, not just functionally right. Pages audited: Agents, Policies, Tools, Workflows, Approvals, Audit, FinOps, Alerts, Assets/CMDB, Discover, Settings.

**Status update (2026-09-24, B-219):** priority-order item 1 below (wiring `EmptyState`'s existing `action` prop everywhere a real create action already exists) is now DONE, live-verified — see `BUILT.md`/`BACKLOG.md`'s B-219 entries. The findings below are left exactly as originally written, as the historical record of what the audit found; only this note reflects that item 1 has since been closed.

**Status update (2026-09-24, B-220):** priority-order item 3 below (real multi-column sort on `DataTable.tsx`) is now DONE, live-verified — see `BUILT.md`/`BACKLOG.md`'s B-220 entries. One real correction to Part A's own findings, made during B-220's investigation: the cross-page summary line below reads as if single-column sort was broadly reachable before this fix ("DataTable.tsx only supports single-column sort") — direct grepping found it was actually reachable on exactly 1 of 11 audited pages (`AssetsPage.tsx`) before B-220, not broadly. Left uncorrected in the findings below as the historical record of what this audit originally said; the accurate version is in B-220's own `BUILT.md`/`BACKLOG.md` entries.

**Method:** direct file reads of every page's real source (`eami-ui/src/pages/...`), plus targeted grep across the whole `eami-ui/src/pages` tree for export/download/CSV/tooltip/help patterns. All findings below are cited to file:line. No assumptions from page names.

**Status update (2026-09-25, B-221):** priority item 4 is now built for Audit and FinOps. Audit exports the exact applied five filters through a bounded, org-scoped backend CSV; FinOps exports its three real, server-date-filtered tables. The original Audit date-filter defect is fixed by RFC3339 serialization, and the former 100,000-row buffered/truncating export is replaced by an all-or-error 10,000-row / 20 MiB / 15-second, formula-safe streaming policy with per-org concurrency protection. FinOps's existing `[from,to)` UTC boundary is explicit in the export UI. Automated API/UI validation and Docker rebuild pass; authenticated browser acceptance remains pending a supplied local test session. Historical findings below are retained.

---

## Part A — Real feature-completeness audit, per page

For each page, checked against 5 standard, well-established enterprise-SaaS capabilities:
1. Real search (not decorative/disabled)
2. Real multi-field filtering (not just a single type-filter tab)
3. Real bulk actions (select multiple rows, act on all at once)
4. Real export capability
5. Real multi-column sort

| Page | Real search | Real multi-field filter | Bulk actions | Export | Multi-column sort |
|---|---|---|---|---|---|
| **Audit** | Partial — field-scoped (agent/tool text inputs → real API query params) | **Yes** — 5 real dimensions (agent, tool, decision, from, to), all combined into one `AuditParams` object and applied together | No | No | No |
| **Discover** | **Yes** — real, API-backed hostname search (`useEndpoints({ search, ... })`) | **Yes** — 2 dimensions (hostname search, API-side + OS/platform dropdown, client-side), genuinely combinable | No | No | No |
| **FinOps** | No | Partial — 1 dimension only (date range picker) | No | **No — notably absent; this is cost data**, the page most likely to need export | No |
| **Alerts** | No | No — Active Alerts tab hardcodes `useAlerts({ resolved: false })`, no user-facing controls; Alert Rules tab has none either | No | No | No |
| **Policies** | No | No — manual priority reorder (up/down chevrons) is not a filter | No | No | No — no column is even marked `sortable` |
| **Tools** | No | No | No | No | No |
| **Workflows** | No | No — `status` is a displayed column with no filter control over it | No | No | No |
| **Assets/CMDB** | No | Partial — single type-filter tab only (All/Endpoints/Agents/Tools), not multi-field | No | No | No |
| **Agents** | No | No | No | No | No |
| **Approvals** | No | No — a 2-tab status split (Pending/All) is not a filter | No | No | No |
| **Settings** (6 tabs: Organisation, Users, Notifications, API Keys, Model Pricing, License) | No, across all 6 tabs | No, across all 6 tabs | No | No | No |

### Universal, cross-page findings (more consequential than any single row above)

- **Bulk actions: 0 of 11 pages.** Every mutating action anywhere in the product is strictly single-row. No checkbox column, no multi-select, no bulk toolbar exists anywhere.
- **Export: 0 of 11 pages.** Confirmed via a codebase-wide grep for real export/download/CSV functionality (excluding the `export function`/`export const` JS keyword) across every file in `eami-ui/src/pages` — zero matches. Most damaging on **FinOps** (cost reporting) and **Audit** (compliance) — the two surfaces where "can't export our own data" is most likely to actually block an enterprise sale.
- **Multi-column sort: 0 of 11 pages** — this is a shared-component ceiling, not a per-page gap. `eami-ui/src/components/common/DataTable.tsx` only supports single-column sort (clicking a header toggles asc/desc on one column; no shift-click/secondary sort). One shared-component enhancement would fix this for every page at once, cheaper than 11 separate per-page fixes.
- **The global top-bar search and notifications bell are decorative on every single page**, via the shared `AppTopBar` component — and the code discloses this itself:
  - `eami-ui/src/components/layout/AppTopBar.tsx:66`: `title="Global search — not built yet (chrome only, not wired to a real search endpoint)"`
  - `eami-ui/src/components/layout/AppTopBar.tsx:73`: `title="Notifications — not built yet (chrome only, no real feed exists)"`
  This isn't a silent gap — it's a component that visually promises a capability it doesn't have (a styled search box with "Search…" placeholder text, a bell icon), present on every admin page. In tension with `DESIGN_SYSTEM.md` §7.4's own "never imply a capability that isn't real" honest-data principle.
- **Real multi-field filtering exists on exactly 2 of 11 pages** (Audit, Discover). **Real search exists on exactly 2 of 11 pages** (Audit — field-scoped, Discover — real hostname search). The other 9 pages have zero of either.

---

## Part B — Real state quality (empty states)

`DESIGN_SYSTEM.md` already covers empty/loading states *looking* visually consistent. This asks a different question: does each empty state actually tell the user what to do next — a real, specific, actionable next step — or just show generic text with no path forward?

`eami-ui/src/components/common/EmptyState.tsx` already supports a real, working `action?: ReactNode` prop for an embedded next-step button (confirmed by reading the component: `{action && <div className="mt-4">{action}</div>}`).

**That capability is used with an actual embedded action in exactly 1 of ~15+ EmptyState call sites across the entire product:**
- `eami-ui/src/pages/ops/AlertsPage.tsx` (Alert Rules tab, ~lines 503-515): `<EmptyState icon={<Bell .../>} title="No alert rules" description="..." action={<button onClick={() => setFormState({ open: true })}>New rule</button>} />` — a real, working button that opens the rule-creation panel.

**Every other empty state across the audited pages is descriptive text only**, even on pages where a real "Add X" button already exists elsewhere on the same page, visually and functionally disconnected from the empty state itself:
- `PoliciesPage.tsx`: `EmptyState title="No policies yet" description="Create your first policy to start governing gateway traffic."` — no button in the call; the real "New policy" button lives separately in the top bar.
- `ToolsPage.tsx`: same pattern — real description, no embedded button; "Add tool" button lives in the top bar.
- `WorkflowsPage.tsx`: same pattern; "Add workflow" button lives in the top bar.
- `SettingsPage.tsx` (API Keys tab): `EmptyState title="No API keys" description="Create a key to authenticate the collector or other services."` — no button; the real "Create API key" button is always visible outside the empty state.
- `SettingsPage.tsx` (Model Pricing tab): same pattern.
- `AuditPage.tsx`: `EmptyState title="No audit events" description="Gateway decisions will appear here once agents start making tool calls."` — text only, no action (there is no create action for audit events, so this is closer to correct-as-is).
- `ApprovalsPage.tsx` (both tabs): generic text, no action, no button.
- `DiscoverPage.tsx`: `EmptyState title="No endpoints found" description="Adjust your filters or wait for agents to check in."` — description gestures at an action but there is no actual button.
- `FinOpsPage.tsx`: chart panels use `EmptyState title="No data for this period"` (text only); the 3 spend tables don't even use the `EmptyState` component — they use `DataTable`'s plain `emptyMessage` string prop instead.

**Some pages don't use the shared `EmptyState` component at all:**
- `AgentsPage.tsx`: bare `<p className="text-sm text-gray-400">No agents registered yet.</p>` — no icon, no `EmptyState` import, no button, worse empty-state hygiene than its peers.
- `SettingsPage.tsx` (Users tab): `DataTable`'s plain `emptyMessage="No users yet"` string prop only.

**Conclusion:** the mechanism for a real, actionable empty state already exists and works (proven by the one real usage in Alerts). It is simply not wired up almost anywhere else — this is a "connect what already exists" fix, not new engineering, for the large majority of cases.

---

## Part C — Real first-run experience

**Question:** what does a brand-new admin, logging in for the very first time after the setup wizard (B-053) completes, actually see and know to do? Is there any real guided onboarding beyond that one-time wizard, or does a new admin land on an empty Dashboard with no guidance at all?

**Confirmed: no real guided onboarding exists beyond the one-time setup wizard.**

- `eami-ui/src/pages/setup/SetupWizardPage.tsx` (lines 272-287), the wizard's own "success" stage: `<h1>Setup complete</h1><p>{org_name} is ready. Sign in with {admin_email} and the password you just set.</p>` followed by a single `<Link to="/login">Go to sign in</Link>` button. No checklist, no summary of recommended next steps, no links to key first actions (add an agent, install the endpoint agent, connect a tool, create a policy).
- `eami-ui/src/pages/dashboard/DashboardPage.tsx` (full file read) has **zero first-run/zero-state detection logic** — it always renders the same live-metrics layout (4 KPI cards, Active Sessions table, Recent Alerts, Recent Audit Events) regardless of whether the org has any real data yet. A brand-new admin with an empty org sees: "0" Active Sessions, "0" Pending Approvals, "—" Endpoints Monitored, "—" Monthly Token Spend, an empty Active Sessions table ("No active sessions"), "No alerts — All clear," and "No audit events" — a wall of zeros, dashes, and empty states with **no guidance anywhere on what to configure first.**

**Conclusion:** a new admin's actual first-run experience, after the one-time wizard's brief success screen, is landing on a Dashboard that is entirely empty and offers zero direction. There is no getting-started checklist, no zero-state variant of the Dashboard, and no prompts pointing toward the first real actions (add an agent, install `eami-agent`, connect a tool).

---

## Part D — Real in-product help

**Question:** does any tooltip, inline help text, or documentation link exist anywhere in the product today?

**Confirmed honestly, not assumed:**

- **No dedicated Help nav item** anywhere — grepped `Navigation.tsx` and `SettingsPage.tsx` directly, zero matches.
- **No systematic tooltip/help-icon pattern** — grepped the entire `eami-ui/src` tree for `HelpCircle`/`helpText`/`Tooltip`/`docs.`/`documentation`/`/help`/`Learn more`/`Info className`. The matches found were false positives on inspection:
  - `FinOpsPage.tsx`/`PasteEventsPage.tsx`: `Tooltip` from the `recharts` charting library (a chart-hover data tooltip), not a help UI pattern.
  - `ToolsPage.tsx`: `HelpCircle` icon used as a status icon for a "misconfigured" connector state, not a help affordance.
- **No documentation links** anywhere in the product (no external docs URL, no "Learn more" link).
- **Exactly one genuine instance of real, contextual in-product help text was found:**
  - `eami-ui/src/pages/ops/AuditEntryDetailPanel.tsx` (lines 183-194): a real explanation of what the audit-chain self-consistency check does and does not prove — *"This checks only that this entry's own hash recomputes correctly from its own recorded fields. It does **not** confirm that no earlier row in the chain was tampered with..."* — a good, specific, honest example of the right pattern. It is not replicated anywhere else in the product.

**Conclusion:** in-product help is effectively absent as a systematic capability. One real, well-executed example exists in a single detail panel, demonstrating the pattern is easy to do well when it's done — it's simply never been extended anywhere else.

---

## Recommended priority order

No code built for this investigation. Sequenced by effort-vs-impact and honest dependency, for whoever scopes the next build brief:

1. **Wire `EmptyState`'s existing `action` prop everywhere a create button already exists on the same page.** Zero new engineering — the mechanism is already built and proven (Alerts' Rules tab). Immediate, low-risk win across ~8 pages (Policies, Tools, Workflows, Settings' API Keys/Model Pricing tabs, and others).
2. **Decide what to do about `AppTopBar`'s decorative global search/notifications chrome.** Either build real global search, or de-style/remove it so it stops implying a capability that isn't real (`DESIGN_SYSTEM.md` §7.4 tension). This needs a real founder decision, not an assumption in either direction.
3. **Real multi-column sort on `DataTable.tsx` itself.** One shared-component enhancement applies to all 11 pages at once — genuinely standard for enterprise admin tables, and cheaper than 11 separate per-page fixes.
4. **Export capability**, starting with FinOps and Audit (cost reporting and compliance are the two areas most likely to actually block an enterprise sale over this gap), then extend the pattern to the rest.
5. **Real search + multi-field filtering**, extending Audit's and Discover's already-proven real patterns to the 9 pages missing them entirely. Policies/Tools/Agents (daily admin config surfaces) are likely higher-value than Settings (used rarely, config-heavy rather than list-heavy).
6. **Bulk actions.** The single most consequential gap for a genuine "enterprise-mature" claim, but the most structurally invasive to build correctly (real selection state, a safe bulk-mutation API design, confirmation UX for bulk-destructive actions). Sequence after the cheaper wins above, not because it matters less, but because it's the most expensive to do right.
7. **First-run guidance (Part C).** A real, scoped, likely-small brief: a zero-state Dashboard variant or a "Getting Started" checklist card, shown only while the org has zero agents/endpoints/tools, disappearing once real data exists.
8. **In-product help (Part D).** Lowest priority of the four investigation parts. Not typically a blocker for an initial enterprise sale; replicate the one good existing pattern (`AuditEntryDetailPanel.tsx`) opportunistically rather than as a dedicated push.
