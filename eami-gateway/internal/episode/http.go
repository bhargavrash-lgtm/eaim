package episode

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

// AgentResolver resolves a JWT subject's agent name, scoped to the
// token's own org_id claim, to its registry record. *registry.Registry
// satisfies this structurally (Go duck typing) with zero changes to the
// registry package. Defined here purely as a test seam for the bearer-JWT
// auth branch. LookupByNameAndOrg, not LookupByName (B-141): this is the
// confirmed cross-tenant episode-content read path -- gateway_agents only
// enforces per-org name uniqueness, so resolving by name alone could
// silently return a different org's identically-named agent, and this
// resolution's OrgID is exactly what scopes which org's episode content
// gets returned below.
type AgentResolver interface {
	LookupByNameAndOrg(ctx context.Context, name, orgID string) (*registry.AgentRecord, error)
}

// Handler serves the dual-auth HTTP surface for full episode content:
//
//	GET /v1/gateway/episodes
//	GET /v1/gateway/episodes/search
//	GET /v1/gateway/episodes/{id}
//
// This endpoint exists because ADR-019 (resolved) requires full episode
// content — the JSONB steps: tool calls, arguments, results — to stay
// on-prem in eami-gateway's Postgres. eami-api must never store or serve it
// directly (ADR-010). eami-api's memory proxy (Brief 2, not yet built) is
// the intended caller for the UI-facing path; this handler never talks to a
// browser directly.
//
// See authenticateCaller for the two accepted auth mechanisms and their
// trust models.
type Handler struct {
	reader     *Reader
	identity   *identity.Manager
	resolver   AgentResolver
	serviceKey string
}

// NewHTTPHandler returns a Handler. serviceKey is the dedicated shared
// secret for the service-to-service auth path (config: api.episode_read_service_key,
// env: GATEWAY_EPISODE_READ_SERVICE_KEY) — deliberately separate from the
// outbound api.service_key used for token-usage writes, so a leak of one
// does not also grant the other.
func NewHTTPHandler(reader *Reader, idm *identity.Manager, resolver AgentResolver, serviceKey string) *Handler {
	return &Handler{reader: reader, identity: idm, resolver: resolver, serviceKey: serviceKey}
}

// authenticateCaller implements the dual-auth check shared by all three
// handlers below. Not a middleware framework — called inline at the top of
// each handler, matching mcp/handler.go's parseBearer style (this package
// has no chi/gorilla dependency and none is being introduced here).
//
// Precedence: if X-Service-Key is present, it is checked first and, if
// valid, wins outright — an Authorization header, if also present, is
// ignored. No caller sends both today; this is a deliberate, documented
// tie-break, not an accident.
//
// TRUST BOUNDARY (service-key path): org_id is a caller-supplied query
// param. Gateway verifies only that the caller IS eami-api (via the service
// key) — it performs NO independent check that the underlying browser user
// is actually authorized for that org_id. That authorization is entirely
// eami-api's responsibility, enforced in its proxy layer (Brief 2) BEFORE it
// ever calls this endpoint. This handler provides zero protection against a
// proxy bug that passes through the wrong org_id — Brief 2's plan must treat
// "verify the requesting user actually has access to org_id" as a hard
// requirement of its own, not an assumption inherited from here.
//
// The bearer-JWT path takes no client-supplied org_id param -- but note
// (B-141) that alone did NOT make it safe before this fix: org_id was
// resolved via the agent registry keyed by name alone, and gateway_agents
// only enforces per-org name uniqueness (UNIQUE(org_id, name), not a
// global unique name), so two different orgs' identically-named agents
// could have the wrong org's row resolved -- a real, traced cross-tenant
// episode-content read, not a theoretical one. Fixed by trusting the
// token's own signed org_id claim (Claims.OrgID, set server-side at
// issuance, B-141) and resolving via LookupByNameAndOrg, scoped to it,
// instead of the bare Subject name.
func (h *Handler) authenticateCaller(r *http.Request) (orgID uuid.UUID, status int, err error) {
	if key := r.Header.Get("X-Service-Key"); key != "" {
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.serviceKey)) != 1 {
			return uuid.Nil, http.StatusUnauthorized, errors.New("invalid service key")
		}
		orgID, err = uuid.Parse(r.URL.Query().Get("org_id"))
		if err != nil {
			return uuid.Nil, http.StatusBadRequest, fmt.Errorf("org_id query param required and must be a UUID: %w", err)
		}
		return orgID, 0, nil
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return uuid.Nil, http.StatusUnauthorized, errors.New("missing X-Service-Key or Authorization: Bearer")
	}
	claims, err := h.identity.Validate(auth[7:])
	if err != nil {
		return uuid.Nil, http.StatusUnauthorized, fmt.Errorf("invalid bearer token: %w", err)
	}
	agentName := strings.TrimPrefix(claims.Subject, "agent:")
	// B-141: hard cutover -- a pre-cutover token (no org_id claim) is
	// rejected outright rather than falling back to the unscoped lookup.
	// This is the confirmed cross-tenant episode-content read path: the
	// resolved rec.OrgID below is the ONLY value that scopes which org's
	// episode content this request can read.
	if claims.OrgID == "" {
		return uuid.Nil, http.StatusUnauthorized, errors.New("token missing org_id claim -- reissue a new token")
	}
	rec, err := h.resolver.LookupByNameAndOrg(r.Context(), agentName, claims.OrgID)
	if err != nil {
		return uuid.Nil, http.StatusForbidden, fmt.Errorf("agent not registered or suspended: %w", err)
	}
	orgID, err = uuid.Parse(rec.OrgID)
	if err != nil {
		return uuid.Nil, http.StatusInternalServerError, fmt.Errorf("registry: malformed org_id for agent %q", agentName)
	}
	return orgID, 0, nil
}

// ListEpisodes handles GET /v1/gateway/episodes.
// Query params: outcome, limit, offset, and (service-key auth only) org_id.
func (h *Handler) ListEpisodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID, status, err := h.authenticateCaller(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	result, err := h.reader.List(r.Context(), orgID, q.Get("outcome"), limit, offset)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type meta struct {
		Total  int64 `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result.Episodes,
		"meta": meta{Total: result.Total, Limit: result.Limit, Offset: result.Offset},
	})
}

// GetEpisode handles GET /v1/gateway/episodes/{id}.
func (h *Handler) GetEpisode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID, status, err := h.authenticateCaller(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid episode id", http.StatusBadRequest)
		return
	}

	ep, err := h.reader.Get(r.Context(), id, orgID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

// SearchEpisodes handles GET /v1/gateway/episodes/search?q=<text>.
func (h *Handler) SearchEpisodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID, status, err := h.authenticateCaller(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "q is required", http.StatusBadRequest)
		return
	}

	episodes, err := h.reader.Search(r.Context(), orgID, query)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": episodes})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
