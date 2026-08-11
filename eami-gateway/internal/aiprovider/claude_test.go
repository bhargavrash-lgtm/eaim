// claude_test.go -- eami-gateway/internal/aiprovider
//
// Unit tests for ClaudeAdapter.Dispatch against a local httptest server,
// mirroring toolrouter's TestForward_* pattern (newWithDialer-equivalent
// seam, unrestrictedDialer for reaching 127.0.0.1). No real Postgres or
// real Anthropic API needed for these -- see BUILT.md for what's proven
// live against the real api.anthropic.com separately.
package aiprovider

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// unrestrictedDialer is a plain net.Dialer with no SSRF restrictions --
// used only to reach a local httptest server (always 127.0.0.1, which the
// real safeDialContext correctly blocks). Mirrors toolrouter's identical
// test helper.
func unrestrictedDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func TestClaudeAdapter_Provider_ReturnsClaude(t *testing.T) {
	a := NewClaudeAdapter()
	if a.Provider() != "claude" {
		t.Errorf("Provider() = %q, want claude", a.Provider())
	}
}

func TestClaudeAdapter_UnsupportedAction_CleanRejection(t *testing.T) {
	a := newClaudeAdapterWithDialer(unrestrictedDialer)
	_, err := a.Dispatch(context.Background(), Credentials{APIKey: "sk-ant-x"}, Request{Action: "count_tokens"})
	if err == nil {
		t.Fatal("expected an error for an unsupported action, got nil")
	}
	if !strings.Contains(err.Error(), "count_tokens") {
		t.Errorf("error should name the unsupported action, got: %v", err)
	}
}

func TestClaudeAdapter_EmptyAPIKey_CleanRejection(t *testing.T) {
	a := newClaudeAdapterWithDialer(unrestrictedDialer)
	_, err := a.Dispatch(context.Background(), Credentials{}, Request{Action: "messages", Params: map[string]any{}})
	if err == nil {
		t.Fatal("expected an error for an empty api_key, got nil")
	}
}

// TestClaudeAdapter_RealRoundTrip_Success proves the actual HTTP mechanics:
// real headers (x-api-key + anthropic-version, NOT Authorization: Bearer --
// this is the exact assumption toolrouter.Forward bakes in for rest_api
// tools and Claude does not follow), real request body passthrough, real
// response parsing.
func TestClaudeAdapter_RealRoundTrip_Success(t *testing.T) {
	var gotAPIKeyHeader, gotVersionHeader, gotAuthHeader string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("x-api-key")
		gotVersionHeader = r.Header.Get("anthropic-version")
		gotAuthHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"claude-opus-4-6","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	a := newClaudeAdapterWithDialer(unrestrictedDialer)
	claudeMessagesURLOverride = srv.URL
	defer func() { claudeMessagesURLOverride = "" }()

	params := map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": 1024,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}
	resp, err := a.Dispatch(context.Background(), Credentials{APIKey: "sk-ant-real-key"}, Request{Action: "messages", Params: params})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "hello") {
		t.Errorf("response body doesn't contain the expected content: %s", resp.Body)
	}

	if gotAPIKeyHeader != "sk-ant-real-key" {
		t.Errorf("x-api-key header = %q, want sk-ant-real-key", gotAPIKeyHeader)
	}
	if gotVersionHeader == "" {
		t.Error("anthropic-version header was not set")
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization header should not be set for Claude (uses x-api-key) -- got %q", gotAuthHeader)
	}
	if gotBody["model"] != "claude-opus-4-6" {
		t.Errorf("request body model = %v, want claude-opus-4-6 (params must pass through unchanged)", gotBody["model"])
	}
}

// TestClaudeAdapter_DownstreamError_CredentialsNeverInErrorMessage (AC6)
// proves a downstream 4xx/5xx is surfaced as a clean error without ever
// interpolating the API key into it.
func TestClaudeAdapter_DownstreamError_CredentialsNeverInErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	a := newClaudeAdapterWithDialer(unrestrictedDialer)
	claudeMessagesURLOverride = srv.URL
	defer func() { claudeMessagesURLOverride = "" }()

	const secretKey = "sk-ant-super-secret-do-not-leak"
	_, err := a.Dispatch(context.Background(), Credentials{APIKey: secretKey}, Request{Action: "messages", Params: map[string]any{}})
	if err == nil {
		t.Fatal("expected an error for a 401 downstream response, got nil")
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Fatalf("AC6 VIOLATION: error message contains the raw API key: %v", err)
	}
}

// TestClaudeAdapter_ResponseSizeCapped proves a runaway/malicious response
// body can't exhaust gateway memory -- mirrors toolrouter's identical
// maxResponseSize discipline.
func TestClaudeAdapter_ResponseSizeCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("x", 1024*1024) // 1MB per write
		for i := 0; i < 11; i++ {               // 11MB total, over the 10MB cap
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	a := newClaudeAdapterWithDialer(unrestrictedDialer)
	claudeMessagesURLOverride = srv.URL
	defer func() { claudeMessagesURLOverride = "" }()

	resp, err := a.Dispatch(context.Background(), Credentials{APIKey: "sk-ant-x"}, Request{Action: "messages", Params: map[string]any{}})
	// A capped read succeeds (status 200, truncated body) rather than
	// erroring -- either outcome is acceptable here, what matters is the
	// body never exceeds the cap.
	if err == nil && len(resp.Body) > claudeMaxResponseSize {
		t.Fatalf("response body size %d exceeds the %d cap", len(resp.Body), claudeMaxResponseSize)
	}
}
