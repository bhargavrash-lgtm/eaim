package api_test

// B-301 T9 (founder decision 2026-10-06): revoking a governed agent's API key
// revokes every live token issued with it, in one transaction with the key
// change and its admin audit event. If the token revocation can't be written,
// the key revocation fails with a stable error and nothing changes.
//
// Throwaway database per test (newAdminAuditEnv, admin_audit_test.go).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type keyFixture struct {
	agent, otherKey, key                                          uuid.UUID
	jtiNow, jti1h, jtiOld, jtiOtherKey, jtiGoneAgent, jtiCrossOrg string
}

func seedKeyFixture(t *testing.T, e *adminAuditEnv, orgA, orgB uuid.UUID) keyFixture {
	t.Helper()
	ctx := context.Background()
	f := keyFixture{agent: uuid.New(), key: uuid.New(), otherKey: uuid.New(),
		jtiNow: "1111111111111111", jti1h: "2222222222222222", jtiOld: "3333333333333333",
		jtiOtherKey: "4444444444444444", jtiGoneAgent: "5555555555555555", jtiCrossOrg: "6666666666666666"}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql[:40], err)
		}
	}
	mustExec(`INSERT INTO gateway_agents (id, org_id, name, model, owner, scope) VALUES ($1,$2,'b301-t9-agent','m','o','s')`, f.agent, orgA)
	for _, k := range []uuid.UUID{f.key, f.otherKey} {
		mustExec(`INSERT INTO api_keys (id, org_id, name, key_hash, prefix, agent_id) VALUES ($1,$2,$3,$4,'eami_',$5)`,
			k, orgA, "k-"+k.String()[:8], "hash-"+k.String(), f.agent)
	}
	ev := func(org, key uuid.UUID, agent uuid.UUID, jti string, age time.Duration) {
		mustExec(`INSERT INTO ai_token_events (org_id, agent_id, agent_name, api_key_id, jti, event_type, created_at)
			VALUES ($1,$2,'b301-t9-agent',$3,$4,'issued', now() - make_interval(secs => $5))`, org, agent, key, jti, age.Seconds())
	}
	ev(orgA, f.key, f.agent, f.jtiNow, time.Second)
	ev(orgA, f.key, f.agent, f.jti1h, time.Hour)
	ev(orgA, f.key, f.agent, f.jtiOld, 5*time.Hour)           // older than the 4 h max lifetime
	ev(orgA, f.otherKey, f.agent, f.jtiOtherKey, time.Second) // another key
	ev(orgA, f.key, uuid.New(), f.jtiGoneAgent, time.Second)  // governed agent no longer exists
	ev(orgB, f.key, f.agent, f.jtiCrossOrg, time.Second)      // same key id recorded under another org
	return f
}

func (e *adminAuditEnv) revokedJTIs(t *testing.T) map[string]bool {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT jti FROM revoked_ai_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var j string
		_ = rows.Scan(&j)
		out[j] = true
	}
	return out
}

