# Task: Add POST /v1/internal/token-usage endpoint to eami-api
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** high  
**Blocked by:** TASK-036 (schema must exist), TASK-039 (gateway calls this endpoint)

## What I need

The gateway fires a goroutine after every proxied MCP call that POSTs token counts to eami-api. This endpoint must exist to receive them.

### Endpoint spec

```
POST /v1/internal/token-usage
X-Service-Key: <service_key>
Content-Type: application/json

Body:
{
  "org_id":        "uuid",
  "agent_id":      "uuid",
  "agent_name":    "string",
  "model":         "string",       // e.g. "claude-sonnet-4-6", "" if unknown
  "input_tokens":  123,
  "output_tokens": 42,
  "recorded_at":   "2026-06-11T10:00:00Z"
}

Response 202:
{ "status": "accepted" }
```

### Auth

Use the existing `requireServiceKey` middleware (delivered in TASK-037). Same `X-Service-Key` header, same `cfg.ServiceKey` value.

### Handler logic

1. Parse the request body.
2. Validate: `org_id` and `agent_id` must be valid UUIDs; `recorded_at` must parse as RFC3339.
3. Look up model price from `model_pricing` table (`SELECT input_cost_per_1k, output_cost_per_1k FROM model_pricing WHERE model = $1`). If model not found, use `0.0` for both costs — do not error.
4. Compute cost: `cost = (input_tokens / 1000.0 * input_cost) + (output_tokens / 1000.0 * output_cost)`.
5. INSERT into `token_usage`:
   ```sql
   INSERT INTO token_usage (org_id, agent_id, model, input_tokens, output_tokens, cost_usd, recorded_at)
   VALUES ($1, $2, $3, $4, $5, $6, $7)
   ```
6. Return `202 {"status": "accepted"}`.

### Route registration

Register on the Chi router in `router.go` **outside** the JWT middleware group — this route uses service key auth, not user JWT:

```go
r.With(s.requireServiceKey).Post("/v1/internal/token-usage", s.IngestTokenUsage)
```

## Context

Without this endpoint, the gateway's token writes silently fail (fire-and-forget goroutine logs the error but doesn't surface it). FinOps charts stay empty.

## Acceptance criteria

- [ ] `POST /v1/internal/token-usage` with valid service key and valid body returns `202`
- [ ] Row appears in `token_usage` table with correct `org_id`, `agent_id`, `model`, token counts, and `cost_usd`
- [ ] When model is not in `model_pricing`, cost is `0.00` and row is still inserted (not an error)
- [ ] Missing or invalid service key returns `401`
- [ ] Malformed body (bad UUID, bad timestamp) returns `400`
- [ ] `go vet ./...` exits 0

## Files to create or modify

- `eami-api/internal/api/reports.go` — add `IngestTokenUsage` handler (alongside the endpoint reports handlers from TASK-037)
- `eami-api/internal/api/router.go` — register the new route
- `eami-api/internal/store/query/token_usage.sql` — INSERT + model price lookup SQL
- `eami-api/internal/store/token_usage.sql.go` — hand-implement store methods

## Files to read first

- `schema/schema.sql` — `token_usage` table definition and `model_pricing` table
- `eami-api/internal/api/middleware.go` — `requireServiceKey` pattern
- `eami-api/internal/api/router.go` — where to add the route
- `eami-api/internal/store/finops.sql.go` — reads from `token_usage`; your INSERT must match its columns
