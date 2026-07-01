# Task: Add pprof listener to eami-gateway for load test goroutine sampling
**From:** PM-EAMI  
**To:** BE-Gateway  
**Priority:** normal  
**Blocked by:** none

## What I need

The k6 load test (`tests/load/gateway.js`) samples goroutine counts via
`GET /debug/pprof/goroutine` at setup and teardown to detect goroutine leaks.
The gateway currently has no pprof listener.

Add a **separate** pprof HTTP listener on a configurable port (default 6060),
enabled only when `GATEWAY_PPROF_ADDR` env var is set.

```go
// cmd/gateway/main.go

import _ "net/http/pprof"

pprofAddr := os.Getenv("GATEWAY_PPROF_ADDR")
if pprofAddr != "" {
    go func() {
        log.Info("pprof listening", "addr", pprofAddr)
        if err := http.ListenAndServe(pprofAddr, nil); err != nil {
            log.Error("pprof server failed", "err", err)
        }
    }()
}
```

Add to `docker-compose.yml` under `eami-gateway`:
```yaml
environment:
  GATEWAY_PPROF_ADDR: ${GATEWAY_PPROF_ADDR:-}
ports:
  - "6060:6060"  # only exposed when GATEWAY_PPROF_ADDR=:6060
```

Add to `.env.example`:
```bash
# Optional: enable pprof on gateway for profiling/load-test goroutine checks.
# Set to ":6060" to enable. Leave empty (default) in production.
GATEWAY_PPROF_ADDR=
```

## Acceptance criteria

- [ ] `GET http://localhost:6060/debug/pprof/goroutine?debug=1` returns 200 when
  `GATEWAY_PPROF_ADDR=:6060` is set
- [ ] With `GATEWAY_PPROF_ADDR` unset (default), no port 6060 is opened
- [ ] `go vet ./...` exits 0
- [ ] `docker-compose.yml` and `.env.example` updated

## Files to modify

- `eami-gateway/cmd/gateway/main.go` — add pprof listener
- `docker-compose.yml` — add env var + port mapping
- `.env.example` — document GATEWAY_PPROF_ADDR
