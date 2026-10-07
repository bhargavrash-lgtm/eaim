package main

// B-301 / B-302: a suspended or revoked governed agent's open MCP session
// must stop dispatching, on every node; gateway 401/403 bodies are fixed.
//
// Real Postgres (throwaway database per test, testdb.NewThrowawayPool via
// newMainTestEnv), a real mcp.Handler + Dispatcher over httptest, and a
// counting downstream: "nothing dispatched" is asserted as zero downstream
// requests, not inferred.
//
// T1-T8 and the two condition tests (fail closed; dropped notification) live
// here; T9 (key revocation revokes its tokens) is in eami-api.

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/registry"
	"github.com/eami/gateway/internal/toolrouter"
	"github.com/eami/gateway/internal/workflow"
	policy "github.com/eami/policy"
)

// alwaysLive is the AgentLiveness stub for the pre-B-301 dispatcher tests.
type alwaysLive struct{}

func (alwaysLive) CheckLive(context.Context, string, string, string, string) (registry.Liveness, error) {
	return registry.Live, nil
}

const b301Issuer = "eami-gateway:primary"

type livenessEnv struct {
	env       *mainTestEnv
	agentID   uuid.UUID
	agentName string
	keyPath   string
	hits      atomic.Int64
	fwd       *proxy.Proxy
}

func newLivenessEnv(t *testing.T) *livenessEnv {
	t.Helper()
	le := &livenessEnv{env: newMainTestEnv(t), keyPath: filepath.Join(t.TempDir(), "gateway.key")}
	le.agentID, le.agentName = le.env.insertAgent(t)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		le.hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(downstream.Close)
	le.fwd = proxy.New(proxy.Config{DownstreamURL: downstream.URL}, downstream.Client())
	return le
}

type livenessNode struct {
	idm        *identity.Manager
	h          *mcp.Handler
	server     *httptest.Server
	dispatcher *Dispatcher
}

// newNode builds one gateway node (its own identity manager, handler and
// dispatcher) over the shared database and signing key. withListener starts
// the B-301 notification listener and waits until it is listening.
func (le *livenessEnv) newNode(t *testing.T, live AgentLiveness, action string, withListener bool) *livenessNode {
	t.Helper()
	pool := le.env.pool
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	auditWriter, err := audit.NewWriter(ctx, pool)
	if err != nil {
		t.Fatalf("audit.NewWriter: %v", err)
	}
	toolRouter := toolrouter.New(pool, nil)
	aiProviderRouter := aiprovider.New(pool, nil, map[string]aiprovider.Adapter{})
	approvalRouter := approval.New(pool, le.fwd, 5*time.Second, "", "", toolRouter, aiProviderRouter, nil)
	go approvalRouter.Run(ctx)
	d := NewDispatcher(toolRouter, aiProviderRouter, alwaysLicensedChecker{}, live,
		staticEvaluatorSource{ev: &fakeEvaluator{action: action}},
		auditWriter, episode.New(pool), approvalRouter, le.fwd, "", "", 5*time.Second)
	idm, err := identity.NewManagerWithDB(le.keyPath, 300, b301Issuer, pool)
	if err != nil {
		t.Fatalf("identity.NewManagerWithDB: %v", err)
	}
	reg := registry.New(pool)
	h := mcp.NewHandler(idm, reg, d.Dispatch, func(context.Context, string) ([]mcp.ToolDefinition, error) { return nil, nil })
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp/sse", h.ServeSSE)
	mux.HandleFunc("/v1/mcp/messages", h.ServeMessages)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if withListener {
		l := mcp.NewLivenessListener(pool, h, idm, reg)
		ready := make(chan struct{})
		var once sync.Once
		l.SetOnListen(func() { once.Do(func() { close(ready) }) })
		go l.Run(ctx)
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			t.Fatal("liveness listener never started listening")
		}
	}
	return &livenessNode{idm: idm, h: h, server: srv, dispatcher: d}
}

func (le *livenessEnv) token(t *testing.T, n *livenessNode, agentName string, orgID uuid.UUID) (string, string) {
	t.Helper()
	return le.tokenWithKey(t, n, agentName, orgID, "")
}

