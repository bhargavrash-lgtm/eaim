package store

import (
	"context"

	"github.com/google/uuid"
)

// ListTools returns all tools for an org ordered by name.
func (q *Queries) ListTools(ctx context.Context, orgID uuid.UUID) ([]GatewayTool, error) {
	const sql = `
SELECT id, org_id, name, type, auth_type, mcp_command, base_url,
       status, last_used, last_tested, created_at, action_paths,
       provider, audit_mode, data_handling_designation, data_handling_note,
       redaction_rules
FROM gateway_tools
WHERE org_id = $1
ORDER BY name ASC`

	rows, err := q.db.Query(ctx, sql, toPgtypeUUID(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []GatewayTool
	for rows.Next() {
		var t GatewayTool
		if err := rows.Scan(
			&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
			&t.MCPCommand, &t.BaseURL, &t.Status,
			&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
			&t.Provider, &t.AuditMode, &t.DataHandlingDesignation, &t.DataHandlingNote,
			&t.RedactionRules,
		); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// CreateToolParams holds fields for inserting a new tool.
type CreateToolParams struct {
	OrgID      uuid.UUID
	Name       string
	Type       string
	AuthType   string
	MCPCommand *string
	MCPArgs    []string
	BaseURL    *string
	// CredentialsEncrypted is the AES-256-GCM-sealed credentials blob (see
	// internal/toolcreds), or nil if the tool has no secrets to store. Never
	// read back by any query in this file -- GatewayTool has no field for
	// it, so it cannot leak through ListTools/CreateTool's own RETURNING.
	CredentialsEncrypted []byte
	// ActionPaths is the raw JSONB bytes for gateway_tools.action_paths
	// (B-046), or nil for a tool with no per-action mappings.
	ActionPaths []byte
	// Provider is the AI provider identifier for type=ai_provider tools
	// (e.g. "claude"), or nil for every other type.
	Provider *string
	// AuditMode is "full" or "structural_metadata_only" -- always set by
	// the caller (tools.go's CreateTool handler defaults it explicitly
	// rather than relying on the column's DB DEFAULT, since this INSERT
	// always lists the column).
	AuditMode string
	// DataHandlingDesignation is "zero_retention" | "standard_retention" |
	// "unknown" (B-078) -- like AuditMode, always set by the caller
	// (defaulted explicitly below, not relying on the column's DB
	// DEFAULT, since this INSERT always lists the column).
	DataHandlingDesignation string
	// DataHandlingNote is nil for "not set" -- unlike DataHandlingDesignation,
	// this column has no NOT NULL/default, so nil is a real, meaningful value.
	DataHandlingNote *string
	// RedactionRules is the raw JSONB bytes for gateway_tools.redaction_rules
	// (B-156/B-167), or nil for a connector with no override (the fail-safe
	// default applies at dispatch time -- see eami-gateway/internal/
	// redaction.DefaultConfig).
	RedactionRules []byte
}

// CreateTool inserts a new gateway tool and returns it. AuditMode defaults
// to "structural_metadata_only" here (not only in tools.go's handler) so
// the fail-safe default holds for every caller of this function, not just
// ones that remember to set it -- matching this column's own CHECK
// constraint, which rejects an empty string.
func (q *Queries) CreateTool(ctx context.Context, p CreateToolParams) (GatewayTool, error) {
	if p.AuditMode == "" {
		p.AuditMode = "structural_metadata_only"
	}
	if p.DataHandlingDesignation == "" {
		p.DataHandlingDesignation = "unknown"
	}
	const sql = `
INSERT INTO gateway_tools (org_id, name, type, auth_type, mcp_command, mcp_args, base_url, credentials_encrypted, action_paths, provider, audit_mode, data_handling_designation, data_handling_note, redaction_rules)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING id, org_id, name, type, auth_type, mcp_command, base_url,
          status, last_used, last_tested, created_at, action_paths,
          provider, audit_mode, data_handling_designation, data_handling_note,
          redaction_rules`

	var t GatewayTool
	err := q.db.QueryRow(ctx, sql,
		toPgtypeUUID(p.OrgID), p.Name, p.Type, p.AuthType,
		toPgtypeText(p.MCPCommand), p.MCPArgs, toPgtypeText(p.BaseURL),
		p.CredentialsEncrypted, p.ActionPaths,
		toPgtypeText(p.Provider), p.AuditMode,
		p.DataHandlingDesignation, toPgtypeText(p.DataHandlingNote),
		p.RedactionRules,
	).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
		&t.MCPCommand, &t.BaseURL, &t.Status,
		&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
		&t.Provider, &t.AuditMode, &t.DataHandlingDesignation, &t.DataHandlingNote,
		&t.RedactionRules,
	)
	return t, err
}

// UpdateToolParams holds fields for a partial tool update (B-045). Only
// non-nil/non-empty fields are applied -- a nil field leaves that column
// unchanged (COALESCE), matching UpdateAgent's established convention for
// this exact "optional partial update" shape. CredentialsEncrypted is nil
// when the caller didn't submit new credentials (existing value preserved)
// vs the newly encrypted blob when they did -- the encryption/decision of
// which case applies happens in tools.go's UpdateTool handler, not here.
type UpdateToolParams struct {
	ID                   uuid.UUID
	OrgID                uuid.UUID
	Name                 *string
	MCPCommand           *string
	MCPArgs              []string
	BaseURL              *string
	CredentialsEncrypted []byte
	// ActionPaths, like MCPArgs, is nil to leave action_paths unchanged
	// (COALESCE) and non-nil -- including []byte("{}") to explicitly clear
	// all mappings -- to overwrite it.
	ActionPaths []byte
	// Provider, like Name, is nil to leave provider unchanged (COALESCE).
	// auth_type/type stay immutable per this function's existing
	// precedent below, but provider is intentionally editable -- an admin
	// correcting which provider a connector points at is an operational
	// fix, not a fundamental type change.
	Provider *string
	// AuditMode, like Provider, is nil to leave audit_mode unchanged
	// (COALESCE) -- lets an admin flip a connector between "full" and
	// "structural_metadata_only" after creation without touching anything
	// else.
	AuditMode *string
	// DataHandlingDesignation (B-078), like AuditMode, is nil to leave it
	// unchanged (COALESCE) -- lets an admin update this independently of
	// every other field.
	DataHandlingDesignation *string
	// DataHandlingNote, like Name/BaseURL/MCPCommand, is nil to leave it
	// unchanged (COALESCE) and a non-nil pointer -- including one pointing
	// at "" -- to overwrite it. An explicit empty string is a real,
	// meaningful value here (not SQL NULL), so COALESCE correctly applies
	// it rather than falling back to the existing note: this is how an
	// admin clears a previously-set note via the UI.
	DataHandlingNote *string
	// RedactionRules (B-156/B-167), like ActionPaths, is nil to leave
	// redaction_rules unchanged (COALESCE) and non-nil -- including
	// []byte("{}") -- to overwrite it. An admin explicitly clearing an
	// override (reverting to the fail-safe default) sends redaction_rules
	// as an explicit JSON null, which tools.go's handler encodes as the
	// literal bytes "null" here (non-nil, so COALESCE overwrites), not
	// left nil (which would mean "don't touch it").
	RedactionRules []byte
}

// UpdateTool applies a partial update to an existing tool and returns the
// updated row. type/auth_type are deliberately not editable here -- an
// admin changing the fundamental integration type/auth mechanism is
// closer to "delete and recreate" than a partial edit, matching
// UpdateAgent's own precedent of only exposing operational fields.
func (q *Queries) UpdateTool(ctx context.Context, p UpdateToolParams) (GatewayTool, error) {
	const sql = `
UPDATE gateway_tools SET
    name                       = COALESCE($3, name),
    mcp_command                = COALESCE($4, mcp_command),
    mcp_args                   = COALESCE($5, mcp_args),
    base_url                   = COALESCE($6, base_url),
    credentials_encrypted      = COALESCE($7, credentials_encrypted),
    action_paths               = COALESCE($8, action_paths),
    provider                   = COALESCE($9, provider),
    audit_mode                 = COALESCE($10, audit_mode),
    data_handling_designation  = COALESCE($11, data_handling_designation),
    data_handling_note         = COALESCE($12, data_handling_note),
    redaction_rules            = COALESCE($13, redaction_rules)
WHERE id = $1 AND org_id = $2
RETURNING id, org_id, name, type, auth_type, mcp_command, base_url,
          status, last_used, last_tested, created_at, action_paths,
          provider, audit_mode, data_handling_designation, data_handling_note,
          redaction_rules`

	var t GatewayTool
	err := q.db.QueryRow(ctx, sql,
		toPgtypeUUID(p.ID), toPgtypeUUID(p.OrgID),
		toPgtypeText(p.Name), toPgtypeText(p.MCPCommand), p.MCPArgs, toPgtypeText(p.BaseURL),
		p.CredentialsEncrypted, p.ActionPaths,
		toPgtypeText(p.Provider), toPgtypeText(p.AuditMode),
		toPgtypeText(p.DataHandlingDesignation), toPgtypeText(p.DataHandlingNote),
		p.RedactionRules,
	).Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Type, &t.AuthType,
		&t.MCPCommand, &t.BaseURL, &t.Status,
		&t.LastUsed, &t.LastTested, &t.CreatedAt, &t.ActionPaths,
		&t.Provider, &t.AuditMode, &t.DataHandlingDesignation, &t.DataHandlingNote,
		&t.RedactionRules,
	)
	return t, err
}

// DeleteTool removes a tool by ID scoped to an org. Returns false if not found.
func (q *Queries) DeleteTool(ctx context.Context, orgID, toolID uuid.UUID) (bool, error) {
	const sql = `DELETE FROM gateway_tools WHERE id = $1 AND org_id = $2`
	tag, err := q.db.Exec(ctx, sql, toPgtypeUUID(toolID), toPgtypeUUID(orgID))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkToolTested updates last_tested and status for a tool.
func (q *Queries) MarkToolTested(ctx context.Context, orgID, toolID uuid.UUID, status string, latencyMs int) error {
	const sql = `
UPDATE gateway_tools
SET last_tested = NOW(), status = $3, test_latency_ms = $4
WHERE id = $1 AND org_id = $2`
	_, err := q.db.Exec(ctx, sql,
		toPgtypeUUID(toolID), toPgtypeUUID(orgID), status, latencyMs)
	return err
}
