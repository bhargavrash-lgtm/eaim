-- name: GetOrgSettings :one
SELECT id, name, slug, plan,
       COALESCE(timezone, 'UTC') AS timezone,
       COALESCE(default_risk_tier, 'low') AS default_risk_tier,
       updated_at
FROM orgs
WHERE id = $1;

-- name: UpdateOrgSettings :one
UPDATE orgs SET
    name             = COALESCE($2, name),
    timezone         = COALESCE($3, timezone),
    default_risk_tier = COALESCE($4, default_risk_tier)
WHERE id = $1
RETURNING id, name, slug, plan,
          COALESCE(timezone, 'UTC') AS timezone,
          COALESCE(default_risk_tier, 'low') AS default_risk_tier,
          updated_at;

-- name: GetNotificationConfig :one
SELECT org_id, slack_enabled, slack_webhook_url, email_enabled,
       email_smtp_host, email_smtp_port, email_from, updated_at
FROM notification_config
WHERE org_id = $1;

-- name: UpsertNotificationConfig :one
INSERT INTO notification_config (
    org_id, slack_enabled, slack_webhook_url,
    email_enabled, email_smtp_host, email_smtp_port, email_from
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (org_id) DO UPDATE SET
    slack_enabled     = EXCLUDED.slack_enabled,
    slack_webhook_url = EXCLUDED.slack_webhook_url,
    email_enabled     = EXCLUDED.email_enabled,
    email_smtp_host   = EXCLUDED.email_smtp_host,
    email_smtp_port   = EXCLUDED.email_smtp_port,
    email_from        = EXCLUDED.email_from,
    updated_at        = NOW()
RETURNING org_id, slack_enabled, slack_webhook_url, email_enabled,
          email_smtp_host, email_smtp_port, email_from, updated_at;
