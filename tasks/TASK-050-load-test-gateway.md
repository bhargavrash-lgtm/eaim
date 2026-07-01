# Task: Load test eami-gateway — 100 concurrent MCP sessions
**From:** PM-EAMI  
**To:** QA-EAMI  
**Priority:** normal  
**Blocked by:** TASK-039 (gateway token usage), TASK-041 (token-usage endpoint), stack must be running

## What I need

The gateway is in the hot path for every AI tool call. Before shipping, we need to know it can handle realistic concurrent load without memory leaks, goroutine leaks, or response time degradation.

### Test tool

Use [k6](https://k6.io/) or `go test -bench` — your choice. k6 is preferred because it can open real SSE connections.

### Test scenario

**Phase 1 — Ramp-up (1 minute):** 0 → 100 concurrent virtual users, each opening one SSE session.

**Phase 2 — Sustained load (3 minutes):** 100 VUs, each sending 1 MCP tool call per 5 seconds (20 calls/min per VU = 2000 calls/min total).

**Phase 3 — Ramp-down (1 minute):** 100 → 0 VUs.

Each VU:
1. Issues a gateway JWT (`POST /v1/identity/issue`).
2. Opens an SSE session (`GET /v1/mcp/sse`).
3. Sends `POST /v1/mcp/messages?sessionId=<id>` every 5s with a valid JSON-RPC tool call.
4. The tool call policy is ALLOW (no DB, no approval wait) — use a seeded agent with no deny policies.
5. Closes the SSE session at the end.

### Thresholds (must all pass)

| Metric | Threshold |
|---|---|
| p95 message dispatch latency | < 100ms (excluding downstream AI time) |
| p99 message dispatch latency | < 500ms |
| Error rate | < 0.1% |
| Memory growth over 5 min | < 50MB |
| Goroutine count after ramp-down | back to baseline ± 10 |

### Goroutine check

After the test, hit `http://localhost:6060/debug/pprof/goroutine?debug=1` (add a pprof endpoint to the gateway for this test) and check that session goroutines have been cleaned up.

### Report

Write results to `tasks/TASK-050-results.md`:
- k6 (or go bench) summary output
- p50/p95/p99 latencies
- Error count and types
- Memory before/after
- Goroutine before/after
- Pass/fail for each threshold
- Any findings (memory leaks, goroutine leaks, bottlenecks)

## Acceptance criteria

- [ ] Load test runs to completion (no test tool crash)
- [ ] All 5 thresholds pass
- [ ] `tasks/TASK-050-results.md` exists with full results
- [ ] Any goroutine or memory leak found is documented as a bug with a proposed fix

## Files to create

- `tasks/TASK-050-results.md` — test results
- `eami-gateway/cmd/gateway/main.go` — add `net/http/pprof` import + `/debug/pprof/` route (behind a `GATEWAY_PPROF_ENABLED=true` env var, disabled by default)
- `tests/load/gateway.js` (k6 script) or `tests/load/gateway_bench_test.go`

## Files to read first

- `eami-gateway/internal/mcp/handler.go` — SSE session lifecycle
- `eami-gateway/internal/mcp/session.go` — session manager, goroutine management
- `eami-gateway/cmd/gateway/main.go` — server setup
