-- Migration: 003_alerts.sql
-- Owner: Architect-EAMI must apply before alerting engine endpoints go live.
-- Adds status / acknowledge / metric_value columns to the alerts table.

ALTER TABLE alerts
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved')),
    ADD COLUMN IF NOT EXISTS acknowledged_by  UUID REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS acknowledged_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS metric_value     NUMERIC;

CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(org_id, status, fired_at DESC);
