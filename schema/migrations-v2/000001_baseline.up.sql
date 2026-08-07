-- EAMI — Baseline migration for the real migration runner (B-051-adjacent).
-- Source of truth: schema/schema.sql as of 2026-08-07, the confirmed-accurate
-- cumulative result of the legacy schema/migrations/001-010 files (verified
-- table-by-table before writing this file — see schema/migrations/README.md).
--
-- Every statement below is wrapped to be safe to re-run (CREATE ... IF NOT
-- EXISTS, DROP ... IF EXISTS before CREATE where Postgres has no IF NOT
-- EXISTS form, ON CONFLICT DO NOTHING for seed data). This is deliberate
-- defense-in-depth for a partial-failure-then-retry scenario, matching the
-- convention schema/migrations/005, 007, and 008 already individually
-- followed — golang-migrate's own schema_migrations bookkeeping is the
-- primary guard against a normal double-apply, this is the second layer.
--
-- This migration is expected to run exactly once, against a genuinely
-- empty database. Existing databases (this dev stack, or any install that
-- predates this migration runner) are stamped at this version instead of
-- replaying it — see scripts/migrate.sh and BUILT.md's B-051 entry for the
-- stamping procedure actually used.

-- ─────────────────────────────────────────────────────────
-- EXTENSIONS
-- ─────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "vector";        -- pgvector for episode embeddings
CREATE EXTENSION IF NOT EXISTS "timescaledb";   -- TimescaleDB for token spend time-series

