package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// IssueHandler serves POST /v1/gateway/tokens (B-098).
//
// Previously unauthenticated: anyone reaching the gateway's port could
// mint a valid AI-agent token for any existing agent name (Manager.Issue
// trusted req.AgentID as opaque input, straight into the JWT Subject).
// This handler closes that gap by requiring a real, unrevoked, unexpired,
// agent-scoped api_keys row (the same api_keys feature the Settings UI
// already manages, previously enforced nowhere -- GetAPIKeyByHash had zero
// callers). See BACKLOG.md's B-098 entry for the full investigation.
//
// TRUST BOUNDARY / the actual scoping proof: the requested agent is
// resolved via AgentResolver.LookupByNameAndOrg, scoped to the validated
// key's OWN org_id (never a client-supplied org) -- and the resolved
// agent's real UUID must equal the key's own bound agent_id. A key valid
// for agent A cannot mint a token for agent B, even within the same org.
type IssueHandler struct {
	manager  *Manager
	resolver AgentResolver
	keys     APIKeyValidator
	events   TokenEventStore
}

// NewIssueHandler returns an IssueHandler.
func NewIssueHandler(m *Manager, resolver AgentResolver, keys APIKeyValidator, events TokenEventStore) *IssueHandler {
	return &IssueHandler{manager: m, resolver: resolver, keys: keys, events: events}
}

// HandleIssue handles POST /v1/gateway/tokens.
func (h *IssueHandler) HandleIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key, err := h.keys.ValidateAPIKey(r.Context(), r.Header.Get("X-API-Key"))
	if err != nil {
		if errors.Is(err, ErrInvalidAPIKey) {
			http.Error(w, "unauthorized: invalid, missing, revoked, or expired X-API-Key", http.StatusUnauthorized)
			return
		}
		slog.Error("identity: api key validation failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if key.AgentID == "" {
		http.Error(w, "forbidden: API key is not scoped to an agent", http.StatusForbidden)
		return
	}

	var req IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.AgentID == "" {
		http.Error(w, "bad request: agent_id is required", http.StatusBadRequest)
		return
	}

	// req.AgentID arrives in the "agent:<name>" JWT-subject shape (see
	// internal/registry's doc comment) -- never trusted as opaque: resolved
	// against the real gateway_agents row, scoped to the validated key's
	// own org, not any org the caller claims. Unlike RevokeHandler (B-042),
	// which treats ErrAgentSuspended as still-valid-enough (revoking a
	// suspended agent's token is fine, even more justified), ISSUING a new
	// token to a suspended/revoked agent would be wrong -- any resolution
	// error, including suspension, blocks issuance here.
	agentName := strings.TrimPrefix(req.AgentID, "agent:")
	rec, err := h.resolver.LookupByNameAndOrg(r.Context(), agentName, key.OrgID)
	// A single, identical rejection message for both "no such agent in this
	// org" and "a real agent exists but this key isn't scoped to it" --
	// found during this brief's own security review: distinguishing the two
	// would let a valid-but-wrongly-scoped key enumerate which agent names
	// exist in its own org (a same-org-only, low-severity oracle, but free
	// to close by not distinguishing them at the HTTP layer).
	const scopeErr = "forbidden: agent not registered or not authorized for this API key"
	if err != nil {
		http.Error(w, scopeErr, http.StatusForbidden)
		return
	}
	// The actual cross-agent scoping proof (AC2): a key bound to a
	// DIFFERENT agent than the one resolved from the request is rejected,
	// even though both belong to the same org and the key itself is
	// otherwise perfectly valid.
	if rec.ID != key.AgentID {
		http.Error(w, scopeErr, http.StatusForbidden)
		return
	}

	// Subject is rebuilt from the resolved record's own canonical name, not
	// echoed from client input.
	req.AgentID = "agent:" + rec.Name
	resp, err := h.manager.Issue(req)
	if err != nil {
		slog.Error("identity: token issuance failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.events.RecordIssued(r.Context(), key.OrgID, rec.ID, rec.Name, key.ID, resp.JTI); err != nil {
		// Issuance already succeeded and the caller already has a valid
		// token -- a logging failure shouldn't fail the request (the token
		// is real and usable either way), but must not be silent.
		slog.Error("identity: failed to record token issuance event", "jti", resp.JTI, "agent", rec.Name, "err", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
