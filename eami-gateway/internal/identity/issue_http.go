package identity

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eami/gateway/internal/safego"
)

// rateLimiter is a small in-memory, per-key fixed-window limiter, algorithm
// deliberately reused from eami-gateway/internal/workflow/ratelimit.go
// (B-070) rather than invented fresh -- same fixed-window/fail-closed/
// Retry-After shape, duplicated (not imported) because sharing it would mean
// either exporting workflow-package internals for one non-workflow caller or
// a new shared package for a two-line struct, neither justified by this
// brief's scope. ADR-020 Model A is always a single process, so in-memory
// state is correct here for the same reason it is in workflow/ratelimit.go.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{attempts: make(map[string][]time.Time), limit: limit, window: window}
}

// Allow records an attempt for key and reports whether it's within the
// window's limit. When it is not, retryAfter is the duration until the
// window's oldest surviving attempt ages out, suitable for a Retry-After
// response header. A non-positive limit fails closed rather than panicking
// on the kept[0] index below -- see workflow/ratelimit.go's identical
// reasoning. This file's one rateLimiter instance (the per-agent limiter)
// is now config-driven (B-119), not a compile-time constant: the real
// production path (config.go's validate()) both rejects a negative value
// at startup and treats an unset/zero env var as "use the default" (never
// actually zero by the time it reaches NewIssueHandler), but this guard
// stays independently defensive against any other caller (a test, a future
// call site) constructing a rateLimiter directly with a non-positive value.
// preAuthGate below is a separate mechanism (B-120, a concurrency bound,
// not a request-rate limiter) with its own, independent fail-closed guard.
func (rl *rateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if rl.limit <= 0 {
		return false, rl.window
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)
	var kept []time.Time
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.attempts[key] = kept
		oldest := kept[0]
		retryAfter = oldest.Add(rl.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}
	rl.attempts[key] = append(kept, now)
	return true, 0
}

// setRetryAfter sets the Retry-After header from a rate-limit duration,
// rounding up to a whole number of seconds per RFC 9110 Sec 10.2.3.
func setRetryAfter(w http.ResponseWriter, d time.Duration) {
	secs := int(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}

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
	limiter *rateLimiter // B-118, thresholds now config-driven (B-119)

	// preAuthGate (B-120) bounds how many not-yet-authenticated
	// ValidateAndResolveAgent calls can be in flight at once, protecting
	// the shared pgxpool from a burst of concurrent DB round trips --
	// limiter above by design only sees callers who already passed key
	// validation and cross-agent scoping (see HandleIssue), so it
	// structurally cannot protect this earlier stage. See preAuthGate's
	// own doc comment for why this is a concurrency bound, not a
	// request-rate limiter.
	preAuthGate *preAuthGate
}

// IssueRateLimits bundles NewIssueHandler's rate-limit configuration into
// one named argument, rather than positional int/time.Duration values a
// caller could transpose without a compile error -- flagged by this
// brief's own mandatory code review, since PerAgentLimit/PerAgentWindow and
// an earlier draft's pre-auth pair shared exactly the same two types
// back-to-back.
type IssueRateLimits struct {
	PerAgentLimit  int
	PerAgentWindow time.Duration
	// PreAuthMaxConcurrent (B-120) -- see preAuthGate's doc comment.
	PreAuthMaxConcurrent int
}

// NewIssueHandler returns an IssueHandler. No AgentResolver parameter (B-107
// removed the separate agent-lookup call this handler used to make) --
// registry.Registry/AgentResolver itself is completely untouched and still
// used unmodified by revoke_http.go.
//
// limits (B-119/B-120) comes from config.RateLimitConfig's
// TokenIssuePerAgent/TokenIssuePerAgentWindowSeconds/
// TokenIssuePreAuthMaxConcurrent fields -- cmd/gateway/main.go is the one
// production call site, threading the loaded config through. Replaces
// B-118's original hardcoded tokenIssueRateLimit/tokenIssueRateLimitWindow
// constants.
func NewIssueHandler(m *Manager, keys APIKeyValidator, events TokenEventStore, limits IssueRateLimits) *IssueHandler {
	return &IssueHandler{
		manager:     m,
		keys:        keys,
		events:      events,
		limiter:     newRateLimiter(limits.PerAgentLimit, limits.PerAgentWindow),
		preAuthGate: newPreAuthGate(limits.PreAuthMaxConcurrent),
	}
}

// preAuthGate bounds how many not-yet-authenticated ValidateAndResolveAgent
// calls can run concurrently, protecting the shared pgxpool from a burst of
// concurrent DB round trips -- the literal threat B-120 exists to close
// (an unauthenticated flood of bogus X-API-Key values each still costing a
// real indexed Postgres lookup).
//
// This is a CONCURRENCY bound, not a request-RATE limiter -- a real design
// change made after this brief's own mandatory security review found a
// real, High-severity problem with the original per-source-IP request-rate
// design: this product's production topology (B-071) fronts the gateway
// with one reverse proxy, so every external caller's r.RemoteAddr is that
// proxy's own container IP -- collapsing a per-IP rate limiter here into
// ONE shared bucket across every org's every agent. A fixed-window
// request-rate limiter in that shape meant a single unauthenticated flood
// (costing the attacker nothing -- a bogus key never needs to even look
// plausible) could lock every legitimate agent in every org out of token
// issuance for the rest of that window, and -- separately -- a handful of
// legitimate agents each independently operating at their own normal
// per-agent pace could sum to the shared threshold and start tripping it
// with no attacker involved at all.
//
// A bounded concurrency gate doesn't have that failure mode: it only ever
// rejects a request that arrives while every slot is genuinely occupied by
// another in-flight DB call, and recovers the instant those calls complete
// (each is a single fast indexed lookup, typically low-single-digit
// milliseconds) -- there is no fixed window an attacker can "fill" and
// walk away from. Deliberately global, not keyed per caller/IP: in the
// same proxy-fronted topology a per-IP concurrency cap would degrade to
// one shared gate anyway (every real caller shares one apparent IP), so
// keying by IP would add complexity without adding real per-attacker
// isolation; a single global gate achieves the actual goal (bounding total
// concurrent DB pressure from this pre-auth stage) directly.
type preAuthGate struct {
	sem chan struct{}
}

