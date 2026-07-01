# Task: Write token_usage on every proxied call + fix JSON-RPC DENY error format
**From:** PM-EAMI  
**To:** BE-Gateway  
**Priority:** high  
**Blocked by:** none

## What I need

Two changes to the gateway dispatch function in `eami-gateway/cmd/gateway/main.go`.

---

### Part 1 — Write token_usage after every ALLOW

The gateway already proxies the call via `fwdProxy.Forward()`. The response is a `json.RawMessage`. Parse the response for token counts and write a record to the `token_usage` table via `POST /v1/internal/token-usage` on the eami-api.

**Where:** In the `default: // policy.ActionAllow` branch, after `fwdProxy.Forward()` succeeds.

**What to send:**

```go
type tokenUsagePayload struct {
    OrgID      string    `json:"org_id"`
    AgentID    string    `json:"agent_id"`
    AgentName  string    `json:"agent_name"`
    Model      string    `json:"model"`      // extract from MCP response if present; else ""
    InputTokens  int     `json:"input_tokens"`
    OutputTokens int     `json:"output_tokens"`
    RecordedAt string    `json:"recorded_at"` // RFC3339
}
```

**How to extract token counts from the MCP response:**

MCP responses from Anthropic models contain a `usage` object:
```json
{ "usage": { "input_tokens": 150, "output_tokens": 42 } }
```

Try to parse this. If parsing fails or the fields are absent, send `input_tokens: 0, output_tokens: 0` (don't fail the request).

**How to write:**

```go
// Fire-and-forget — do not block the MCP response on this write
go func() {
    writeTokenUsage(context.Background(), cfg.API.BaseURL, cfg.API.ServiceKey, payload)
}()
```

Add a helper `writeTokenUsage(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error` that POSTs JSON to `apiBase + "/v1/internal/token-usage"` with `X-Service-Key: serviceKey`. Log errors but do not propagate them.

**Config:** Add to `eami-gateway.yaml`:
```yaml
api:
  base_url: "http://eami-api:8081"
  service_key: "changeme"
```

Add corresponding fields to `eami-gateway/internal/config/config.go` and wire env vars `GATEWAY_API_BASE_URL` + `GATEWAY_API_SERVICE_KEY`.

---

### Part 2 — JSON-RPC structured error for DENY

Currently the DENY path returns `fmt.Errorf("policy denied: %s", reason)`. The MCP handler converts this to some error response — but it must conform to the JSON-RPC 2.0 error spec.

Find where `DecisionHandler` errors are converted to HTTP/SSE responses in `eami-gateway/internal/mcp/handler.go`. Change the error response to:

```json
{
  "jsonrpc": "2.0",
  "id": <original_request_id>,
  "error": {
    "code": -32600,
    "message": "Request denied by policy",
    "data": {
      "reason": "<decision.Reason>",
      "policy_id": "<decision.PolicyID or empty string>"
    }
  }
}
```

To pass the structured error data from the dispatch function to the handler, use a sentinel error type:

```go
// In eami-gateway/internal/mcp/errors.go (new file)
type PolicyDeniedError struct {
    Reason   string
    PolicyID string
}
func (e *PolicyDeniedError) Error() string { return "policy denied: " + e.Reason }
```

In the dispatch function, return `&PolicyDeniedError{...}` instead of `fmt.Errorf(...)`.
In the MCP handler's error handler, type-assert to `*PolicyDeniedError` and build the structured JSON-RPC error.

---

## Context

The FinOps page is currently empty because no `token_usage` rows are written. This is the primary value prop for the CIO buyer ("how much am I spending on AI?"). Token counting is fire-and-forget — it must not add latency to the proxied call.

The DENY error format matters for MCP client compatibility — Claude Desktop and other clients expect a properly structured JSON-RPC error, not a raw HTTP error string.

## Acceptance criteria

- [ ] After 5 proxied MCP calls, `SELECT COUNT(*) FROM token_usage` returns 5 (or more, if token counts are split per call)
- [ ] `token_usage` rows have non-zero `org_id`, `agent_id`, and `recorded_at`
- [ ] Token write failure does not affect the MCP response — proxied call still returns normally
- [ ] A denied MCP call returns a JSON-RPC error with `"code": -32600` and a `"data"` object containing `"reason"` and `"policy_id"`
- [ ] `go vet ./...` exits 0 in `eami-gateway/`

## Files to create or modify

- `eami-gateway/cmd/gateway/main.go` — add `writeTokenUsage` helper, fire goroutine in ALLOW branch, add API config
- `eami-gateway/internal/config/config.go` — add `API.BaseURL`, `API.ServiceKey`; add env var overrides
- `eami-gateway/eami-gateway.yaml` — add `api:` section
- `eami-gateway/internal/mcp/errors.go` — new file: `PolicyDeniedError` type
- `eami-gateway/internal/mcp/handler.go` — use `PolicyDeniedError` to build structured JSON-RPC error

## Files to read first

- `eami-gateway/cmd/gateway/main.go` — dispatch function (lines 110-179)
- `eami-gateway/internal/mcp/handler.go` — how `DecisionHandler` errors are converted to responses
- `eami-gateway/internal/config/config.go` — existing config struct + env var pattern
- `schema/schema.sql` — `token_usage` table definition (see the TimescaleDB section)
