package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// B-238: the Slack webhook routes let an org admin make eami-api POST to
// loopback, compose-internal hosts and the cloud metadata IP, and the
// test route's error text told open ports from closed ones. These tests
// reproduce that attack against both layers separately:
//   - save time: PUT /v1/settings/notifications rejects every internal or
//     non-https URL with one fixed body, and stores nothing;
//   - send time: with the URL planted directly in the DB (a row saved
//     before validation existed), POST .../test never reaches the target
//     and returns one identical body for an open port, a closed port, the
//     metadata IP and an unresolvable host -- the oracle is closed.

type webhookSSRFEnv struct {
	pool  *pgxpool.Pool
	orgID uuid.UUID
	token string
}

func newWebhookSSRFEnv(t *testing.T) *webhookSSRFEnv {
	t.Helper()
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

	orgID, userID := uuid.New(), uuid.New()
	slug := "b238-api-" + orgID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $2)`, orgID, slug); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	// users and notification_config cascade from orgs.
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })
	email := "admin-" + userID.String()[:8] + "@b238.test"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'admin')`, userID, orgID, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return &webhookSSRFEnv{pool: pool, orgID: orgID, token: webhookSSRFToken(t, userID, orgID, email)}
}

var webhookSSRFAuth *auth.Service

func webhookSSRFToken(t *testing.T, userID, orgID uuid.UUID, email string) string {
	t.Helper()
	if webhookSSRFAuth == nil {
		a, err := auth.NewService("", time.Hour, 30*24*time.Hour)
		if err != nil {
			t.Fatalf("auth.NewService: %v", err)
		}
		webhookSSRFAuth = a
	}
	tok, _, err := webhookSSRFAuth.IssueAccessToken(userID, orgID, email, "admin")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// server returns a full HTTP server; dialOverride nil means production
// (the netguard guard).
func (e *webhookSSRFEnv) server(t *testing.T, dialOverride dialContextFunc) *httptest.Server {
	t.Helper()
	s := NewServer(store.New(e.pool), webhookSSRFAuth, nil, nil)
	s.toolDialOverride = dialOverride
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func (e *webhookSSRFEnv) call(t *testing.T, ts *httptest.Server, method, path string, body any) (int, string) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *webhookSSRFEnv) plant(t *testing.T, url string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO notification_config (org_id, slack_enabled, slack_webhook_url) VALUES ($1, TRUE, $2)
		 ON CONFLICT (org_id) DO UPDATE SET slack_enabled = TRUE, slack_webhook_url = EXCLUDED.slack_webhook_url`, e.orgID, url); err != nil {
		t.Fatalf("plant webhook url: %v", err)
	}
}

func (e *webhookSSRFEnv) storedURL(t *testing.T) string {
	t.Helper()
	var u *string
	err := e.pool.QueryRow(context.Background(), `SELECT slack_webhook_url FROM notification_config WHERE org_id = $1`, e.orgID).Scan(&u)
	if err != nil || u == nil {
		return ""
	}
	return *u
}

// internalServer is a loopback HTTP server standing in for an internal
// service; its counter proves whether any request reached it.
func internalServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestNotificationWebhook_SaveRejectsInternalTargets_RealDB(t *testing.T) {
	env := newWebhookSSRFEnv(t)
	ts := env.server(t, nil)
	const want = "{\"code\":\"invalid_webhook_url\",\"message\":\"webhook URL must be a public https URL\"}\n"
	for _, url := range []string{
		// the exact URLs from the live-confirmed attack
		"http://127.0.0.1:8081/health", "http://127.0.0.1:1/", "http://postgres:5432/",
		"http://eami-gateway:8080/health", "http://169.254.169.254/latest/meta-data/",
		// their https forms, so the scheme rule isn't the only thing tested
		"https://127.0.0.1:8081/health", "https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5/", "https://[::1]/", "https://localhost/", "https://does-not-exist.invalid/",
	} {
		status, body := env.call(t, ts, http.MethodPut, "/v1/settings/notifications", map[string]any{"slack_enabled": true, "slack_webhook_url": url})
		if status != http.StatusBadRequest || body != want {
			t.Fatalf("save %s: %d %q, want 400 %q", url, status, body, want)
		}
		if got := env.storedURL(t); got != "" {
			t.Fatalf("save %s was rejected but stored %q", url, got)
		}
	}
	// A public https URL still saves; clearing with "" still works.
	if status, body := env.call(t, ts, http.MethodPut, "/v1/settings/notifications", map[string]any{"slack_enabled": true, "slack_webhook_url": "https://93.184.216.34/services/T/B/x"}); status != http.StatusOK {
		t.Fatalf("public https webhook save: %d %s", status, body)
	}
	if status, body := env.call(t, ts, http.MethodPut, "/v1/settings/notifications", map[string]any{"slack_webhook_url": ""}); status != http.StatusOK {
		t.Fatalf("clearing webhook: %d %s", status, body)
	}
}

func TestNotificationWebhook_TestSendBlockedAndUniform_RealDB(t *testing.T) {
	env := newWebhookSSRFEnv(t)
	ts := env.server(t, nil)
	open, hits := internalServer(t)
	const want = "{\"sent\":false,\"reason\":\"webhook_delivery_failed\"}\n"
	for _, url := range []string{
		open.URL,              // open HTTP port on loopback
		"http://127.0.0.1:1/", // closed port
		"http://169.254.169.254/latest/meta-data/",
		"http://postgres:5432/",
		"https://does-not-exist.invalid/", // unresolvable
	} {
		env.plant(t, url)
		status, body := env.call(t, ts, http.MethodPost, "/v1/settings/notifications/test", map[string]any{"channel": "slack"})
		if status != http.StatusOK || body != want {
			t.Fatalf("test send to %s: %d %q, want 200 %q (identical for every target -- no port-scan oracle)", url, status, body, want)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("REGRESSION B-238: the test route delivered %d request(s) to a loopback server", n)
	}
}

// Control for the test above: with the guard swapped for an unrestricted
// dialer, the same route really delivers (so "0 hits" above is the guard,
// not an unreachable server), and a redirect is still not followed.
func TestNotificationWebhook_UnguardedControlDelivers_RealDB(t *testing.T) {
	env := newWebhookSSRFEnv(t)
	ts := env.server(t, unrestrictedDial)
	open, hits := internalServer(t)
	env.plant(t, open.URL)
	if status, body := env.call(t, ts, http.MethodPost, "/v1/settings/notifications/test", map[string]any{"channel": "slack"}); status != http.StatusOK || body != "{\"sent\":true}\n" || hits.Load() != 1 {
		t.Fatalf("unguarded send: %d %q hits=%d, want sent:true and 1 hit", status, body, hits.Load())
	}
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, open.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	env.plant(t, redirector.URL)
	status, body := env.call(t, ts, http.MethodPost, "/v1/settings/notifications/test", map[string]any{"channel": "slack"})
	if status != http.StatusOK || body != "{\"sent\":false,\"reason\":\"webhook_delivery_failed\"}\n" || hits.Load() != 1 {
		t.Fatalf("redirect: %d %q hits=%d, want delivery_failed and the redirect target not contacted", status, body, hits.Load())
	}
}
