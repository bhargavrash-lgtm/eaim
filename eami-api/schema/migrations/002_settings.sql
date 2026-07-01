-- Migration: 002_settings.sql
-- Owner: Architect-EAMI must apply before eami-api settings endpoints go live.
-- Adds columns and tables required by Part 1-4 of the Settings API task.
-- Apply via: psql -f schema/migrations/002_settings.sql

-- ── Org settings columns ────────────────────────────────────────────────────
-- Required by GET/PUT /v1/settings/org
ALTER TABLE orgs
    ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC',
    ADD COLUMN IF NOT EXISTS default_risk_tier TEXT NOT NULL DEFAULT 'low'
        CHECK (default_risk_tier IN ('low','medium','high'));

-- ── User management columns ─────────────────────────────────────────────────
-- Required by DELETE /v1/users/{id} (soft delete) and POST /v1/users/invite
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS deleted_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invited_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS invited_by  UUID REFERENCES users(id);

CREATE INDEX IF NOT EXISTS idx_users_active ON users(org_id) WHERE deleted_at IS NULL;

-- ── API key expiry ───────────────────────────────────────────────────────────
-- Required by GET /v1/auth/api-keys (expires_at field in response)
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;  -- NULL = never expires

-- ── Notification config ─────────────────────────────────────────────────────
-- Required by GET/PUT /v1/settings/notifications
-- One row per org; upserted on every PUT.
CREATE TABLE IF NOT EXISTS notification_config (
    org_id              UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    slack_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    slack_webhook_url   TEXT,                           -- stored in full; masked on read
    email_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    email_smtp_host     TEXT,
    email_smtp_port     INTEGER NOT NULL DEFAULT 587,
    email_from          TEXT,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trg_notification_config_updated_at
    BEFORE UPDATE ON notification_config
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
