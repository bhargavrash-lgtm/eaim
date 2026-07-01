-- name: ListAlertRules :many
SELECT id, org_id, name, description, condition, condition_config,
       severity, channels, enabled, created_by, created_at
FROM alert_rules
WHERE org_id = $1
ORDER BY created_at DESC;

-- name: ListAllActiveAlertRules :many
SELECT id, org_id, name, description, condition, condition_config,
       severity, channels, enabled, created_by, created_at
FROM alert_rules
WHERE enabled = TRUE;

-- name: GetAlertRule :one
SELECT id, org_id, name, description, condition, condition_config,
       severity, channels, enabled, created_by, created_at
FROM alert_rules
WHERE id = $1 AND org_id = $2;

-- name: CreateAlertRule :one
INSERT INTO alert_rules (org_id, name, description, condition, condition_config, severity, channels, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, org_id, name, description, condition, condition_config,
          severity, channels, enabled, created_by, created_at;

-- name: UpdateAlertRule :one
UPDATE alert_rules SET
    name             = COALESCE($3, name),
    description      = COALESCE($4, description),
    condition        = COALESCE($5, condition),
    condition_config = COALESCE($6, condition_config),
    severity         = COALESCE($7, severity),
    channels         = COALESCE($8, channels),
    enabled          = COALESCE($9, enabled)
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, name, description, condition, condition_config,
          severity, channels, enabled, created_by, created_at;

-- name: DeleteAlertRule :exec
DELETE FROM alert_rules WHERE id = $1 AND org_id = $2;

-- name: ListAlerts :many
SELECT a.id, a.org_id, a.rule_id, r.name AS rule_name,
       a.severity, a.message, a.context, a.fired_at, a.resolved_at, a.notified,
       a.status, a.acknowledged_by, a.acknowledged_at, a.metric_value
FROM alerts a
JOIN alert_rules r ON r.id = a.rule_id
WHERE a.org_id = $1
  AND ($2::text IS NULL OR a.status = $2)
ORDER BY a.fired_at DESC
LIMIT $3 OFFSET $4;

-- name: CountAlerts :one
SELECT COUNT(*) FROM alerts WHERE org_id = $1 AND ($2::text IS NULL OR status = $2);

-- name: CreateAlert :one
INSERT INTO alerts (org_id, rule_id, severity, message, context, metric_value)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, org_id, rule_id, severity, message, context, fired_at,
          resolved_at, notified, status, acknowledged_by, acknowledged_at, metric_value;

-- name: GetOpenAlertByRuleID :one
SELECT id, org_id, rule_id, severity, message, context, fired_at,
       resolved_at, notified, status, acknowledged_by, acknowledged_at, metric_value
FROM alerts
WHERE rule_id = $1 AND status = 'open'
LIMIT 1;

-- name: UpdateAlertStatus :one
UPDATE alerts SET
    status          = $3,
    acknowledged_by = CASE WHEN $3 = 'acknowledged' THEN $4 ELSE acknowledged_by END,
    acknowledged_at = CASE WHEN $3 = 'acknowledged' THEN NOW() ELSE acknowledged_at END,
    resolved_at     = CASE WHEN $3 = 'resolved' THEN NOW() ELSE resolved_at END
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, rule_id, severity, message, context, fired_at,
          resolved_at, notified, status, acknowledged_by, acknowledged_at, metric_value;

-- name: MarkAlertNotified :exec
UPDATE alerts SET notified = TRUE WHERE id = $1;

-- name: CountScopeDriftEvents :one
-- Returns the number of escalated decisions in audit_log within the given window.
-- Used by the alerting engine for the scope_drift_count metric.
SELECT COUNT(*)
FROM audit_log
WHERE org_id = $1
  AND decision = 'escalated'
  AND timestamp >= NOW() - ($2 || ' minutes')::INTERVAL;
