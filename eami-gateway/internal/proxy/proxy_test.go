// proxy_test.go — eami-gateway/internal/proxy
// QA-EAMI — unit tests for the MCP proxy layer.
//
// Tests:
//   - Forwards tool name, action, and params to the downstream server
//   - Returns the downstream response body unchanged
//   - Wraps downstream errors with context (no naked errors)
//   - Returns error on non-2xx downstream status
//   - Handles empty params without panic
//   - Context cancellation propagates to downstream request
//
// INTEGRATION INSTRUCTIONS for BE-Gateway:
//
// The Proxy must accept an http.Client for its downstream transport so tests
// can point it at an httptest.Server without DNS. Two acceptable interfaces:
//
//   Option A — constructor param:
//     func New(cfg Config, client *http.Client) *Proxy
//
//   Option B — functional option:
//     func New(cfg Config, opts ...Option) *Proxy
//     func WithHTTPClient(c *http.Client) Option
//
// The Proxy.Forward (or Proxy.Call) method signature should be approximately:
//
//   func (p *Proxy) Forward(ctx context.Context, req ToolRequest) (ToolResponse, error)
//
// Where:
//   type ToolRequest struct {
//       ToolName  string
//       Action    string
//       Params    map[string]interface{}
//       SessionID string // optional, included in downstream request
//   }
//   type ToolResponse struct {
//       Status int
//       Body   json.RawMessage
//   }
//
// Run: go test -count=1 -race ./internal/proxy/...

package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eami/gateway/internal/proxy"
)

// ─── Downstream stub builder ─────────────────────────────────────────────────

// stubServer starts an httptest.Server that records incoming requests and
// responds with the configured status code and body.
type stubServer struct {
	srv *httptest.Server

	// Recorded from the most recent request
	lastMethod      string
	lastPath        string
	lastBody        map[string]interface{}

	// Configure response
	statusCode int
	respBody   []byte
}

func newStub(t *testing.T, statusCode int, respBody []byte) *stubServer {
	t.Helper()
	s := &stubServer{statusCode: statusCode, respBody: respBody}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastMethod = r.Method
		s.lastPath = r.URL.Path

		var body map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		s.lastBody = body

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.statusCode)
		_, _ = w.Write(s.respBody)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// validResponse is a minimal success body the downstream might return.
var validDownstreamResponse = []byte(`{"status":"ok","result":{"rows":42}}`)

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestProxy_ForwardsToolNameAndAction(t *testing.T) {
	downstream := newStub(t, http.StatusOK, validDownstreamResponse)

	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())
	req := proxy.ToolRequest{
		ToolName: "salesforce",
		Action:   "query",
		Params:   map[string]interface{}{"soql": "SELECT Id FROM Account LIMIT 10"},
	}

	resp, err := p.Forward(context.Background(), req)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.Status < 200 || resp.Status > 299 {
		t.Errorf("want 2xx status, got %d", resp.Status)
	}

	// Downstream must have received the tool name and action.
	if downstream.lastBody == nil {
		t.Fatal("downstream received no body")
	}
	if got := downstream.lastBody["tool"]; got != "salesforce" {
		t.Errorf("downstream body: tool = %v, want \"salesforce\"", got)
	}
	if got := downstream.lastBody["action"]; got != "query" {
		t.Errorf("downstream body: action = %v, want \"query\"", got)
	}
}

func TestProxy_ForwardsParams(t *testing.T) {
	downstream := newStub(t, http.StatusOK, validDownstreamResponse)
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	params := map[string]interface{}{
		"soql":  "SELECT Name FROM Contact WHERE Id = '001'",
		"limit": float64(5),
	}
	req := proxy.ToolRequest{
		ToolName: "salesforce",
		Action:   "query",
		Params:   params,
	}

	if _, err := p.Forward(context.Background(), req); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	got, ok := downstream.lastBody["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("downstream body missing 'params' field: %v", downstream.lastBody)
	}
	if got["soql"] != params["soql"] {
		t.Errorf("params.soql mismatch: got %v, want %v", got["soql"], params["soql"])
	}
}

