## TASK_BRIEF — B-001: Bootstrap docs + reconcile legacy state

**Paste this whole file into Claude Code (`cd` into the repo, run `claude`, paste below).**

### 1. Objective
`CLAUDE.md`, `BUILT.md`, and `BACKLOG.md` exist at repo root and accurately describe the current codebase — with the legacy product docs archived (not deleted) and the uncommitted working tree resolved into a clean, deliberate, documented state.

### 2. B-ID
B-001

### 3. Assumptions made
- This repo was originally scoped as "Enterprise AI Monitoring & Intelligence" (a gateway/policy/audit governance platform) and has since pivoted to "Enterprise AI Maturity Index" (a five-level organizational maturity assessment app). The founder confirmed this is the same repo, pivoted — not a different project.
- `ARCHITECTURE.md`, `BOUNDARIES.md`, `DECISIONS.md`, `ROADMAP.md`, `PROJECT-STATUS.md`, and `tasks/` at repo root belong to the old framing. They are not authoritative for the current product. Do not delete them — archive them so history isn't lost.
- `pm/PM_PROMPT.md` and `pm/STATUS.md` are Cowork-owned state files. Read them for context if useful, but do not edit them.

### 4. Scope

**MAY READ:** the entire repo — this is an orientation/bootstrap task, full exploration is expected and required here (this is the one time "read only what's in scope" doesn't apply).

**MAY MODIFY:**
- Create at repo root: `CLAUDE.md`, `BUILT.md`, `BACKLOG.md`
- Move (don't delete) `ARCHITECTURE.md`, `BOUNDARIES.md`, `DECISIONS.md`, `ROADMAP.md`, `PROJECT-STATUS.md`, `tasks/` into `docs/legacy/` (or similar), with a one-line pointer in `BUILT.md` explaining what they were and why they're archived
- Any file in the current uncommitted diff (`git status` shows ~25 modified, ~19 untracked, all from the old product line) — review each, decide keep/commit vs. discard, and record the decision
- Do NOT touch `pm/PM_PROMPT.md`, `pm/STATUS.md`, `CONTINUATION_PROMPT.md`, `HANDOFF-B-001.md`

### 5. Contracts
None frozen yet — this is the first bootstrap. Exception: if code in the uncommitted diff represents a real, working interface (e.g. an API route, a store function), document it in `BUILT.md` as it actually behaves. Don't redesign it as part of this task.

### 6. Acceptance criteria
- [ ] `CLAUDE.md` exists, ≤150 lines: product one-liner, architecture summary, stack, conventions (naming, error handling, commit format, test framework), and the hard rules from `CONTINUATION_PROMPT.md` reproduced verbatim
- [ ] `BUILT.md` exists: one section per real module, each with purpose, status (STABLE / WORKING-BUT-FRAGILE / PARTIAL), key files, interfaces consumed by other modules (exact signatures/routes/payloads), data owned, test coverage, known limitations
- [ ] `BACKLOG.md` exists with NEXT (leave empty — founder/PM assigns), QUEUED (numbered B-002, B-003… for stubbed/missing/below-enterprise-grade work — check auth, RBAC, multi-tenancy, audit logging, input validation, error handling, test coverage, secrets handling), BLOCKED, DONE
- [ ] Legacy docs archived under `docs/legacy/`, not deleted
- [ ] `git status` is clean at the end — every kept change committed with a message explaining what/why; anything discarded is named explicitly in the completion report (not just silently dropped)
- [ ] `BUILT.md` and `BACKLOG.md` shown to the founder for review before the final commit + push

### 7. Test requirement
Run the existing test suites as they are — `go test ./...` in each Go module (`eami-agent`, `eami-api`, `eami-collector`, `eami-gateway`, `eami-policy`), and whatever test runner `eami-ui` has (`tsc --noEmit` at minimum). Record actual pass/fail in `BUILT.md` per module — don't assume green.

### 8. Out of scope
- No new maturity-index features or schema changes — this task is documentation + reconciliation only
- Do not delete any legacy doc outright
- Do not silently redesign any of the uncommitted code toward "maturity index" concepts — flag it in `BACKLOG.md` as QUEUED instead and let the founder/PM sequence it

### 9. Standing rules (verbatim, every brief)
- Read only the files in scope. Everything needed is in this brief, `CLAUDE.md`, `BUILT.md`. Do not explore the repo beyond what's needed for this bootstrap.
- Do not re-read unchanged files between iterations.
- No refactoring outside scope; log suggestions in `NOTES.md`.
- Before final commit: reviewer subagent pass (acceptance criteria + conventions) and security subagent pass (OWASP top 10, secrets, injection, authz). Fix findings first.
- Update `BUILT.md` and `BACKLOG.md`, commit docs with code referencing the B-ID, push, then produce the COMPLETION REPORT.

### 10. Kickoff
Read `CONTINUATION_PROMPT.md` first — it has the full bootstrap procedure this brief refers to. Then confirm your understanding of this brief (objective, file scope, and how you'll handle the legacy docs + uncommitted diff) in 5 lines before you start. Use the COMPLETION REPORT format from `CONTINUATION_PROMPT.md` when done, and paste it back into the PM (Cowork) session via "ingest report".
