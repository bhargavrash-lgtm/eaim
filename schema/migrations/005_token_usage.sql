-- Migration: 005_token_usage.sql
-- Adds token_usage (TimescaleDB hypertable) and model_pricing tables.
-- For fresh installs these already exist via schema.sql; this file covers
-- incremental upgrades on existing databases.
--
-- Apply: psql -f schema/migrations/005_token_usage.sql

-- ── Model pricing ─────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS model_pricing (
    model           TEXT PRIMARY KEY,
    cost_per_1k_in  NUMERIC(10,6) NOT NULL,
    cost_per_1k_out NUMERIC(10,6) NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out) VALUES
    ('claude-opus-4-6',            0.015000, 0.075000),
    ('claude-sonnet-4-6',          0.003000, 0.015000),
    ('claude-haiku-4-5-20251001',  0.000800, 0.004000),
    ('claude-3-5-sonnet-20241022', 0.003000, 0.015000),
    ('claude-3-opus-20240229',     0.015000, 0.075000),
    ('claude-3-haiku-20240307',    0.000250, 0.001250),
    ('gpt-4o',                     0.005000, 0.015000),
    ('gpt-4o-mini',                0.000150, 0.000600),
    ('gpt-4-turbo',                0.010000, 0.030000)
ON CONFLICT (model) DO NOTHING;

-- ── Token usage ───────────────────────────────────────────────────────────────
-- Matches schema.sql exactly: UUID id, soft FKs (required for TimescaleDB
-- hypertables), extra columns used by FinOps queries.

CREATE TABLE IF NOT EXISTS token_usage (
    id           UUID NOT NULL DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL,
    agent_id     UUID,
    agent_name   TEXT NOT NULL,
    team         TEXT,
    model        TEXT NOT NULL,
    tool_name    TEXT,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    cost_usd     NUMERIC(12,6),
    outcome      TEXT CHECK (outcome IN ('success','blocked','failed','partial')),
    audit_log_id UUID,
    recorded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Convert to TimescaleDB hypertable (no-op if already a hypertable or if
-- TimescaleDB is not installed, so local dev without TimescaleDB still works).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')
       AND NOT EXISTS (
           SELECT 1 FROM timescaledb_information.hypertables
           WHERE hypertable_name = 'token_usage'
       )
    THEN
        PERFORM create_hypertable('token_usage', 'recorded_at', if_not_exists => TRUE);
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS idx_token_usage_org     ON token_usage(org_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_usage_agent   ON token_usage(agent_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_usage_model   ON token_usage(model, recorded_at DESC);