func (e *adminAuditEnv) del(t *testing.T, path, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.url+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestB301_T9_KeyRevocationRevokesItsLiveTokens(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	admin := e.seedUser(t, orgA, "admin")
	f := seedKeyFixture(t, e, orgA, orgB)
	ctx := context.Background()

	// Listen for token_revoked before the request: notifications are sent
	// on commit.
	conn, err := pgx.Connect(ctx, e.conn.dbURL(e.dbName))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "LISTEN token_revoked"); err != nil {
		t.Fatal(err)
	}

	code, body := e.del(t, "/v1/auth/api-keys/"+f.key.String(), e.token(t, admin, orgA, "admin"))
	if code != http.StatusNoContent {
		t.Fatalf("revoke key: %d %s", code, body)
	}

	got := e.revokedJTIs(t)
	for _, j := range []string{f.jtiNow, f.jti1h} {
		if !got[j] {
			t.Errorf("live token %s was not revoked", j)
		}
	}
	for _, j := range []string{f.jtiOld, f.jtiOtherKey, f.jtiGoneAgent, f.jtiCrossOrg} {
		if got[j] {
			t.Errorf("token %s must not be revoked", j)
		}
	}
	var keyRevoked, otherRevoked bool
	_ = e.pool.QueryRow(ctx, `SELECT revoked FROM api_keys WHERE id=$1`, f.key).Scan(&keyRevoked)
	_ = e.pool.QueryRow(ctx, `SELECT revoked FROM api_keys WHERE id=$1`, f.otherKey).Scan(&otherRevoked)
	if !keyRevoked || otherRevoked {
		t.Fatalf("key revoked=%v other key revoked=%v", keyRevoked, otherRevoked)
	}
	var events int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM ai_token_events WHERE event_type='revoked' AND api_key_id=$1`, f.key).Scan(&events)
	if events != 2 {
		t.Errorf("ai_token_events revoked rows = %d, want 2", events)
	}

	// Admin audit trail: one api_key.revoked event with the agent and count.
	var action, target, summary string
	if err := e.pool.QueryRow(ctx, `SELECT action, target_id, summary FROM admin_audit_events WHERE org_id=$1`, orgA).Scan(&action, &target, &summary); err != nil {
		t.Fatalf("admin audit event: %v", err)
	}
	var sum struct {
		Refs struct {
			AgentID string `json:"agent_id"`
			Count   int    `json:"count"`
		} `json:"refs"`
	}
	_ = json.Unmarshal([]byte(summary), &sum)
	if action != "api_key.revoked" || target != f.key.String() || sum.Refs.Count != 2 || sum.Refs.AgentID != f.agent.String() {
		t.Errorf("audit event = %s %s %s", action, target, summary)
	}
	if r := e.verify(t, orgA); !r.Valid || r.Checked != 1 {
		t.Errorf("admin audit chain: %+v", r)
	}

	// token_revoked notifications for exactly the two revoked tokens.
	seen := map[string]bool{}
	for len(seen) < 2 {
		wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		n, err := conn.WaitForNotification(wctx)
		cancel()
		if err != nil {
			break
		}
		seen[n.Payload] = true
	}
	if !seen[f.jtiNow] || !seen[f.jti1h] || len(seen) != 2 {
		t.Errorf("token_revoked notifications = %v", seen)
	}
}

func TestB301_T9_KeyRevocationFailsClosed(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	admin := e.seedUser(t, orgA, "admin")
	f := seedKeyFixture(t, e, orgA, orgB)
	ctx := context.Background()
	tok := e.token(t, admin, orgA, "admin")

	// Unknown key and another org's key: 404, nothing changes.
	if code, _ := e.del(t, "/v1/auth/api-keys/"+uuid.New().String(), tok); code != http.StatusNotFound {
		t.Errorf("unknown key: %d, want 404", code)
	}
	adminB := e.seedUser(t, orgB, "admin")
	if code, _ := e.del(t, "/v1/auth/api-keys/"+f.key.String(), e.token(t, adminB, orgB, "admin")); code != http.StatusNotFound {
		t.Errorf("another org's key: %d, want 404", code)
	}

	// Make the token revocation impossible to write.
	if _, err := e.pool.Exec(ctx, `
		CREATE FUNCTION b301_block() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'blocked by test'; END; $$ LANGUAGE plpgsql;
		CREATE TRIGGER b301_block BEFORE INSERT ON revoked_ai_tokens FOR EACH ROW EXECUTE FUNCTION b301_block();`); err != nil {
		t.Fatal(err)
	}
	code, body := e.del(t, "/v1/auth/api-keys/"+f.key.String(), tok)
	if code != http.StatusInternalServerError || !strings.Contains(body, `"code":"key_revocation_failed"`) ||
		strings.Contains(body, "blocked by test") || strings.Contains(body, "SQLSTATE") {
		t.Fatalf("failed token revocation: %d %s, want 500 key_revocation_failed with no raw error", code, body)
	}
	var keyRevoked bool
	_ = e.pool.QueryRow(ctx, `SELECT revoked FROM api_keys WHERE id=$1`, f.key).Scan(&keyRevoked)
	var audit, revokedEvents int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events`).Scan(&audit)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM ai_token_events WHERE event_type='revoked'`).Scan(&revokedEvents)
	if keyRevoked || audit != 0 || revokedEvents != 0 || len(e.revokedJTIs(t)) != 0 {
		t.Fatalf("partial revocation committed: key=%v audit=%d events=%d", keyRevoked, audit, revokedEvents)
	}
}
