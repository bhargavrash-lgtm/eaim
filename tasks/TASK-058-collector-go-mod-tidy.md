# Task: Run go mod tidy in eami-collector and eami-agent; commit go.sum
**From:** PM-EAMI  
**To:** BE-Collector  
**Priority:** high  
**Blocked by:** none

## What I need

The Windows service lifecycle changes (TASK-035) added imports from
`golang.org/x/sys/windows/svc`, `svc/mgr`, and `svc/eventlog`.
These packages are new transitive dependencies. `go.sum` is stale —
CI will fail on `go mod verify` or any `go build` on a clean machine
that doesn't have the packages cached.

## Steps

```bash
cd eami-collector
go mod tidy
# Verify go.sum updated:
git diff go.sum

cd ../eami-agent
go mod tidy
git diff go.sum
```

Then commit both updated `go.sum` files (and any `go.mod` changes) in
a single commit: `chore: go mod tidy for svc/mgr + svc/eventlog deps`.

## Acceptance criteria

- [ ] `go mod tidy` exits 0 in `eami-collector/`
- [ ] `go mod tidy` exits 0 in `eami-agent/`  
- [ ] `go.sum` in both modules is committed and up-to-date
- [ ] `go build ./...` in both modules exits 0 on a clean module cache
  (`GOFLAGS=-mod=readonly go build ./...`)

## Files to modify

- `eami-collector/go.sum`
- `eami-collector/go.mod` (if tidy removes unused deps)
- `eami-agent/go.sum`
- `eami-agent/go.mod` (if tidy removes unused deps)
