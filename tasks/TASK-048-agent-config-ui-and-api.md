# Task: Agent config management — API endpoint + UI tab
**From:** PM-EAMI  
**To:** BE-Policy (API) + FE-Gateway (UI)  
**Priority:** normal  
**Blocked by:** TASK-037 (reports endpoint), TASK-047 (agent poll logic)

## Part A — BE-Policy: GET /v1/agents/{agentId}/config

Add a new endpoint to eami-api that returns the current config for a registered agent.

### Data model

Add a `agent_configs` table via migration 005:

```sql
CREATE TABLE agent_configs (
    agent_id             UUID PRIMARY KEY REFERENCES gateway_agents(id) ON DELETE CASCADE,
    scan_interval_seconds INT NOT NULL DEFAULT 300,
    model_scan_paths     TEXT[] NOT NULL DEFAULT ARRAY['/home', '/Users', 'C:\\Users'],
    max_report_size_bytes INT NOT NULL DEFAULT 5242880,
    enabled_scanners     TEXT[] NOT NULL DEFAULT ARRAY['ai_apps','models','mcp_servers','cloud_clients','network_activity','browser'],
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Create a row in `agent_configs` with defaults whenever a new `gateway_agents` row is created (use a trigger or handle in the `CreateAgent` handler).

### Endpoints

```
GET  /v1/agents/{agentId}/config
     Auth: user JWT (viewer+) OR X-Service-Key (for collector proxy)
     Response 200: AgentConfig object

PUT  /v1/agents/{agentId}/config
     Auth: user JWT (operator+)
     Body: { scan_interval_seconds, model_scan_paths, max_report_size_bytes, enabled_scanners }
     Response 200: updated AgentConfig
```

Register both routes on the chi router.

### Openapi

Add `AgentConfig` schema and both endpoints to `api/openapi.yaml` (Architect-EAMI reviews).

---

## Part B — FE-Gateway: Config tab on Agents page

Add a "Config" slide-out panel on `AgentsPage.tsx` that opens when you click "Configure" on an agent row.

### Config panel fields

| Field | Input | Validation |
|---|---|---|
| Scan interval | Number input (seconds) | min 60, max 86400 |
| Model scan paths | Tag input (comma-separated paths) | at least 1 |
| Max report size | Number input (MB, convert to bytes) | min 1MB, max 50MB |
| Enabled scanners | Multi-checkbox: ai_apps, models, mcp_servers, cloud_clients, network_activity, browser | at least 1 |

Use RHF + Zod. On submit, call `PUT /v1/agents/{agentId}/config`.

### Hook

Add `useAgentConfig(agentId)` and `useUpdateAgentConfig()` to `eami-ui/src/hooks/useAgents.ts`.

## Acceptance criteria

**API:**
- [ ] `GET /v1/agents/{agentId}/config` returns defaults for a newly created agent
- [ ] `PUT /v1/agents/{agentId}/config` updates the config and returns the new values
- [ ] New agent creation automatically creates a default `agent_configs` row
- [ ] `go vet ./...` exits 0

**UI:**
- [ ] "Configure" button on agent row opens the config panel
- [ ] All fields pre-populated with current config
- [ ] Saving calls `PUT` and shows success toast
- [ ] Validation prevents saving with invalid values
- [ ] `tsc --noEmit` exits 0

## Files to create or modify

**BE-Policy:**
- `schema/migrations/005_agent_configs.sql` — new migration (coordinate with Architect-EAMI)
- `eami-api/internal/api/agents.go` — add GetAgentConfig, UpdateAgentConfig handlers; ensure CreateAgent seeds defaults
- `eami-api/internal/api/router.go` — register 2 new routes
- `eami-api/internal/store/query/agent_configs.sql` — SQL
- `eami-api/internal/store/agent_configs.sql.go` — store methods

**FE-Gateway:**
- `eami-ui/src/pages/gateway/AgentsPage.tsx` — add Configure button + config panel
- `eami-ui/src/hooks/useAgents.ts` — add useAgentConfig, useUpdateAgentConfig

## Files to read first

- `eami-api/internal/api/agents.go` — existing agent CRUD
- `eami-ui/src/pages/gateway/AgentsPage.tsx` — existing page
- `api/openapi.yaml` — agents section
