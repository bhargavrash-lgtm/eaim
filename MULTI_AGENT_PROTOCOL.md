# Multi-Agent Coordination Protocol

## Standing rule for this repo whenever more than one AI coding agent is involved

**This document carries the same standing weight as CLAUDE.md's own Conventions section — read it before starting any task, whichever agent you are.**

---

## 0. The one non-negotiable rule

**Exactly one agent works on this codebase at a time. Never concurrently.**

Not a soft preference — a hard rule. Two independent agents editing a shared, living system without perfect synchronization is how real conflicts happen, regardless of how capable either one is individually. Before starting any task, confirm no other agent is currently mid-task (see §3, the active-agent marker).

---

## 1. Mandatory onboarding read — before any task, either agent

In this exact order, every time, regardless of how recently you last read them (they may have changed):

1. `CLAUDE.md` — standing engineering rules and conventions
2. `CONTEXT.md` — the current decision thread and last-updated state
3. `rheoARC_Roadmap_Enterprise_AI_Platform.md` — confirm which Horizon/item this task maps to (CLAUDE.md's own roadmap-discipline rule applies to both agents equally)
4. `DESIGN_SYSTEM.md` — mandatory if the task touches any UI
5. `MATURITY_AUDIT.md` — mandatory if the task touches any Horizon 0 page's real feature completeness
6. A **fresh, live** scan of `BACKLOG.md` — never trust a cached or remembered B-ID counter. Confirm the real next-free ID directly against the file, every time, no exceptions. This risk doubles with two agents in rotation; treat it accordingly.

---

## 2. Every standing rule applies equally to both agents — no exceptions, no lighter bar

This includes, without limitation: mandatory reviewer + security subagent passes on security-relevant work, real-Postgres-only integration tests (never mocked), `t.Cleanup(func() { pool.Close() })` always, `defer pool.Close()` never, live verification against the real running stack (not just a disposable instance — see the B-200 deployment-gap lesson, which applies with equal force regardless of which agent is building), and the full BUILT.md/BACKLOG.md/CONTEXT.md documentation discipline on every completion.

**Do not assume a new agent will internalize this by default.** State it explicitly in every kickoff, and treat the first several completion reports from a new agent with real, active scrutiny — not the same established trust level Code has earned over this entire session's real track record. Trust is calibrated by evidence, not assumed by capability.

---

## 3. The active-agent marker

CONTEXT.md's own header carries one additional line, updated at the start and end of every task, by whichever agent is working:
```php-template
ACTIVE AGENT: <Code | Codex | none> — <B-ID or "none"> — started <timestamp>
```

Before starting any task, check this line. If it shows another agent active, do not proceed — that is a real, current violation of §0, not a formality to skip past.

---

## 4. Who assigns what

Task assignment is sequenced through one coordinating point (the PM-layer conversation), not decided unilaterally by either agent or juggled manually across two parallel, disconnected contexts by the human in the loop. This avoids both agents self-assigning overlapping work and avoids information loss between two independently-managed threads.

---

## 5. Handoff discipline

Every completion report, from either agent, must leave the written record complete enough that the *other* agent could pick up the very next task with zero information loss beyond what's in BUILT.md/BACKLOG.md/ CONTEXT.md. If a real judgment call was made that isn't fully captured in those files, that is itself a gap worth closing before considering the task done — the same discipline already applied to every completion report tonight, now serving a second, real purpose: making the *next* agent's onboarding actually sufficient, not just this session's own memory.
