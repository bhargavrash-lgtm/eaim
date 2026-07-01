# Task: Wire scope_drift_count and failed_delivery_count alert metrics
**From:** PM-EAMI  
**To:** BE-Policy  
**Priority:** normal  
**Blocked by:** TASK-038 (migrations must be clean)

## What I need

Two alert metrics in `eami-api/internal/alerting/engine.go` are currently stubbed and return zero. Wire them to real DB queries.

### 1. `scope_drift_count`

**Definition:** Number of MCP tool calls in the evaluation window where the policy decision was `escalated` (scope drift detected).

**Query against `audit_log`:**

```sql
SELECT COUNT(*)
FROM audit_log
WHERE org_id = $1
  AND decision = 'escalated'
  AND timestamp >= NOW() - ($2 || ' minutes')::INTERVAL
```

Where `$2` is the rule's `window_minutes` from the `AlertRule` condition.

**Where to add:** In the `evaluateMetric` function (or equivalent) in `engine.go`, handle the `"scope_drift_count"` case by running this query against the DB pool.

### 2. `failed_delivery_count`

**Definition:** Number of agent reports that landed in the collector dead-letter table (failed to forward to the SaaS API) in the evaluation window.

This metric crosses a service boundary — the `dead_letter` table is in the **collector's SQLite buffer**, not in PostgreSQL. The cleanest approach for v1 is:

Add a new eami-api endpoint:
```
GET /v1/internal/collector/dead-letter-count?window_minutes=N
X-Service-Key: <service_key>
```

The collector exposes this via its own API (`GET /dead-letter/count?since=<RFC3339>`), and eami-api calls the collector URL to get the count.

**For now (v1 simplification):** Query the PostgreSQL `alerts` table for alerts of type `"failed_delivery"` as a proxy, or stub with a TODO comment and skip to unblock the milestone. Document clearly that real implementation requires the collector's HTTP API.

**Action:** Implement `scope_drift_count` fully. For `failed_delivery_count`, add a TODO with the correct query design and return 0 with a `slog.Warn`. Do not silently swallow the stub — make it visible in logs.

### 3. Verify alert rule fires

After wiring `scope_drift_count`:
1. In the running stack, create an alert rule: metric = `scope_drift_count`, threshold = `1`, window = `60` minutes, severity = `warning`.
2. Trigger a policy escalation through the gateway.
3. Wait up to 2 minutes (the engine ticks every 1 minute).
4. Verify an alert row appears in the `alerts` table.
5. If Slack webhook is configured, verify the Slack message arrives.

## Acceptance criteria

- [ ] `scope_drift_count` metric runs a real SQL query against `audit_log`
- [ ] Creating an alert rule with `scope_drift_count` and triggering a scope drift causes an alert to be created within 2 minutes
- [ ] `failed_delivery_count` has a `slog.Warn("failed_delivery_count not fully implemented")` + returns 0 (not silently 0)
- [ ] Alert deduplication still works — same rule firing twice does not create a second open alert
- [ ] `go vet ./...` exits 0

## Files to modify

- `eami-api/internal/alerting/engine.go` — implement metric queries
- `eami-api/internal/store/query/alerts.sql` — add `CountScopeDriftEvents` query
- `eami-api/internal/store/alerts.sql.go` — add the method

## Files to read first

- `eami-api/internal/alerting/engine.go` — existing metric evaluation structure
- `eami-api/internal/alerting/dispatcher.go` — Slack dispatch (already implemented)
- `schema/schema.sql` — `audit_log` table definition, `alerts` table
- `api/openapi.yaml` — AlertRule schema (metric enum values)