// tokenWithKey issues a token bound to the named agent's row (looked up, as
// HandleIssue does) and, when keyID is set, to that API key.
func (le *livenessEnv) tokenWithKey(t *testing.T, n *livenessNode, agentName string, orgID uuid.UUID, keyID string) (string, string) {
	t.Helper()
	var agentUUID string
	_ = le.env.pool.QueryRow(context.Background(), `SELECT id::text FROM gateway_agents WHERE name = $1 AND org_id = $2`, agentName, orgID).Scan(&agentUUID)
	resp, err := n.idm.Issue(identity.IssueRequest{AgentID: "agent:" + agentName, OrgID: orgID.String(),
		AgentUUID: agentUUID, APIKeyID: keyID, TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return resp.Token, resp.JTI
}

// stream is one open SSE connection, read on a goroutine.
type stream struct {
	sessionID string
	frames    chan sseFrame
	resp      *http.Response
}

func openStream(t *testing.T, n *livenessNode, token string) (*stream, int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, n.server.URL+"/v1/mcp/sse", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/mcp/sse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, resp.StatusCode, string(b)
	}
	t.Cleanup(func() { resp.Body.Close() })
	s := &stream{frames: make(chan sseFrame, 16), resp: resp}
	r := bufio.NewReader(resp.Body)
	go func() {
		defer close(s.frames)
		var f sseFrame
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				f.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.Data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if f.Event != "" {
					s.frames <- f
				}
				f = sseFrame{}
			}
		}
	}()
	first := s.next(t, 5*time.Second)
	if first.Event != "endpoint" {
		t.Fatalf("first frame = %+v, want endpoint", first)
	}
	s.sessionID = first.Data[strings.Index(first.Data, "sessionId=")+len("sessionId="):]
	return s, http.StatusOK, ""
}

func (s *stream) next(t *testing.T, timeout time.Duration) sseFrame {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			return sseFrame{Event: "EOF"}
		}
		return f
	case <-time.After(timeout):
		return sseFrame{Event: "TIMEOUT"}
	}
}

func postCall(t *testing.T, n *livenessNode, sessionID string, id int) int {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"tool_call","params":{"name":"probe.action","arguments":{}}}`
	resp, err := http.Post(n.server.URL+"/v1/mcp/messages?sessionId="+sessionID, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/mcp/messages: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// expectRefused asserts the reply to a call is the fixed "session ended"
// JSON-RPC error, then that the stream ends.
func expectRefused(t *testing.T, s *stream) {
	t.Helper()
	f := s.next(t, 5*time.Second)
	if f.Event != "message" || !strings.Contains(f.Data, mcp.MsgSessionEnded) || !strings.Contains(f.Data, "-32001") {
		t.Fatalf("reply = %+v, want the fixed session-ended error", f)
	}
	expectEnded(t, s, 5*time.Second)
}

func expectEnded(t *testing.T, s *stream, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		f := s.next(t, time.Until(deadline))
		if f.Event == "EOF" || (f.Event == "error" && strings.Contains(f.Data, "session ended")) {
			return
		}
		if f.Event == "TIMEOUT" {
			break
		}
	}
	t.Fatal("the SSE stream did not end")
}

func expectOK(t *testing.T, s *stream) {
	t.Helper()
	f := s.next(t, 5*time.Second)
	if f.Event != "message" || strings.Contains(f.Data, `"error"`) {
		t.Fatalf("reply = %+v, want a successful result", f)
	}
}

func (le *livenessEnv) setStatus(t *testing.T, id uuid.UUID, status string, notify bool) {
	t.Helper()
	if _, err := le.env.pool.Exec(context.Background(), `UPDATE gateway_agents SET status = $1 WHERE id = $2`, status, id); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if notify {
		if _, err := le.env.pool.Exec(context.Background(), `SELECT pg_notify('agent_status', $1)`, id.String()); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}
}

func (le *livenessEnv) deniedAuditRows(t *testing.T) int {
	t.Helper()
	var n int
	_ = le.env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE agent_id = $1 AND decision = 'denied'`, le.agentID).Scan(&n)
	return n
}

