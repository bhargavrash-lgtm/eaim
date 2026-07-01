-- Migration: 004_endpoints
-- Date: 2026-06-12
-- Author: Architect-EAMI
-- Description: Add discovered_endpoints table.
--              Stores network-observed API endpoints surfaced by the
--              on-prem collector forwarder and written via POST /v1/reports.

CREATE TABLE discovered_endpoints (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source_host  TEXT        NOT NULL,             -- hostname/IP of the reporting endpoint agent
    method       TEXT        NOT NULL CHECK (method IN ('GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS')),
    path         TEXT        NOT NULL,             -- e.g. /api/v2/completions
    host         TEXT        NOT NULL,             -- destination host
    port         INT,
    tls          BOOLEAN     NOT NULL DEFAULT FALSE,
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    hit_count    INT         NOT NULL DEFAULT 1,
    tags         JSONB       NOT NULL DEFAULT '[]', -- e.g. ["llm","openai"]
    raw_headers  JSONB,                            -- optional sampled headers (no auth values)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Unique: one row per (org, source_host, method, host, path) tuple
CREATE UNIQUE INDEX uq_discovered_endpoints
    ON discovered_endpoints(org_id, source_host, method, host, path);

CREATE INDEX idx_discovered_endpoints_org       ON discovered_endpoints(org_id, last_seen DESC);
CREATE INDEX idx_discovered_endpoints_host      ON discovered_endpoints(org_id, host);
CREATE INDEX idx_discovered_endpoints_source    ON discovered_endpoints(org_id, source_host);
