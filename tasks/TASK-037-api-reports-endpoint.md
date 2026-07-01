# Task: Add POST /v1/reports + GET /v1/discover/endpoints to eami-api
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** high  
**Blocked by:** none (TASK-036 delivered — 004_endpoints.sql exists)

## What I need

The eami-collector forwarder sends batches of agent network-observation reports to the API.
Add `POST /v1/reports` to receive them, plus `GET /v1/discover/endpoints` and
`GET /v1/discover/endpoints/{id}` to serve the Discover page.

**READ THESE FIRST before writing any code:**
- `schema/migrations/004_endpoints.sql` — the actual table definition (column names matter)
- `api/openapi.yaml` — the contract (Architect already added all three routes)
- `eami-api/internal/api/agents.go` — pattern for CRUD handlers
- `eami-api/internal/api/middleware.go` — existing middleware
- `eami-api/internal/api/router.go` — where to register routes
- `eami-api/internal/store/agents.sql.go` — hand-implemented store pattern

---

## `POST /v1/reports` — collector write path

### Auth

New middleware `requireServiceKey`:
```go
// eami-api/internal/api/middleware.go
func (s *Server) requireServiceKey(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        key := r.Header.Get("X-Service-Key")
        if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.ServiceKey)) != 1 {
            respondError(w, http.StatusUnauthorized, "invalid service key")
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

Add `ServiceKey string` to `eami-api/internal/config/config.go` and `eami-api.yaml`
(example value: `"changeme"`). Read from env `API_SERVICE_KEY` as override.

### Request body

```go
type IngestReportsRequest struct {
    Reports []ObservedEndpoint `json:"reports"`
}

type ObservedEndpoint struct {
    OrgID      string   `json:"org_id"`       // UUID string
    SourceHost string   `json:"source_host"`   // reporting machine hostname
    Method     string   `json:"method"`        // HTTP method: GET, POST, etc.
    Host       string   `json:"host"`          // destination host e.g. api.openai.com
    Path       string   `json:"path"`          // e.g. /v1/chat/completions
    Port       int      `json:"port,omitempty"`
    TLS        bool     `json:"tls,omitempty"`
    Tags       []string `json:"tags,omitempty"` // e.g. ["llm","openai"]
    RawHeaders map[string]string `json:"raw_headers,omitempty"` // SCRUB auth values (see below)
}
```

### UPSERT SQL (must match 004_endpoints.sql exactly)

Unique key is `(org_id, source_host, method, host, path)`.

```sql
-- eami-api/internal/store/query/endpoints.sql

-- name: UpsertEndpoint :exec
INSERT INTO discovered_endpoints
    (org_id, source_host, method, host, path, port, tls, tags, raw_headers, last_seen)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (org_id, source_host, method, host, path) DO UPDATE SET
    last_seen  = NOW(),
    hit_count  = discovered_endpoints.hit_count + 1,
    tags       = EXCLUDED.tags,
    raw_headers = EXCLUDED.raw_headers;

-- name: ListEndpoints :many
SELECT * FROM discovered_endpoints
WHERE org_id = $1
ORDER BY last_seen DESC
LIMIT $2 OFFSET $3;

-- name: GetEndpoint :one
SELECT * FROM discovered_endpoints
WHERE id = $1 AND org_id = $2;
```

Hand-implement these in `eami-api/internal/store/endpoints.sql.go` (do not wait for sqlc).

### Header scrubbing

Before upserting `raw_headers`, strip any key whose name contains (case-insensitive):
`authorization`, `x-api-key`, `x-service-key`, `cookie`, `set-cookie`.

```go
func scrubHeaders(h map[string]string) map[string]string {
    blocked := []string{"authorization", "x-api-key", "x-service-key", "cookie", "set-cookie"}
    out := make(map[string]string, len(h))
    for k, v := range h {
        lower := strings.ToLower(k)
        safe := true
        for _, b := range blocked {
            if strings.Contains(lower, b) {
                safe = false
                break
            }
        }
        if safe {
            out[k] = v
        }
    }
    return out
}
```

### Response

```
202 {"accepted": <N>}
```

Where N = number of rows successfully upserted.

---

## `GET /v1/discover/endpoints` — paginated list

- Auth: user JWT, any role (viewer minimum)
- Query params: `page` (default 1), `per_page` (default 50, max 200), `host` (filter), `source_host` (filter), `tag` (filter — match if tags array contains value)
- Returns: `{"data": [...], "total": N, "page": P, "per_page": PP}`
- org_id comes from the JWT claims

## `GET /v1/discover/endpoints/{endpointId}`

- Auth: user JWT, any role
- Returns: full `discovered_endpoints` row including `raw_headers` (already scrubbed at write time)
- 404 if not found or wrong org

---

## Route registration

In `eami-api/internal/api/router.go`:

```go
// Under /v1 group, after existing routes:
r.With(s.requireServiceKey).Post("/reports", s.IngestReports)

// Under authenticated /v1 group:
r.Get("/discover/endpoints", s.ListEndpoints)
r.Get("/discover/endpoints/{endpointId}", s.GetEndpoint)
```

---

## Acceptance criteria

- [ ] `POST /v1/reports` with valid `X-Service-Key` and a 5-endpoint payload returns `202 {"accepted": 5}`
- [ ] Duplicate `(org_id, source_host, method, host, path)` on second POST increments `hit_count` and updates `last_seen`
- [ ] `POST /v1/reports` missing or wrong `X-Service-Key` returns `401`
- [ ] `raw_headers` in stored row has no `Authorization`, `X-API-Key`, `X-Service-Key`, `Cookie` keys
- [ ] `GET /v1/discover/endpoints` with valid user JWT returns `{"data": [...], "total": N, ...}`
- [ ] `GET /v1/discover/endpoints/{id}` returns the full row; wrong org returns `404`
- [ ] `go vet ./...` exits 0

## Files to create or modify

- `eami-api/internal/api/reports.go` — new: `IngestReports`, `ListEndpoints`, `GetEndpoint` handlers
- `eami-api/internal/api/middleware.go` — add `requireServiceKey`
- `eami-api/internal/api/router.go` — register 3 new routes
- `eami-api/internal/store/query/endpoints.sql` — 3 SQL statements above
- `eami-api/internal/store/endpoints.sql.go` — hand-implement store funcs
- `eami-api/eami-api.yaml` — add `service_key: "changeme"`
- `eami-api/internal/config/config.go` — add `ServiceKey string`
