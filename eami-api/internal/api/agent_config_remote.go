package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/store"
)

// AgentRemoteConfig handles GET /v1/agents/{agent_id}/config -- the exact
// route eami-collector's ConfigProxyHandler has always proxied eami-agent's
// own remote-config poll to (see eami-collector's internal/api/
// config_proxy.go), which never existed anywhere in eami-api's router
// until now (B-165). Every deployed eami-agent has been polling this exact
// path and silently receiving 404s since the feature was built -- both
// eami-agent's FetchConfig and the collector's proxy treat any non-200 as
// "not registered yet, not an error," so nothing ever surfaced the gap.
//
// Auth: X-Service-Key (requireServiceKey middleware, see router.go) --
// eami-agent's poll is a machine-to-machine call relayed through
// eami-collector's own service-key-authenticated identity, never a user
// JWT. The pre-existing GET /v1/gateway/agents/{agentId}/config (admin-
// facing, JWT-role-gated) is a structurally different route serving the
// same underlying agent_configs data: it's keyed on a real
// gateway_agents.id an admin already knows, and its middleware
// (requireRole, reading JWT claims) would reject a service-key-only
// caller regardless of URL -- confirmed directly against middleware.go
// before choosing to add a new route here rather than just correcting a
// URL string.
//
// Identity resolution is the real reason this can't just reuse the admin
// route directly: eami-agent's own agentID here is its free-text
// discovery identity (its config-file agent_id, or OS hostname fallback --
// see B-164's Part B), which has no relationship to a gateway_agents.id
// except through an explicit admin link (B-164's endpoints.gateway_agent_id).
// This handler resolves that link and serves the linked agent's real
// config; if none exists, it 404s -- the same "not registered yet"
// contract eami-agent already handles gracefully, since an unlinked
// endpoint genuinely has no governed config to receive.
//
// Single-tenant v1: resolves the endpoint via GetDefaultOrgID, the same
// convention this file's sibling ingest.go already uses for every other
// collector-facing write.
func (s *Server) AgentRemoteConfig(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agent_id")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing agent_id")
		return
	}

	ctx := r.Context()
	orgID, err := s.queries.GetDefaultOrgID(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no_org",
			"no org found; run reseed.sql before agents can fetch remote config")
		return
	}

	gatewayAgentID, err := s.queries.ResolveEndpointGatewayAgent(ctx, orgID, agentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// This endpoint has never reported in via the ingest pipeline
			// at all -- eami-agent's own FetchConfig treats 404 as "not
			// registered yet," which is exactly true here.
			writeError(w, http.StatusNotFound, "not_found", "endpoint not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to resolve endpoint")
		return
	}
	if gatewayAgentID == nil {
		// The endpoint exists but isn't linked to any governed agent yet
		// (B-164) -- there is no config to serve. Same 404 contract as
		// "not registered": eami-agent can't distinguish the two, and
		// doesn't need to -- both mean "nothing to apply yet."
		writeError(w, http.StatusNotFound, "not_found", "endpoint not linked to a governed agent")
		return
	}

	cfg, err := s.queries.GetAgentConfig(ctx, *gatewayAgentID)
	if err != nil {
		// No config row yet (shouldn't normally happen -- gateway_agents
		// auto-seeds one via trigger on insert) -- fail open with defaults
		// rather than 500, matching GetAgentConfig's own admin-facing
		// fallback behavior exactly.
		d := store.AgentConfigDefaults
		d.AgentID = *gatewayAgentID
		writeJSON(w, http.StatusOK, agentConfigToResp(d))
		return
	}
	writeJSON(w, http.StatusOK, agentConfigToResp(*cfg))
}
