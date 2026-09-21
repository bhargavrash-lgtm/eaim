# rheoARC Design System — The Standing Reference
## Read this before building or changing any UI. Not optional.

Source of truth: the live design canvas at
https://claude.ai/artifact/4j2DqNrWQbHRxTZD3KAJYf (6 artboards + 1 focused
component study). This document is the written extraction of that canvas.
Where a real visual detail is in question (exact spacing, exact curve
shape, exact color relationship), check the canvas directly — it is ground
truth, this document is the summary.

---

## 0. The One Question to Ask Before Building Anything

**Which of the three modes is this page in?** Every screen in this
product belongs to exactly one. Get this wrong and everything downstream
(density, color palette, what the top bar contains) will be wrong too.

| Mode | Who | Feel | Reference artboard |
|---|---|---|---|
| **Admin** | IT / platform admins | Dense, detail-oriented, maximum real options, Material-rail navigation | Layer 2, Layer 2b |
| **Workspace** | Delegated team admins (HR, Finance, Engineering) | Outcome-oriented, restricted, calm, few numbers | Layer 4, Layer 4b |
| **Chat Engine** | Ordinary end users | Claude-Desktop-familiar, warm, minimal, conversational | Layer 5 |

Do not blend these. A Workspace page that starts accumulating dense data
tables has drifted into Admin's job. An Admin page that hides real
configuration behind vague outcome cards has drifted into Workspace's job.

---

## 1. Design Philosophy — "Calm Authority, Corrected"

The governing principle, confirmed against Freshworks' own real December
2025 "Dew" redesign (reduce visual clutter, generous white space,
accessible contrast) and against direct founder correction of this
session's own first attempt (too vivid, too dark-dominant, "not user
friendly"):

**Restraint beats boldness. A security/governance product's whole value
proposition is "you can trust what this screen tells you" — a busy,
decorative interface undermines that claim before a word is read.**

Hard rules that follow from this:
1. Color means state or real relationship meaning, never decoration.
2. Elevation (soft shadow), not flat borders alone, is the primary depth
   cue — see §4.
3. Empty states are real design surfaces, not afterthoughts.
4. Never cram multiple data domains onto one outcome-oriented dashboard —
   give each its own focused page (§7).
5. No AI-generated-design clichés: no gradient washes (confirmed not to
   render reliably in this stack anyway), no left-border accent cards, no
   Inter/Roboto/Arial, no emoji as icons.

---

## 2. Color — Two Intentional Systems, Not an Inconsistency

Admin and Workspace share one cool palette. Chat Engine deliberately uses
a separate warm palette. This is intentional: Admin/Workspace are internal
tools; Chat Engine is the actual daily product, styled toward genuine
Claude Desktop familiarity, not enterprise-dashboard chrome.

### Admin / Workspace palette
| Token | Hex | Use |
|---|---|---|
| Ink (text, dark surfaces) | `#1A2140` / `#0B1130` | Primary text; dark accents |
| Panel / page background | `#F7F8FB` | Default page background |
| Surface | `#FFFFFF` | Cards, panels |
| Border (hairline, paired with elevation, never alone) | `#E4E7F0` at ~55% | Card edges |
| Accent (interactive, sparingly) | `#3B5BDB` | Buttons, active nav, links, selected states |
| Muted text | `#6B7290` / `#8890AD` | Secondary text, captions |
| Success | `#1A7A4C` on `#E8F5EE` | Healthy, allowed |
| Warning | `#8A6512` on `#FBF1DD` | Escalated, running |
| Danger | `#8A2E2E` on `#FBEAEA` | Denied, broken |

### Chat Engine palette
| Token | Hex | Use |
|---|---|---|
| Background | `#FBF9F6` | Page background — warm, not cool gray |
| Sidebar | `#F3F0EA` | Conversation list |
| Text | `#2A281F` | Primary text |
| Muted text | `#9C9484` / `#5C5748` | Captions, model-source labels |
| Accent (kept consistent with Admin/Workspace for brand continuity) | `#3B5BDB` | Send button, assistant icon only |

---

## 3. Typography

**IBM Plex Sans** (UI text) + **IBM Plex Mono** (technical/ID strings —
agent IDs, hashes, timestamps). Deliberately not Inter/Roboto/Arial —
those read as generic/templated. Google Fonts, loaded via a standard
`<link>` in production.

Scale (Admin/Workspace): Page title 24–34px/700, Section header 15–20px/
600, Body 13–14px/400, Label/caption 10–11px/600 with slight letter-
spacing on all-caps labels.

---

## 4. Elevation — Real Depth, Not Flat Borders

Confirmed via direct correction mid-session: flat 1px borders alone read
as boxy and dated. Real elevation is the primary depth cue; a hairline
border is a secondary, optional reinforcement only where contrast against
the page background is otherwise too low.

