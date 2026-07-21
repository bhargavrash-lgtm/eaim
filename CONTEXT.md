# CONTEXT.md — Living project continuity log
# Updated by: Claude Code (after every task) AND the PM chat (after every
# planning decision). Read by both at the start of every session, before
# anything else.

## Product identity (do not re-litigate without explicit founder instruction)
EAMI = Enterprise AI Monitoring & Intelligence. Gateway, policy engine,
endpoint agent, audit log. NOT a maturity-assessment tool — that's a separate,
unrelated framework the founder uses elsewhere. If any file, commit message,
or prior context suggests otherwise, it is wrong; trust this line.

## Active decision thread (update every time one moves)
- ADR-019: RESOLVED — 2026-07-21. Full episode content stays on-prem;
  eami-api never serves it. See DECISIONS.md D-0XX.
- B-002 resolution in progress, 3-brief split:
  - Brief 1 (gateway dual-auth endpoint): ABOUT TO START — plan pending review
  - Brief 2 (eami-api proxy layer): NOT STARTED — depends on Brief 1
  - Brief 3 (memory.go + MemoryPage.tsx rewire): NOT STARTED — depends on Brief 2

## Standing facts Code and PM must both know
- Desktop app: planned future feature, not yet built. Gateway auth should
  support it (Bearer JWT path) without a live consumer yet.
- No deploy infrastructure exists in this repo (no deploy.yml, no IaC).
  Nothing is live in production. api.eami.io in openapi.yaml is a spec
  placeholder, not a real deployment.
- Solo founder, pre-first-customer, evening/weekend hours.

## Last updated
2026-07-21 by Claude Code — CONTEXT.md created, B-002 3-brief plan recorded
