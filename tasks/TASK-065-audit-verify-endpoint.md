# Task: Add audit chain integrity verify endpoint
**From:** PM-EAMI
**To:** BE-Gateway
**Priority:** low
**Blocked by:** TASK-055 (v1.0.0 release — implement post-ship)

## What I need

Add `GET /v1/admin/audit/verify` that walks the entire audit_log chain, re-computes each
row's expected hash from (prevHash || id || orgID || agentName || toolName || action ||
decision || timestamp), compares it to the stored hash, and returns a summary of whether
the chain is intact or identifies the first broken link.

## Context

FINDING-007 from TASK-051-security-findings.md (MEDIUM): a CISO cannot run an on-demand
integrity check without direct DB access. This endpoint gives them a one-click chain
verification path from the UI or curl. Filed as post-v1 — do not block the release on this.

## Acceptance criteria

- [ ] `GET /v1/admin/audit/verify` returns `{"ok": true, "entries_checked": N}` when chain is intact
- [ ] Returns `{"ok": false, "first_broken_id": "<uuid>", "entries_checked": N}` on first hash mismatch
- [ ] Endpoint requires admin JWT (role check) — not accessible to viewer or member roles
- [ ] `go vet ./...` exits 0
- [ ] At least one unit test: seeded chain passes; tampered hash fails

## Files to create or modify

- `eami-gateway/internal/audit/writer.go` — add `VerifyChain(ctx) (int, *uuid.UUID, error)` method
- `eami-gateway/internal/api/audit_handler.go` — new handler (create)
- `eami-gateway/internal/api/router.go` — wire GET /v1/admin/audit/verify