```
L1 (resting card/panel):
  box-shadow: 0 1px 2px rgba(26,33,64,0.04), 0 2px 6px rgba(26,33,64,0.06);
  border: 1px solid rgba(228,231,240,0.55);   /* optional reinforcement */

L2 (persistent chrome — top app bar):
  box-shadow: 0 2px 10px rgba(26,33,64,0.07);
  /* no border — shadow alone signals "above the content" */

L3 (selected/focused element):
  box-shadow: 0 4px 16px rgba(59,91,219,0.20), 0 2px 6px rgba(26,33,64,0.08);
  border: 1.5px solid #3B5BDB;
  transform: translateY(-2px);
```

---

## 5. Icons — Treatment, Not Just a Library Choice

Inline stroke-style SVG (Feather-icon shape language), never emoji, never
a filled/solid icon style. Icons sit inside a small square "chip" with a
thin accent-colored border — never a plain filled circle (an earlier,
corrected pass in this same session).

```html
<div style="width:Npx;height:Npx;border-radius:.045*N;
            background:#0B1130;border:1.25px solid <accent>;">
  <svg .../>
</div>
```

---

## 6. The Common Top Bar — Mandatory on Every Standalone Page

**Every full, standalone page in Admin, Workspace, and Chat Engine modes
carries the same functional elements**, adapted visually to that mode's
palette but never dropped:

- A left-side context label (breadcrumb in Admin/Workspace; conversation
  title in Chat Engine)
