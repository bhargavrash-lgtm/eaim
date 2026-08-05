package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/registry"
)

// AgentResolver resolves an agent's short name (JWT sub without "agent:"
// prefix), scoped to a specific org, to its registry record.
// *registry.Registry satisfies this structurally; defined here as its own
// interface (mirroring internal/episode/http.go's identical pattern)
// purely as a test seam.
//
// Org-scoped, not LookupByName's plain by-name lookup: gateway_agents.name
// is only unique per (org_id, name) (schema.sql), and unlike every other
// caller of the registry, this handler's agent_name comes directly from
// caller-supplied request-body input with no JWT backing it (see the
// TRUST BOUNDARY comment below) — an unscoped lookup would let one org's
// caller resolve, and revoke a token for, a different org's
// identically-named agent. Found and fixed during this task's own
// investigation, not part of the original stated scope — see BACKLOG.md
// B-042.
type AgentResolver interface {
	LookupByNameAndOrg(ctx context.Context, name, orgID string) (*registry.AgentRecord, error)
}

// RevokeHandler serves POST /v1/gateway/tokens/{jti}/revoke (B-042).
//
// GATEWAY-LOCAL, SERVICE-KEY ONLY — NOT what api/openapi.yaml currently
// documents for this route. openapi.yaml declares `security: BearerAuth`,
// but BearerAuth is explicitly defined elsewhere in the same spec as "JWT
// (RS256) issued by POST /v1/auth/login" — eami-api's user-session JWT.
// eami-gateway has no code path to validate that token (different issuer,
// different signing key, different claims shape) and no human/role concept
// of its own. This deviation was investigated and confirmed deliberate
// (not a silent shortcut) — see BACKLOG.md B-042. The intended long-term
// shape, not built here, is an eami-api-hosted route (requireRole
// "admin"/"operator", matching /v1/gateway/agents*'s established
// precedent for this exact class of action) that proxies to this endpoint
// using the service key, mirroring B-002's episode-read proxy pattern
// exactly.
//
// TRUST BOUNDARY: nothing here cryptographically binds a given jti to the
// named agent — no issued-token ledger exists anywhere in this system, so
// there is no way to independently verify "this jti really was issued to
// this agent." This handler verifies only that (a) the caller holds the
// service key, and (b) (agent_name, org_id) resolves to a real
// gateway_agents row in that exact org (closing B-042's gap 2 for this
// endpoint specifically — a caller cannot silently lose a revocation to a
// stale/malformed/cross-org agent reference the way Issue()/HandleIssue's
// own unvalidated AgentID still can more broadly). A caller holding the
// service key is fully trusted to submit only legitimate (jti, agent_name,
// org_id) triples, identical to the trust model episode/http.go's own
// service-key path already documents for its own caller-supplied org_id
// query param. Enforcing finer-grained "which specific admin may revoke
// which specific agent's token" is the responsibility of whatever calls
// this endpoint (the future eami-api proxy above), not this handler.
//
// org_id is required, caller-supplied, and used only to scope the
// agent_name lookup (see AgentResolver's doc comment) — it does not by
// itself grant any additional capability beyond that scoping.
type RevokeHandler struct {
	identity   *Manager
	resolver   AgentResolver
	serviceKey string
}

// NewRevokeHandler returns a RevokeHandler. serviceKey is
// api.token_revoke_service_key / GATEWAY_TOKEN_REVOKE_SERVICE_KEY —
// deliberately separate from ServiceKey/EpisodeReadServiceKey so a leak of
// one does not also grant the others.
func NewRevokeHandler(idm *Manager, resolver AgentResolver, serviceKey string) *RevokeHandler {
	return &RevokeHandler{identity: idm, resolver: resolver, serviceKey: serviceKey}
}

type revokeRequest struct {
	// AgentName is the agent's short name (JWT sub without the "agent:"
	// prefix), matching registry.LookupByNameAndOrg's own parameter
	// contract directly -- not the raw Claims.Subject string.
	AgentName string `json:"agent_name"`
	// OrgID scopes the AgentName lookup -- required, since gateway_agents.name
	// is only unique per (org_id, name). See AgentResolver's doc comment.
	OrgID string `json:"org_id"`
}

// HandleRevoke handles POST /v1/gateway/tokens/{jti}/revoke.
func (h *RevokeHandler) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := r.Header.Get("X-Service-Key")
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(h.serviceKey)) != 1 {
		http.Error(w, "unauthorized: invalid or missing X-Service-Key", http.StatusUnauthorized)
		return
	}

	jti := r.PathValue("jti")
	if jti == "" {
		http.Error(w, "bad request: jti path segment is required", http.StatusBadRequest)
		return
	}

	var req revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.AgentName == "" {
		http.Error(w, "bad request: agent_name is required", http.StatusBadRequest)
		return
	}
	if _, err := uuid.Parse(req.OrgID); err != nil {
		http.Error(w, "bad request: org_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	rec, err := h.resolver.LookupByNameAndOrg(r.Context(), req.AgentName, req.OrgID)
	// ErrAgentSuspended still returns a populated record -- revoking a
	// token for an agent whose registration is already suspended/revoked
	// is a valid, even more justified action (e.g. an existing session
	// established before suspension), not an error case. Only a genuinely
	// missing/unresolvable agent (rec == nil) blocks the request -- there
	// is no valid agent_id to persist otherwise.
	if err != nil && !errors.Is(err, registry.ErrAgentSuspended) {
		http.Error(w, "forbidden: agent not registered: "+err.Error(), http.StatusForbidden)
		return
	}
	if rec == nil {
		http.Error(w, "forbidden: agent not registered", http.StatusForbidden)
		return
	}

	if err := h.identity.Revoke(jti, rec.ID); err != nil {
		slog.Error("identity: revoke request failed to persist", "jti", jti, "agent", req.AgentName, "err", err)
		http.Error(w, "internal error: revocation could not be persisted", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
