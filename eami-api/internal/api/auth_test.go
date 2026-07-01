// auth_test.go — eami-api/internal/api
// QA-EAMI — handler-level unit tests for POST /v1/auth/login.
//
// Setup: real auth.Service (ephemeral RSA key), MockStore, httptest.Server.
// No database. No network.
//
// Run: go test -count=1 ./internal/api/... -run TestLogin

package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// mustBcrypt hashes a plaintext password for seeding the mock store.
func mustBcrypt(t *testing.T, plain string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(hash)
}

// newAuthTestServer creates an httptest.Server running the API with:
//   - a mock store pre-seeded with the given users
//   - a real auth.Service using an ephemeral RSA key
//
// The server is registered for cleanup automatically.
func newAuthTestServer(t *testing.T, store *api.MockStore) *httptest.Server {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	h := api.NewHandler(store, authSvc)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv
}

// post sends a JSON POST to path on srv and returns the response.
func post(t *testing.T, srv *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// decodeBody reads the response body as a JSON map.
func decodeBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

// ─── shared fixture ──────────────────────────────────────────────────────────

var (
	testOrgID  = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	testUserID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
)

const (
	validEmail    = "alice@example.com"
	validPassword = "correct-horse-battery-staple"
)

func seedValidUser(t *testing.T, ms *api.MockStore) {
	t.Helper()
	ms.SeedUser(api.StoreUser{
		ID:           testUserID,
		Email:        validEmail,
		Name:         "Alice",
		Role:         "admin",
		OrgID:        testOrgID,
		PasswordHash: mustBcrypt(t, validPassword),
	})
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	ms := api.NewMockStore()
	seedValidUser(t, ms)
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": validPassword,
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)

	// access_token must be present and non-empty
	accessToken, ok := body["access_token"].(string)
	if !ok || accessToken == "" {
		t.Fatalf("access_token missing or empty in response: %v", body)
	}

	// refresh_token must be present and non-empty
	refreshToken, ok := body["refresh_token"].(string)
	if !ok || refreshToken == "" {
		t.Fatalf("refresh_token missing or empty in response: %v", body)
	}

	// expires_in must be > 0
	expiresIn, ok := body["expires_in"].(float64)
	if !ok || expiresIn <= 0 {
		t.Fatalf("expires_in missing or zero in response: %v", body)
	}

	// Tokens are distinct
	if accessToken == refreshToken {
		t.Error("access_token and refresh_token must be different")
	}

	// access_token looks like a JWT (three base64url segments)
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		t.Errorf("access_token is not a JWT (got %d parts): %s", len(parts), accessToken)
	}

	t.Logf("login OK — expires_in=%.0fs", expiresIn)
}

func TestLogin_WrongPassword(t *testing.T) {
	ms := api.NewMockStore()
	seedValidUser(t, ms)
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": "wrong-password",
	})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if _, hasToken := body["access_token"]; hasToken {
		t.Error("access_token must not be present on 401 response")
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	ms := api.NewMockStore()
	// Deliberately do NOT seed any user.
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    "nobody@example.com",
		"password": "anything",
	})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestLogin_MissingEmail(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		// email intentionally omitted
		"password": validPassword,
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestLogin_MissingPassword(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email": validEmail,
		// password intentionally omitted
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestLogin_EmptyBody(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestLogin_MalformedJSON(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)

	resp, err := srv.Client().Post(
		srv.URL+"/v1/auth/login",
		"application/json",
		strings.NewReader(`{not valid json`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestLogin_ContentTypeNotJSON(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)

	resp, err := srv.Client().Post(
		srv.URL+"/v1/auth/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("email=alice@example.com&password=secret"),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Should be 400 (not 500)
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("content-type mismatch must not cause 500, got %d", resp.StatusCode)
	}
}

func TestLogin_StoreError(t *testing.T) {
	ms := api.NewMockStore()
	ms.GetUserByEmailErr = errors.New("DB connection refused")
	srv := newAuthTestServer(t, ms)

	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": validPassword,
	})

	// Store error → should NOT expose internal error; return 500 or 503
	if resp.StatusCode < 500 {
		t.Errorf("store error on login: got %d, want >= 500", resp.StatusCode)
	}
}
