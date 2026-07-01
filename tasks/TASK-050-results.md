# TASK-050 Load Test Results

**Status: BLOCKED — stack not running**

The k6 script (`tests/load/gateway.js`) is written and ready.
Real numbers cannot be produced without a live EAMI stack.

## How to run

```bash
# 1. Start the stack
docker compose up -d

# 2. Register an agent and get a token
# (see docs/quickstart.md — Step 4)

# 3. Run the load test
GATEWAY_URL=http://localhost:8080 \
PPROF_URL=http://localhost:6060 \
AGENT_TOKEN=<token> \
k6 run tests/load/gateway.js

# 4. In a separate terminal: monitor memory during the run
watch -n5 "docker stats --no-stream eami-gateway --format '{{.MemUsage}}'"
```

## Prerequisites

- k6 v0.50+ installed
- pprof endpoint enabled on the gateway (TASK-061)
- A running EAMI stack (`docker compose up -d`)
- A registered gateway agent with a valid bearer token

## Thresholds (pass/fail)

| Metric | Threshold | Result |
|--------|-----------|--------|
| p95 MCP latency | < 100ms | PENDING |
| p99 MCP latency | < 500ms | PENDING |
| Error rate | < 0.1% | PENDING |
| Memory growth | < 50MB | PENDING |
| Goroutine baseline | restored after ramp-down | PENDING |

## Known limitations

- k6 standard HTTP closes the SSE connection after the first chunk.
  If the gateway requires a persistent SSE stream for session validity,
  switch to `k6/experimental/sse` (k6 v0.50+).
- Memory growth is measured via `docker stats` externally — k6 cannot
  measure server-side memory.

## Update this file

Replace PENDING rows with actual measured values after a successful run.
