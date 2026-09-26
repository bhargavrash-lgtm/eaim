-- EAMI — PostgreSQL Database Schema
-- Version: 0.1.0
-- Owner: Architect-EAMI
-- Apply via: psql -f schema.sql
-- Extensions required: pgvector, timescaledb

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
CREATE TABLE orgs (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name              TEXT NOT NULL,
    slug              TEXT NOT NULL UNIQUE,
    plan              TEXT NOT NULL DEFAULT 'trial' CHECK (plan IN ('trial','starter','business','enterprise')),
    timezone          TEXT NOT NULL DEFAULT 'UTC',                              -- migration 002
    default_risk_tier TEXT NOT NULL DEFAULT 'medium'
                          CHECK (default_risk_tier IN ('low','medium','high')), -- migration 002
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT,
    password_hash TEXT,                           -- NULL if SSO-only
    role          TEXT NOT NULL DEFAULT 'operator' CHECK (role IN ('admin','operator','approver','viewer')),
    sso_provider  TEXT,                           -- e.g. 'okta', 'azure'
    sso_subject   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login    TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,                    -- migration 002: soft delete
    invited_at    TIMESTAMPTZ,                    -- migration 002
    invited_by    UUID REFERENCES users(id),      -- migration 002
    UNIQUE (org_id, email)
);
CREATE INDEX idx_users_org ON users(org_id);

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,             -- SHA-256 of the token
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked     BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);

CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    key_hash    TEXT NOT NULL UNIQUE,             -- SHA-256 of the full key
    prefix      TEXT NOT NULL,                    -- first 12 chars, shown in UI
    scopes      TEXT[] NOT NULL DEFAULT '{}',
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used   TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,                      -- migration 002: optional expiry
    revoked     BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX idx_api_keys_org ON api_keys(org_id);
CREATE INDEX idx_api_keys_hash ON api_keys(key_hash);

-- ─────────────────────────────────────────────────────────
-- ENDPOINT DISCOVERY
-- ─────────────────────────────────────────────────────────
CREATE TABLE endpoints (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL,                -- from eami-agent config
    hostname        TEXT NOT NULL,
    agent_version   TEXT,
    os_info         JSONB,                        -- {os, version, arch}
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    risk_score      NUMERIC(5,2) DEFAULT 0,
    UNIQUE (org_id, agent_id)
);
CREATE INDEX idx_endpoints_org ON endpoints(org_id);
CREATE INDEX idx_endpoints_last_seen ON endpoints(last_seen DESC);

-- Full endpoint reports (raw JSON, kept for 90 days)
CREATE TABLE endpoint_reports (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id  UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    org_id       UUID NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    report       JSONB NOT NULL,                  -- full EndpointReport JSON
    schema_version TEXT NOT NULL DEFAULT '1.0'
);
CREATE INDEX idx_reports_endpoint ON endpoint_reports(endpoint_id, collected_at DESC);
CREATE INDEX idx_reports_collected ON endpoint_reports(collected_at DESC);
-- Note: not a hypertable — UUID PRIMARY KEY is incompatible with TimescaleDB
-- partitioning (child tables FK-reference id alone). Plain indexes suffice for v1.

