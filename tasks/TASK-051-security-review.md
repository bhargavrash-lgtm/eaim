# Task: Security review — JWT, API keys, audit integrity, RLS
**From:** PM-EAMI  
**To:** QA-EAMI + Architect-EAMI  
**Priority:** normal  
**Blocked by:** TASK-038 (clean migrations), TASK-039 (gateway token auth)

## What I need

A structured security review covering the attack surface a CISO will scrutinise before buying. Review each area, document findings, and fix any critical or high issues.

### Area 1 — JWT security (QA-EAMI)

File: `eami-api/internal/auth/auth.go`, `eami-gateway/internal/identity/tokens.go`

Check:
- **Algorithm confusion**: Is `alg` in the JWT header validated? Can an attacker substitute `"none"` or switch RS256 to HS256?
- **Key strength**: Auto-generated dev RSA key — is it 2048-bit minimum? What happens if the key file is missing in production?
- **Token expiry**: Are `exp` claims validated on every request? What is the access token TTL?
- **Refresh token security**: Are refresh tokens single-use? Are they stored hashed?
- **Revocation**: Is there an in-memory or DB revocation list? What happens if a user's JWT is compromised?

### Area 2 — API key entropy (QA-EAMI)

File: `eami-api/internal/auth/auth.go` (API key generation)

Check:
- Are API keys generated with `crypto/rand`? (not `math/rand`)
- Are they at least 32 bytes of entropy?
- Are they stored hashed (bcrypt or SHA-256) in the DB, not in plaintext?
- Is there a brute-force protection on the key validation endpoint?

### Area 3 — Audit log integrity (Architect-EAMI)

File: `eami-gateway/internal/audit/writer.go`, `schema/schema.sql`

Check:
- Is the SHA-256 hash chain seeded correctly from genesis on first start?
- Does `WriteAuditEntry` validate that the previous hash exists before writing?
- Is there a `DELETE` or `UPDATE` pathway that could silently break the chain?
- Is there a verification endpoint (`GET /v1/audit/verify-chain`) that checks integrity? If not, flag as a P1 gap.
- RLS on `audit_log` — does the Postgres policy correctly prevent `eami_app` from deleting rows?

### Area 4 — Input validation and injection (QA-EAMI)

Check:
- Are all UUIDs validated before being interpolated into SQL? (`pgx` parameterised queries should prevent this — verify no raw string interpolation)
- Is there any `fmt.Sprintf` used in SQL query construction? This would be a critical finding.
- Are file paths from agent reports sanitised before any server-side use?
- Is the collector API key compared using constant-time comparison (`subtle.ConstantTimeCompare`)?

### Area 5 — Service-to-service auth (QA-EAMI)

Check:
- `X-Service-Key` is compared against `cfg.ServiceKey` — is this a constant-time comparison?
- What is the default value of `service_key` in `eami-api.yaml` and `eami-gateway.yaml`? If it's `"changeme"`, `setup.sh` must replace it with a random value.
- Verify `setup.sh` generates a cryptographically random service key.

### Report format

Write findings to `tasks/TASK-051-security-findings.md`. Use this format per finding:

```
## FINDING-001: <title>
**Severity:** Critical | High | Medium | Low | Info
**Area:** JWT | API Keys | Audit | Injection | Service Auth
**File:** path/to/file.go:line
**Description:** What the vulnerability is.
**Impact:** What an attacker could do.
**Fix:** Specific code change required.
**Status:** Open | Fixed (link to change)
```

Fix all Critical and High findings before closing this task.

## Acceptance criteria

- [ ] All 5 areas reviewed
- [ ] `tasks/TASK-051-security-findings.md` exists with all findings documented
- [ ] Zero Critical findings remain open
- [ ] Zero High findings remain open
- [ ] All fixes pass `go vet ./...`
- [ ] `setup.sh` generates a random service key (not "changeme")

## Files to read

- `eami-api/internal/auth/auth.go`
- `eami-gateway/internal/identity/tokens.go`
- `eami-gateway/internal/audit/writer.go`
- `eami-api/internal/api/middleware.go`
- `eami-collector/internal/api/middleware.go`
- `schema/schema.sql` — RLS policies
- `scripts/setup.sh` — secret generation
