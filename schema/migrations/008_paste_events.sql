-- Migration: 008_paste_events
-- Date: 2026-07-25
-- Author: Code-EAMI
-- Description: Add paste_events table -- append-only event log for
--              browser-extension paste-detection (known-destination
--              detection only; no content-classification/DLP).
--
--              Deliberately NOT a reuse of discovered_endpoints: that table
--              is dedup-by-(org,source_host,method,host,path) with a single
--              overwritten last_seen column, designed for "have we ever
--              seen this endpoint" discovery -- it cannot answer "how many
--              paste events happened, and when" (see investigation prior
--              to this brief). paste_events keeps one row per event.
--
--              TimescaleDB hypertable, not audit_log's pg_cron monthly-
--              partition pattern: audit_log's manual partitioning exists
--              to support its RLS insert-only policy and hash-chain
--              tamper-evidence requirements (compliance-driven -- see
--              schema.sql's audit_log block and 007_audit_partitions.sql).
--              paste_events has neither requirement; it is pure
--              time-series analytics data shaped exactly like token_usage
--              (005_token_usage.sql / schema.sql), so it follows that
--              table's create_hypertable() pattern instead: automatic
--              chunk management, no PK on id (the partition column must
--              be part of any unique constraint/PK, so -- like token_usage
--              and audit_log -- id is a plain column, not a primary key).
--
--              For fresh installs this already exists via schema.sql; this
--              file covers incremental upgrades on existing databases, and
--              -- matching 005_token_usage.sql's convention -- is safe to
--              re-run and degrades gracefully if TimescaleDB isn't
--              installed (local dev without TimescaleDB still works).
--
-- Apply: psql -f schema/migrations/008_paste_events.sql

CREATE TABLE IF NOT EXISTS paste_events (
    id                  UUID        NOT NULL DEFAULT uuid_generate_v4(),
    org_id              UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- Reporting endpoint machine. Resolved the same way ingest.go resolves
    -- it for the agent-report pipeline: UpsertAgentEndpoint's
    -- find-or-create-by-(org_id, agent_id) against the endpoints table.
    source_endpoint_id  UUID        NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    -- Destination domain the paste was sent to (e.g. "chat.openai.com").
    -- Validated server-side against a known-destination allowlist at
    -- ingest time (see eami-api/internal/api/paste_domains.go) -- not a
    -- FK, since no domains dimension table exists anywhere in this schema;
    -- the allowlist is code, matching the network_activity.KnownAIHosts /
    -- browser.knownExtensions convention.
    destination_domain  TEXT        NOT NULL,
    -- Coarse content indicators only -- NEVER the pasted text itself.
    -- Mirrors the existing house scrub pattern (cloud_clients' key-prefix
    -- truncation, reports.go's sensitiveHeaders scrub, B-022's toolcreds
    -- "no field to carry it" guarantee).
    content_length      INTEGER,
    content_hash        TEXT,
    -- Optional, anonymised local OS username -- same convention as
    -- eami-agent's browser scanner (BrowserExtension.UserPath, "anonymised:
    -- username only"). Not a FK: no employee/user-directory table exists
    -- in this schema to reference (the `users` table is dashboard login
    -- accounts, an unrelated identity space).
    os_username         TEXT,
    -- Event time, as reported by the client. Hypertable partition column.
    occurred_at         TIMESTAMPTZ NOT NULL,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Convert to TimescaleDB hypertable (no-op if already a hypertable or if
-- TimescaleDB is not installed, so local dev without TimescaleDB still
-- works) -- same guarded pattern as 005_token_usage.sql.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')
       AND NOT EXISTS (
           SELECT 1 FROM timescaledb_information.hypertables
           WHERE hypertable_name = 'paste_events'
       )
    THEN
        PERFORM create_hypertable('paste_events', 'occurred_at', if_not_exists => TRUE);
    END IF;
END;
$$;

-- Reporting query shapes this indexes for (CIO dashboard):
--   * counts/trend by domain over a time range, for one org
--     -> idx_paste_events_org_domain (org_id, destination_domain, occurred_at DESC)
--   * counts/trend by org over a time range (no domain filter)
--     -> idx_paste_events_org (org_id, occurred_at DESC); also usable as a
--        leading-columns prefix of idx_paste_events_org_domain, but kept
--        separate (mirrors token_usage's idx_token_usage_org) since the
--        org-only query is common enough to deserve its own smaller index.
--   * per-machine drill-down
--     -> idx_paste_events_endpoint (source_endpoint_id, occurred_at DESC)
CREATE INDEX IF NOT EXISTS idx_paste_events_org         ON paste_events(org_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_paste_events_org_domain  ON paste_events(org_id, destination_domain, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_paste_events_endpoint    ON paste_events(source_endpoint_id, occurred_at DESC);

-- Retention: 90 days, matching the only concrete retention number this
-- codebase already commits to (endpoint_reports' "kept for 90 days"
-- comment in schema.sql). Unlike that comment-only convention, Timescale's
-- add_retention_policy is actually enforced by a background job -- no cron
-- script or manual DELETE needed. Guarded the same way create_hypertable
-- is above: only runs if TimescaleDB is installed, and if_not_exists makes
-- re-running this migration safe.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        PERFORM add_retention_policy('paste_events', INTERVAL '90 days', if_not_exists => TRUE);
    END IF;
END;
$$;