-- Normalised AI app signals (for fast querying)
CREATE TABLE endpoint_ai_apps (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    version     TEXT,
    source      TEXT,
    detected_at TIMESTAMPTZ NOT NULL,
    report_id   UUID REFERENCES endpoint_reports(id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_apps_endpoint ON endpoint_ai_apps(endpoint_id);

CREATE TABLE endpoint_model_files (
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
CREATE INDEX idx_model_files_endpoint ON endpoint_model_files(endpoint_id);

CREATE TABLE endpoint_mcp_servers (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    transport   TEXT CHECK (transport IN ('stdio','sse','socket')),
    port        INTEGER,
    source      TEXT CHECK (source IN ('claude_desktop','vscode','cursor','live_port')),
    detected_at TIMESTAMPTZ NOT NULL,
    report_id   UUID REFERENCES endpoint_reports(id) ON DELETE CASCADE
);
CREATE INDEX idx_mcp_servers_endpoint ON endpoint_mcp_servers(endpoint_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: AGENT REGISTRY
-- ─────────────────────────────────────────────────────────
CREATE TABLE gateway_agents (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    model            TEXT NOT NULL,
    owner            TEXT NOT NULL,
    scope            TEXT NOT NULL,               -- declared task scope (plain language)
    risk_tier        TEXT NOT NULL DEFAULT 'low' CHECK (risk_tier IN ('low','medium','high')),
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','revoked')),
    token_ttl_seconds INTEGER NOT NULL DEFAULT 900,
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen        TIMESTAMPTZ,
    UNIQUE (org_id, name)
);
CREATE INDEX idx_gateway_agents_org ON gateway_agents(org_id);
CREATE INDEX idx_gateway_agents_status ON gateway_agents(status);

-- AI token revocation list (broadcast to all nodes via Serf)
CREATE TABLE revoked_ai_tokens (
    jti         TEXT PRIMARY KEY,                 -- JWT ID (jti claim)
    agent_id    UUID NOT NULL REFERENCES gateway_agents(id) ON DELETE CASCADE,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reason      TEXT
);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: POLICIES
-- ─────────────────────────────────────────────────────────
CREATE TABLE policies (
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
CREATE INDEX idx_policies_org_priority ON policies(org_id, priority ASC);
CREATE INDEX idx_policies_status ON policies(status);

CREATE TABLE policy_conditions (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    policy_id            UUID NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    agent_name_pattern   TEXT,                    -- glob pattern, NULL = any
    tool_names           TEXT[],                  -- empty = any
    action_types         TEXT[],                  -- empty = any
    environments         TEXT[],                  -- empty = any
    record_count_gt      INTEGER,                 -- NULL = no limit
    semantic_rule        TEXT,                    -- LLM-evaluated natural language rule
    scope_drift          BOOLEAN DEFAULT FALSE,
    tool_server_ids      TEXT[]                   -- gateway_tools.id UUIDs, empty/NULL = any (B-044, migration 009)
);
CREATE INDEX idx_policy_conditions_policy ON policy_conditions(policy_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: TOOL CONNECTIONS
-- ─────────────────────────────────────────────────────────
CREATE TABLE gateway_tools (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    type         TEXT NOT NULL CHECK (type IN ('mcp','rest_api','database')),
    auth_type    TEXT NOT NULL CHECK (auth_type IN ('oauth2','api_key','basic','db_connection_string')),
    mcp_command  TEXT,
    mcp_args     TEXT[],
    base_url     TEXT,
    -- Credentials stored encrypted using pgcrypto. Never returned in API responses.
    credentials_encrypted BYTEA,
    status       TEXT NOT NULL DEFAULT 'connected' CHECK (status IN ('connected','degraded','disconnected')),
    last_used    TIMESTAMPTZ,
    last_tested  TIMESTAMPTZ,
    test_latency_ms INTEGER,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Per-action path/method mappings for rest_api tools (B-046):
    -- {"<action>": {"path": "/contacts", "method": "POST"}}. NULL = no
    -- mappings, every action routes to the flat base_url (B-044's
    -- original behavior, unchanged for any tool that doesn't define this).
    action_paths JSONB,
    UNIQUE (org_id, name)
);
CREATE INDEX idx_gateway_tools_org ON gateway_tools(org_id);

-- ─────────────────────────────────────────────────────────
-- GATEWAY: NODES
-- ─────────────────────────────────────────────────────────
CREATE TABLE gateway_nodes (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('primary','edge','dr_standby')),
    status              TEXT NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy','degraded','standby','offline')),
    address             TEXT NOT NULL,            -- IP:port
    hostname            TEXT,
    version             TEXT,
    last_heartbeat      TIMESTAMPTZ,
    UNIQUE (org_id, name)
);
CREATE INDEX idx_gateway_nodes_org ON gateway_nodes(org_id);

-- Node telemetry time-series
CREATE TABLE gateway_node_metrics (
    node_id         UUID NOT NULL REFERENCES gateway_nodes(id) ON DELETE CASCADE,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cpu_pct         NUMERIC(5,2),
    memory_mb       INTEGER,
    requests_per_min INTEGER,
    active_sessions INTEGER
);
SELECT create_hypertable('gateway_node_metrics', 'recorded_at', if_not_exists => TRUE);
CREATE INDEX idx_node_metrics ON gateway_node_metrics(node_id, recorded_at DESC);

-- ─────────────────────────────────────────────────────────
-- APPROVALS
-- ─────────────────────────────────────────────────────────
CREATE TABLE approval_requests (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id       UUID NOT NULL REFERENCES gateway_agents(id),
    agent_name     TEXT NOT NULL,
    tool_name      TEXT NOT NULL,
    action         TEXT NOT NULL,
    parameters     JSONB,                         -- sanitised, no raw prompts
    justification  TEXT NOT NULL,
    risk_level     TEXT NOT NULL CHECK (risk_level IN ('low','medium','high','critical')),
    -- blast radius
    estimated_records INTEGER,
    reversible     BOOLEAN,
    environment    TEXT CHECK (environment IN ('production','staging','development','unknown')),
    data_types     TEXT[],
    -- policy that triggered escalation
    policy_id      UUID REFERENCES policies(id),
    -- decision
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','denied','expired','cancelled')),
    approved_by    UUID REFERENCES users(id),
    decision_reason TEXT,
    decided_at     TIMESTAMPTZ,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- gateway callback
    gateway_session_id TEXT NOT NULL,             -- used by gateway to resume/cancel session
    gateway_node_address TEXT NOT NULL            -- which node is holding the session
);
CREATE INDEX idx_approvals_org_status ON approval_requests(org_id, status, created_at DESC);
CREATE INDEX idx_approvals_pending ON approval_requests(status, expires_at) WHERE status = 'pending';

-- ─────────────────────────────────────────────────────────
-- AUDIT LOG
-- ─────────────────────────────────────────────────────────
CREATE TABLE audit_log (
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
    -- hash chain for tamper evidence
    prev_hash      TEXT NOT NULL,
    hash           TEXT NOT NULL
) PARTITION BY RANGE (timestamp);

-- Monthly partitions — pre-created through 2027; auto-extended by pg_cron (see 007_audit_partitions.sql)
-- 2026
CREATE TABLE audit_log_2026_05 PARTITION OF audit_log
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE audit_log_2026_06 PARTITION OF audit_log
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE audit_log_2026_07 PARTITION OF audit_log
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE audit_log_2026_08 PARTITION OF audit_log
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE audit_log_2026_09 PARTITION OF audit_log
    FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE audit_log_2026_10 PARTITION OF audit_log
    FOR VALUES FROM ('2026-10-01') TO ('2026-11-01');
CREATE TABLE audit_log_2026_11 PARTITION OF audit_log
    FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE audit_log_2026_12 PARTITION OF audit_log
    FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
-- 2027
CREATE TABLE audit_log_2027_01 PARTITION OF audit_log
    FOR VALUES FROM ('2027-01-01') TO ('2027-02-01');
CREATE TABLE audit_log_2027_02 PARTITION OF audit_log
    FOR VALUES FROM ('2027-02-01') TO ('2027-03-01');
CREATE TABLE audit_log_2027_03 PARTITION OF audit_log
    FOR VALUES FROM ('2027-03-01') TO ('2027-04-01');
CREATE TABLE audit_log_2027_04 PARTITION OF audit_log
    FOR VALUES FROM ('2027-04-01') TO ('2027-05-01');
CREATE TABLE audit_log_2027_05 PARTITION OF audit_log
    FOR VALUES FROM ('2027-05-01') TO ('2027-06-01');
CREATE TABLE audit_log_2027_06 PARTITION OF audit_log
    FOR VALUES FROM ('2027-06-01') TO ('2027-07-01');
CREATE TABLE audit_log_2027_07 PARTITION OF audit_log
    FOR VALUES FROM ('2027-07-01') TO ('2027-08-01');
CREATE TABLE audit_log_2027_08 PARTITION OF audit_log
    FOR VALUES FROM ('2027-08-01') TO ('2027-09-01');
CREATE TABLE audit_log_2027_09 PARTITION OF audit_log
    FOR VALUES FROM ('2027-09-01') TO ('2027-10-01');
CREATE TABLE audit_log_2027_10 PARTITION OF audit_log
    FOR VALUES FROM ('2027-10-01') TO ('2027-11-01');
CREATE TABLE audit_log_2027_11 PARTITION OF audit_log
    FOR VALUES FROM ('2027-11-01') TO ('2027-12-01');
CREATE TABLE audit_log_2027_12 PARTITION OF audit_log
    FOR VALUES FROM ('2027-12-01') TO ('2028-01-01');

CREATE INDEX idx_audit_log_org_ts ON audit_log(org_id, timestamp DESC);
CREATE INDEX idx_audit_log_agent ON audit_log(agent_name, timestamp DESC);
CREATE INDEX idx_audit_log_decision ON audit_log(decision, timestamp DESC);

-- Row-level security: app user may INSERT only (no UPDATE, no DELETE)
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_insert_only ON audit_log
    FOR INSERT
    WITH CHECK (true);
-- Revoke update/delete from the app database user
-- (run as superuser: REVOKE UPDATE, DELETE ON audit_log FROM eami_app;)

-- ─────────────────────────────────────────────────────────
-- TOKEN SPEND (FINOPS)
-- ─────────────────────────────────────────────────────────
-- TimescaleDB hypertable for token usage events
CREATE TABLE token_usage (
    id           UUID NOT NULL DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL,
    agent_id     UUID,
    agent_name   TEXT NOT NULL,
    team         TEXT,
    model        TEXT NOT NULL,
    tool_name    TEXT,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    cost_usd     NUMERIC(12,6),                  -- calculated at insert time
    outcome      TEXT CHECK (outcome IN ('success','blocked','failed','partial')),
    audit_log_id UUID,
    recorded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
SELECT create_hypertable('token_usage', 'recorded_at', if_not_exists => TRUE);
CREATE INDEX idx_token_usage_org   ON token_usage(org_id, recorded_at DESC);
CREATE INDEX idx_token_usage_agent ON token_usage(agent_id, recorded_at DESC);
CREATE INDEX idx_token_usage_model ON token_usage(model, recorded_at DESC);

-- Model pricing table (maintained by DevOps/admin)
CREATE TABLE model_pricing (
    model           TEXT PRIMARY KEY,
    cost_per_1k_in  NUMERIC(10,6) NOT NULL,       -- USD per 1000 input tokens
    cost_per_1k_out NUMERIC(10,6) NOT NULL,        -- USD per 1000 output tokens
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
    ('gpt-4-turbo',                0.010000, 0.030000);

-- ─────────────────────────────────────────────────────────
-- EPISODE MEMORY
-- ─────────────────────────────────────────────────────────
CREATE TABLE episodes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id        UUID REFERENCES gateway_agents(id),
    agent_name      TEXT NOT NULL,
    task            TEXT NOT NULL,
    steps           JSONB NOT NULL DEFAULT '[]',  -- array of EpisodeStep
    outcome         TEXT NOT NULL CHECK (outcome IN ('success','blocked','failed','partial')),
    token_total     INTEGER DEFAULT 0,
    approved_by     TEXT,
    -- pgvector embedding of the task description (1536 dims = text-embedding-3-small)
    embedding       vector(1536),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_episodes_org ON episodes(org_id, created_at DESC);
CREATE INDEX idx_episodes_agent ON episodes(agent_id);
CREATE INDEX idx_episodes_outcome ON episodes(outcome);
-- HNSW index for fast approximate nearest-neighbour search
CREATE INDEX idx_episodes_embedding ON episodes
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- ─────────────────────────────────────────────────────────
-- ALERTS
-- ─────────────────────────────────────────────────────────
CREATE TABLE alert_rules (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    condition    TEXT NOT NULL,                   -- human-readable condition description
    condition_config JSONB NOT NULL,              -- machine-readable: {type, threshold, field, ...}
    severity     TEXT NOT NULL CHECK (severity IN ('info','warning','high','critical')),
    channels     TEXT[] NOT NULL DEFAULT '{}',    -- ['slack','email','teams']
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_alert_rules_org ON alert_rules(org_id);

CREATE TABLE alerts (
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
                         CHECK (status IN ('open','acknowledged','resolved')), -- migration 003
    acknowledged_by  TEXT,                         -- migration 003
    acknowledged_at  TIMESTAMPTZ,                  -- migration 003
    metric_value     NUMERIC                       -- migration 003: value that triggered the alert
);
CREATE INDEX idx_alerts_org ON alerts(org_id, fired_at DESC);
CREATE INDEX idx_alerts_unresolved ON alerts(org_id, fired_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX idx_alerts_status ON alerts(org_id, status, fired_at DESC);  -- migration 003

-- ─────────────────────────────────────────────────────────
-- NOTIFICATION CONFIG (migration 002)
-- ─────────────────────────────────────────────────────────
-- One row per org: org-level Slack and email delivery settings.
CREATE TABLE notification_config (
    org_id           UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    slack_webhook_url TEXT,                        -- stored encrypted; masked in API responses
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
CREATE TABLE notification_channels (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('slack','email','teams','webhook')),
    name         TEXT NOT NULL,
    config       JSONB NOT NULL,                  -- {webhook_url} or {email} or {teams_url}
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_notification_channels_org ON notification_channels(org_id);

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

CREATE TRIGGER trg_orgs_updated_at
    BEFORE UPDATE ON orgs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_gateway_agents_updated_at
    BEFORE UPDATE ON gateway_agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_policies_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─────────────────────────────────────────────────────────
-- ROLES & PERMISSIONS
-- ─────────────────────────────────────────────────────────
-- Create application database user (run as superuser)
-- CREATE USER eami_app WITH PASSWORD 'changeme';
-- GRANT CONNECT ON DATABASE eami TO eami_app;
-- GRANT USAGE ON SCHEMA public TO eami_app;
-- GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO eami_app;
-- REVOKE UPDATE, DELETE ON audit_log FROM eami_app;  -- enforce append-only
-- GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eami_app;

-- ─────────────────────────────────────────────────────────
-- DISCOVERED ENDPOINTS (migration 004)
-- ─────────────────────────────────────────────────────────
-- Network-observed API endpoints surfaced by the on-prem collector
-- forwarder and written via POST /v1/reports (service API key auth).
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

CREATE INDEX idx_discovered_endpoints_org    ON discovered_endpoints(org_id, last_seen DESC);
CREATE INDEX idx_discovered_endpoints_host   ON discovered_endpoints(org_id, host);
CREATE INDEX idx_discovered_endpoints_source ON discovered_endpoints(org_id, source_host);

-- ── 006: agent_configs ───────────────────────────────────────────────────────

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

CREATE TRIGGER trg_agent_configs_default
AFTER INSERT ON gateway_agents
FOR EACH ROW EXECUTE FUNCTION create_default_agent_config();

-- ─────────────────────────────────────────────────────────
-- PASTE EVENTS (migration 008)
-- ─────────────────────────────────────────────────────────
-- Append-only event log for browser-extension paste-detection
-- (known-destination detection only; no content-classification/DLP).
-- NOT a reuse of discovered_endpoints: that table dedups by
-- (org,source_host,method,host,path) with a single overwritten
-- last_seen, designed for "have we ever seen this endpoint" discovery,
-- not per-event history. Written via a future POST /v1/reports/paste-events.
--
-- TimescaleDB hypertable (like token_usage), not audit_log's pg_cron
-- monthly-partition pattern: audit_log's manual partitioning exists for
-- its RLS insert-only policy + hash-chain tamper-evidence requirements,
-- which don't apply here. See schema/migrations/008_paste_events.sql for
-- the full design rationale.
CREATE TABLE paste_events (
    id                  UUID        NOT NULL DEFAULT uuid_generate_v4(),
    org_id              UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source_endpoint_id  UUID        NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    destination_domain  TEXT        NOT NULL,
    content_length      INTEGER,                      -- coarse indicator only, never the pasted text
    content_hash        TEXT,                          -- coarse indicator only, never the pasted text
    os_username         TEXT,                          -- optional, anonymised (mirrors browser scanner's UserPath)
    occurred_at         TIMESTAMPTZ NOT NULL,           -- hypertable partition column
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
SELECT create_hypertable('paste_events', 'occurred_at', if_not_exists => TRUE);

CREATE INDEX idx_paste_events_org         ON paste_events(org_id, occurred_at DESC);
CREATE INDEX idx_paste_events_org_domain  ON paste_events(org_id, destination_domain, occurred_at DESC);
CREATE INDEX idx_paste_events_endpoint    ON paste_events(source_endpoint_id, occurred_at DESC);

SELECT add_retention_policy('paste_events', INTERVAL '90 days');

-- CMDB CLASSIFICATION (B-196 Increment 2; canonical form of migration 000024)
CREATE TABLE ci_categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    description TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, org_id),
    UNIQUE (org_id, normalized_name)
);

CREATE TABLE ci_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    category_id UUID NOT NULL,
    asset_kind TEXT NOT NULL CHECK (asset_kind IN ('endpoint','agent','tool')),
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    description TEXT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (id, org_id),
    UNIQUE (org_id, normalized_name),
    CONSTRAINT ci_types_category_org_fk FOREIGN KEY (category_id, org_id)
        REFERENCES ci_categories(id, org_id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX ci_types_one_default_per_kind ON ci_types(org_id, asset_kind) WHERE is_default;
CREATE INDEX ci_types_category_idx ON ci_types(category_id);

CREATE OR REPLACE FUNCTION normalize_ci_name() RETURNS TRIGGER AS $$
BEGIN
    NEW.name := btrim(NEW.name);
    NEW.normalized_name := lower(regexp_replace(NEW.name, '\s+', ' ', 'g'));
    IF NEW.normalized_name = '' THEN RAISE EXCEPTION 'classification name must not be empty' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_ci_categories_normalize BEFORE INSERT OR UPDATE OF name ON ci_categories FOR EACH ROW EXECUTE FUNCTION normalize_ci_name();
CREATE TRIGGER trg_ci_types_normalize BEFORE INSERT OR UPDATE OF name ON ci_types FOR EACH ROW EXECUTE FUNCTION normalize_ci_name();
CREATE TRIGGER trg_ci_categories_updated_at BEFORE UPDATE ON ci_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_ci_types_updated_at BEFORE UPDATE ON ci_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE OR REPLACE FUNCTION keep_ci_type_scope_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.asset_kind <> OLD.asset_kind THEN RAISE EXCEPTION 'asset_kind is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.org_id <> OLD.org_id THEN RAISE EXCEPTION 'org_id is immutable' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_ci_types_kind_immutable BEFORE UPDATE OF asset_kind,org_id ON ci_types FOR EACH ROW EXECUTE FUNCTION keep_ci_type_scope_immutable();

CREATE OR REPLACE FUNCTION require_ci_default() RETURNS TRIGGER AS $$
DECLARE checked_org UUID := COALESCE(NEW.org_id, OLD.org_id); checked_kind TEXT := COALESCE(NEW.asset_kind, OLD.asset_kind);
BEGIN
    IF EXISTS (SELECT 1 FROM orgs WHERE id=checked_org)
       AND (SELECT count(*) FROM ci_types WHERE org_id=checked_org AND asset_kind=checked_kind AND is_default) <> 1 THEN
        RAISE EXCEPTION 'exactly one default CI type is required for org % kind %', checked_org, checked_kind USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER trg_ci_types_require_default AFTER INSERT OR UPDATE OR DELETE ON ci_types DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_ci_default();

ALTER TABLE endpoints ADD COLUMN ci_type_id UUID;
ALTER TABLE gateway_agents ADD COLUMN ci_type_id UUID;
ALTER TABLE gateway_tools ADD COLUMN ci_type_id UUID;
ALTER TABLE endpoints ADD CONSTRAINT endpoints_ci_type_org_fk FOREIGN KEY (ci_type_id,org_id) REFERENCES ci_types(id,org_id) ON DELETE RESTRICT;
ALTER TABLE gateway_agents ADD CONSTRAINT gateway_agents_ci_type_org_fk FOREIGN KEY (ci_type_id,org_id) REFERENCES ci_types(id,org_id) ON DELETE RESTRICT;
ALTER TABLE gateway_tools ADD CONSTRAINT gateway_tools_ci_type_org_fk FOREIGN KEY (ci_type_id,org_id) REFERENCES ci_types(id,org_id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION check_asset_ci_type_kind() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.ci_type_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM ci_types WHERE id=NEW.ci_type_id AND org_id=NEW.org_id AND asset_kind=TG_ARGV[0]) THEN
        RAISE EXCEPTION 'CI type does not belong to asset organization and kind' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_endpoints_ci_type_kind BEFORE INSERT OR UPDATE OF ci_type_id,org_id ON endpoints FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('endpoint');
CREATE TRIGGER trg_gateway_agents_ci_type_kind BEFORE INSERT OR UPDATE OF ci_type_id,org_id ON gateway_agents FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('agent');
CREATE TRIGGER trg_gateway_tools_ci_type_kind BEFORE INSERT OR UPDATE OF ci_type_id,org_id ON gateway_tools FOR EACH ROW EXECUTE FUNCTION check_asset_ci_type_kind('tool');

CREATE OR REPLACE FUNCTION seed_default_ci_taxonomy(seed_org UUID) RETURNS VOID AS $$
DECLARE endpoint_category UUID := uuid_generate_v5(seed_org,'cmdb-category:end-user-compute'); ai_category UUID := uuid_generate_v5(seed_org,'cmdb-category:ai-systems'); integrations_category UUID := uuid_generate_v5(seed_org,'cmdb-category:integrations');
BEGIN
    INSERT INTO ci_categories(id,org_id,name,normalized_name,sort_order) VALUES
      (endpoint_category,seed_org,'End-user compute','end-user compute',10),
      (ai_category,seed_org,'AI systems','ai systems',20),
      (integrations_category,seed_org,'Integrations','integrations',30)
    ON CONFLICT (org_id,normalized_name) DO NOTHING;
    SELECT id INTO endpoint_category FROM ci_categories WHERE org_id=seed_org AND normalized_name='end-user compute';
    SELECT id INTO ai_category FROM ci_categories WHERE org_id=seed_org AND normalized_name='ai systems';
    SELECT id INTO integrations_category FROM ci_categories WHERE org_id=seed_org AND normalized_name='integrations';
    INSERT INTO ci_types(id,org_id,category_id,asset_kind,name,normalized_name,is_default) VALUES
      (uuid_generate_v5(seed_org,'cmdb-type:endpoint:default'),seed_org,endpoint_category,'endpoint','Endpoint','endpoint',TRUE),
      (uuid_generate_v5(seed_org,'cmdb-type:agent:default'),seed_org,ai_category,'agent','Governed agent','governed agent',TRUE),
      (uuid_generate_v5(seed_org,'cmdb-type:tool:default'),seed_org,integrations_category,'tool','Connector','connector',TRUE)
    ON CONFLICT (org_id,normalized_name) DO NOTHING;
END;
$$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION seed_default_ci_taxonomy_for_org() RETURNS TRIGGER AS $$ BEGIN PERFORM seed_default_ci_taxonomy(NEW.id); RETURN NEW; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER trg_orgs_seed_default_ci_taxonomy AFTER INSERT ON orgs FOR EACH ROW EXECUTE FUNCTION seed_default_ci_taxonomy_for_org();
SELECT seed_default_ci_taxonomy(id) FROM orgs;
