# Task: Wire Slack webhook config for approvals into docker-compose
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** normal  
**Blocked by:** none

## What I need

The gateway's approval router sends Slack notifications when a policy ESCALATEs a tool call. The webhook URL is already read from `GATEWAY_APPROVAL_SLACK_WEBHOOK` env var (set in session 3). But the docker-compose and `.env.example` need to make this easy for a customer to configure.

### Step 1 — `.env.example`

Add these two entries with comments:

```bash
# Slack webhook for approval notifications (optional — approvals work without it, just no Slack message)
# Create at: https://api.slack.com/apps → Incoming Webhooks
GATEWAY_APPROVAL_SLACK_WEBHOOK=https://hooks.slack.com/services/YOUR/WEBHOOK/URL

# The public URL of your EAMI UI — used in Slack approval links
GATEWAY_UI_BASE_URL=http://localhost:5173
```

### Step 2 — `docker-compose.yml`

In the `eami-gateway` service `environment:` block, add:

```yaml
GATEWAY_APPROVAL_SLACK_WEBHOOK: ${GATEWAY_APPROVAL_SLACK_WEBHOOK:-}
GATEWAY_UI_BASE_URL: ${GATEWAY_UI_BASE_URL:-http://localhost:5173}
```

The `:-` default means if the var is not set in `.env`, it passes an empty string (Slack disabled gracefully).

### Step 3 — Verify

With `GATEWAY_APPROVAL_SLACK_WEBHOOK` set to a real webhook URL in `.env`:
1. Restart gateway: `docker compose restart eami-gateway`
2. Check gateway logs: `docker compose logs eami-gateway | grep slack`
3. Expected: `approval router ready slack_enabled=true`

Without the var set:
- Expected: `approval router ready slack_enabled=false`
- No error, no crash.

## Context

Customers won't know to add this manually. The `.env.example` is the primary onboarding artifact — it needs to document every knob.

## Acceptance criteria

- [ ] `.env.example` documents `GATEWAY_APPROVAL_SLACK_WEBHOOK` and `GATEWAY_UI_BASE_URL` with explanatory comments
- [ ] `docker-compose.yml` passes both vars to `eami-gateway` with empty-string defaults
- [ ] Gateway starts cleanly with `GATEWAY_APPROVAL_SLACK_WEBHOOK` unset (no crash, `slack_enabled=false` in logs)
- [ ] Gateway starts cleanly with `GATEWAY_APPROVAL_SLACK_WEBHOOK` set (no crash, `slack_enabled=true` in logs)

## Files to modify

- `.env.example`
- `docker-compose.yml`