- A real search field (global scope in Admin/Workspace; can be
  supplemented by a page-scoped second search field when browsing a
  filtered/classified set — see §7's CMDB pattern)
- Notifications: a bell icon with a real unread-count badge
- A page-specific contextual action where relevant (an overflow menu on
  detail pages, an "+ Add X" button on list pages, a Skills configuration
  control specific to Chat Engine — never force an irrelevant action onto
  a page that doesn't need one)
- Profile avatar

**The three, and only three, exceptions**, all reasoned, none silent:
1. Pure style/reference artboards that were never meant to be a real page
   (a design-token style tile).
2. A zoomed-in component study meant to be composed *into* another real
   page, not viewed standalone (the relationship-graph detail study).
3. **Pre-authentication pages** (login, first-boot setup wizard). The top
   bar's own required elements — profile avatar, notifications, a
   breadcrumb into the app's own navigation — presuppose an authenticated
   identity and an app shell that doesn't exist yet at this point in the
   flow. Confirmed via the real B-201 top-bar audit: neither `LoginPage`
   nor `SetupWizardPage` render inside `AppShell`/`Sidebar` at all, so
   there is no context for a top bar to sit inside of, not just a reason
   to omit one. Applies only to the pages a user sees *before* a real
   session exists — never to an authenticated page that merely "feels
   different," which does not qualify for this exception.

If you are building a new standalone page and are tempted to skip the top
bar "because this page feels different" — it doesn't qualify for an
exception unless it matches one of the three above. Ask first.

---

## 7. Pattern Library — Reusable, Confirmed Structures

### 7.1 The Relationship Graph ("Connections")
**Problem it solves:** direct, repeated feedback that the product's
entities had no visible sense of how they connect.

**Confirmed pattern, NOT a flat lane list (an earlier, corrected
approach):** an organic branching tree, drawn via SVG cubic-bezier paths
(`M x,y C cx1,cy1 cx2,cy2 x2,y2`), not straight CSS lines. One
relationship-type junction can branch to **multiple** real target nodes
(e.g., "governed by" fanning out to two separate policies) — this
one-to-many capability is the entire reason the lane layout was replaced.

Structure: a center node (the subject), 4 junction dots at fixed
relationship types, each labeled with real directional language
("governed by," "dispatches through," "appears in," "linked to") — never
an unlabeled line implying mere association. The path/junction carrying
real, active usage data is visually distinguished (accent-colored line +
dot + elevated selected-state card).

**Interaction:** clicking any node opens the same `SlideOverPanel`
component already used for every other detail view in this product — do
not invent a second detail-view pattern for graph nodes.

**Scope discipline:** Focused Mode only (one entity's own connections) —
never a full org-wide everything-graph by default; that's a real, separate
future initiative (Overview Mode), not part of this pattern.

### 7.2 CMDB Asset Classification
**Confirmed pattern, NOT a flat filtered list (an earlier, corrected
approach):** a real left-hand classification panel (category groups with
counts, e.g., Hardware → Laptops/Servers, AI Workloads → Deployed LLMs/
Training Jobs), with the selected category driving both the page's
heading and a **second, dedicated search field scoped to that category** —
distinct from and in addition to the global top-bar search.

The CI taxonomy must include AI-workload types (Deployed LLM Instance,
Training Job), not just physical hardware — this was a real, corrected
scope gap in an earlier pass of this same session.

### 7.3 Workspace Governance Visibility
Policies and rules shown to a workspace user must be visually labeled by
their real source: **"Global floor"** (set by IT, gray, cannot be
loosened) versus **workspace-specific** (added on top, accent-colored).
This is not decoration — it makes the actual two-tier governance model
(global floor + workspace-added restrictions, global always wins,
workspace can only add restrictions, never relax) visible and
comprehensible to a non-technical workspace admin.

### 7.4 Honest Data — Never Fabricate What Isn't Real Yet
Where a real metric is genuinely unsolved (e.g., ROI — flagged elsewhere
in this project as a hard, unsolved measurement problem, not a formula),
render it as an explicitly pending state (dashed border, "Not yet
available," a one-line honest reason) — never a fabricated confident
number. This is a real, standing content rule, not just a mockup
convenience.

### 7.5 Workspace Switcher Is Not a Free Toggle
A workspace context indicator in navigation shows the workspace(s) a
given user is actually, restrictively a member of — never a free
multi-workspace switcher for an ordinary workspace user. Free switching
across all workspaces is an account-wide-admin-only affordance. Render a
single-workspace user's context as a fixed label, not an interactive
dropdown.

### 7.6 Layout & Alignment
Extracted from two real, live-screenshot-confirmed bugs on Agent Detail
(B-204) — grounded in the actual values just fixed, the same way §4's
shadow values came from real, tested elevation work, not invented in the
abstract.

**(a) Label-value pairs: compact and grouped, never edge-justified across
a wide container.** Agent Detail's Owner/Scope rows used
`flex items-center justify-between` inside a full-width (~1100px+) card —
that stretches a short label to the far left and its value to the far
right of the row, reading as an awkward extreme rather than a scannable
pair. The confirmed fix: keep the card full-width (it should still match
the page's own card language), but change the *internal* alignment to a
fixed-width label column immediately followed by its value:

```html
<div class="flex items-center gap-3 rounded-lg bg-white px-4.5 py-3.5 shadow-l1">
  <span class="w-20 flex-shrink-0 text-sm font-semibold text-ink">Owner</span>
  <span class="text-sm text-ink-faint">{value}</span>
</div>
```

`w-20` (enough for a short label like "Owner"/"Scope" without wrapping) +
`gap-3` (12px) keeps every row's value starting at the same x-position
regardless of label length, and keeps the whole pair grouped on the left
rather than stretched to the row's full width. **This is the default for
any future label-value UI, not only a retrofit for already-wide
containers.** `DiscoverPage.tsx`'s `EndpointDrawer` has a structurally
identical `justify-between` name+metadata pattern (its `<li>` rows,
e.g. MCP servers/AI apps/local models), but inside a narrow 480px
`SlideOverPanel` — the same edge-justification mechanism is present, it's
just naturally far less visually extreme at that width, which is why it
wasn't flagged as broken. Don't use container width as a reason to skip
this pattern on a new panel: build label-value rows compact from the
start, so the next person doesn't have to rediscover this the way this
session did.

**(b) Dynamic-content containers must size to their real content — with
real headroom, not a bare-minimum fit.** The relationship graph
(§7.1)'s container height is computed from real node/junction counts
(`PAD`/`ROW_H`/`JUNCTION_GAP`), not fixed — the right mechanism, since a
fixed height can't know in advance how many real connections an agent
has. But it was paired with `overflow: hidden` and a tight `PAD` (40px),
which is a silent-failure combination: if the sizing formula is ever off
by a few px in some future edge case (a new junction type, an unusual
target count), content clips with **no visible error and no scroll
affordance** — it just vanishes. Confirmed fix, applied without waiting
for a live-reproduced clip: `PAD` increased to 48px, real headroom on
both ends of the computed height, cheap insurance against that failure
class. **The rule going forward:** a dynamic-content container that
computes its own size from real data may use `overflow: hidden` for
corner-radius masking (as this one does, `rounded-xl`), but its sizing
formula must include deliberate extra padding beyond the tightest
mathematically-sufficient fit — never size to the exact pixel boundary of
the content it's measuring. If a future container's content can vary
enough that even generous padding might not be enough, prefer a real
scroll affordance (`overflow-y-auto` with a definite max-height) over
`overflow: hidden` with a fixed/computed height — silent clipping is
always the worse failure mode of the two.

---

## 8. Anti-Patterns — Explicitly Rejected, Do Not Reintroduce

- Gradient fills on shapes — confirmed NOT to render reliably through
  this stack's rendering pipeline; don't attempt them again without
  re-verifying.
- Left-border accent cards as the default hierarchy device — a
  recognizable, generic AI-design cliché; use elevation and background
  differentiation instead.
- A flat, unlabeled-relationship "everything connects to everything" view
  — every edge needs real directional, labeled meaning.
- Cramming CMDB assets + Memory + Training metrics + LLM deployment onto
  one Workspace dashboard — each gets its own focused page.
- Reusing status colors (green/amber/red) for anything that isn't real
  state — e.g., a wayfinding dot on a diagram must use a neutral color,
  not green, even though green looks nice, because green is reserved for
  "healthy/allowed."

---

## 9. When You're About to Build Something New

1. Identify the mode (§0).
2. Pull tokens from §2–5. Do not invent new colors or fonts.
3. Confirm the top bar (§6) — build it in, don't skip it without a
   qualifying exception.
4. Check §7 for an existing pattern before designing a new one — a new
   "list of related things" screen almost certainly wants 7.1 or 7.2, not
   a fresh invention.
5. Check §8 before shipping — none of these should appear in the diff.
