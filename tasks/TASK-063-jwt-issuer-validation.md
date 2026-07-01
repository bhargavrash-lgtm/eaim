# Task: Add issuer validation to JWT Validate() (FINDING JWT-001)
**From:** PM-EAMI  
**To:** BE-Gateway  
**Priority:** high  
**Blocked by:** none

## Problem

`Validate()` in `eami-gateway/internal/identity/tokens.go` does not check the `iss` claim.
A token issued by any instance with a compatible key passes validation.

Failing test: `TestManager_Validate_WrongIssuer_ReturnsError` in `tokens_test.go`

## Fix

1. Set `iss: "eami-gateway"` at token issuance in `IssueToken`.
2. Add `jwt.WithIssuer("eami-gateway")` to the `jwt.Parse` call in `Validate()`.

```go
// In IssueToken:
token.Set(jwt.IssuerKey, "eami-gateway")

// In Validate:
parsed, err := jwt.Parse(tokenStr, jwt.WithKey(...), jwt.WithIssuer("eami-gateway"))
```

## Acceptance criteria

- [ ] `TestManager_Validate_WrongIssuer_ReturnsError` passes (currently FAIL)
- [ ] Tokens issued by `IssueToken` include `"iss": "eami-gateway"`
- [ ] Tokens with wrong or missing issuer are rejected with an error
- [ ] `go vet ./...` exits 0

## Files to modify

- `eami-gateway/internal/identity/tokens.go`
