package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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
	UpdateTool(ctx context.Context, p store.UpdateToolParams) (store.GatewayTool, error)
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

// ── Action path mappings (B-046) ────────────────────────────────────────────────

// ActionPathMapping is one named action's routing entry for a rest_api
// tool: the path (relative to base_url) and HTTP method eami-gateway's
// toolrouter should use for that action, instead of B-044's flat
// "always POST to base_url" default. Mirrors eami-gateway/internal/
// toolrouter.ActionPathEntry's JSON shape exactly -- kept as a separate
// duplicate type (not shared) since the two modules cannot import each
// other's internal packages (established B-044/B-025 constraint).
//
// InputSchema (B-075) is purely additive and purely descriptive -- never
// read by toolrouter.Forward (dispatch), which only ever uses Path/Method.
// It exists so tools/list (eami-gateway, B-061) can report the real
// parameter shape a spec-generated action expects instead of B-061's
// original generic {"type":"object"} fallback. A manually-defined mapping
// (B-046, pre-dating this field) simply omits it and keeps working exactly
// as before -- toolToResp/CreateTool/UpdateTool never require it.
type ActionPathMapping struct {
	Path        string         `json:"path"`
	Method      string         `json:"method"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// ── AI provider connectors (Thread A Model 1) ───────────────────────────────────

// validAIProviders is the allowlist of provider identifiers eami-gateway
// has a real Adapter registered for (see eami-gateway/internal/aiprovider).
// Deliberately a Go-level allowlist, not a DB CHECK constraint on
// gateway_tools.provider -- adding a provider is one entry here plus one
// gateway adapter, not another migration (AC2: "ready for a second
// provider... without UI rework" extends to not needing a schema change
// either).
var validAIProviders = map[string]bool{"claude": true}

var validAuditModes = map[string]bool{"full": true, "structural_metadata_only": true}

const defaultAuditMode = "structural_metadata_only"

// validDataHandling (B-078) mirrors validAuditModes' exact shape and
// purpose. This is a VISIBILITY designation, not a technical control --
// EAMI cannot enforce what a third-party AI provider does with dispatched
// data, this only records what the admin has confirmed the actual
// commercial agreement says. "unknown" is the fail-safe default (see
// defaultDataHandling below), not any specific retention posture -- a
// newly created connector must never silently imply an agreement nobody
// has actually confirmed.
var validDataHandling = map[string]bool{"zero_retention": true, "standard_retention": true, "unknown": true}

const defaultDataHandling = "unknown"

// maxCustomRedactionPatterns caps how many custom_patterns entries a
// single connector's redaction_rules may declare (B-156/B-167, security-
// review finding). Each pattern is recompiled and evaluated against every
// string in every outbound dispatch's params on the connector's hot
// dispatch path -- an unbounded count lets a misconfigured or malicious
// admin session multiply per-dispatch CPU cost arbitrarily. 50 is
// generously above any realistic legitimate use (a handful of org-
// specific identifiers) while still bounding the worst case.
const maxCustomRedactionPatterns = 50

// validateRedactionRules validates a submitted redaction_rules object
// (B-156/B-167) by round-tripping it through the exact parser eami-gateway
// uses at dispatch time -- see redaction.ParseConfig's own doc comment for
// the shape ({"enabled": bool, "disabled_patterns": [...], "custom_
// patterns": [{"name","pattern"}]}). Rejecting an invalid custom regex
// here, at write time, is deliberate: eami-gateway's own Router.Dispatch
// fails closed (a clean error) on a malformed value rather than silently
// skipping redaction, but catching it here means an admin sees a clear 400
// immediately instead of discovering it only when a real dispatch fails.
// eami-api cannot import eami-gateway/internal/redaction (separate Go
// modules per go.work, same established boundary as ActionPathMapping's
// own doc comment) -- this is a deliberately narrow, independent
// re-validation of the same wire shape, not a shared implementation.
func validateRedactionRules(raw json.RawMessage) error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var cfg struct {
		Enabled          *bool    `json:"enabled"`
		DisabledPatterns []string `json:"disabled_patterns"`
		CustomPatterns   []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
		} `json:"custom_patterns"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("redaction_rules must be a JSON object: %w", err)
	}
	if len(cfg.CustomPatterns) > maxCustomRedactionPatterns {
		return fmt.Errorf("redaction_rules: at most %d custom_patterns are allowed, got %d", maxCustomRedactionPatterns, len(cfg.CustomPatterns))
	}
	for _, cp := range cfg.CustomPatterns {
		if strings.TrimSpace(cp.Name) == "" {
			return errors.New("redaction_rules: custom_patterns entry has an empty name")
		}
		re, err := regexp.Compile(cp.Pattern)
		if err != nil {
			return fmt.Errorf("redaction_rules: custom pattern %q does not compile: %w", cp.Name, err)
		}
		// A pattern that matches the empty string (e.g. "", "a*", `\b`)
		// matches at every rune boundary in any real dispatch content --
		// each match still gets replaced with a real mask token, so this
		// is a resource-amplification lever (output size grows roughly
		// linearly with input length) rather than a correctness bug, but
		// it can never be a legitimate sensitive-value detector either --
		// rejected outright rather than silently accepted (security-review
		// finding).
		if re.MatchString("") {
			return fmt.Errorf("redaction_rules: custom pattern %q matches the empty string -- refine it to match a real value, not every position", cp.Name)
		}
	}
	return nil
}

var allowedActionPathMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// validateActionPaths normalizes and validates a submitted action_paths
// map: every entry needs a non-empty action name and path; method defaults
// to POST (matching toolrouter.Forward's own default) and is restricted to
// a fixed allow-list. A nil/empty input returns (nil, nil) -- callers use
// that together with whether the field was present at all in the request
// body to distinguish "don't change action_paths" from "clear it".
func validateActionPaths(m map[string]ActionPathMapping) (map[string]ActionPathMapping, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]ActionPathMapping, len(m))
	for action, entry := range m {
		if strings.TrimSpace(action) == "" {
			return nil, errors.New("action_paths: action name cannot be empty")
		}
		if strings.TrimSpace(entry.Path) == "" {
			return nil, fmt.Errorf("action_paths[%s]: path cannot be empty", action)
		}
		// path is always joined onto base_url as a literal path segment
		// (toolrouter.joinURLPath, eami-gateway) -- never resolved as a
		// separate/relative URL -- so a scheme-qualified value here can
		// never actually redirect a dispatch to a different host. Rejected
		// anyway at write time: an admin pasting a full URL by mistake is a
		// real, easy-to-make error worth catching early with a clear
		// message, rather than silently accepting it and having it show up
		// as an odd literal path segment on base_url later.
		if strings.Contains(entry.Path, "://") {
			return nil, fmt.Errorf("action_paths[%s]: path must be relative to the tool's base_url, not a full URL", action)
		}
		method := strings.ToUpper(strings.TrimSpace(entry.Method))
		if method == "" {
			method = http.MethodPost
		}
		if !allowedActionPathMethods[method] {
			return nil, fmt.Errorf("action_paths[%s]: unsupported method %q", action, entry.Method)
		}
		out[action] = ActionPathMapping{Path: entry.Path, Method: method, InputSchema: entry.InputSchema}
	}
	return out, nil
}

// ── Response types ────────────────────────────────────────────────────────────

type ToolResp struct {
	ID                      uuid.UUID                    `json:"id"`
	Name                    string                       `json:"name"`
	Type                    string                       `json:"type"`
	AuthType                string                       `json:"auth_type"`
	MCPCommand              *string                      `json:"mcp_command,omitempty"`
	BaseURL                 *string                      `json:"base_url,omitempty"`
	Status                  string                       `json:"status"`
	LastUsed                *time.Time                   `json:"last_used,omitempty"`
	CreatedAt               time.Time                    `json:"created_at"`
	ActionPaths             map[string]ActionPathMapping `json:"action_paths,omitempty"`
	Provider                *string                      `json:"provider,omitempty"`
	AuditMode               string                       `json:"audit_mode"`
	DataHandlingDesignation string                       `json:"data_handling_designation"`
	DataHandlingNote        *string                      `json:"data_handling_note,omitempty"`
	// RedactionRules (B-156/B-167) is the connector's raw redaction_rules
	// JSON, or nil when it has no override (the fail-safe default applies
	// -- see redaction.DefaultConfig). Passed through as opaque JSON
	// rather than a typed struct: the admin UI only needs to display/edit
	// it, not interpret its meaning server-side.
	RedactionRules json.RawMessage `json:"redaction_rules,omitempty"`
}

