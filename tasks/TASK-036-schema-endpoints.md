# Task: Add discovered_endpoints table + migration 004
**From:** PM-EAMI  
**To:** Architect-EAMI  
**Priority:** high  
**Blocked by:** none

## What I need

The Discover page in the UI is backed by `GET /v1/discover/endpoints` (see `openapi.yaml`). Right now there is no `discovered_endpoints` table in the schema. When the eami-api receives forwarded agent reports from the collector, it needs somewhere to store them.

### 1. `schema/migrations/004_endpoints.sql`

Add a migration file that creates the `discovered_endpoints` table:

```sql
CREATE TABLE discovered_endpoints (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        UUID,                          -- links to gateway_agents if registered
    hostname        TEXT NOT NULL,
    os_platform     TEXT NOT NULL,                 -- "windows" | "darwin" | "linux"
    os_version      TEXT,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    report          JSONB NOT NULL,                -- full agent report payload
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, hostname)
);

CREATE INDEX idx_endpoints_org ON discovered_endpoints(org_id, last_seen_at DESC);
CREATE INDEX idx_endpoints_hostname ON discovered_endpoints(hostname);
```

Use `ON CONFLICT (org_id, hostname) DO UPDATE SET report = EXCLUDED.report, last_seen_at = NOW(), updated_at = NOW()` in the upsert — the API will use this pattern when writing.

### 2. `schema/schema.sql`

Merge the above table definition into the canonical schema file (in the correct section, after `gateway_agents`).

### 3. `api/openapi.yaml`

Verify that `GET /v1/discover/endpoints` and `GET /v1/discover/endpoints/{id}` exist in the spec. If they are missing or stubs, add them with a `DiscoveredEndpoint` schema that mirrors the table above. The response shape should include the `report` field as an inline object (not a raw JSONB string).

Also add `POST /v1/reports` to the spec — this is the internal endpoint that eami-api exposes for the collector forwarder to post agent reports to. It does not require a user JWT; it requires a service API key header (`X-Service-Key`).

## Context

This is a new table, not a change to an existing one. The agent reports contain rich JSON (AI apps, models, MCP servers, GPU, etc.) — storing the full report as JSONB lets the UI query for specific fields without requiring a fixed schema for every possible detection type.

## Acceptance criteria

- [ ] `schema/migrations/004_endpoints.sql` exists and applies cleanly on top of migration 003
- [ ] `schema/schema.sql` contains the `discovered_endpoints` table definition
- [ ] `api/openapi.yaml` contains `GET /v1/discover/endpoints` with response schema
- [ ] `api/openapi.yaml` contains `POST /v1/reports` with request schema
- [ ] Running `psql -f schema/schema.sql` on a fresh DB creates all tables without error
- [ ] No existing tables are dropped or altered

## Files to create or modify

- `schema/migrations/004_endpoints.sql` — new migration
- `schema/schema.sql` — add `discovered_endpoints` table
- `api/openapi.yaml` — add or complete discover endpoints + POST /v1/reports

## Files to read first

- `schema/schema.sql` — understand existing table structure and conventions
- `api/openapi.yaml` — find the existing discover section
- `Instructions/openapi.yaml` — baseline spec to start from (always read this first)
