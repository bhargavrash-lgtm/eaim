-- Migration: 003_alerts
-- Date: 2026-06-06
-- Author: Architect-EAMI
-- Description: Add alert lifecycle fields to the alerts table:
--              status, acknowledged_by, acknowledged_at, metric_value.

-- ── alerts ────────────────────────────────────────────────
ALTER TABLE alerts
    ADD COLUMN status           TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved')),
    ADD COLUMN acknowledged_by  TEXT,
    ADD COLUMN acknowledged_at  TIMESTAMPTZ,
    ADD COLUMN metric_value     NUMERIC;  -- value that triggered the alert

CREATE INDEX idx_alerts_status ON alerts(org_id, status, fired_at DESC);