func toolToResp(t store.GatewayTool) ToolResp {
	r := ToolResp{
		ID: t.ID, Name: t.Name, Type: t.Type,
		AuthType: t.AuthType, Status: t.Status, CreatedAt: t.CreatedAt,
		AuditMode:               t.AuditMode,
		DataHandlingDesignation: t.DataHandlingDesignation,
	}
	if t.MCPCommand.Valid {
		r.MCPCommand = &t.MCPCommand.String
	}
	if t.BaseURL.Valid {
		r.BaseURL = &t.BaseURL.String
	}
	if t.Provider.Valid {
		r.Provider = &t.Provider.String
	}
	if t.DataHandlingNote.Valid {
		r.DataHandlingNote = &t.DataHandlingNote.String
	}
	if t.LastUsed.Valid {
		ts := t.LastUsed.Time
		r.LastUsed = &ts
	}
	if len(t.ActionPaths) > 0 {
		// Malformed stored JSON would only happen via a direct DB write
		// bypassing this API's validation -- surfacing it as "no mappings"
		// rather than a 500 keeps ListTools/GetTool resilient to that.
		_ = json.Unmarshal(t.ActionPaths, &r.ActionPaths)
	}
	if len(t.RedactionRules) > 0 {
		r.RedactionRules = t.RedactionRules
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
		Name                    string                       `json:"name"`
		Type                    string                       `json:"type"`
		AuthType                string                       `json:"auth_type"`
		MCPCommand              *string                      `json:"mcp_command"`
		MCPArgs                 []string                     `json:"mcp_args"`
		BaseURL                 *string                      `json:"base_url"`
		Credentials             json.RawMessage              `json:"credentials"`
		ActionPaths             map[string]ActionPathMapping `json:"action_paths"`
		Provider                *string                      `json:"provider"`
		AuditMode               *string                      `json:"audit_mode"`
		DataHandlingDesignation *string                      `json:"data_handling_designation"`
		DataHandlingNote        *string                      `json:"data_handling_note"`
		RedactionRules          json.RawMessage              `json:"redaction_rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name == "" || body.Type == "" || body.AuthType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name, type, and auth_type are required")
		return
	}
	if body.Type == "ai_provider" {
		if body.Provider == nil || !validAIProviders[*body.Provider] {
			writeError(w, http.StatusBadRequest, "bad_request", "type ai_provider requires a supported provider")
			return
		}
		// api_key is the only credential shape aiprovider.Credentials/
		// ClaudeAdapter understand -- catching a wrong auth_type here,
		// not at dispatch time, means a misconfigured connector fails
		// loudly at creation (easy to fix) instead of on every real call
		// with a confusing "no api_key configured" error (code review
		// finding, this task).
		if body.AuthType != "api_key" {
			writeError(w, http.StatusBadRequest, "bad_request", "type ai_provider requires auth_type \"api_key\"")
			return
		}
	} else if body.Provider != nil {
		// provider is only meaningful for type=ai_provider -- reject it
		// outright for every other type rather than silently persisting
		// it unused (code review finding, this task; mirrors UpdateTool's
		// identical guard for the same reason).
		writeError(w, http.StatusBadRequest, "bad_request", "provider can only be set on an ai_provider-type tool")
		return
	}
	if body.Type != "ai_provider" && (body.DataHandlingDesignation != nil || body.DataHandlingNote != nil) {
		// Same guard as provider immediately above, same reason (B-078):
		// data_handling_* is only meaningful for ai_provider -- a
		// data-retention designation on a rest_api/mcp/database connector
		// would be misleading persisted state, not a harmless no-op.
		writeError(w, http.StatusBadRequest, "bad_request", "data_handling_designation/data_handling_note can only be set on an ai_provider-type tool")
		return
	}
	if body.Type != "ai_provider" && len(body.RedactionRules) > 0 && strings.TrimSpace(string(body.RedactionRules)) != "null" {
		// Same guard, same reason, for redaction_rules (B-156/B-167):
		// pattern-based redaction only ever runs on the ai_provider
		// dispatch path.
		writeError(w, http.StatusBadRequest, "bad_request", "redaction_rules can only be set on an ai_provider-type tool")
		return
	}
	if err := validateRedactionRules(body.RedactionRules); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	auditMode := defaultAuditMode
	if body.AuditMode != nil {
		if !validAuditModes[*body.AuditMode] {
			writeError(w, http.StatusBadRequest, "bad_request", "audit_mode must be \"full\" or \"structural_metadata_only\"")
			return
		}
		auditMode = *body.AuditMode
	}
	dataHandling := defaultDataHandling
	if body.DataHandlingDesignation != nil {
		if !validDataHandling[*body.DataHandlingDesignation] {
			writeError(w, http.StatusBadRequest, "bad_request", "data_handling_designation must be \"zero_retention\", \"standard_retention\", or \"unknown\"")
			return
		}
		dataHandling = *body.DataHandlingDesignation
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

	actionPaths, err := validateActionPaths(body.ActionPaths)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var actionPathsJSON []byte
	if actionPaths != nil {
		actionPathsJSON, err = json.Marshal(actionPaths)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode action_paths")
			return
		}
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
		OrgID:                   uc.OrgID,
		Name:                    body.Name,
		Type:                    body.Type,
		AuthType:                body.AuthType,
		MCPCommand:              body.MCPCommand,
		MCPArgs:                 body.MCPArgs,
		BaseURL:                 body.BaseURL,
		CredentialsEncrypted:    encrypted,
		ActionPaths:             actionPathsJSON,
		Provider:                body.Provider,
		AuditMode:               auditMode,
		DataHandlingDesignation: dataHandling,
		DataHandlingNote:        body.DataHandlingNote,
		RedactionRules:          []byte(body.RedactionRules),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toolToResp(t))
}

// UpdateTool handles PATCH /v1/gateway/tools/{toolId} (B-045). Not yet
// documented in api/openapi.yaml (Architect-EAMI-owned, out of this task's
// scope) -- mirrors B-038's precedent of shipping an undocumented route
// and flagging the spec gap rather than touching that file. Partial
// update: only fields present in the request body are changed. name/
// base_url/mcp_command/mcp_args behave like a normal partial update;
// credentials is special-cased below so editing other fields never
// requires re-submitting a working secret.
func (s *Server) UpdateTool(w http.ResponseWriter, r *http.Request) {
	uc := claimsFromContext(r)
	toolID, err := uuid.Parse(chi.URLParam(r, "toolId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid tool ID")
		return
	}

	var body struct {
		Name                    *string                      `json:"name"`
		MCPCommand              *string                      `json:"mcp_command"`
		MCPArgs                 []string                     `json:"mcp_args"`
		BaseURL                 *string                      `json:"base_url"`
		Credentials             json.RawMessage              `json:"credentials"`
		ActionPaths             map[string]ActionPathMapping `json:"action_paths"`
		Provider                *string                      `json:"provider"`
		AuditMode               *string                      `json:"audit_mode"`
		DataHandlingDesignation *string                      `json:"data_handling_designation"`
		DataHandlingNote        *string                      `json:"data_handling_note"`
		RedactionRules          json.RawMessage              `json:"redaction_rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Provider != nil && !validAIProviders[*body.Provider] {
		writeError(w, http.StatusBadRequest, "bad_request", "provider is not a supported AI provider")
		return
	}
	if body.AuditMode != nil && !validAuditModes[*body.AuditMode] {
		writeError(w, http.StatusBadRequest, "bad_request", "audit_mode must be \"full\" or \"structural_metadata_only\"")
		return
	}
	if body.DataHandlingDesignation != nil && !validDataHandling[*body.DataHandlingDesignation] {
		writeError(w, http.StatusBadRequest, "bad_request", "data_handling_designation must be \"zero_retention\", \"standard_retention\", or \"unknown\"")
		return
	}
	if err := validateRedactionRules(body.RedactionRules); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// A present-but-empty name would otherwise pass straight through
	// COALESCE and blank out the tool's name (nothing else guards this --
	// the frontend's HTML `required` attribute is not a server-side
	// control, and this route has no generated-client/schema validation
	// backstop since it isn't documented in openapi.yaml yet). Matches
	// CreateTool's identical check for the same field.
	if body.Name != nil && *body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name cannot be empty")
		return
	}

	ts, ok := s.toolQueries()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "tool store is not configured")
		return
	}

	// provider/audit_mode/data_handling_* are only meaningful for
	// ai_provider-type connectors -- reusing GetToolForTest's existing read
	// (already in the toolStore interface, no new query added) to check
	// the row's actual current type before honoring any of them. Without
	// this, PATCH could silently set provider="claude" on e.g. a
	// database-type tool -- no dispatch path ever reads it there, but it
	// produces confusing, misleading state (code review finding, B-047;
	// data_handling_* extends the identical guard, B-078).
	if body.Provider != nil || body.AuditMode != nil || body.DataHandlingDesignation != nil || body.DataHandlingNote != nil || body.RedactionRules != nil {
		row, err := ts.GetToolForTest(r.Context(), uc.OrgID, toolID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "tool not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if row.Type != "ai_provider" {
			writeError(w, http.StatusBadRequest, "bad_request", "provider/audit_mode/data_handling_designation/data_handling_note/redaction_rules can only be set on an ai_provider-type connector")
			return
		}
	}

	// credentialsProvided distinguishes "field omitted" (preserve the
	// existing encrypted value -- AC2) from "field present" (re-encrypt --
	// AC3), the same way CreateTool already does. Omitted credentials means
	// encrypted stays nil, and UpdateTool's COALESCE(NULL, credentials_encrypted)
	// leaves the stored value exactly as it was -- never nulled out, never
	// requiring the admin to re-enter a secret they aren't changing.
	hasCredentials, err := credentialsProvided(body.Credentials)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	var encrypted []byte
	if hasCredentials {
		if s.toolCreds == nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"tool credential encryption is not configured; cannot store credentials")
			return
		}
		encrypted, err = s.toolCreds.Encrypt(body.Credentials)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to encrypt credentials")
			return
		}
	}

	// body.ActionPaths is nil for both an omitted field and an explicit
	// JSON null (encoding/json's documented behavior for maps) -- either
	// way that means "leave action_paths unchanged". A present, even
	// empty, {} object means "replace" -- encoded to the literal bytes
	// "{}" so it's non-nil and UpdateTool's COALESCE overwrites rather
	// than preserves, letting an admin explicitly clear all mappings.
	var actionPathsJSON []byte
	if body.ActionPaths != nil {
		actionPaths, verr := validateActionPaths(body.ActionPaths)
		if verr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", verr.Error())
			return
		}
		if len(actionPaths) == 0 {
			actionPathsJSON = []byte("{}")
		} else {
			actionPathsJSON, err = json.Marshal(actionPaths)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to encode action_paths")
				return
			}
		}
	}

	t, err := ts.UpdateTool(r.Context(), store.UpdateToolParams{
		ID:                      toolID,
		OrgID:                   uc.OrgID,
		Name:                    body.Name,
		MCPCommand:              body.MCPCommand,
		MCPArgs:                 body.MCPArgs,
		ActionPaths:             actionPathsJSON,
		BaseURL:                 body.BaseURL,
		CredentialsEncrypted:    encrypted,
		Provider:                body.Provider,
		AuditMode:               body.AuditMode,
		DataHandlingDesignation: body.DataHandlingDesignation,
		DataHandlingNote:        body.DataHandlingNote,
		// RedactionRules (B-156/B-167): nil for an omitted field (leave
		// unchanged, via COALESCE) -- unlike ActionPaths above, json.
		// RawMessage preserves the literal bytes of an explicit JSON
		// `null` rather than collapsing it to the same nil Go value as
		// "omitted", so an admin can distinguish "don't touch this" from
		// "clear my override back to the default" without the {}-sentinel
		// workaround ActionPaths needs for its map-typed field.
		RedactionRules: []byte(body.RedactionRules),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toolToResp(t))
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
