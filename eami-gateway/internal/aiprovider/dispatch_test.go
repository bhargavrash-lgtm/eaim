// dispatch_test.go -- eami-gateway/internal/aiprovider
//
// Unit tests for Router.Dispatch's decrypt-and-route wiring. No real
// Postgres needed: Dispatch takes an already-resolved *ToolRow directly,
// it never queries the DB itself (only Resolve does, covered separately
// in router_pg_test.go) -- mirrors toolrouter's own TestForward_* tests'
// use of a nil/unused pool for the same reason.
package aiprovider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eami/gateway/internal/toolrouter"
)

// testKeyHex is a fixed, valid 32-byte AES-256 key for tests only.
const testKeyHex = "7105980461784fcb4998a025fa91b7a762f46f345248b41b6cff6265d14d8b1f"

// encryptForTest seals plaintext the same way eami-api/internal/toolcreds.
// Cipher.Encrypt (B-022) does -- nonce||ciphertext -- using stdlib
// primitives directly rather than any package's internals: toolrouter.
// Cipher is decrypt-only by design (see its own doc comment), so there is
// no exported Encrypt this package could call even in a test.
func encryptForTest(t *testing.T, hexKey string, plaintext []byte) []byte {
	t.Helper()
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("decode test key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil)
}

// fakeAdapter is a minimal in-test Adapter -- proves Router.Dispatch's own
// decrypt-then-route wiring in isolation from any real provider's HTTP
// behavior (that's claude_test.go's job).
type fakeAdapter struct {
	provider   string
	gotCreds   Credentials
	gotReq     Request
	returnResp Response
	returnErr  error
}

func (f *fakeAdapter) Provider() string { return f.provider }
func (f *fakeAdapter) Dispatch(_ context.Context, creds Credentials, req Request) (Response, error) {
	f.gotCreds = creds
	f.gotReq = req
	return f.returnResp, f.returnErr
}

func TestDispatch_NilRow_Rejected(t *testing.T) {
	r := New(nil, nil, nil)
	_, _, err := r.Dispatch(context.Background(), nil, "messages", nil)
	if err == nil {
		t.Fatal("expected an error for a nil row, got nil")
	}
}

func TestDispatch_NoProviderConfigured_Rejected(t *testing.T) {
	r := New(nil, nil, map[string]Adapter{"claude": &fakeAdapter{provider: "claude"}})
	row := &ToolRow{ID: "t1", Provider: ""}
	_, _, err := r.Dispatch(context.Background(), row, "messages", nil)
	if err == nil {
		t.Fatal("expected an error for a connector with no provider configured, got nil")
	}
}

func TestDispatch_UnregisteredProvider_Rejected(t *testing.T) {
	r := New(nil, nil, map[string]Adapter{"claude": &fakeAdapter{provider: "claude"}})
	row := &ToolRow{ID: "t1", Provider: "gemini"} // no adapter registered for it
	_, _, err := r.Dispatch(context.Background(), row, "messages", nil)
	if err == nil {
		t.Fatal("expected an error for an unregistered provider, got nil")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error should name the unregistered provider, got: %v", err)
	}
}

func TestDispatch_CredentialsPresentButNoCipher_CleanRejection(t *testing.T) {
	r := New(nil, nil, map[string]Adapter{"claude": &fakeAdapter{provider: "claude"}}) // cipher is nil
	row := &ToolRow{ID: "t1", Provider: "claude", CredentialsEncrypted: []byte("not-empty")}
	_, _, err := r.Dispatch(context.Background(), row, "messages", nil)
	if err == nil {
		t.Fatal("expected a clean rejection when credentials are stored but no cipher is configured, got nil")
	}
}

func TestDispatch_UndecryptableCredentials_CleanRejection(t *testing.T) {
	toolCipher, err := toolrouter.NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	r := New(nil, toolCipher, map[string]Adapter{"claude": &fakeAdapter{provider: "claude"}})
	row := &ToolRow{ID: "t1", Provider: "claude", CredentialsEncrypted: []byte("garbage-not-a-real-sealed-blob")}
	_, _, err = r.Dispatch(context.Background(), row, "messages", nil)
	if err == nil {
		t.Fatal("expected a clean rejection for undecryptable credentials, got nil")
	}
}

// TestDispatch_ValidCredentials_DecryptedAndPassedToAdapter proves the
// full happy-path wiring: a real encrypted blob decrypts to the expected
// api_key, and the resolved Adapter receives it plus the caller's
// action/params unchanged.
func TestDispatch_ValidCredentials_DecryptedAndPassedToAdapter(t *testing.T) {
	toolCipher, err := toolrouter.NewCipher(testKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext, _ := json.Marshal(Credentials{APIKey: "sk-ant-real-key"})
	sealed := encryptForTest(t, testKeyHex, plaintext)

	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200, Body: []byte(`{"ok":true}`)}}
	r := New(nil, toolCipher, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude", CredentialsEncrypted: sealed}

	params := map[string]any{"model": "claude-opus-4-6", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	resp, _, err := r.Dispatch(context.Background(), row, "messages", params)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if adapter.gotCreds.APIKey != "sk-ant-real-key" {
		t.Errorf("adapter received APIKey = %q, want sk-ant-real-key", adapter.gotCreds.APIKey)
	}
	if adapter.gotReq.Action != "messages" {
		t.Errorf("adapter received Action = %q, want messages", adapter.gotReq.Action)
	}
	if len(adapter.gotReq.Params) != len(params) {
		t.Errorf("adapter received %d params, want %d", len(adapter.gotReq.Params), len(params))
	}
}

// TestDispatch_NoCredentialsStored_EmptyCredentialsPassed proves a
// connector with no stored secret at all (CredentialsEncrypted empty) is
// not treated as an error -- Dispatch passes a zero-value Credentials
// through, and it's the Adapter's own job to reject a missing api_key
// (ClaudeAdapter does, see claude_test.go).
func TestDispatch_NoCredentialsStored_EmptyCredentialsPassed(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnResp: Response{StatusCode: 200}}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude"} // CredentialsEncrypted is nil

	if _, _, err := r.Dispatch(context.Background(), row, "messages", nil); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if adapter.gotCreds.APIKey != "" {
		t.Errorf("expected empty APIKey, got %q", adapter.gotCreds.APIKey)
	}
}

// TestDispatch_AdapterError_Propagated proves an adapter-level failure
// (e.g. a real downstream error from Claude) is returned as-is, not
// swallowed or converted into a false success.
func TestDispatch_AdapterError_Propagated(t *testing.T) {
	adapter := &fakeAdapter{provider: "claude", returnErr: errors.New("downstream boom")}
	r := New(nil, nil, map[string]Adapter{"claude": adapter})
	row := &ToolRow{ID: "t1", Provider: "claude"}

	_, _, err := r.Dispatch(context.Background(), row, "messages", nil)
	if err == nil || !strings.Contains(err.Error(), "downstream boom") {
		t.Errorf("expected the adapter's error to propagate, got: %v", err)
	}
}
