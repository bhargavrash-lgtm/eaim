# Task: Add test coverage — eami-api FinOps handlers
**From:** PM-EAMI
**To:** QA-EAMI
**Priority:** normal
**Blocked by:** TASK-031 (complete)

## Context

`eami-api/internal/api/finops.go` was delivered in TASK-031. It has two handlers:
- `FinOpsSummary` — `GET /v1/finops/summary?from=DATE&to=DATE`
- `FinOpsTimeSeries` — `GET /v1/finops/timeseries?from=DATE&to=DATE&granularity=day|hour|week&agent_id=UUID`

**Important:** Both handlers call `s.queries.DB()` to obtain a raw `*pgxpool.Pool` and run raw SQL with `time_bucket()` (a TimescaleDB function). They do **not** go through the `Store` interface. This means:
- The DB-execution path cannot be tested with the existing `MockStore`.
- Unit tests can only cover the **validation and routing logic** (which is still valuable).
- Full SQL execution tests require testcontainers-go with `timescale/timescaledb-ha:pg16` — defer those to a later task.

## What I need

### `eami-api/internal/api/finops_test.go`

Write handler unit tests that cover the validation paths. Use `httptest.NewServer` with a `Server` backed by `MockStore` (already in `store_mock.go`). You do NOT need a real database.

The `DB()` path will not be exercised in these tests — that's acceptable for now. The tests should return before reaching `s.queries.DB()` because the request fails validation first.

#### Tests to write

**`parseDateParam` helper:**
- `TestParseDateParam_RFC3339` — parses `2025-01-01T00:00:00Z` correctly
- `TestParseDateParam_DateOnly` — parses `2025-01-01` correctly
- `TestParseDateParam_Missing` — empty string → error
- `TestParseDateParam_Invalid` — `"notadate"` → error

**`FinOpsSummary` validation:**
- `TestFinOpsSummary_MissingFrom` — no `from` param → 400
- `TestFinOpsSummary_MissingTo` — no `to` param → 400
- `TestFinOpsSummary_ToBeforeFrom` — `from=2025-12-01&to=2025-01-01` → 400
- `TestFinOpsSummary_EqualFromTo` — same date for both → 400 (`to must be after from`)
- `TestFinOpsSummary_RequiresAuth` — no JWT → 401

**`FinOpsTimeSeries` validation:**
- `TestFinOpsTimeSeries_InvalidGranularity` — `granularity=month` → 400
- `TestFinOpsTimeSeries_InvalidAgentID` — `agent_id=notauuid` → 400
- `TestFinOpsTimeSeries_MissingFrom` → 400
- `TestFinOpsTimeSeries_ToBeforeFrom` → 400
- `TestFinOpsTimeSeries_RequiresAuth` — no JWT → 401

## How to wire up the test server

Look at how `auth_test.go` and `agents_test.go` construct the server. Same pattern — use `NewMockStore()` and `auth.NewService("", ...)` (ephemeral dev key). You do not need `s.queries` to be non-nil for these tests because all assertions fail before reaching the DB call.

If `Server.queries` is a non-interface field that panics on nil access in middleware/other paths, set it to a minimal stub that satisfies the compiler. Check `internal/api/router.go` and `server.go` to see the exact field type.

## Acceptance criteria

- [ ] `go test -count=1 -run TestParseDateParam ./internal/api/...` from `eami-api/` passes
- [ ] `go test -count=1 -run TestFinOpsSummary ./internal/api/...` from `eami-api/` passes
- [ ] `go test -count=1 -run TestFinOpsTimeSeries ./internal/api/...` from `eami-api/` passes
- [ ] All tests compile: `go vet ./...` exits 0
- [ ] No `t.Skip()` — all tests must actually run
- [ ] No new external deps beyond what is already in `eami-api/go.mod`
- [ ] `go 1.24` — do not bump

## Files to create or modify

- `eami-api/internal/api/finops_test.go` — new file

## Files to read first

- `eami-api/internal/api/finops.go` — handler source
- `eami-api/internal/api/store_mock.go` — existing mock + Store interface
- `eami-api/internal/api/auth_test.go` — reference for test server setup pattern
- `eami-api/internal/api/router.go` — to understand Server struct wiring
