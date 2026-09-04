// redaction_dispatch_test.go -- eami-gateway/internal/aiprovider
//
// Proves B-156/B-167's contract at the exact chokepoint (Router.Dispatch)
// both real callers (cmd/gateway/dispatcher.go, internal/approval/router.go)
// converge through -- including AC1's own wire-level requirement: not just
// "the function was called," but that the real bytes an adapter would send
// externally never contain the original sensitive value.
package aiprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eami/gateway/internal/toolrouter"
)

// TestDispatch_RedactsBeforeAdapter_FunctionLevel proves the interception
// point itself: a fakeAdapter (dispatch_test.go) sees the masked value in
// req.Params, never the original.
func TestDispatch_RedactsBeforeAdapter_FunctionLevel(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude"} // RedactionRules nil -> default (enabled)

	params := map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []any{map[string]any{"role": "user", "content": "my SSN is 123-45-6789"}},
	}
	resp, count, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if count != 1 {
		t.Errorf("redacted count = %d, want 1", count)
	}

	msgs := adapter.gotReq.Params["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	if strings.Contains(content, "123-45-6789") {
		t.Errorf("adapter received the original SSN, redaction did not run: %q", content)
	}
	if !strings.Contains(content, "[REDACTED:SSN]") {
		t.Errorf("adapter did not receive the mask token: %q", content)
	}
}

// TestDispatch_OriginalParamsMapNeverMutated proves the caller's own map
// (also used to build the audit_log/episode snapshot in the real call
// chain) is never touched, even though a redacted copy is what actually
// reaches the adapter.
func TestDispatch_OriginalParamsMapNeverMutated(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude"}

	params := map[string]any{"text": "email me at real@example.com"}
	if _, _, err := r.Dispatch(context.Background(), row, "messages", params); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if params["text"].(string) != "email me at real@example.com" {
		t.Errorf("caller's own params map was mutated: %q", params["text"])
	}
}

// TestDispatch_NoMatchingContent_ByteIdenticalToBefore proves AC5: content
// with no matching pattern reaches the adapter completely unchanged.
func TestDispatch_NoMatchingContent_ByteIdenticalToBefore(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude"}

	params := map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": 1024,
		"messages":   []any{map[string]any{"role": "user", "content": "hello, how are you today?"}},
	}
	_, count, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if count != 0 {
		t.Errorf("redacted count = %d, want 0 for content matching no pattern", count)
	}
	gotBytes, _ := json.Marshal(adapter.gotReq.Params)
	wantBytes, _ := json.Marshal(params)
	if string(gotBytes) != string(wantBytes) {
		t.Errorf("no-match content changed:\n  got:  %s\n  want: %s", gotBytes, wantBytes)
	}
}

// TestDispatch_RedactionDisabledPerConnector proves AC4's own contract at
// the Router.Dispatch level: a connector explicitly configured with
// {"enabled": false} sends its content unredacted, exactly as before this
// brief, while a sibling connector with no override still redacts.
func TestDispatch_RedactionDisabledPerConnector(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude", RedactionRules: []byte(`{"enabled": false}`)}

	params := map[string]any{"text": "ssn 123-45-6789"}
	_, count, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if count != 0 {
		t.Errorf("redacted count = %d, want 0 (redaction disabled for this connector)", count)
	}
	if adapter.gotReq.Params["text"].(string) != "ssn 123-45-6789" {
		t.Errorf("content was redacted despite {\"enabled\": false}: %q", adapter.gotReq.Params["text"])
	}
}

// TestDispatch_CustomPatternPerConnector proves an admin-defined custom
// regex (AC4) is honored end-to-end through the real interception point.
func TestDispatch_CustomPatternPerConnector(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{
		ID:             "t1",
		Provider:       "claude",
		RedactionRules: []byte(`{"custom_patterns":[{"name":"employee_id","pattern":"EMP-[0-9]{6}"}]}`),
	}

	params := map[string]any{"text": "requested by EMP-123456"}
	_, count, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if count != 1 {
		t.Errorf("redacted count = %d, want 1", count)
	}
	if strings.Contains(adapter.gotReq.Params["text"].(string), "EMP-123456") {
		t.Error("custom-pattern match reached the adapter unredacted")
	}
}

// TestDispatch_InvalidRedactionRules_CleanRejection proves a malformed
// stored redaction_rules value fails closed (a clean error, blocking the
// dispatch) rather than silently skipping redaction and sending raw
// content through.
func TestDispatch_InvalidRedactionRules_CleanRejection(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude"}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude", RedactionRules: []byte(`not json`)}

	_, _, err := r.Dispatch(context.Background(), row, "messages", map[string]any{"text": "hi"})
	if err == nil {
		t.Fatal("expected an error for malformed redaction_rules, got nil")
	}
}

// ─── Wire-level proof (AC1) ─────────────────────────────────────────────────

// TestDispatch_RealClaudeAdapter_WireLevel_OriginalValueNeverSent is AC1's
// direct proof: dispatch through the REAL Router.Dispatch -> REAL
// ClaudeAdapter chain (not a fake) against a local httptest server, and
// inspect the LITERAL BYTES the server actually received off the wire --
// not the decoded/re-marshaled request, the raw io.Reader content HTTP
// itself delivered. A genuine SSN and a genuine email are both present in
// the outbound request; this proves neither ever appears in what actually
// went out over HTTP.
func TestDispatch_RealClaudeAdapter_WireLevel_OriginalValueNeverSent(t *testing.T) {
	const originalSSN = "123-45-6789"
	const originalEmail = "jane.doe@example.com"

	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rawBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"got it"}]}`))
	}))
	defer srv.Close()

	adapter := newClaudeAdapterWithDialer(unrestrictedDialer)
	claudeMessagesURLOverride = srv.URL
	defer func() { claudeMessagesURLOverride = "" }()

	toolCipher, err := toolrouter.NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext, _ := json.Marshal(Credentials{APIKey: "sk-ant-real-key-for-wire-test"})
	sealed := encryptForTest(t, testKeyHex, plaintext)

	r := New(nil, toolCipher, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude", CredentialsEncrypted: sealed} // default redaction rules

	params := map[string]any{
		"model": "claude-opus-4-6",
		"messages": []any{
			map[string]any{"role": "user", "content": "My SSN is " + originalSSN + " and my email is " + originalEmail},
		},
	}
	resp, count, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if count != 2 {
		t.Errorf("redacted count = %d, want 2 (SSN + email)", count)
	}

	wire := string(rawBody)
	if wire == "" {
		t.Fatal("captured no request body at all -- test setup is broken, not proving anything")
	}
	if strings.Contains(wire, originalSSN) {
		t.Errorf("AC1 FAILED: the real SSN reached the wire: %s", wire)
	}
	if strings.Contains(wire, originalEmail) {
		t.Errorf("AC1 FAILED: the real email reached the wire: %s", wire)
	}
	if !strings.Contains(wire, "[REDACTED:SSN]") || !strings.Contains(wire, "[REDACTED:EMAIL]") {
		t.Errorf("wire body missing expected mask tokens: %s", wire)
	}

	// Cross-check: the server's own decoded view (a second, independent
	// vantage point on the same bytes) confirms the same thing structurally,
	// not just via substring search.
	var decoded map[string]any
	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		t.Fatalf("decode captured wire body: %v", err)
	}
	msgs := decoded["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	if strings.Contains(content, originalSSN) || strings.Contains(content, originalEmail) {
		t.Errorf("decoded wire content still carries an original value: %q", content)
	}
}