// newPreAuthGate builds a gate with room for maxConcurrent in-flight calls.
// A non-positive value fails closed (every TryAcquire call denied) rather
// than panicking on a negative buffered-channel size -- same "fail closed,
// never panic" contract as rateLimiter.Allow above; config.go's validate()
// rejects a negative value at startup and defaults an unset/zero one, so
// the real production path never actually passes zero here, but this stays
// independently defensive for any other caller (a test, a future call
// site).
func newPreAuthGate(maxConcurrent int) *preAuthGate {
	if maxConcurrent < 0 {
		maxConcurrent = 0
	}
	return &preAuthGate{sem: make(chan struct{}, maxConcurrent)}
}

// TryAcquire reserves one slot without blocking. ok is false when every
// slot is currently occupied -- the caller should reject the request
// rather than wait (waiting would tie up the handler goroutine without
// bounding anything, since the whole point is limiting concurrent DB work,
// not queuing callers behind it). Release must be called exactly once for
// every TryAcquire that returns true.
func (g *preAuthGate) TryAcquire() bool {
	select {
	case g.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees the slot reserved by a prior successful TryAcquire.
func (g *preAuthGate) Release() {
	<-g.sem
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

	// B-120: bounds concurrent in-flight ValidateAndResolveAgent calls,
	// specifically -- not the whole handler -- so a flood of bogus
	// X-API-Key values can't pile up unbounded concurrent DB round trips
	// against the shared pgxpool. See preAuthGate's own doc comment for why
	// this is a concurrency bound rather than a per-caller request-rate
	// limiter. No fixed window exists to compute a real Retry-After from
	// (a slot frees the instant any in-flight call completes, typically
	// low-single-digit milliseconds); 1 second is a generic, honest
	// backoff hint, not a real deadline.
	if !h.preAuthGate.TryAcquire() {
		setRetryAfter(w, time.Second)
		http.Error(w, "gateway is busy validating other requests -- try again shortly", http.StatusTooManyRequests)
		return
	}
	key, rec, err := h.keys.ValidateAndResolveAgent(r.Context(), r.Header.Get("X-API-Key"), agentName)
	h.preAuthGate.Release()
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

	// B-118: rate-limited by the resolved agent's real registry UUID, not
	// the raw client-claimed agent_id string -- same cross-org-collision
	// reasoning as workflow/ratelimit.go's RateLimitRunMiddleware (agent
	// names are unique only per-org, schema.sql's UNIQUE (org_id, name)).
	// Checked only once the caller is fully authenticated and scoped (past
	// both the API-key validation and the cross-agent scoping check above)
	// so an invalid/misscoped request never consumes a legitimate agent's
	// quota -- same ordering precedent as B-070's own workflow-run limiter.
	if ok, retryAfter := h.limiter.Allow(rec.ID); !ok {
		setRetryAfter(w, retryAfter)
		http.Error(w, "too many token issuance requests for this agent -- try again later", http.StatusTooManyRequests)
		return
	}

	// B-116: Subject/Scope/Model/Owner/RiskTier/TTLSeconds are all rebuilt
	// from the resolved gateway_agents record, not echoed from client input
	// -- previously only Subject was. A signed token's claims must reflect
	// what the agent is actually authorized for in the DB, not whatever the
	// issuance request happened to say. TTLSeconds is not latent the way
	// the other four originally were -- Manager.Issue honors it directly
	// and Manager.Validate enforces exp from it, so a client that could set
	// its own ttl_seconds could mint a token that outlives the per-agent
	// window an admin configured via gateway_agents.token_ttl_seconds (a
	// real, admin-managed column previously never read by eami-gateway at
	// all) -- caught by this brief's own mandatory review passes, not in
	// B-116's original backlog description. api/openapi.yaml's
	// AITokenRequest still documents ttl_seconds as client-settable
	// (60-14400s) -- that's Architect-EAMI-owned (BOUNDARIES.md), disclosed
	// as a now-stale contract rather than edited, same B-086/B-107
	// precedent. Task is the one field deliberately left client-supplied:
	// it's a per-request purpose string, not an agent-identity attribute,
	// and gateway_agents has no matching column to rebuild it from.
	// B-141: OrgID comes from the validated API key's own org_id (key.OrgID,
	// already resolved above by ValidateAndResolveAgent's org-scoped query),
	// never from client input -- IssueRequest.OrgID has no JSON tag
	// specifically so a request body can't set it. Every consumer that
	// later resolves this token's agent identity (ServeSSE, HandleRun,
	// RateLimitRunMiddleware, episode's authenticateCaller) uses this claim
	// to scope that resolution instead of the bare Subject name alone.
	req.OrgID = key.OrgID
	req.AgentID = "agent:" + rec.Name
	req.Scope = rec.Scope
	req.Model = rec.Model
	req.Owner = rec.Owner
	req.RiskTier = rec.RiskTier
	req.TTLSeconds = rec.TokenTTLSeconds
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
