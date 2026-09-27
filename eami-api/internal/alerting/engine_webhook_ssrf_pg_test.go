package alerting

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/netguard"
	"github.com/eami/api/internal/store"
)

// B-238, the real alert-engine path: a firing rule's Slack notification to
// an org's webhook URL must never reach an internal address. The URL is
// planted directly in notification_config (as a row saved before save-time
// validation existed would be), so this exercises the send-time guard on
// its own. A loopback httptest server stands in for an internal service:
// its hit counter is the proof of whether a request got through.
func TestEngineSlackDispatch_InternalTargetBlocked_RealDB(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		pw := os.Getenv("POSTGRES_PASSWORD")
		if pw == "" {
			t.Skip("skipping: set TEST_DATABASE_URL or POSTGRES_PASSWORD to run against a real Postgres")
		}
		dsn = fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// Registered before the org DELETE cleanup, so it runs last (CLAUDE.md's
	// mandatory real-Postgres pool lifecycle rule).
	t.Cleanup(func() { pool.Close() })

	orgID := uuid.New()
	slug := "b238-engine-" + orgID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $2)`, orgID, slug); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	// alerts/alert_rules/notification_config all cascade from orgs.
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })

	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(internal.Close)
	if _, err := pool.Exec(ctx, `INSERT INTO notification_config (org_id, slack_enabled, slack_webhook_url) VALUES ($1, TRUE, $2)`, orgID, internal.URL); err != nil {
		t.Fatalf("seed notification config: %v", err)
	}

	// denied_actions_count is 0 for a fresh org, so threshold -1 always fires.
	newRule := func(name string) store.AlertRule {
		cond := []byte(`{"metric":"denied_actions_count","condition":"gt","threshold":-1,"window_minutes":5}`)
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO alert_rules (org_id, name, condition, condition_config, severity) VALUES ($1, $2, 'b238', $3, 'warning') RETURNING id`,
			orgID, name, cond).Scan(&id); err != nil {
			t.Fatalf("seed rule: %v", err)
		}
		return store.AlertRule{ID: id, OrgID: orgID, Name: name, ConditionConfig: cond, Severity: "warning", Enabled: true}
	}
	notified := func(ruleID uuid.UUID) (bool, bool) {
		var n bool
		err := pool.QueryRow(ctx, `SELECT notified FROM alerts WHERE rule_id = $1`, ruleID).Scan(&n)
		return err == nil, n
	}

	// Production engine: the alert fires, but the webhook send is refused.
	e := NewEngine(store.New(pool), "", "")
	blocked := newRule("b238-blocked")
	if err := e.evaluateRule(ctx, blocked); err != nil {
		t.Fatalf("evaluateRule: %v", err)
	}
	if exists, n := notified(blocked.ID); !exists || n {
		t.Fatalf("blocked rule: alert exists=%v notified=%v, want an alert that was NOT notified", exists, n)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("REGRESSION B-238: the alert engine delivered %d webhook request(s) to a loopback address", n)
	}

	// Same path with the guard swapped out: the request does arrive, so the
	// zero above is the guard's doing, not an unreachable server.
	e.webhook = netguard.NewHTTPClient(unrestrictedDial, 5*time.Second)
	allowed := newRule("b238-allowed")
	if err := e.evaluateRule(ctx, allowed); err != nil {
		t.Fatalf("evaluateRule: %v", err)
	}
	if exists, n := notified(allowed.ID); !exists || !n || hits.Load() != 1 {
		t.Fatalf("unguarded control: alert exists=%v notified=%v hits=%d, want notified and 1 hit", exists, n, hits.Load())
	}
}
