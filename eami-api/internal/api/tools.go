package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// toolStore is the subset of *store.Queries CreateTool/ListTools/DeleteTool/
// TestTool need. It exists so tests can inject a double that proves e.g.
// "encryption failed => the store is never called" without a live Postgres,
// without touching the broader Store interface (store_adapter.go/
// store_mock.go) used by other handlers.
type toolStore interface {
	ListTools(ctx context.Context, orgID uuid.UUID) ([]store.GatewayTool, error)
	CreateTool(ctx context.Context, p store.CreateToolParams) (store.GatewayTool, error)
	DeleteTool(ctx context.Context, orgID, toolID uuid.UUID) (bool, error)
	MarkToolTested(ctx context.Context, orgID, toolID uuid.UUID, status string, latencyMs int) error
	// GetToolForTest reads back credentials_encrypted -- deliberately NOT
	// exposed by any other method here (ListTools/CreateTool's GatewayTool
	// return type has no such field at all, by design, per B-022). Used
	// only internally by TestTool to decrypt-and-probe; the result must
	// never be serialized into any HTTP response.
	GetToolForTest(ctx context.Context, orgID, toolID uuid.UUID) (toolTestRow, error)
}

// toolTestRow is what TestTool needs to run a connectivity check.
type toolTestRow struct {
	Type                 string
	AuthType             string
	BaseURL              *string
	CredentialsEncrypted []byte
}

// toolQueries returns the toolStore to use for this request: the test
// override if set, otherwise the production *store.Queries (wrapped, see
// toolStoreWithConnectivity). ok is false if neither is configured (e.g. a
// Server built via NewHandler for another handler's tests, which never
// sets s.queries) -- callers must check ok rather than dereferencing a nil
// *store.Queries through the interface, mirroring the "if s.queries != nil"
// guard every other handler in this package (policies.go, agents.go, ...)
// uses for the same reason.
func (s *Server) toolQueries() (ts toolStore, ok bool) {
	if s.toolStoreOverride != nil {
		return s.toolStoreOverride, true
	}
	if s.queries != nil {
		return toolStoreWithConnectivity{s.queries}, true
	}
	return nil, false
}

// toolStoreWithConnectivity wraps *store.Queries, adding GetToolForTest via
// a raw SQL query run through s.queries.DB() -- the same escape hatch
// finops.go already uses for queries with no sqlc-style wrapper -- so this
// credentials-reading path stays confined to this file instead of touching
// eami-api/internal/store (which owns every other gateway_tools query, and
// is intentionally left frozen by this task; see B-022's comments there).
type toolStoreWithConnectivity struct {
	*store.Queries
}

func (w toolStoreWithConnectivity) GetToolForTest(ctx context.Context, orgID, toolID uuid.UUID) (toolTestRow, error) {
	const q = `
SELECT type, auth_type, base_url, credentials_encrypted
FROM gateway_tools
WHERE id = $1 AND org_id = $2`

	var row toolTestRow
	var baseURL pgtype.Text
	err := w.Queries.DB().QueryRow(ctx, q,
		pgtype.UUID{Bytes: toolID, Valid: true},
		pgtype.UUID{Bytes: orgID, Valid: true},
	).Scan(&row.Type, &row.AuthType, &baseURL, &row.CredentialsEncrypted)
	if err != nil {
		return toolTestRow{}, err
	}
	if baseURL.Valid {
		row.BaseURL = &baseURL.String
	}
	return row, nil
}

// ToolCredentials documents openapi.yaml's ToolCreate.credentials shape --
// sensitive, write-only, never echoed back in any response. Not used to
// decode the request body (see credentialsProvided/CreateTool below): the
// wire payload is stored and encrypted as raw JSON bytes rather than
// re-marshaled through this struct, so a client sending a field name this
// struct doesn't happen to declare (a typo, a future field, a differently-
// cased key) still gets encrypted and stored instead of being silently
// dropped by encoding/json's default "ignore unknown fields" behavior.
type ToolCredentials struct {
	APIKey            string `json:"api_key,omitempty"`
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string `json:"oauth_client_secret,omitempty"`
	ConnectionString  string `json:"connection_string,omitempty"`
}

// credentialsProvided reports whether raw represents actual submitted
// credential material, as opposed to an omitted field, an explicit JSON
// null, or an empty {} object. It deliberately does not decode into
// ToolCredentials first -- that would silently treat "object with only
// unrecognized keys" the same as "no credentials", which is exactly the
// silent-data-loss failure mode this handler exists to prevent. An error
// is returned if raw is present but not a JSON object at all.
func credentialsProvided(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil // field omitted entirely
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return false, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false, fmt.Errorf("credentials must be a JSON object: %w", err)
	}
	return len(obj) > 0, nil
}