// T1: suspend (no notification at all) -> the next call on the already-open
// session is refused, audited as denied, nothing dispatched, session ended.
func TestB301_T1_SuspendRefusesNextCallOnOpenSession(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, code, _ := openStream(t, n, tok)
	if code != 200 {
		t.Fatalf("open: %d", code)
	}
	postCall(t, n, s.sessionID, 1)
	expectOK(t, s)
	if le.hits.Load() != 1 {
		t.Fatalf("baseline downstream hits = %d, want 1", le.hits.Load())
	}
	denied := le.deniedAuditRows(t)

	le.setStatus(t, le.agentID, "suspended", false)
	postCall(t, n, s.sessionID, 2)
	expectRefused(t, s)
	if le.hits.Load() != 1 {
		t.Fatalf("downstream hits after suspend = %d, want 1 (nothing dispatched)", le.hits.Load())
	}
	if le.deniedAuditRows(t) != denied+1 {
		t.Fatal("the refused call has no denied audit row")
	}
	if code := postCall0(t, n, s.sessionID); code != http.StatusNotFound {
		t.Fatalf("POST on the ended session = %d, want 404", code)
	}
}

func postCall0(t *testing.T, n *livenessNode, sessionID string) int {
	t.Helper()
	resp, err := http.Post(n.server.URL+"/v1/mcp/messages?sessionId="+sessionID, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tool_call","params":{"name":"probe.action","arguments":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// T2: suspend + agent_status notification ends the open stream with no call.
func TestB301_T2_NotificationClosesOpenStream(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	le.setStatus(t, le.agentID, "suspended", true)
	expectEnded(t, s, 5*time.Second)
}

// T3: a token revoked on node A ends node B's open session (notification)
// and refuses a new session on B (in-memory set).
func TestB301_T3_RevocationReachesSecondNode(t *testing.T) {
	le := newLivenessEnv(t)
	a := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	b := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	tok, jti := le.token(t, a, le.agentName, le.env.orgID)
	s, code, _ := openStream(t, b, tok)
	if code != 200 {
		t.Fatalf("open on B: %d", code)
	}
	if err := a.idm.Revoke(jti, le.agentID.String()); err != nil {
		t.Fatalf("revoke on A: %v", err)
	}
	expectEnded(t, s, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !b.idm.IsRevoked(jti) {
		time.Sleep(50 * time.Millisecond)
	}
	if _, code, body := openStream(t, b, tok); code != http.StatusUnauthorized || body != identity.MsgUnauthorized+"\n" {
		t.Fatalf("new session on B with the revoked token = %d %q, want 401 fixed body", code, body)
	}
}

// T3b / condition 2 (token side): node B gets NO notification (no listener);
// its open session's next call is still refused by the per-call check.
func TestB301_T3b_SecondNodeWithoutNotification_NextCallRefused(t *testing.T) {
	le := newLivenessEnv(t)
	a := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	b := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, jti := le.token(t, a, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, b, tok)
	if err := a.idm.Revoke(jti, le.agentID.String()); err != nil {
		t.Fatalf("revoke on A: %v", err)
	}
	if b.idm.IsRevoked(jti) {
		t.Fatal("test setup: node B must not know about the revocation")
	}
	postCall(t, b, s.sessionID, 1)
	expectRefused(t, s)
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits = %d, want 0", le.hits.Load())
	}
}

// Condition 2 (status side): the listener is running but the suspension's
// notification never comes; the session stays open, and its next call is
// still refused.
func TestB301_NotificationDropped_NextCallStillRefused(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	le.setStatus(t, le.agentID, "suspended", false) // notification dropped
	if f := s.next(t, 1500*time.Millisecond); f.Event != "TIMEOUT" {
		t.Fatalf("with no notification the session should still be open, got %+v", f)
	}
	postCall(t, n, s.sessionID, 1)
	expectRefused(t, s)
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits = %d, want 0", le.hits.Load())
	}
}

// T4: the check is in Dispatch itself, which every workflow step calls (the
// executor passes each step's ActionContext to Dispatch). A step after the
// suspension is refused and not dispatched.
func TestB301_T4_DispatchRefusesWorkflowStepAfterSuspend(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	ac := mcp.ActionContext{
		AgentID: "agent:" + le.agentName, AgentUUID: le.agentID.String(), AgentName: le.agentName,
		OrgID: le.env.orgID.String(), Tool: "probe", Action: "action", Parameters: map[string]any{},
		WorkflowRunID: uuid.New().String(), SessionID: "workflow-run-test", ReceivedAt: time.Now().UTC(),
	}
	if _, err := n.dispatcher.Dispatch(context.Background(), ac); err != nil {
		t.Fatalf("step 1: %v", err)
	}
	le.setStatus(t, le.agentID, "suspended", false)
	ac.ReceivedAt = time.Now().UTC()
	if _, err := n.dispatcher.Dispatch(context.Background(), ac); !errors.Is(err, mcp.ErrNotLive) {
		t.Fatalf("step 2 after suspend: err = %v, want ErrNotLive", err)
	}
	if le.hits.Load() != 1 {
		t.Fatalf("downstream hits = %d, want 1 (step 2 not dispatched)", le.hits.Load())
	}
}

// T5: a session whose token has expired dispatches nothing.
func TestB301_T5_ExpiredTokenSessionDispatchesNothing(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok := shortToken(t, le.keyPath, le.agentName, le.agentID, le.env.orgID, 2*time.Second)
	s, code, body := openStream(t, n, tok)
	if code != 200 {
		t.Fatalf("open with a 2s token: %d %s", code, body)
	}
	time.Sleep(3 * time.Second)
	status := postCall0(t, n, s.sessionID)
	time.Sleep(500 * time.Millisecond)
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits after expiry = %d, want 0 (POST status %d)", le.hits.Load(), status)
	}
}

