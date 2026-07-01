# Task: Propagate DB error in audit writer init (FINDING AUDIT-001)
**From:** PM-EAMI  
**To:** BE-Gateway  
**Priority:** high  
**Blocked by:** none

## Problem

In `eami-gateway/internal/audit/writer.go`, when `GetLastHash` returns any error
other than `ErrNoRows`, the writer silently falls back to the genesis hash. This
resets the audit chain on any transient DB error at startup — the gap is undetectable.

Failing test: `TestWriter_DBErrorOnInit_PropagatesError` in `writer_test.go`

## Fix

In the lazy-init block inside `Write()`, distinguish between "no rows" (genesis seed)
and "real error" (must abort):

```go
hash, err := w.db.GetLastHash(ctx)
if err != nil {
    if errors.Is(err, ErrNoRows) {
        // First entry — seed genesis hash
        g := sha256.Sum256([]byte("eami-genesis-2026"))
        w.lastHash = hex.EncodeToString(g[:])
    } else {
        // Real DB error — do not silently reset the chain
        return fmt.Errorf("audit: failed to load last hash: %w", err)
    }
} else {
    w.lastHash = hash
}
w.initialized = true
```

## Acceptance criteria

- [ ] `TestWriter_DBErrorOnInit_PropagatesError` passes (currently FAIL)
- [ ] `Write()` returns an error when `GetLastHash` fails with a non-ErrNoRows error
- [ ] `Write()` still seeds genesis correctly when `GetLastHash` returns `ErrNoRows`
- [ ] `go vet ./...` exits 0

## Files to modify

- `eami-gateway/internal/audit/writer.go` — lazy-init block in `Write()`
