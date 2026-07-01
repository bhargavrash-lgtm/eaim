# Task: Fix Docker build isolation for replace directive
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** high  
**Blocked by:** none

## What I need

`eami-gateway/go.mod` contains a `replace` directive pointing to `../eami-policy`.
This works for local development but breaks in Docker because the build context
does not include the sibling directory. CI and `docker compose build` fail with
"local replacement not found".

## Fix options (pick one and implement it)

### Option A — Go workspace (recommended)
Create a `go.work` file at the repo root that includes all modules:

```
go 1.24

use (
    ./eami-api
    ./eami-gateway
    ./eami-policy
    ./eami-collector
    ./eami-agent
)
```

Update each `Dockerfile` to:
1. `COPY go.work go.work.sum ./` before any `go mod download`
2. `COPY eami-policy/ ./eami-policy/` before building `eami-gateway`

### Option B — Publish eami-policy as a real module
Remove the `replace` directive; publish `eami-policy` to the internal module proxy or
GitHub so it can be fetched normally. More work, not recommended for this sprint.

## Docker build change (for Option A)

In `eami-gateway/Dockerfile`:
```dockerfile
WORKDIR /build

# Copy workspace files first
COPY go.work go.work.sum ./

# Copy all modules that gateway depends on
COPY eami-policy/ ./eami-policy/
COPY eami-gateway/ ./eami-gateway/

WORKDIR /build/eami-gateway
RUN go build -o /gateway ./cmd/gateway
```

Verify the same fix in `eami-api/Dockerfile` if it has any replace directives.

## Acceptance criteria

- [ ] `docker compose build eami-gateway` exits 0 on a clean checkout (no local caches)
- [ ] `docker compose build` (all services) exits 0
- [ ] `go build ./...` inside `eami-gateway/` still works locally without `go.work` (developer experience preserved)
- [ ] CI workflow (if present) passes

## Files to create or modify

- `go.work` — new file at repo root
- `eami-gateway/Dockerfile` — update COPY + WORKDIR sequence
- `eami-api/Dockerfile` — check and fix if needed