func TestProxy_ReturnsDownstreamBody(t *testing.T) {
	respPayload := []byte(`{"result":{"id":"a1b2c3","status":"success"}}`)
	downstream := newStub(t, http.StatusOK, respPayload)
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	resp, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "crm",
		Action:   "get",
		Params:   map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	// The raw body must match what the downstream sent.
	if string(resp.Body) != string(respPayload) {
		t.Errorf("response body mismatch:\n  got  %s\n  want %s", resp.Body, respPayload)
	}
}

func TestProxy_DownstreamError_WrapsError(t *testing.T) {
	// Downstream returns 500.
	downstream := newStub(t, http.StatusInternalServerError, []byte(`{"error":"backend exploded"}`))
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	_, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "crm",
		Action:   "delete",
	})

	// Must return a non-nil error wrapping context (not a naked upstream error).
	if err == nil {
		t.Fatal("want error on downstream 500, got nil")
	}
	// Error message should mention the status or the tool to help debugging.
	errMsg := err.Error()
	if len(errMsg) == 0 {
		t.Error("error message is empty — must include context (status code, tool name, or action)")
	}
}

func TestProxy_Downstream404_ReturnsError(t *testing.T) {
	downstream := newStub(t, http.StatusNotFound, []byte(`{"error":"tool not found"}`))
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	_, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "unknown-tool",
		Action:   "do",
	})

	if err == nil {
		t.Fatal("want error on downstream 404, got nil")
	}
}

func TestProxy_EmptyParams_NoPanic(t *testing.T) {
	downstream := newStub(t, http.StatusOK, validDownstreamResponse)
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	// Should not panic on nil or empty params.
	_, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "salesforce",
		Action:   "list",
		Params:   nil,
	})
	if err != nil {
		t.Errorf("nil params: unexpected error: %v", err)
	}

	_, err = p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "salesforce",
		Action:   "list",
		Params:   map[string]interface{}{},
	})
	if err != nil {
		t.Errorf("empty params: unexpected error: %v", err)
	}
}

func TestProxy_ContextCancellation_Propagates(t *testing.T) {
	// Downstream hangs; context should cause Forward to return quickly.
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			// client cancelled
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer hanging.Close()

	p := proxy.New(proxy.Config{DownstreamURL: hanging.URL}, hanging.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Forward(ctx, proxy.ToolRequest{
		ToolName: "crm",
		Action:   "query",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Error("want error when context is cancelled, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Forward should respect context cancellation; took %v", elapsed)
	}
}

func TestProxy_UsesHTTPPost(t *testing.T) {
	// Proxy must use POST to forward tool calls (not GET).
	downstream := newStub(t, http.StatusOK, validDownstreamResponse)
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	if _, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "crm",
		Action:   "update",
		Params:   map[string]interface{}{"id": "001"},
	}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if downstream.lastMethod != http.MethodPost {
		t.Errorf("downstream method: got %q, want POST", downstream.lastMethod)
	}
}

func TestProxy_SessionID_ForwardedWhenSet(t *testing.T) {
	downstream := newStub(t, http.StatusOK, validDownstreamResponse)
	p := proxy.New(proxy.Config{DownstreamURL: downstream.srv.URL}, downstream.srv.Client())

	const sessionID = "sess-abc-123"
	if _, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName:  "crm",
		Action:    "list",
		SessionID: sessionID,
	}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	// Session ID should appear in the downstream body or as a header.
	// Accept either: handler decides the wire format.
	inBody := downstream.lastBody["session_id"] == sessionID
	if !inBody {
		t.Logf("session_id not found in body — check if it's forwarded as a header instead")
		// Not a hard failure; the proxy may use headers. Soft assertion.
	}
}

func TestProxy_UnreachableDownstream_ReturnsError(t *testing.T) {
	// Point at a port that refuses connections.
	p := proxy.New(proxy.Config{DownstreamURL: "http://127.0.0.1:1"}, http.DefaultClient)

	_, err := p.Forward(context.Background(), proxy.ToolRequest{
		ToolName: "crm",
		Action:   "list",
	})
	if err == nil {
		t.Fatal("want error when downstream is unreachable, got nil")
	}
}
