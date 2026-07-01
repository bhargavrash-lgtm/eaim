# Task: Implement FinOps API endpoints
**From:** PM-EAMI
**To:** BE-Policy
**Priority:** high
**Blocked by:** none

## What I need

Implement two read-only endpoints in `eami-api` against the `token_usage` TimescaleDB table:

1. `GET /v1/finops/summary?from=DATE&to=DATE`
2. `GET /v1/finops/timeseries?from=DATE&to=DATE&granularity=day|hour|week&agent_id=UUID`

The FinOps UI page (`eami-ui/src/pages/finops/FinOpsPage.tsx`) is already built and calls these via `useFinOpsSummary` and `useFinOpsTimeSeries` hooks. It is waiting for real data.

## Context

- `token_usage` table is defined in `schema/schema.sql` — it is a TimescaleDB hypertable partitioned on `recorded_at`
- All queries must filter by `org_id` from the JWT claims (never cross-org leakage)
- `model_pricing` table holds `cost_per_1k_in` and `cost_per_1k_out` — use it to compute `cost_usd` at query time if the `token_usage.cost_usd` column is null
- Memory/episodes endpoints (`/v1/memory/*`) are low priority (episode recorder not built yet) — implement as stubs returning empty lists

## Acceptance criteria

- [ ] `GET /v1/finops/summary` returns `TokenSpendSummary` shape:
  - `period_start`, `period_end` (from query params)
  - `total_cost_usd`, `total_tokens_in`, `total_tokens_out`
  - `by_agent`: array of `{agent_id, agent_name, cost_usd, tokens_in, tokens_out, request_count}`
  - `by_team`: array of `{team, cost_usd, tokens_in, tokens_out}`
  - `by_model`: array of `{model, cost_usd, tokens_in, tokens_out}`
  - Returns `{"total_cost_usd":0,...,"by_agent":[],...}` (not 404) when no data
- [ ] `GET /v1/finops/timeseries` returns `SpendTimeSeries` shape:
  - `granularity` echoed back
  - `series`: array of `{timestamp, cost_usd, tokens}` bucketed by granularity
  - Uses `time_bucket()` TimescaleDB function for bucketing
  - `agent_id` filter is optional
- [ ] Both endpoints require Bearer JWT (`jwtMiddleware` already applied to all `/v1/*` routes via router.go)
- [ ] Both endpoints registered in `internal/api/router.go` under the viewer read group
- [ ] `GET /v1/memory/episodes` → stub returning `{"data":[],"meta":{"total":0,"page":1,"per_page":25}}`
- [ ] `GET /v1/memory/episodes/search` → stub returning `{"data":[]}`
- [ ] All 3 memory stubs registered in router.go
- [ ] `go build ./...` passes from `eami-api/`
- [ ] No `any` types; all SQL via `pgx` row scanning (no sqlc needed for these queries — hand-write them)

## Files to create or modify

- `eami-api/internal/api/finops.go` — new file: FinOpsSummary, FinOpsTimeSeries handlers
- `eami-api/internal/api/memory.go` — new file: stub handlers for /v1/memory/*
- `eami-api/internal/api/router.go` — add the 5 new routes
- `eami-api/internal/api/types.go` — add request/response types: TokenSpendSummary, AgentSpend, TeamSpend, ModelSpend, SpendTimeSeries

## SQL reference

```sql
-- Summary totals
SELECT
  COALESCE(SUM(cost_usd), 0) AS total_cost_usd,
  COALESCE(SUM(tokens_in), 0) AS total_tokens_in,
  COALESCE(SUM(tokens_out), 0) AS total_tokens_out
FROM token_usage
WHERE org_id = $1
  AND recorded_at >= $2
  AND recorded_at < $3;

-- By agent
SELECT agent_id, agent_name,
  COALESCE(SUM(cost_usd),0) AS cost_usd,
  SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out,
  COUNT(*) AS request_count
FROM token_usage
WHERE org_id = $1 AND recorded_at >= $2 AND recorded_at < $3
GROUP BY agent_id, agent_name ORDER BY cost_usd DESC;

-- Timeseries (day granularity)
SELECT time_bucket('1 day', recorded_at) AS ts,
  COALESCE(SUM(cost_usd),0) AS cost_usd,
  SUM(tokens_in + tokens_out) AS tokens
FROM token_usage
WHERE org_id = $1 AND recorded_at >= $2 AND recorded_at < $3
GROUP BY ts ORDER BY ts;
```

## Note on `go 1.24`

`eami-api/go.mod` must stay on `go 1.24`. Do not bump to 1.25 (doesn't exist).
