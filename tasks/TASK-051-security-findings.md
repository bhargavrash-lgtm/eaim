# TASK-051 Security Review Findings

**Reviewer:** QA-EAMI  
**Date:** 2026-06-29  
**Status:** 3 HIGH open (fix tasks filed), 4 MEDIUM open, 0 Critical

---

## FINDING-001: AUDIT-001 — Hash chain silently resets to genesis on DB error at startup
**Severity:** High  
**Area:** Audit  
**File:** `eami-gateway/internal/audit/writer.go` — `GetLastHash` error handling  
**Description:** When `GetLastHash` returns any error other than `ErrNoRows` (e.g. transient DB connectivity issue at startup), the writer treats it as an empty table and seeds with the genesis hash. This silently resets the chain — a new valid chain starts from genesis, making prior entries unverifiable.  
**Impact:** An attacker (or misconfiguration) that causes a transient DB error at gateway startup can cause the audit chain to fork. The gap is undetectable without an external chain-verification run.  
**Fix:** Propagate the error. Only treat `ErrNoRows` as genesis seed; all other errors must return and prevent the writer from starting.  
**Status:** Open — failing test `TestWriter_DBErrorOnInit_PropagatesError` in `writer_test.go`. Fix task: TASK-064  

---

## FINDING-002: JWT-001 — `Validate()` does not check issuer claim
**Severity:** High  
**Area:** JWT  
**File:** `eami-gateway/internal/identity/tokens.go` — `Validate()`  
**Description:** The JWT validation does not call `jwt.WithIssuer()`. A token issued by any EAMI instance (or any system using the same algorithm and a guessable key) will pass validation as long as the signature verifies.  
**Impact:** In multi-tenant or multi-cluster deployments, a token from cluster A is accepted by cluster B's gateway. Cross-cluster replay attacks become possible.  
**Fix:** Add `jwt.WithIssuer("eami-gateway")` to the `jwt.Parse` call. The issuer must be set at token issuance (`"iss": "eami-gateway"`) and validated on every request.  
**Status:** Open — failing test `TestManager_Validate_WrongIssuer_ReturnsError` in `tokens_test.go`. Fix task: TASK-063  

---

## FINDING-003: JWT-002 — In-memory revocation list is lost on gateway restart
**Severity:** High  
**Area:** JWT  
**File:** `eami-gateway/internal/identity/tokens.go` — revocation store  
**Description:** The revoked JTI list is held in memory only. On gateway restart (crash, deploy, or scale-out), all revocations are lost. A revoked token is accepted again after restart.  
**Impact:** Compromised tokens that were explicitly revoked become valid again after any gateway restart. In a multi-node deployment, revocations from node A are invisible to node B.  
**Fix:** Persist revocations to the `revoked_ai_tokens` DB table (already exists in schema.sql). On startup, load all non-expired rows into the in-memory set. On revoke, write to DB first, then update memory.  
**Status:** Open — failing test `TestManager_Validate_RevokedToken_SurvivesRestart` in `tokens_test.go`. Fix task: TASK-062  

---

## FINDING-004: JTI entropy — JWT IDs use UUID v4 (acceptable, documented)
**Severity:** Medium  
**Area:** JWT  
**File:** `eami-gateway/internal/identity/tokens.go`  
**Description:** JTIs are UUID v4 (128-bit, crypto/rand). This is acceptable entropy for revocation tracking. No fix required but worth noting for audit purposes.  
**Status:** Informational — no action needed  

---

## FINDING-005: GetLastHash ordering relies on `timestamp DESC` not sequence
**Severity:** Medium  
**Area:** Audit  
**File:** `eami-gateway/internal/audit/writer.go` — `GetLastHash` query  
**Description:** `SELECT hash FROM audit_log ORDER BY timestamp DESC LIMIT 1` — if two entries share the same timestamp (sub-millisecond writes), the ordering is non-deterministic and the wrong `prev_hash` could be loaded on restart.  
**Fix:** Add a monotonic sequence column (`serial` or `BIGSERIAL`) to `audit_log` and order by it instead of timestamp.  
**Status:** Medium — acceptable for v1 given the writer mutex serialises writes within a single process. File as a post-v1 improvement.  

---

## FINDING-006: bcrypt cost=10 in setup.sh (borderline for 2026 hardware)
**Severity:** Medium  
**Area:** Service Auth  
**File:** `scripts/setup.sh`  
**Description:** Admin password is hashed with bcrypt cost 10. OWASP 2023 recommends cost ≥ 12 for bcrypt on modern hardware.  
**Fix:** Change to `bcrypt.gensalt(rounds=12)` in the python3 bcrypt call in `setup.sh`.  
**Status:** Open — low urgency, fix in TASK-052 (setup.sh macOS pass). Added to that task's scope.  

---

## FINDING-007: No audit chain verify endpoint
**Severity:** Medium  
**Area:** Audit  
**File:** N/A — missing endpoint  
**Description:** There is no `GET /v1/audit/verify-chain` endpoint. A CISO cannot run an on-demand integrity check without manual DB access.  
**Status:** Open — post-v1 feature. File as backlog after ship.  

---

## Passing areas (no findings)

- **SQL injection:** All queries use pgx parameterised statements. No `fmt.Sprintf` in SQL construction found.  
- **API key entropy:** `crypto/rand` used throughout. Keys stored as SHA-256 hashes. ✅  
- **Constant-time comparison:** Both `requireServiceKey` middlewares use `subtle.ConstantTimeCompare`. ✅  
- **Algorithm confusion:** `jwt-go` configured with explicit algorithm allowlist — `"none"` and algorithm switching not possible. ✅  
- **Token expiry:** `exp` claim validated on every request via `jwt.Parse`. ✅  

---

## Fix tasks filed

| Task | Severity | Assignee | Finding |
|------|----------|----------|---------|
| TASK-062 | High | BE-Gateway | JWT-002: persist revocation to DB |
| TASK-063 | High | BE-Gateway | JWT-001: add issuer validation |
| TASK-064 | High | BE-Gateway | AUDIT-001: propagate DB error on init |
