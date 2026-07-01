-- Migration: 002_settings
-- Date: 2026-06-06
-- Author: Architect-EAMI
-- Description: Add org settings columns, user soft-delete/invite fields,
--              api_key expiry, and notification_config table.

-- ── orgs ──────────────────────────────────────────────────
ALTER TABLE orgs
    ADD COLUMN timezone          TEXT NOT NULL DEFAULT 'UTC',
    ADD COLUMN default_risk_tier TEXT NOT NULL DEFAULT 'medium'
        CHECK (default_risk_tier IN ('low','medium','high'));

-- ── users ─────────────────────────────────────────────────
ALTER TABLE users
    ADD COLUMN deleted_at  TIMESTAMPTZ,
    ADD COLUMN invited_at  TIMESTAMPTZ,
    ADD COLUMN invited_by  UUID REFERENCES users(id);

-- Extend role check to include 'approver'
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin','operator','approver','viewer'));

-- ── api_keys ──────────────────────────────────────────────
ALTER TABLE api_keys
    ADD COLUMN expires_at TIMESTAMPTZ;

-- ── notification_config ───────────────────────────────────
-- One row per org: org-level Slack and email delivery settings.
CREATE TABLE notification_config (
    org_id            UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    slack_webhook_url TEXT,           -- stored encrypted; masked (first 8 chars) in API responses
    slack_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    email_smtp_host   TEXT,
    email_smtp_port   INT NOT NULL DEFAULT 587,
    email_from        TEXT,
    email_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
