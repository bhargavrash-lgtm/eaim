# Task: Agent remote config management — pull-based update
**From:** PM-EAMI  
**To:** BE-Collector  
**Priority:** normal  
**Blocked by:** TASK-037 (eami-api reports endpoint must exist)

## What I need

Allow an admin to change agent scan settings from the UI, and have the agent pick up the new config on its next check-in. Use a pull model — the agent polls for its config on every send cycle.

### Backend (eami-api) — BE-Policy will handle this, you handle the agent side

The API endpoint `GET /v1/agents/{agent_id}/config` will be delivered by BE-Policy (see TASK-048's backend component). It returns:

```json
{
  "scan_interval_seconds": 300,
  "model_scan_paths": ["/home", "/Users"],
  "max_report_size_bytes": 5242880,
  "enabled_scanners": ["ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser"]
}
```

### Agent side — your task

In `eami-agent/internal/collector/sender.go`, after successfully sending a report:

1. Make a `GET` request to `{collector_url}/v1/agent-config/{agent_id}` (the collector proxies this to the API).
2. The collector exposes `GET /v1/agent-config/{agent_id}` which calls the eami-api and returns the config JSON.
3. If the response is 200, unmarshal into an `AgentConfigUpdate` struct and apply the changes:
   - Update `cfg.Detection.ScanIntervalSeconds` in memory.
   - Update `cfg.Detection.ModelScanPaths` in memory.
   - Update `cfg.Detection.EnabledScanners` in memory.
4. Changes take effect on the next scan tick — no restart required.
5. If the config endpoint returns 404 or errors, log and continue with existing config. Never crash on config fetch failure.

### Collector proxy endpoint

Add to `eami-collector/internal/api/`:

```
GET /v1/agent-config/{agent_id}
Authorization: X-Api-Key: <collector_api_key>  (same as the ingest key)
```

The collector calls eami-api:
```
GET {saas_url}/v1/agents/{agent_id}/config
X-Service-Key: {service_key}
```

And proxies the response JSON directly to the agent.

### AgentConfigUpdate struct

```go
type AgentConfigUpdate struct {
    ScanIntervalSeconds int      `json:"scan_interval_seconds"`
    ModelScanPaths      []string `json:"model_scan_paths"`
    MaxReportSizeBytes  int      `json:"max_report_size_bytes"`
    EnabledScanners     []string `json:"enabled_scanners"`
}
```

## Acceptance criteria

- [ ] After an admin changes scan interval in the UI (TASK-048), the agent picks up the new interval within 2 scan cycles (no restart)
- [ ] Agent config fetch failure (network error, 404) does not crash the agent
- [ ] Collector proxy endpoint returns the config from eami-api
- [ ] Agent correctly skips disabled scanners
- [ ] `go vet ./...` exits 0 in eami-agent and eami-collector

## Files to create or modify

- `eami-agent/internal/collector/sender.go` — add config poll after send
- `eami-agent/internal/config/config.go` — add `EnabledScanners []string` field
- `eami-collector/internal/api/config_proxy.go` — new file: proxy GET /v1/agent-config/{id}
- `eami-collector/cmd/collector/main.go` — register the config proxy route

## Files to read first

- `eami-agent/internal/collector/sender.go` — existing send logic
- `eami-agent/internal/config/config.go` — AgentConfig struct
- `eami-collector/internal/api/ingest.go` — pattern for collector API handlers
