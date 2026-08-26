package identity

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/eami/gateway/internal/safego"
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
// resolved via APIKeyValidator.ValidateAndResolveAgent (B-107 -- combines
// what used to be a separate AgentResolver.LookupByNameAndOrg call into the
// same query as key validation), scoped to the validated key's OWN org_id
// (never a client-supplied org) -- and the resolved agent's real UUID must
// equal the key's own bound agent_id. A key valid for agent A cannot mint a
// token for agent B, even within the same org.
type IssueHandler struct {
	manager *Manager
	keys    APIKeyValidator
	events  TokenEventStore
}

// NewIssueHandler returns an IssueHandler. No AgentResolver parameter (B-107
// removed the separate agent-lookup call this handler used to make) --
// registry.Registry/AgentResolver itself is completely untouched and still
// used unmodified by revoke_http.go.
func NewIssueHandler(m *Manager, keys APIKeyValidator, events TokenEventStore) *IssueHandler {
	return &IssueHandler{manager: m, keys: keys, events: events}
}

// HandleIssue handles POST /v1/gateway/tokens.
func (h *IssueHandler) HandleIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// B-107 moved this decode ahead of API-key validation (see the trust
	// boundary comment above) -- that reordering put an attacker-controlled,
	// unauthenticated buffer allocation ahead of any credential check.
	// MaxBytesReader closes that gap: IssueRequest is six short strings and
	// an int, so 8KiB is already generous headroom, not a tight fit.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
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
	// own org, not any org the caller claims. Decoded before validating the
	// key (B-107) since ValidateAndResolveAgent's combined query needs
	// agentName as one of its own parameters -- a malformed/missing body
	// now surfaces as 400 before an invalid key would surface as 401,
	// swapped from the original order. The reorder itself doesn't leak
	// anything (both are pre-issuance rejections), but it does mean the
	// body is parsed before the caller is authenticated at all -- security
	// review flagged that as worth closing explicitly, not waving off, so
	// the MaxBytesReader immediately below caps what an unauthenticated
	// caller can make this handler buffer.
	agentName := strings.TrimPrefix(req.AgentID, "agent:")

	key, rec, err := h.keys.ValidateAndResolveAgent(r.Context(), r.Header.Get("X-API-Key"), agentName)
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

	// A single, identical rejection message for "no such agent in this
	// org", "a real agent exists but is suspended/revoked" (ISSUING a new
	// token to a suspended/revoked agent would be wrong, unlike
	// RevokeHandler's B-042 treatment of revoking one), and "a real agent
	// exists but this key isn't scoped to it" -- found during B-098's own
	// security review: distinguishing them would let a valid-but-wrongly-
	// scoped key enumerate which agent names exist in its own org (a
	// same-org-only, low-severity oracle, but free to close by not
	// distinguishing them at the HTTP layer).
	const scopeErr = "forbidden: agent not registered or not authorized for this API key"
	if rec == nil || rec.Status == "suspended" || rec.Status == "revoked" {
		http.Error(w, scopeErr, http.StatusForbidden)
		return
	}
	// The actual cross-agent scoping proof (AC2, unmodified per this
	// brief's own MUST NOT MODIFY): a key bound to a DIFFERENT agent than
	// the one resolved from the request is rejected, even though both
	// belong to the same org and the key itself is otherwise perfectly
	// valid.
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

	// RecordIssued (B-107): fire-and-forget, mirroring cmd/gateway/main.go's
	// established safeWriteTokenUsage pattern (B-099) -- the caller already
	// has a valid, usable token at this point regardless of whether this
	// logging write succeeds, so it no longer blocks the response.
	//
	// Code review on this batch caught that the initial version only copied
	// half of the mirrored pattern: safeWriteTokenUsage wraps its body in
	// safego.Guard (an unrecovered panic in a detached goroutine crashes the
	// whole process, not just this request -- see safego's package doc), and
	// this didn't. Fixed to match. Also switched from
	// context.WithoutCancel(r.Context()) to a freshly bounded
	// context.WithTimeout: WithoutCancel never expires, so a stalled DB
	// would hang this goroutine (and its pool connection) indefinitely; a
	// bounded context still survives the handler returning (same reasoning
	// as B-039's Hold() fix -- net/http cancels r.Context() the moment this
	// handler returns) but gives up after a fixed window instead of forever.
	//
	// Known, disclosed limitation (unchanged by the above): this write can
	// still be lost on process crash or shutdown between the response being
	// sent and the goroutine completing, so a live token can briefly exist
	// with no ai_token_events row. Same accepted tradeoff as the episode-
	// recorder's async-write-vs-shutdown race (see B-102's BUILT.md entry)
	// -- not fixed here because doing so (draining in-flight writes on
	// shutdown, or reverting to synchronous) would give back the very
	// round-trip reduction B-107 exists to deliver; noted in BUILT.md's
	// limitations section for this entry instead.
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		safego.Guard("token-issuance-event-recorder", func() {
			if err := h.events.RecordIssued(recordCtx, key.OrgID, rec.ID, rec.Name, key.ID, resp.JTI); err != nil {
				// Issuance already succeeded and the caller already has a
				// valid token -- a logging failure shouldn't fail the
				// request (the token is real and usable either way), but
				// must not be silent.
				slog.Error("identity: failed to record token issuance event", "jti", resp.JTI, "agent", rec.Name, "err", err)
			}
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