// ── Response types ────────────────────────────────────────────────────────────

type ToolResp struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	AuthType  string     `json:"auth_type"`
	MCPCommand *string   `json:"mcp_command,omitempty"`
	BaseURL   *string    `json:"base_url,omitempty"`
	Status    string     `json:"status"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func toolToResp(t store.GatewayTool) ToolResp {
	r := ToolResp{
		ID: t.ID, Name: t.Name, Type: t.Type,
		AuthType: t.AuthType, Status: t.Status, CreatedAt: t.CreatedAt,
	}
	if t.MCPCommand.Valid {
		r.MCPCommand = &t.MCPCommand.String
	}
	if t.BaseURL.Valid {
		r.BaseURL = &t.BaseURL.String
	}
	if t.LastUsed.Valid {
		ts := t.LastUsed.Time
		r.LastUsed = &ts
	}
	return r
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// ListTools handles GET /v1/gateway/tools
func (s *Server) ListTools(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	ts, ok := s.toolQueries()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "tool store is not configured")
		return
	}
	tools, err := ts.ListTools(r.Context(), uc.OrgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	resp := make([]ToolResp, 0, len(tools))
	for _, t := range tools {
		resp = append(resp, toolToResp(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resp})
}

// CreateTool handles POST /v1/gateway/tools
func (s *Server) CreateTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)

	var body struct {
		Name        string          `json:"name"`
		Type        string          `json:"type"`
		AuthType    string          `json:"auth_type"`
		MCPCommand  *string         `json:"mcp_command"`
		MCPArgs     []string        `json:"mcp_args"`
		BaseURL     *string         `json:"base_url"`
		Credentials json.RawMessage `json:"credentials"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name == "" || body.Type == "" || body.AuthType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name, type, and auth_type are required")
		return
	}
	ts, ok := s.toolQueries()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "tool store is not configured")
		return
	}

	hasCredentials, err := credentialsProvided(body.Credentials)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	var encrypted []byte
	if hasCredentials {
		if s.toolCreds == nil {
			// Fail closed: this is the exact bug this handler was built to
			// fix -- never report success while discarding the secret.
			writeError(w, http.StatusInternalServerError, "internal_error",
				"tool credential encryption is not configured; cannot store credentials")
			return
		}
		// Encrypt the raw submitted bytes, not a re-marshaled ToolCredentials
		// -- see credentialsProvided's comment: this guarantees nothing the
		// client sent is silently dropped before it's encrypted.
		encrypted, err = s.toolCreds.Encrypt(body.Credentials)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encrypt credentials")
			return
		}
	}

	t, err := ts.CreateTool(r.Context(), store.CreateToolParams{
		OrgID:                uc.OrgID,
		Name:                 body.Name,
		Type:                 body.Type,
		AuthType:             body.AuthType,
		MCPCommand:           body.MCPCommand,
		MCPArgs:              body.MCPArgs,
		BaseURL:              body.BaseURL,
		CredentialsEncrypted: encrypted,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toolToResp(t))
}

// DeleteTool handles DELETE /v1/gateway/tools/{toolId}
func (s *Server) DeleteTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	toolID, err := uuid.Parse(chi.URLParam(r, "toolId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid tool ID")
		return
	}
	ts, ok := s.toolQueries()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "tool store is not configured")
		return
	}
	found, err := ts.DeleteTool(r.Context(), uc.OrgID, toolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "tool not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestTool handles POST /v1/gateway/tools/{toolId}/test -- attempts a real
// connection to the tool using its stored (decrypted) credentials, per
// tool_connectivity.go, and reports connected/auth-failed/unreachable/
// misconfigured. Response shape matches openapi.yaml's documented
// {success, latency_ms, error} exactly (the previous synthetic stub
// returned {status, latency_ms}, which never matched the spec).
func (s *Server) TestTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	toolID, err := uuid.Parse(chi.URLParam(r, "toolId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid tool ID")
		return
	}

	ts, ok := s.toolQueries()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "tool store is not configured")
		return
	}

	row, err := ts.GetToolForTest(r.Context(), uc.OrgID, toolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	dial := dialContextFunc(safeDialContext)
	if s.toolDialOverride != nil {
		dial = s.toolDialOverride
	}
	result := testToolConnectivityWithDialer(r.Context(), row.Type, row.AuthType, row.BaseURL, row.CredentialsEncrypted, s.toolCreds, dial)

	if err := ts.MarkToolTested(r.Context(), uc.OrgID, toolID, result.dbStatus(), int(result.LatencyMs)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    result.Connected,
		"latency_ms": result.LatencyMs,
		"error":      result.errorMessage(),
	})
}
