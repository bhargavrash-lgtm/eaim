-- Migration: 005_token_usage.sql
-- Adds token_usage (TimescaleDB hypertable) and model_pricing tables.
-- Apply via: psql -f schema/migrations/005_token_usage.sql

-- Model pricing reference table.
CREATE TABLE IF NOT EXISTS model_pricing (
    model               TEXT        PRIMARY KEY,
    cost_per_1k_in      NUMERIC(12, 6) NOT NULL DEFAULT 0,
    cost_per_1k_out     NUMERIC(12, 6) NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed well-known models so FinOps charts are populated from day one.
INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out) VALUES
    ('claude-opus-4-6',            0.015000, 0.075000),
    ('claude-sonnet-4-6',          0.003000, 0.015000),
    ('claude-haiku-4-5-20251001',  0.000800, 0.004000),
    ('claude-3-5-sonnet-20241022', 0.003000, 0.015000),
    ('claude-3-opus-20240229',     0.015000, 0.075000),
    ('claude-3-haiku-20240307',    0.000250, 0.001250)
ON CONFLICT (model) DO NOTHING;

-- Token usage fact table (TimescaleDB hypertable partitioned by recorded_at).
CREATE TABLE IF NOT EXISTS token_usage (
    id              BIGSERIAL,
    org_id          UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        UUID        REFERENCES gateway_agents(id) ON DELETE SET NULL,
    agent_name      TEXT        NOT NULL DEFAULT '',
    model           TEXT        NOT NULL DEFAULT '',
    tokens_in       INTEGER     NOT NULL DEFAULT 0,
    tokens_out      INTEGER     NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(14, 6),
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Convert to a TimescaleDB hypertable (no-op if TimescaleDB is not installed,
-- so the table still works as a plain Postgres table in local dev).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_extension WHERE extname = 'timescaledb'
    ) THEN
        PERFORM create_hypertable(
            'token_usage', 'recorded_at',
            if_not_exists => TRUE,
            chunk_time_interval => INTERVAL '1 day'
        );
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS idx_token_usage_org_recorded
    ON token_usage (org_id, recorded_at DESC);

CREATE INDEX IF NOT EXISTS idx_token_usage_agent
    ON token_usage (agent_id, recorded_at DESC);

CREATE INDEX IF NOT EXISTS idx_token_usage_model
    ON token_usage (model, recorded_at DESC);
