package api

// B-253, "operators contain; admins expand or destroy" -- the field-level
// half of the RBAC split. Route groups (router.go) make whole routes
// admin-only; this file covers the two PATCH routes operators may still
// use, where some fields are containment/neutral and others are expansions:
//
//   PATCH /v1/gateway/agents/{id}
//     containment (operator ok): status -> suspended | revoked
//     admin only:  status -> active from any other status (reactivate);
//                  scope (drift detection + token claims), risk_tier (token
//                  claims), token_ttl_seconds (token issuance)
//   PATCH /v1/gateway/tools/{id}
//     descriptive (operator ok): data_handling_note
//     admin only:  name (tool resolution + policy tool_names + audit),
//                  mcp_command, mcp_args (founder: default admin; inert
//                  today), base_url, credentials, action_paths (dispatch
//                  routing + approval config hash), provider, audit_mode,
//                  data_handling_designation (snapshotted into audit),
//                  redaction_rules
//
// A restricted field counts only when it is present AND differs from the
// stored value, so a client that echoes unchanged values (the UI's edit
// forms send every field) is not rejected. For non-admins the unchanged
// restricted fields are also dropped from the write, so an echoed value can
// never race an admin's concurrent change (e.g. an echoed status "active"
// re-activating an agent an admin just suspended). A request with ANY
// changed restricted field is rejected whole, before anything is written,
// with requireRole's exact 403 body (writeRoleForbidden).

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// agentAdminOnlyChange reports whether req changes an admin-only agent
// field relative to cur, and strips unchanged restricted fields from req.
func agentAdminOnlyChange(req *AgentUpdateRequest, cur store.GatewayAgent) bool {
	restricted := false
	if req.Status != nil {
		switch {
		case *req.Status == cur.Status:
			req.Status = nil // no-op: never written by a non-admin
		case *req.Status == "active":
			restricted = true // reactivation is an expansion
		}
	}
	if req.Scope != nil {
		if *req.Scope == cur.Scope {
			req.Scope = nil
		} else {
			restricted = true
		}
	}
	if req.RiskTier != nil {
		if *req.RiskTier == cur.RiskTier {
			req.RiskTier = nil
		} else {
			restricted = true
		}
	}
	if req.TokenTTLSeconds != nil {
		if int32(*req.TokenTTLSeconds) == cur.TokenTTLSeconds {
			req.TokenTTLSeconds = nil
		} else {
			restricted = true
		}
	}
	return restricted
}

// toolAdminFields is the stored value of every admin-only tool field.
type toolAdminFields struct {
	Name                    string
	MCPCommand              pgtype.Text
	MCPArgs                 []string
	BaseURL                 pgtype.Text
	ActionPaths             []byte
	Provider                pgtype.Text
	AuditMode               string
	DataHandlingDesignation string
	RedactionRules          []byte
}

// loadToolAdminFields reads a tool's admin-only fields, org-scoped.
// Returns pgx.ErrNoRows for a missing or other-org tool.
func loadToolAdminFields(ctx context.Context, q *store.Queries, orgID, toolID uuid.UUID) (toolAdminFields, error) {
	var f toolAdminFields
	err := q.DB().QueryRow(ctx, `
		SELECT name, mcp_command, mcp_args, base_url, action_paths, provider,
		       audit_mode, data_handling_designation, redaction_rules
		FROM gateway_tools WHERE id = $1 AND org_id = $2`, toolID, orgID,
	).Scan(&f.Name, &f.MCPCommand, &f.MCPArgs, &f.BaseURL, &f.ActionPaths, &f.Provider,
		&f.AuditMode, &f.DataHandlingDesignation, &f.RedactionRules)
	return f, err
}

func textEq(p *string, stored pgtype.Text) bool {
	if !stored.Valid {
		return *p == ""
	}
	return *p == stored.String
}

// jsonObjEq compares two JSON objects semantically; empty, null and {} are
// all "no entries".
func jsonObjEq(a, b []byte) bool {
	norm := func(raw []byte) map[string]any {
		m := map[string]any{}
		if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
			return m
		}
		_ = json.Unmarshal(raw, &m)
		return m
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// redactionRulesEq compares redaction rules by meaning: a stored NULL (and
// an explicit JSON null) is the fail-safe default -- enabled, no disabled
// patterns, no custom patterns -- and disabled patterns are a set.
func redactionRulesEq(a, b []byte) bool {
	type rules struct {
		Enabled          *bool            `json:"enabled"`
		DisabledPatterns []string         `json:"disabled_patterns"`
		CustomPatterns   []map[string]any `json:"custom_patterns"`
	}
	norm := func(raw []byte) (bool, []string, []map[string]any) {
		var r rules
		if s := strings.TrimSpace(string(raw)); s != "" && s != "null" {
			_ = json.Unmarshal(raw, &r)
		}
		enabled := r.Enabled == nil || *r.Enabled
		dp := append([]string{}, r.DisabledPatterns...)
		sort.Strings(dp)
		cp := r.CustomPatterns
		if cp == nil {
			cp = []map[string]any{}
		}
		return enabled, dp, cp
	}
	ae, ad, ac := norm(a)
	be, bd, bc := norm(b)
	return ae == be && reflect.DeepEqual(ad, bd) && reflect.DeepEqual(ac, bc)
}

// toolUpdateFields is the subset of UpdateTool's request this check needs.
type toolUpdateFields struct {
	Name                    *string
	MCPCommand              *string
	MCPArgs                 []string
	BaseURL                 *string
	HasCredentials          bool
	ActionPathsJSON         []byte // normalized, nil when omitted
	Provider                *string
	AuditMode               *string
	DataHandlingDesignation *string
	RedactionRules          json.RawMessage // nil when omitted
}

// toolAdminOnlyChange reports whether u changes an admin-only tool field
// relative to cur, and clears unchanged restricted fields in u.
func toolAdminOnlyChange(u *toolUpdateFields, cur toolAdminFields) bool {
	restricted := u.HasCredentials // any credential write is a rotation
	check := func(p **string, eq bool) {
		if *p == nil {
			return
		}
		if eq {
			*p = nil
		} else {
			restricted = true
		}
	}
	if u.Name != nil {
		check(&u.Name, *u.Name == cur.Name)
	}
	if u.MCPCommand != nil {
		check(&u.MCPCommand, textEq(u.MCPCommand, cur.MCPCommand))
	}
	if u.BaseURL != nil {
		check(&u.BaseURL, textEq(u.BaseURL, cur.BaseURL))
	}
	if u.Provider != nil {
		check(&u.Provider, textEq(u.Provider, cur.Provider))
	}
	if u.AuditMode != nil {
		check(&u.AuditMode, *u.AuditMode == cur.AuditMode)
	}
	if u.DataHandlingDesignation != nil {
		check(&u.DataHandlingDesignation, *u.DataHandlingDesignation == cur.DataHandlingDesignation)
	}
	if u.MCPArgs != nil {
		stored := cur.MCPArgs
		if stored == nil {
			stored = []string{}
		}
		if reflect.DeepEqual(append([]string{}, u.MCPArgs...), append([]string{}, stored...)) {
			u.MCPArgs = nil
		} else {
			restricted = true
		}
	}
	if u.ActionPathsJSON != nil {
		if jsonObjEq(u.ActionPathsJSON, cur.ActionPaths) {
			u.ActionPathsJSON = nil
		} else {
			restricted = true
		}
	}
	if u.RedactionRules != nil {
		if redactionRulesEq(u.RedactionRules, cur.RedactionRules) {
			u.RedactionRules = nil
		} else {
			restricted = true
		}
	}
	return restricted
}
