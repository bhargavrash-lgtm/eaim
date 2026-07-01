# Task: TASK-057 Revision — restore "critical" to validRiskTiers
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** high  
**Blocked by:** none

## What needs fixing

`validRiskTiers` in `agents.go` currently has `{low, medium, high}` but the error message
on lines 105 and 169 reads `"must be one of: low, medium, high, critical"`.
The map and the error message disagree. This is a live bug — the user sees "critical is valid"
but the code returns 400 for it.

**PM decision: "critical" is a valid risk tier.** The four-tier hierarchy is
`low → medium → high → critical`. This was in the original TASK-057 spec.

## Fix

### agents.go

```go
var validRiskTiers = map[string]bool{
    "low": true, "medium": true, "high": true, "critical": true,
}
```

Error messages on lines ~105 and ~169 are already correct — no change needed there.

### agents_test.go

Line ~286 currently sets `risk_tier: "critical"` and expects 400.
Change it to use an actually-invalid value:

```go
payload["risk_tier"] = "extreme" // not a valid enum value
```

The comment and assertion stay the same — only the test value changes.

## Acceptance criteria

- [ ] `validRiskTiers` includes "critical"
- [ ] `POST /v1/gateway/agents` with `risk_tier: "critical"` returns 201 (valid)
- [ ] `POST /v1/gateway/agents` with `risk_tier: "extreme"` returns 400 (invalid)
- [ ] Error message matches map: `"must be one of: low, medium, high, critical"`
- [ ] `go test ./...` passes

## Files to modify

- `eami-api/internal/api/agents.go` — add "critical" to validRiskTiers
- `eami-api/internal/api/agents_test.go` — change "critical" → "extreme" in invalid-tier test