// shortToken signs a token with the node's key that expires in ttl (the
// issuer clamps real tokens to at least 60 s).
func shortToken(t *testing.T, keyPath, agentName string, agentID, orgID uuid.UUID, ttl time.Duration) string {
	t.Helper()
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	pk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claims := identity.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "agent:" + agentName, Issuer: b301Issuer, Audience: jwt.ClaimStrings{"eami-gateway"},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)), ID: "00000000deadbeef",
		},
		OrgID:     orgID.String(),
		AgentUUID: agentID.String(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(pk)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// T6: no over-blocking. After a suspend, reactivation lets a new session work.
func TestB301_T6_ReactivatedAgentWorksAgain(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	le.setStatus(t, le.agentID, "suspended", false)
	postCall(t, n, s.sessionID, 1)
	expectRefused(t, s)
	le.setStatus(t, le.agentID, "active", false)
	s2, code, _ := openStream(t, n, tok)
	if code != 200 {
		t.Fatalf("new session after reactivation: %d", code)
	}
	postCall(t, n, s2.sessionID, 2)
	expectOK(t, s2)
}

// T7: two orgs with identically named governed agents. Suspending org A's
// closes only org A's session (matched by UUID, never by name).
func TestB301_T7_SameNameOtherOrgUnaffected(t *testing.T) {
	le := newLivenessEnv(t)
	ctx := context.Background()
	orgB := uuid.New()
	if _, err := le.env.pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $2)`, orgB, "b301-org-b-"+orgB.String()[:8]); err != nil {
		t.Fatal(err)
	}
	agentB := uuid.New()
	if _, err := le.env.pool.Exec(ctx, `INSERT INTO gateway_agents (id, org_id, name, model, owner, scope) VALUES ($1, $2, $3, 'm', 'o', 's')`,
		agentB, orgB, le.agentName); err != nil {
		t.Fatal(err)
	}
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	tokA, _ := le.token(t, n, le.agentName, le.env.orgID)
	tokB, _ := le.token(t, n, le.agentName, orgB)
	sA, _, _ := openStream(t, n, tokA)
	sB, _, _ := openStream(t, n, tokB)
	le.setStatus(t, le.agentID, "suspended", true)
	expectEnded(t, sA, 5*time.Second)
	postCall(t, n, sB.sessionID, 1)
	expectOK(t, sB)
}

// T8: malformed or hostile notification payloads are ignored, and the
// listener keeps working afterwards.
func TestB301_T8_HostileNotificationsIgnored(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, true)
	tok, jti := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	ctx := context.Background()
	hostile := []struct{ ch, payload string }{
		{"agent_status", "not-a-uuid"},
		{"agent_status", ""},
		{"token_revoked", "zz' OR 1=1--"},
		{"token_revoked", strings.Repeat("a", 15)},
		{"token_revoked", strings.Repeat("A", 16)},
	}
	// Well-formed but forged: the agent is active and the token unrevoked.
	hostile = append(hostile,
		struct{ ch, payload string }{"agent_status", le.agentID.String()},
		struct{ ch, payload string }{"token_revoked", jti})
	for _, h := range hostile {
		if _, err := le.env.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, h.ch, h.payload); err != nil {
			t.Fatal(err)
		}
	}
	// Barrier: notifications arrive in order, so once a real revocation of
	// a second session's token has closed that session, every hostile one
	// above has been handled.
	barrierTok, barrierJTI := le.token(t, n, le.agentName, le.env.orgID)
	barrier, _, _ := openStream(t, n, barrierTok)
	if err := n.idm.Revoke(barrierJTI, le.agentID.String()); err != nil {
		t.Fatal(err)
	}
	expectEnded(t, barrier, 5*time.Second)
	for _, h := range hostile {
		if h.ch == "token_revoked" && n.idm.IsRevoked(h.payload) {
			t.Fatalf("hostile payload %q was marked revoked", h.payload)
		}
	}
	postCall(t, n, s.sessionID, 1)
	expectOK(t, s)
	// Still listening, and a genuine revocation of this session's token is
	// applied.
	if err := n.idm.Revoke(jti, le.agentID.String()); err != nil {
		t.Fatal(err)
	}
	expectEnded(t, s, 5*time.Second)
}

// Condition 1: the per-call check fails closed when the database is
// unreachable (here: the liveness checker's pool is closed).
func TestB301_FailClosed_DatabaseUnreachable(t *testing.T) {
	le := newLivenessEnv(t)
	dead := testdbClosedRegistry(t)
	n := le.newNode(t, dead, policy.ActionAllow, false)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	postCall(t, n, s.sessionID, 1)
	expectRefused(t, s)
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits with the database unreachable = %d, want 0", le.hits.Load())
	}
	// No checker at all also refuses.
	n2 := le.newNode(t, nil, policy.ActionAllow, false)
	s2, _, _ := openStream(t, n2, tok)
	postCall(t, n2, s2.sessionID, 2)
	expectRefused(t, s2)
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits with no liveness checker = %d, want 0", le.hits.Load())
	}
}

// testdbClosedRegistry returns a registry whose pool is closed: every query
// fails, as when the database is unreachable.
func testdbClosedRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	other := newMainTestEnv(t)
	reg := registry.New(other.pool)
	other.pool.Close()
	return reg
}

// Resume-time re-check: an escalated call approved after its governed agent
// was suspended is not executed, and the approval records agent_not_active.
func TestB301_ApprovedCallNotExecutedAfterSuspend(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionEscalate, false)
	ac := mcp.ActionContext{
		AgentID: "agent:" + le.agentName, AgentUUID: le.agentID.String(), AgentName: le.agentName,
		OrgID: le.env.orgID.String(), Tool: "probe", Action: "action", Parameters: map[string]any{},
		SessionID: "b301-escalation", ReceivedAt: time.Now().UTC(),
	}
	done := make(chan error, 1)
	go func() { _, err := n.dispatcher.Dispatch(context.Background(), ac); done <- err }()
	approvalID := waitForPendingApproval(t, le.env.pool, le.env.orgID, 10*time.Second)
	le.setStatus(t, le.agentID, "suspended", false)
	decideTestApproval(t, le.env.pool, approvalID, "approved")
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("dispatch never returned after the approval")
	}
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits = %d, want 0 (approved call must not run for a suspended agent)", le.hits.Load())
	}
	var outcome *string
	_ = le.env.pool.QueryRow(context.Background(), `SELECT resume_outcome FROM approval_requests WHERE id = $1`, approvalID).Scan(&outcome)
	if outcome == nil || *outcome != "agent_not_active" {
		t.Fatalf("resume_outcome = %v, want agent_not_active", outcome)
	}
}

// B-302: 401 and 403 bodies are fixed. Unknown, other-org and suspended
// governed agents get byte-identical 403s; malformed, revoked and
// pre-cutover tokens get byte-identical 401s; nothing echoes a JTI, name or
// status. Covered for the MCP SSE route, the workflow-run route and the
// episode routes.
func TestB302_FixedUnauthorizedAndForbiddenBodies(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	reg := registry.New(le.env.pool)
	wf := workflow.NewHTTPHandler(n.idm, reg, nil)
	ep := episode.NewHTTPHandler(nil, n.idm, reg, "unused-service-key")
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp/sse", n.h.ServeSSE)
	mux.HandleFunc("POST /v1/gateway/workflows/{workflowId}/run", wf.HandleRun)
	mux.HandleFunc("GET /v1/gateway/episodes", ep.ListEpisodes)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Tokens: unknown agent, suspended agent, revoked token, pre-cutover.
	unknownTok, _ := le.token(t, n, "no-such-agent", le.env.orgID)
	suspended := uuid.New()
	if _, err := le.env.pool.Exec(context.Background(), `INSERT INTO gateway_agents (id, org_id, name, model, owner, scope, status) VALUES ($1,$2,'b302-suspended','m','o','s','suspended')`, suspended, le.env.orgID); err != nil {
		t.Fatal(err)
	}
	suspendedTok, _ := le.token(t, n, "b302-suspended", le.env.orgID)
	revokedTok, revokedJTI := le.token(t, n, le.agentName, le.env.orgID)
	if err := n.idm.Revoke(revokedJTI, le.agentID.String()); err != nil {
		t.Fatal(err)
	}
	preCutover, _ := n.idm.Issue(identity.IssueRequest{AgentID: "agent:" + le.agentName, TTLSeconds: 300})

	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/mcp/sse"},
		{http.MethodPost, "/v1/gateway/workflows/" + uuid.New().String() + "/run"},
		{http.MethodGet, "/v1/gateway/episodes"},
	}
	do := func(method, path, tok string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, rt := range routes {
		for label, tok := range map[string]string{"unknown agent": unknownTok, "suspended agent": suspendedTok} {
			code, body := do(rt.method, rt.path, tok)
			if code != http.StatusForbidden || body != identity.MsgForbidden+"\n" {
				t.Errorf("%s %s (%s) = %d %q, want 403 %q", rt.method, rt.path, label, code, body, identity.MsgForbidden)
			}
		}
		for label, tok := range map[string]string{"garbage": "not.a.jwt", "revoked": revokedTok, "pre-cutover": preCutover.Token, "none": ""} {
			code, body := do(rt.method, rt.path, tok)
			if code != http.StatusUnauthorized || body != identity.MsgUnauthorized+"\n" {
				t.Errorf("%s %s (%s) = %d %q, want 401 %q", rt.method, rt.path, label, code, body, identity.MsgUnauthorized)
			}
			if strings.Contains(body, revokedJTI) || strings.Contains(body, le.agentName) {
				t.Errorf("%s %s (%s) body leaks a JTI or agent name", rt.method, rt.path, label)
			}
		}
	}
}

// B-301 review High 1: a token is bound to the governed agent ROW. Deleting
// the agent and re-creating one with the same name must not bring its old
// tokens back, at session open or on an already-open session.
func TestB301_DeleteRecreate_OldTokenRefused(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, jti := le.token(t, n, le.agentName, le.env.orgID)
	open, _, _ := openStream(t, n, tok)
	if err := n.idm.Revoke(jti, le.agentID.String()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Delete (the FK cascade also deletes the revocation row) and re-create.
	if _, err := le.env.pool.Exec(ctx, `DELETE FROM gateway_agents WHERE id = $1`, le.agentID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if _, err := le.env.pool.Exec(ctx, `INSERT INTO gateway_agents (id, org_id, name, model, owner, scope) VALUES ($1,$2,$3,'m','o','s')`,
		uuid.New(), le.env.orgID, le.agentName); err != nil {
		t.Fatal(err)
	}
	postCall(t, n, open.sessionID, 1)
	expectRefused(t, open)
	// A fresh node (empty in-memory revoked set): the old token is unbound
	// to the new row and refused at session open.
	n2 := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	if _, code, body := openStream(t, n2, tok); code != http.StatusForbidden || body != identity.MsgForbidden+"\n" {
		t.Fatalf("old token against the re-created agent = %d %q, want 403 (same as an unknown agent)", code, body)
	}
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits = %d, want 0", le.hits.Load())
	}
}

// B-301 review High 2: a token carries its issuing key's id, and the
// per-call check refuses it once the key is revoked, even with NO
// ai_token_events row (the asynchronous issuance record was lost or late).
func TestB301_RevokedKey_TokensRefusedWithoutEvents(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	keyID := uuid.New()
	if _, err := le.env.pool.Exec(context.Background(), `INSERT INTO api_keys (id, org_id, name, key_hash, prefix, agent_id) VALUES ($1,$2,'b301-key',$3,'eami_',$4)`,
		keyID, le.env.orgID, "hash-"+keyID.String(), le.agentID); err != nil {
		t.Fatal(err)
	}
	tok, _ := le.tokenWithKey(t, n, le.agentName, le.env.orgID, keyID.String())
	s, _, _ := openStream(t, n, tok)
	postCall(t, n, s.sessionID, 1)
	expectOK(t, s)
	if _, err := le.env.pool.Exec(context.Background(), `UPDATE api_keys SET revoked = TRUE WHERE id = $1`, keyID); err != nil {
		t.Fatal(err)
	}
	postCall(t, n, s.sessionID, 2)
	expectRefused(t, s)
	if _, code, _ := openStream(t, n, tok); code != http.StatusUnauthorized {
		t.Fatalf("new session with a revoked key's token = %d, want 401", code)
	}
	if le.hits.Load() != 1 {
		t.Fatalf("downstream hits = %d, want 1", le.hits.Load())
	}
}

// B-301 review: tools/list on an open session is refused once the governed
// agent is suspended (no notification), and the session ends.
func TestB301_ToolsListRefusedAfterSuspend(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	le.setStatus(t, le.agentID, "suspended", false)
	resp, err := http.Post(n.server.URL+"/v1/mcp/messages?sessionId="+s.sessionID, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	expectRefused(t, s)
}

// B-301 review: episode content is refused for a token revoked in the
// database even when this node's in-memory set doesn't know it.
func TestB301_EpisodeReadRefusedForRevokedToken(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, jti := le.token(t, n, le.agentName, le.env.orgID)
	reg := registry.New(le.env.pool)
	ep := episode.NewHTTPHandler(episode.NewReader(le.env.pool), n.idm, reg, "unused-service-key")
	srv := httptest.NewServer(http.HandlerFunc(ep.ListEpisodes))
	t.Cleanup(srv.Close)
	get := func() int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("episode read with a live token = %d, want 200", code)
	}
	// Revoke in the database only (as another node would); this node's
	// memory doesn't know.
	if _, err := le.env.pool.Exec(context.Background(), `INSERT INTO revoked_ai_tokens (jti, agent_id) VALUES ($1, $2)`, jti, le.agentID); err != nil {
		t.Fatal(err)
	}
	if n.idm.IsRevoked(jti) {
		t.Fatal("test setup: this node must not know about the revocation")
	}
	if code := get(); code != http.StatusUnauthorized {
		t.Fatalf("episode read with a revoked token = %d, want 401", code)
	}
}

// The in-memory pre-check: a token this node knows is revoked is refused
// before Dispatch (no audit row, nothing dispatched) even though the
// database row is absent.
func TestB301_PreCheck_LocallyRevokedTokenRefusedBeforeDispatch(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, jti := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	before := le.deniedAuditRows(t)
	n.idm.MarkRevoked(jti) // memory only
	postCall(t, n, s.sessionID, 1)
	expectRefused(t, s)
	if le.hits.Load() != 0 || le.deniedAuditRows(t) != before {
		t.Fatalf("pre-check let the call reach Dispatch: hits=%d audit=%d->%d", le.hits.Load(), before, le.deniedAuditRows(t))
	}
}

// Reconnect catch-up: a session whose governed agent was suspended while
// no listener was running is closed as soon as a listener connects.
func TestB301_ReconnectCatchUpClosesStaleSession(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	tok, _ := le.token(t, n, le.agentName, le.env.orgID)
	s, _, _ := openStream(t, n, tok)
	le.setStatus(t, le.agentID, "suspended", false)
	l := mcp.NewLivenessListener(le.env.pool, n.h, n.idm, registry.New(le.env.pool))
	ready := make(chan struct{})
	var once sync.Once
	l.SetOnListen(func() { once.Do(func() { close(ready) }) })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go l.Run(ctx)
	<-ready
	expectEnded(t, s, 5*time.Second)
}

// A token without the agent_uuid claim (minted before this change) is
// refused at session open: a hard cutover.
func TestB301_UnboundTokenRefused(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionAllow, false)
	resp, err := n.idm.Issue(identity.IssueRequest{AgentID: "agent:" + le.agentName, OrgID: le.env.orgID.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, code, body := openStream(t, n, resp.Token); code != http.StatusForbidden || body != identity.MsgForbidden+"\n" {
		t.Fatalf("unbound token = %d %q, want 403", code, body)
	}
	// The workflow-run and episode routes refuse it too.
	reg := registry.New(le.env.pool)
	wf := workflow.NewHTTPHandler(n.idm, reg, nil)
	ep := episode.NewHTTPHandler(episode.NewReader(le.env.pool), n.idm, reg, "unused-service-key")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateway/workflows/{workflowId}/run", wf.HandleRun)
	mux.HandleFunc("GET /v1/gateway/episodes", ep.ListEpisodes)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/v1/gateway/workflows/" + uuid.New().String() + "/run"},
		{http.MethodGet, "/v1/gateway/episodes"},
	} {
		req, _ := http.NewRequest(rt.method, srv.URL+rt.path, nil)
		req.Header.Set("Authorization", "Bearer "+resp.Token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden || string(b) != identity.MsgForbidden+"\n" {
			t.Errorf("%s %s with an unbound token = %d %q, want 403", rt.method, rt.path, r.StatusCode, b)
		}
	}
}

// Resume-time re-check, token side: the call's token is revoked while it
// waits for approval; the approved call is not executed.
func TestB301_ApprovedCallNotExecutedAfterTokenRevoked(t *testing.T) {
	le := newLivenessEnv(t)
	n := le.newNode(t, registry.New(le.env.pool), policy.ActionEscalate, false)
	_, jti := le.token(t, n, le.agentName, le.env.orgID)
	ac := mcp.ActionContext{
		AgentID: "agent:" + le.agentName, AgentUUID: le.agentID.String(), AgentName: le.agentName, TokenID: jti,
		OrgID: le.env.orgID.String(), Tool: "probe", Action: "action", Parameters: map[string]any{},
		SessionID: "b301-escalation-token", ReceivedAt: time.Now().UTC(),
	}
	done := make(chan error, 1)
	go func() { _, err := n.dispatcher.Dispatch(context.Background(), ac); done <- err }()
	approvalID := waitForPendingApproval(t, le.env.pool, le.env.orgID, 10*time.Second)
	if err := n.idm.Revoke(jti, le.agentID.String()); err != nil {
		t.Fatal(err)
	}
	decideTestApproval(t, le.env.pool, approvalID, "approved")
	var err error
	select {
	case err = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("dispatch never returned after the approval")
	}
	if !errors.Is(err, mcp.ErrNotLive) {
		t.Fatalf("Dispatch err = %v, want ErrNotLive (session ends)", err)
	}
	if le.hits.Load() != 0 {
		t.Fatalf("downstream hits = %d, want 0", le.hits.Load())
	}
}
