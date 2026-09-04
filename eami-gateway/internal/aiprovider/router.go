package aiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/redaction"
	"github.com/eami/gateway/internal/toolrouter"
)

// ErrNotFound is returned by Resolve when no ai_provider-type gateway_tools
// row matches the given org and name. Callers should fall back to their
// existing resolution order, not treat this as a hard failure -- mirrors
// toolrouter.ErrNotFound's identical convention.
var ErrNotFound = errors.New("aiprovider: no matching connector found")

// ToolRow is the subset of gateway_tools fields dispatch needs for an
// ai_provider-type row. A separate type from toolrouter.ToolRow (not an
// extension of it): toolrouter.Resolve's SELECT doesn't fetch provider or
// audit_mode, and toolrouter is read-only scope for this brief.
type ToolRow struct {
	ID                   string
	Provider             string
	AuthType             string
	CredentialsEncrypted []byte
	// AuditMode is "full" or "structural_metadata_only" (schema/
	// migrations-v2/000004). Governs only the audit_log write for calls
	// through this connector -- approval_requests and episodes are
	// unaffected, matching this brief's explicit scope (AC5's own
	// wording: "audit logging" specifically).
	AuditMode string
	// DataHandling is "zero_retention" | "standard_retention" | "unknown"
	// (schema/migrations-v2/000008, B-078) -- a VISIBILITY designation
	// only, never a technical control (EAMI cannot enforce what a
	// third-party provider does with dispatched data). Snapshotted into
	// the audit_log entry for each call through this connector, mirroring
	// AuditMode's own resolve-then-apply-once pattern, so a later change
	// to this connector's designation never retroactively alters an
	// already-written audit record.
	DataHandling string
	// RedactionRules is the raw JSONB bytes of gateway_tools.redaction_rules
	// (B-156/B-167), or nil for a connector with no override -- passed
	// as-is to redaction.ParseConfig, which treats nil identically to an
	// explicit {"enabled": true} (see that function's own doc comment).
	RedactionRules []byte
}

// Router resolves ai_provider gateway_tools rows and dispatches their
// calls to the matching registered Adapter.
type Router struct {
	pool     *pgxpool.Pool
	cipher   *toolrouter.Cipher // reused directly from B-022/B-044, not reinvented
	registry map[string]Adapter
}

// New creates a Router. cipher may be nil -- see Dispatch's handling of a
// row with stored credentials but no configured key, mirroring
// toolrouter.New's identical contract. registry maps provider identifiers
// (e.g. "claude") to their Adapter; a name with no entry is a clean
// rejection at Dispatch time, not a panic.
func New(pool *pgxpool.Pool, cipher *toolrouter.Cipher, registry map[string]Adapter) *Router {
	return &Router{pool: pool, cipher: cipher, registry: registry}
}

// Resolve looks up name within org's gateway_tools, org-scoped, restricted
// to type='ai_provider' -- the same org-safety discipline as
// toolrouter.Resolve (an unscoped lookup would let one org's agent route
// to, and exfiltrate credentials/traffic through, a different org's
// identically-named connector). Returns ErrNotFound when no ai_provider
// row matches (including when a row of that name exists but is a
// different type -- deliberately not surfaced as a different error, same
// as toolrouter's convention of a single not-found signal).
func (r *Router) Resolve(ctx context.Context, orgID, name string) (*ToolRow, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, provider, auth_type, credentials_encrypted, audit_mode, data_handling_designation, redaction_rules
		FROM gateway_tools
		WHERE org_id = $1 AND name = $2 AND type = 'ai_provider'
	`, orgID, name)

	var t ToolRow
	var provider *string
	if err := row.Scan(&t.ID, &provider, &t.AuthType, &t.CredentialsEncrypted, &t.AuditMode, &t.DataHandling, &t.RedactionRules); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("aiprovider: query connector %q: %w", name, err)
	}
	if provider != nil {
		t.Provider = *provider
	}
	return &t, nil
}

// Dispatch decrypts row's stored credentials, resolves the Adapter for
// row.Provider, and calls it. Every failure path here -- nil row, no
// provider configured, unregistered provider, undecryptable/malformed
// credentials -- is a clean rejection, never a panic, mirroring
// toolrouter.Forward's identical discipline for its own failure paths.
//
// Pattern-based redaction (B-156/B-167) runs here, immediately before the
// adapter.Dispatch call below -- this is the one real chokepoint both of
// this package's callers (cmd/gateway/dispatcher.go's immediate-Allow
// branch and internal/approval/router.go's escalation-resume path)
// converge through, confirmed by the B-156 investigation before this was
// built (see BACKLOG.md). params itself is never mutated -- only the copy
// handed to the adapter is redacted (see redaction.Redact's own doc
// comment for why: the caller's own map is also used to build the
// audit_log/episode snapshot, which must not silently change shape as a
// side effect of this call). The returned int is the total number of
// items redacted, always 0 for a connector with redaction disabled or no
// matching content -- callers thread it into their own audit.Entry.
func (r *Router) Dispatch(ctx context.Context, row *ToolRow, action string, params map[string]any) (Response, int, error) {
	if row == nil {
		return Response{}, 0, errors.New("aiprovider: Dispatch called with a nil row")
	}
	if row.Provider == "" {
		return Response{}, 0, fmt.Errorf("aiprovider: connector %s has no provider configured", row.ID)
	}
	adapter, ok := r.registry[row.Provider]
	if !ok {
		return Response{}, 0, fmt.Errorf("aiprovider: connector %s has provider %q, which is not a registered adapter", row.ID, row.Provider)
	}

	var creds Credentials
	if len(row.CredentialsEncrypted) > 0 {
		if r.cipher == nil {
			return Response{}, 0, fmt.Errorf("aiprovider: connector %s has stored credentials but no decryption key is configured", row.ID)
		}
		plaintext, err := r.cipher.Decrypt(row.CredentialsEncrypted)
		if err != nil {
			return Response{}, 0, fmt.Errorf("aiprovider: connector %s credentials could not be decrypted: %w", row.ID, err)
		}
		if err := json.Unmarshal(plaintext, &creds); err != nil {
			return Response{}, 0, fmt.Errorf("aiprovider: connector %s stored credentials are not in the expected shape", row.ID)
		}
	}

	redactionCfg, err := redaction.ParseConfig(row.RedactionRules)
	if err != nil {
		return Response{}, 0, fmt.Errorf("aiprovider: connector %s: %w", row.ID, err)
	}
	redactedParams, redactedCount, err := redaction.Redact(params, redactionCfg)
	if err != nil {
		return Response{}, 0, fmt.Errorf("aiprovider: connector %s: redaction failed: %w", row.ID, err)
	}

	resp, err := adapter.Dispatch(ctx, creds, Request{Action: action, Params: redactedParams})
	return resp, redactedCount, err
}
