# Task: Run go test ./... across all services and report failures
**From:** PM-EAMI  
**To:** QA-EAMI  
**Priority:** high  
**Blocked by:** none (go vet is already clean)

## What I need

`go vet ./...` is clean across `eami-api/`, `eami-gateway/`, `eami-policy/`, `eami-collector/`, and `eami-agent/`. The next step is to run the full test suite and find out what fails.

### Step 1 — Run go test in each service

```bash
# From D:\AI\EAMI\Repo\

docker run --rm -v D:\AI\EAMI\Repo\eami-policy:/src -w /src golang:1.24-alpine go test -count=1 -timeout 60s ./... 2>&1
docker run --rm -v D:\AI\EAMI\Repo\eami-api:/src -w /src golang:1.24-alpine go test -count=1 -timeout 120s ./... 2>&1
docker run --rm -v D:\AI\EAMI\Repo\eami-gateway:/src -w /src golang:1.24-alpine go test -count=1 -timeout 120s ./... 2>&1
docker run --rm -v D:\AI\EAMI\Repo\eami-collector:/src -w /src golang:1.24-alpine go test -count=1 -timeout 60s ./... 2>&1
docker run --rm -v D:\AI\EAMI\Repo\eami-agent:/src -w /src golang:1.24-alpine go test -count=1 -timeout 60s -tags "!windows" ./... 2>&1
```

Note: `eami-agent` has Windows-only build tags — run with `-tags "!windows"` on Linux to test the platform-agnostic code. Platform-specific code is tested manually on Windows.

### Step 2 — Triage failures

For each failing test, determine:
1. Is it a **compilation failure**? (fix immediately)
2. Is it a **test that requires a real DB**? (these are expected to fail in CI without a live PostgreSQL — mark as "needs testcontainers or skip tag")
3. Is it a **logic bug**? (file a bug, fix if straightforward)
4. Is it a **flaky test**? (mark and move on)

### Step 3 — Fix what you can

Fix any compilation failures and straightforward logic bugs directly. For tests that require a live DB (integration tests), add a `t.Skip("requires live DB")` guard:

```go
if os.Getenv("EAMI_TEST_DB_DSN") == "" {
    t.Skip("set EAMI_TEST_DB_DSN to run integration tests")
}
```

Do not use `t.Skip` to hide logic bugs — only for infrastructure dependencies.

### Step 4 — Report

Write a test status report to `tasks/TASK-040-results.md` with:
- Which services are clean (all tests pass)
- Which services have failures, and what kind
- List of tests skipped vs. failing vs. passing
- Recommended fixes for anything you couldn't fix in this session

## Context

`go test` will surface things `go vet` doesn't catch: nil pointer panics in test setup, incorrect mock assertions, handlers that call `s.queries` on a nil pointer, etc. We need to know the real state before committing to the M6 milestone ("all tests green").

## Acceptance criteria

- [ ] All 5 services have been run through `go test ./...`
- [ ] Compilation failures are zero
- [ ] All test failures are triaged (type categorised)
- [ ] Any straightforward logic bugs are fixed
- [ ] Integration test guards (`t.Skip`) added where appropriate
- [ ] `tasks/TASK-040-results.md` exists with the full triage report

## Files to create

- `tasks/TASK-040-results.md` — test run output + triage

## Files to modify (if needed)

- Any `*_test.go` file with compilation errors or straightforward logic bugs
- No production code should be modified in this task — only test files
