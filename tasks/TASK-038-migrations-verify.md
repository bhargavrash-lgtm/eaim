# Task: Verify + fix DB migrations in docker-compose
**From:** PM-EAMI  
**To:** DevOps-EAMI  
**Priority:** high  
**Blocked by:** none

## What I need

Migrations 002 (settings) and 003 (alerts) added columns to existing tables. The docker-compose init mounts `schema/schema.sql` — but if those migrations were applied incrementally rather than merged into schema.sql, the init file may be out of date.

### Step 1 — Verify

Run this and check the output:

```bash
# Bring up a fresh postgres with no existing volume
docker compose down -v
docker compose up -d postgres
sleep 5

# Check the columns exist
docker compose exec postgres psql -U eami_app -d eami -c "\d orgs"
docker compose exec postgres psql -U eami_app -d eami -c "\d users"
docker compose exec postgres psql -U eami_app -d eami -c "\d alerts"
docker compose exec postgres psql -U eami_app -d eami -c "\d notification_config"
```

Expected columns from migration 002:
- `orgs.timezone` (TEXT, default 'UTC')
- `orgs.default_risk_tier` (TEXT)
- `users.deleted_at` (TIMESTAMPTZ nullable)
- `users.invited_at`, `users.invited_by`
- `api_keys.expires_at`
- Table `notification_config` exists

Expected columns from migration 003:
- `alerts.status` (TEXT)
- `alerts.acknowledged_by`, `alerts.acknowledged_at`
- `alerts.metric_value` (NUMERIC nullable)

### Step 2 — Fix

If any column or table is missing, merge the migration DDL into `schema/schema.sql` at the correct location. The canonical schema must be the single source of truth — no separate migration files should be required for a first-time setup.

If the migration files (`schema/migrations/002_settings.sql`, `schema/migrations/003_alerts.sql`) exist as separate files, check whether they are referenced in `docker-compose.yml` or just in `schema/schema.sql`. Make sure the docker-compose postgres init path applies them in the correct order.

### Step 3 — Also add migration 004

Once Architect-EAMI delivers `schema/migrations/004_endpoints.sql` (TASK-036), add it to the docker-compose init sequence as well.

### Step 4 — Bring the full stack back up

```bash
docker compose down -v
docker compose up -d
# Wait for all 5 containers healthy
docker compose ps
```

Confirm all 5 services are healthy. Log in at http://localhost:5173 with `bhargavrash@gmail.com` / `Admin1234!`. Navigate to Settings → verify the org settings page loads without a 500.

## Context

If settings and alerts columns are missing, every API call to those endpoints returns 500. This makes the UI appear broken even though the code is correct.

## Acceptance criteria

- [ ] `\d orgs` shows `timezone` and `default_risk_tier` columns
- [ ] `\d notification_config` shows the table exists
- [ ] `\d alerts` shows `status`, `acknowledged_by`, `acknowledged_at`, `metric_value`
- [ ] All 5 containers healthy after `docker compose up -d` on a fresh volume
- [ ] Settings page (http://localhost:5173/settings) loads without error
- [ ] Alerts page (http://localhost:5173/alerts) loads without error
- [ ] `scripts/seed-db.sh` runs without error after the fresh init

## Files to create or modify

- `schema/schema.sql` — merge 002+003 DDL if not already present
- `docker-compose.yml` — update init volume mounts if needed
- `schema/migrations/` — create 002/003 files if they don't exist as separate files (for CI migration testing)
