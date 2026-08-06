// Package toolrouter resolves an incoming tool_call's parsed tool name to
// an org-scoped gateway_tools row and, for rest_api-type rows, dispatches
// to that tool's real base_url using its real decrypted credentials.
//
// Scope (B-044): rest_api-type tools only. mcp/database-type gateway_tools
// rows, and any tool name with no matching row at all, are left entirely
// to the caller (cmd/gateway/main.go's dispatch) to fall back to the
// existing static proxy.Proxy -- this package never decides that fallback
// itself, it only ever answers "did I find a usable rest_api row, yes or
// no."
//
// No per-endpoint action-to-path mapping exists yet (gateway_tools has no
// column for it -- see the data-access-governance/routing investigation's
// B7 finding: that's real, larger, deferred future work). Forward's
// minimum-viable behavior is one endpoint per tool: every action for a
// given tool POSTs the same JSON envelope to that tool's single base_url.
package toolrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/proxy"
)

const (
	defaultTimeout  = 30 * time.Second // matches internal/proxy's static-forward timeout
	maxResponseSize = 10 * 1024 * 1024 // matches internal/proxy's response cap
)

// ErrNotFound is returned by Resolve when no gateway_tools row matches the
// given org and name. Callers should fall back to the existing static
// proxy, not treat this as a hard failure -- see the package doc comment.
var ErrNotFound = errors.New("toolrouter: no matching tool found")

// ToolRow is the subset of gateway_tools fields dispatch needs.
type ToolRow struct {
	ID                   string
	Type                 string // "mcp" | "rest_api" | "database"
	AuthType             string // "oauth2" | "api_key" | "basic" | "db_connection_string"
	BaseURL              *string
	CredentialsEncrypted []byte
}

// Router resolves gateway_tools rows and dispatches rest_api-type tool calls.
type Router struct {
	pool   *pgxpool.Pool
	cipher *Cipher // nil if TOOL_CREDENTIALS_ENCRYPTION_KEY is unset; see Forward
	client *http.Client
}

// New creates a Router. cipher may be nil -- see Forward's handling of a
// row with stored credentials but no configured key. Always dials via the
// real safeDialContext -- the only production call site.
func New(pool *pgxpool.Pool, cipher *Cipher) *Router {
	return newWithDialer(pool, cipher, safeDialContext)
}

// newWithDialer is New's actual implementation, parameterized by dial so
// tests can exercise real round trips against local httptest servers
// without tripping safeDialContext's loopback block. New always passes
// safeDialContext; this seam exists only for _test.go files, mirroring
// eami-api/internal/api/tool_connectivity.go's identical convention.
func newWithDialer(pool *pgxpool.Pool, cipher *Cipher, dial dialContextFunc) *Router {
	return &Router{
		pool:   pool,
		cipher: cipher,
		client: &http.Client{
			Timeout:   defaultTimeout,
			Transport: &http.Transport{DialContext: dial},
		},
	}
}

// Resolve looks up name within org's gateway_tools, org-scoped -- the same
// org-safety discipline as B-042's registry.LookupByNameAndOrg fix (an
// unscoped lookup would let one org's agent route to, and exfiltrate
// credentials/traffic through, a different org's identically-named tool).
// Returns ErrNotFound (not an error the caller should surface to the
// agent) when no row matches; any other error is a genuine DB failure.
func (r *Router) Resolve(ctx context.Context, orgID, name string) (*ToolRow, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, type, auth_type, base_url, credentials_encrypted
		FROM gateway_tools
		WHERE org_id = $1 AND name = $2
	`, orgID, name)

	var t ToolRow
	if err := row.Scan(&t.ID, &t.Type, &t.AuthType, &t.BaseURL, &t.CredentialsEncrypted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("toolrouter: query tool %q: %w", name, err)
	}
	return &t, nil
}

// downstreamRequest is the JSON body sent to a rest_api tool's base_url --
// deliberately the same envelope shape as internal/proxy's downstreamRequest
// (a duplicate, not an import: proxy.go stays untouched, see the router
// package doc comment on why no per-endpoint schema exists yet).
type downstreamRequest struct {
	Tool      string         `json:"tool"`
	Action    string         `json:"action"`
	Params    map[string]any `json:"params,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

// Forward dispatches req to row's real base_url using its real decrypted
// credentials. row.Type must be "rest_api" -- callers (dispatch) must only
// call Forward after confirming that via Resolve; Forward defensively
// rejects any other type rather than silently proceeding.
//
// Every failure path here -- nil row, wrong type, missing base_url,
// undecryptable/malformed credentials -- is a clean rejection (a returned
// error), never a panic and never a silent fall-through with a blank/wrong
// URL. The single production call site (dispatch, in cmd/gateway/main.go)
// only ever calls Forward with a non-nil, already-resolved rest_api row,
// but Router is an importable package -- a future caller shouldn't be able
// to panic it by skipping that check.
func (r *Router) Forward(ctx context.Context, row *ToolRow, req proxy.ToolRequest) (proxy.ToolResponse, error) {
	if row == nil {
		return proxy.ToolResponse{}, errors.New("toolrouter: Forward called with a nil row")
	}
	if row.Type != "rest_api" {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: Forward called for non-rest_api tool type %q (id=%s)", row.Type, row.ID)
	}
	if row.BaseURL == nil || *row.BaseURL == "" {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: tool %s has no base_url configured", row.ID)
	}

	var creds Credentials
	if len(row.CredentialsEncrypted) > 0 {
		if r.cipher == nil {
			return proxy.ToolResponse{}, fmt.Errorf("toolrouter: tool %s has stored credentials but no decryption key is configured", row.ID)
		}
		plaintext, err := r.cipher.Decrypt(row.CredentialsEncrypted)
		if err != nil {
			return proxy.ToolResponse{}, fmt.Errorf("toolrouter: tool %s credentials could not be decrypted: %w", row.ID, err)
		}
		if err := json.Unmarshal(plaintext, &creds); err != nil {
			return proxy.ToolResponse{}, fmt.Errorf("toolrouter: tool %s stored credentials are not in the expected shape", row.ID)
		}
	}

	body := downstreamRequest{
		Tool:      req.ToolName,
		Action:    req.Action,
		Params:    req.Params,
		SessionID: req.SessionID,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, *row.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// authType "basic" has no username/password fields in Credentials (same
	// gap B-023's testRESTTool already documents -- ToolCreate.credentials'
	// shape, frozen by B-022, has none) -- no header is set for it here
	// either; a real downstream would then correctly reject with 401,
	// surfaced as an ordinary proxy error below, not a crash.
	switch {
	case creds.APIKey != "":
		httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	case creds.OAuthClientID != "" && creds.OAuthClientSecret != "":
		// Best-effort, matching testRESTTool: not a real OAuth2 token
		// exchange (no token endpoint is stored anywhere), HTTP Basic using
		// the client credentials, which some simple API-key-style
		// integrations accept in place of a real OAuth flow.
		httpReq.SetBasicAuth(creds.OAuthClientID, creds.OAuthClientSecret)
	}

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: request failed (tool=%s action=%s): %w",
			req.ToolName, req.Action, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return proxy.ToolResponse{}, fmt.Errorf("toolrouter: downstream error %d (tool=%s action=%s): %s",
			resp.StatusCode, req.ToolName, req.Action, string(raw))
	}

	return proxy.ToolResponse{Status: resp.StatusCode, Body: json.RawMessage(raw)}, nil
}