-- ─────────────────────────────────────────────────────────
-- ORGANISATIONS & USERS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orgs (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name              TEXT NOT NULL,
    slug              TEXT NOT NULL UNIQUE,
    plan              TEXT NOT NULL DEFAULT 'trial' CHECK (plan IN ('trial','starter','business','enterprise')),
    timezone          TEXT NOT NULL DEFAULT 'UTC',
    default_risk_tier TEXT NOT NULL DEFAULT 'medium'
                          CHECK (default_risk_tier IN ('low','medium','high')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT,
    password_hash TEXT,
    role          TEXT NOT NULL DEFAULT 'operator' CHECK (role IN ('admin','operator','approver','viewer')),
    sso_provider  TEXT,
    sso_subject   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login    TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,
    invited_at    TIMESTAMPTZ,
    invited_by    UUID REFERENCES users(id),
    UNIQUE (org_id, email)
);
CREATE INDEX IF NOT EXISTS idx_users_org ON users(org_id);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked     BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON refresh_tokens(token_hash);

CREATE TABLE IF NOT EXISTS api_keys (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    key_hash    TEXT NOT NULL UNIQUE,
    prefix      TEXT NOT NULL,
    scopes      TEXT[] NOT NULL DEFAULT '{}',
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used   TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,
    revoked     BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_api_keys_org ON api_keys(org_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

-- ─────────────────────────────────────────────────────────
-- ENDPOINT DISCOVERY
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS endpoints (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL,
    hostname        TEXT NOT NULL,
    agent_version   TEXT,
    os_info         JSONB,
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    risk_score      NUMERIC(5,2) DEFAULT 0,
    UNIQUE (org_id, agent_id)
);
CREATE INDEX IF NOT EXISTS idx_endpoints_org ON endpoints(org_id);
CREATE INDEX IF NOT EXISTS idx_endpoints_last_seen ON endpoints(last_seen DESC);

CREATE TABLE IF NOT EXISTS endpoint_reports (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id  UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    org_id       UUID NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    report       JSONB NOT NULL,
    schema_version TEXT NOT NULL DEFAULT '1.0'
);
CREATE INDEX IF NOT EXISTS idx_reports_endpoint ON endpoint_reports(endpoint_id, collected_at DESC);
CREATE INDEX IF NOT EXISTS idx_reports_collected ON endpoint_reports(collected_at DESC);

CREATE TABLE IF NOT EXISTS endpoint_ai_apps (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    version     TEXT,
    source      TEXT,
    detected_at TIMESTAMPTZ NOT NULL,
    report_id   UUID REFERENCES endpoint_reports(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ai_apps_endpoint ON endpoint_ai_apps(endpoint_id);

CREATE TABLE IF NOT EXISTS endpoint_model_files (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    path        TEXT,
    size_mb     NUMERIC(10,2),
    format      TEXT,
    source      TEXT CHECK (source IN ('ollama','lmstudio','huggingface','unknown')),
    detected_at TIMESTAMPTZ NOT NULL,
    report_id   UUID REFERENCES endpoint_reports(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_model_files_endpoint ON endpoint_model_files(endpoint_id);

CREATE TABLE IF NOT EXISTS endpoint_mcp_servers (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    transport   TEXT CHECK (transport IN ('stdio','sse','socket')),
    port        INTEGER,
    source      TEXT CHECK (source IN ('claude_desktop','vscode','cursor','live_port')),
    detected_at TIMESTAMPTZ NOT NULL,
    report_id   UUID REFERENCES endpoint_reports(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_endpoint ON endpoint_mcp_servers(endpoint_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: AGENT REGISTRY
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS gateway_agents (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    model            TEXT NOT NULL,
    owner            TEXT NOT NULL,
    scope            TEXT NOT NULL,
    risk_tier        TEXT NOT NULL DEFAULT 'low' CHECK (risk_tier IN ('low','medium','high')),
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','revoked')),
    token_ttl_seconds INTEGER NOT NULL DEFAULT 900,
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen        TIMESTAMPTZ,
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_gateway_agents_org ON gateway_agents(org_id);
CREATE INDEX IF NOT EXISTS idx_gateway_agents_status ON gateway_agents(status);

CREATE TABLE IF NOT EXISTS revoked_ai_tokens (
    jti         TEXT PRIMARY KEY,
    agent_id    UUID NOT NULL REFERENCES gateway_agents(id) ON DELETE CASCADE,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reason      TEXT
);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: POLICIES
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS policies (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    priority     INTEGER NOT NULL,
    action       TEXT NOT NULL CHECK (action IN ('allow','deny','escalate')),
    alert        BOOLEAN NOT NULL DEFAULT FALSE,
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('active','draft','disabled')),
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, priority) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX IF NOT EXISTS idx_policies_org_priority ON policies(org_id, priority ASC);
CREATE INDEX IF NOT EXISTS idx_policies_status ON policies(status);

CREATE TABLE IF NOT EXISTS policy_conditions (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    policy_id            UUID NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    agent_name_pattern   TEXT,
    tool_names           TEXT[],
    action_types         TEXT[],
    environments         TEXT[],
    record_count_gt      INTEGER,
    semantic_rule        TEXT,
    scope_drift          BOOLEAN DEFAULT FALSE,
    tool_server_ids      TEXT[]
);
CREATE INDEX IF NOT EXISTS idx_policy_conditions_policy ON policy_conditions(policy_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: TOOL CONNECTIONS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS gateway_tools (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    type         TEXT NOT NULL CHECK (type IN ('mcp','rest_api','database')),
    auth_type    TEXT NOT NULL CHECK (auth_type IN ('oauth2','api_key','basic','db_connection_string')),
    mcp_command  TEXT,
    mcp_args     TEXT[],
    base_url     TEXT,
    credentials_encrypted BYTEA,
    status       TEXT NOT NULL DEFAULT 'connected' CHECK (status IN ('connected','degraded','disconnected')),
    last_used    TIMESTAMPTZ,
    last_tested  TIMESTAMPTZ,
    test_latency_ms INTEGER,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    action_paths JSONB,
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_gateway_tools_org ON gateway_tools(org_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: NODES
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS gateway_nodes (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('primary','edge','dr_standby')),
    status              TEXT NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy','degraded','standby','offline')),
    address             TEXT NOT NULL,
    hostname            TEXT,
    version             TEXT,
    last_heartbeat      TIMESTAMPTZ,
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_gateway_nodes_org ON gateway_nodes(org_id);

CREATE TABLE IF NOT EXISTS gateway_node_metrics (
    node_id         UUID NOT NULL REFERENCES gateway_nodes(id) ON DELETE CASCADE,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cpu_pct         NUMERIC(5,2),
    memory_mb       INTEGER,
    requests_per_min INTEGER,
    active_sessions INTEGER
);
SELECT create_hypertable('gateway_node_metrics', 'recorded_at', if_not_exists => TRUE);
CREATE INDEX IF NOT EXISTS idx_node_metrics ON gateway_node_metrics(node_id, recorded_at DESC);

-- ─────────────────────────────────────────────────────────
-- APPROVALS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS approval_requests (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id       UUID NOT NULL REFERENCES gateway_agents(id),
    agent_name     TEXT NOT NULL,
    tool_name      TEXT NOT NULL,
    action         TEXT NOT NULL,
    parameters     JSONB,
    justification  TEXT NOT NULL,
    risk_level     TEXT NOT NULL CHECK (risk_level IN ('low','medium','high','critical')),
    estimated_records INTEGER,
    reversible     BOOLEAN,
    environment    TEXT CHECK (environment IN ('production','staging','development','unknown')),
    data_types     TEXT[],
    policy_id      UUID REFERENCES policies(id),
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','denied','expired','cancelled')),
    approved_by    UUID REFERENCES users(id),
    decision_reason TEXT,
    decided_at     TIMESTAMPTZ,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    gateway_session_id TEXT NOT NULL,
    gateway_node_address TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_approvals_org_status ON approval_requests(org_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_approvals_pending ON approval_requests(status, expires_at) WHERE status = 'pending';

-- ─────────────────────────────────────────────────────────
-- AUDIT LOG
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_log (
    id             UUID NOT NULL DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL,
    agent_id       UUID,
    agent_name     TEXT NOT NULL,
    tool_name      TEXT NOT NULL,
    action         TEXT NOT NULL,
    parameters     JSONB,
    decision       TEXT NOT NULL CHECK (decision IN ('allowed','denied','escalated')),
    policy_id      UUID,
    approval_id    UUID,
    approved_by    TEXT,
    latency_ms     INTEGER,
    token_in       INTEGER,
    token_out      INTEGER,
    timestamp      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    prev_hash      TEXT NOT NULL,
    hash           TEXT NOT NULL
) PARTITION BY RANGE (timestamp);

-- Monthly partitions — pre-created through 2027; auto-extended by pg_cron.
-- Each guarded individually (matches schema/migrations/007's own "safe to
-- re-run" convention) since PARTITION OF has no IF NOT EXISTS form.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_05') THEN
        CREATE TABLE audit_log_2026_05 PARTITION OF audit_log FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_06') THEN
        CREATE TABLE audit_log_2026_06 PARTITION OF audit_log FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_07') THEN
        CREATE TABLE audit_log_2026_07 PARTITION OF audit_log FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_08') THEN
        CREATE TABLE audit_log_2026_08 PARTITION OF audit_log FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_09') THEN
        CREATE TABLE audit_log_2026_09 PARTITION OF audit_log FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_10') THEN
        CREATE TABLE audit_log_2026_10 PARTITION OF audit_log FOR VALUES FROM ('2026-10-01') TO ('2026-11-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_11') THEN
        CREATE TABLE audit_log_2026_11 PARTITION OF audit_log FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2026_12') THEN
        CREATE TABLE audit_log_2026_12 PARTITION OF audit_log FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_01') THEN
        CREATE TABLE audit_log_2027_01 PARTITION OF audit_log FOR VALUES FROM ('2027-01-01') TO ('2027-02-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_02') THEN
        CREATE TABLE audit_log_2027_02 PARTITION OF audit_log FOR VALUES FROM ('2027-02-01') TO ('2027-03-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_03') THEN
        CREATE TABLE audit_log_2027_03 PARTITION OF audit_log FOR VALUES FROM ('2027-03-01') TO ('2027-04-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_04') THEN
        CREATE TABLE audit_log_2027_04 PARTITION OF audit_log FOR VALUES FROM ('2027-04-01') TO ('2027-05-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_05') THEN
        CREATE TABLE audit_log_2027_05 PARTITION OF audit_log FOR VALUES FROM ('2027-05-01') TO ('2027-06-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_06') THEN
        CREATE TABLE audit_log_2027_06 PARTITION OF audit_log FOR VALUES FROM ('2027-06-01') TO ('2027-07-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_07') THEN
        CREATE TABLE audit_log_2027_07 PARTITION OF audit_log FOR VALUES FROM ('2027-07-01') TO ('2027-08-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_08') THEN
        CREATE TABLE audit_log_2027_08 PARTITION OF audit_log FOR VALUES FROM ('2027-08-01') TO ('2027-09-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_09') THEN
        CREATE TABLE audit_log_2027_09 PARTITION OF audit_log FOR VALUES FROM ('2027-09-01') TO ('2027-10-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_10') THEN
        CREATE TABLE audit_log_2027_10 PARTITION OF audit_log FOR VALUES FROM ('2027-10-01') TO ('2027-11-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_11') THEN
        CREATE TABLE audit_log_2027_11 PARTITION OF audit_log FOR VALUES FROM ('2027-11-01') TO ('2027-12-01');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'audit_log_2027_12') THEN
        CREATE TABLE audit_log_2027_12 PARTITION OF audit_log FOR VALUES FROM ('2027-12-01') TO ('2028-01-01');
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS idx_audit_log_org_ts ON audit_log(org_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_agent ON audit_log(agent_name, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_decision ON audit_log(decision, timestamp DESC);

-- Row-level security: app user may INSERT only (no UPDATE, no DELETE).
-- ENABLE ROW LEVEL SECURITY is idempotent (no error re-running). CREATE
-- POLICY has no IF NOT EXISTS form, so DROP IF EXISTS first.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS audit_insert_only ON audit_log;
CREATE POLICY audit_insert_only ON audit_log
    FOR INSERT
    WITH CHECK (true);
-- Revoke update/delete from the app database user
-- (run as superuser: REVOKE UPDATE, DELETE ON audit_log FROM eami_app;)

-- ─────────────────────────────────────────────────────────
-- TOKEN SPEND (FINOPS)
-- ─────────────────────────────────────────────────────────
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
SELECT create_hypertable('token_usage', 'recorded_at', if_not_exists => TRUE);
CREATE INDEX IF NOT EXISTS idx_token_usage_org   ON token_usage(org_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_usage_agent ON token_usage(agent_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_usage_model ON token_usage(model, recorded_at DESC);

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

-- ─────────────────────────────────────────────────────────
-- EPISODE MEMORY
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS episodes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        UUID REFERENCES gateway_agents(id),
    agent_name      TEXT NOT NULL,
    task            TEXT NOT NULL,
    steps           JSONB NOT NULL DEFAULT '[]',
    outcome         TEXT NOT NULL CHECK (outcome IN ('success','blocked','failed','partial')),
    token_total     INTEGER DEFAULT 0,
    approved_by     TEXT,
    embedding       vector(1536),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_episodes_org ON episodes(org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_episodes_agent ON episodes(agent_id);
CREATE INDEX IF NOT EXISTS idx_episodes_outcome ON episodes(outcome);
CREATE INDEX IF NOT EXISTS idx_episodes_embedding ON episodes
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- ─────────────────────────────────────────────────────────
-- ALERTS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS alert_rules (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    condition    TEXT NOT NULL,
    condition_config JSONB NOT NULL,
    severity     TEXT NOT NULL CHECK (severity IN ('info','warning','high','critical')),
    channels     TEXT[] NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_alert_rules_org ON alert_rules(org_id);

CREATE TABLE IF NOT EXISTS alerts (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    rule_id          UUID NOT NULL REFERENCES alert_rules(id),
    severity         TEXT NOT NULL CHECK (severity IN ('info','warning','high','critical')),
    message          TEXT NOT NULL,
    context          JSONB,
    fired_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at      TIMESTAMPTZ,
    notified         BOOLEAN NOT NULL DEFAULT FALSE,
    status           TEXT NOT NULL DEFAULT 'open'
                         CHECK (status IN ('open','acknowledged','resolved')),
    acknowledged_by  TEXT,
    acknowledged_at  TIMESTAMPTZ,
    metric_value     NUMERIC
);
CREATE INDEX IF NOT EXISTS idx_alerts_org ON alerts(org_id, fired_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_unresolved ON alerts(org_id, fired_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(org_id, status, fired_at DESC);

-- ─────────────────────────────────────────────────────────
-- NOTIFICATION CONFIG
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notification_config (
    org_id           UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    slack_webhook_url TEXT,
    slack_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    email_smtp_host   TEXT,
    email_smtp_port   INT NOT NULL DEFAULT 587,
    email_from        TEXT,
    email_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────
-- NOTIFICATION SETTINGS
-- ─────────────────────────────────────────────────────────
-- NOTE: no known application code references this table (see BACKLOG.md
-- B-011, still open) — kept in the baseline unchanged because dropping it
-- is a real schema change, out of scope for this migration-mechanism-only
-- brief.
CREATE TABLE IF NOT EXISTS notification_channels (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('slack','email','teams','webhook')),
    name         TEXT NOT NULL,
    config       JSONB NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_org ON notification_channels(org_id);

-- ─────────────────────────────────────────────────────────
-- UTILITY: updated_at trigger
-- ─────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- CREATE TRIGGER has no IF NOT EXISTS form — DROP IF EXISTS first.
DROP TRIGGER IF EXISTS trg_orgs_updated_at ON orgs;
CREATE TRIGGER trg_orgs_updated_at
    BEFORE UPDATE ON orgs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS trg_gateway_agents_updated_at ON gateway_agents;
CREATE TRIGGER trg_gateway_agents_updated_at
    BEFORE UPDATE ON gateway_agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS trg_policies_updated_at ON policies;
CREATE TRIGGER trg_policies_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─────────────────────────────────────────────────────────
-- ROLES & PERMISSIONS
-- ─────────────────────────────────────────────────────────
-- Create application database user (run as superuser, not part of this
-- migration — matches schema.sql's own convention of leaving this as a
-- documented manual step, not an automated one):
-- CREATE USER eami_app WITH PASSWORD 'changeme';
-- GRANT CONNECT ON DATABASE eami TO eami_app;
-- GRANT USAGE ON SCHEMA public TO eami_app;
-- GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO eami_app;
-- REVOKE UPDATE, DELETE ON audit_log FROM eami_app;
-- GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eami_app;

-- ─────────────────────────────────────────────────────────
-- DISCOVERED ENDPOINTS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS discovered_endpoints (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source_host  TEXT        NOT NULL,
    method       TEXT        NOT NULL CHECK (method IN ('GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS')),
    path         TEXT        NOT NULL,
    host         TEXT        NOT NULL,
    port         INT,
    tls          BOOLEAN     NOT NULL DEFAULT FALSE,
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    hit_count    INT         NOT NULL DEFAULT 1,
    tags         JSONB       NOT NULL DEFAULT '[]',
    raw_headers  JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_discovered_endpoints
    ON discovered_endpoints(org_id, source_host, method, host, path);
CREATE INDEX IF NOT EXISTS idx_discovered_endpoints_org    ON discovered_endpoints(org_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_discovered_endpoints_host   ON discovered_endpoints(org_id, host);
CREATE INDEX IF NOT EXISTS idx_discovered_endpoints_source ON discovered_endpoints(org_id, source_host);

-- ─────────────────────────────────────────────────────────
-- AGENT CONFIGS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS agent_configs (
    agent_id              UUID PRIMARY KEY REFERENCES gateway_agents(id) ON DELETE CASCADE,
    scan_interval_seconds INT          NOT NULL DEFAULT 300,
    model_scan_paths      TEXT[]       NOT NULL DEFAULT ARRAY['/home', '/Users', 'C:\\Users'],
    max_report_size_bytes INT          NOT NULL DEFAULT 5242880,
    enabled_scanners      TEXT[]       NOT NULL DEFAULT ARRAY['ai_apps','models','mcp_servers','cloud_clients','network_activity','browser'],
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION create_default_agent_config()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO agent_configs (agent_id) VALUES (NEW.id)
    ON CONFLICT (agent_id) DO NOTHING;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_agent_configs_default ON gateway_agents;
CREATE TRIGGER trg_agent_configs_default
AFTER INSERT ON gateway_agents
FOR EACH ROW EXECUTE FUNCTION create_default_agent_config();

-- ─────────────────────────────────────────────────────────
-- PASTE EVENTS
-- ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS paste_events (
    id                  UUID        NOT NULL DEFAULT uuid_generate_v4(),
    org_id              UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source_endpoint_id  UUID        NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    destination_domain  TEXT        NOT NULL,
    content_length      INTEGER,
    content_hash        TEXT,
    os_username         TEXT,
    occurred_at         TIMESTAMPTZ NOT NULL,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
SELECT create_hypertable('paste_events', 'occurred_at', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS idx_paste_events_org         ON paste_events(org_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_paste_events_org_domain  ON paste_events(org_id, destination_domain, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_paste_events_endpoint    ON paste_events(source_endpoint_id, occurred_at DESC);

SELECT add_retention_policy('paste_events', INTERVAL '90 days', if_not_exists => TRUE);
