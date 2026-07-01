-- Migration: 004_endpoints.sql
-- Adds the discovered_endpoints table for the Discover page.
-- Apply via: psql -f schema/migrations/004_endpoints.sql

CREATE TABLE IF NOT EXISTS discovered_endpoints (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source_host TEXT        NOT NULL,
    method      TEXT        NOT NULL,
    host        TEXT        NOT NULL,
    path        TEXT        NOT NULL,
    port        INTEGER,
    tls         BOOLEAN     NOT NULL DEFAULT FALSE,
    tags        TEXT[]      NOT NULL DEFAULT '{}'::TEXT[],
    raw_headers JSONB       NOT NULL DEFAULT '{}'::JSONB,
    hit_count   INTEGER     NOT NULL DEFAULT 1,
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_endpoint UNIQUE (org_id, source_host, method, host, path)
);

CREATE INDEX IF NOT EXISTS idx_discovered_endpoints_org_last_seen
    ON discovered_endpoints (org_id, last_seen DESC);

CREATE INDEX IF NOT EXISTS idx_discovered_endpoints_host
    ON discovered_endpoints (org_id, host);
