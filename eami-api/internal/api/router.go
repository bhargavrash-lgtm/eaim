package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/eami/api/internal/alerting"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
	"github.com/eami/api/internal/toolcreds"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	queries       *store.Queries
	authSvc       *auth.Service
	alertEngine   *alerting.Engine
	cfg           *config.Config
	storeIface    Store                // set when constructed via NewHandler (testing)
	gatewayClient GatewayEpisodeClient // B-002 Brief 2: eami-gateway episode proxy

	// toolCreds encrypts gateway_tools credentials before they are stored
	// (see internal/toolcreds). Nil if cfg.ToolCredentialsEncryptionKey is
	// unset -- CreateTool then fails closed for any request carrying
	// credentials rather than storing them unencrypted or discarding them.
	toolCreds *toolcreds.Cipher
	// toolStoreOverride is a test-injection point for tools.go's handlers,
	// mirroring storeIface/gatewayClient above. Nil in production.
	toolStoreOverride toolStore
	// toolDialOverride is a test-injection point for TestTool's outbound
	// connectivity checks (tool_connectivity.go). Nil in production, which
	// means safeDialContext -- tests set this to an unrestricted dialer so
	// they can exercise real round-trips against local httptest servers,
	// which safeDialContext's loopback/private-address block would
	// otherwise reject exactly as it's designed to in production.
	toolDialOverride dialContextFunc

	// setupLimiter rate-limits the first-boot setup wizard's token-guessing
	// surface (bootstrap.go). Defense-in-depth only -- the setup token's own
	// 256-bit entropy is the primary defense against brute force.
	setupLimiter *rateLimiter

	// loginIPLimiter/loginAccountLimiter rate-limit POST /v1/auth/login
	// (B-070) -- see ratelimit_login.go. Both must pass for a login attempt
	// to reach Login() at all: per-IP alone doesn't stop credential
	// stuffing spread across many accounts from one IP, and per-account
	// alone doesn't stop one IP hammering many different accounts.
	loginIPLimiter      *rateLimiter
	loginAccountLimiter *rateLimiter
}

// NewServer creates a Server with the given dependencies. cfg may be nil
// (some existing tests -- e.g. finops_test.go's newFinOpsTestEnv -- rely on
// that); the gateway proxy client is then built with empty URL/key, which
// gatewayNotConfigured (gateway_episodes.go) already treats as "not
// configured" and fails cleanly per-request rather than panicking.
func NewServer(queries *store.Queries, authSvc *auth.Service, engine *alerting.Engine, cfg *config.Config) *Server {
	s := &Server{queries: queries, authSvc: authSvc, alertEngine: engine, cfg: cfg}
	s.storeIface = &queriesAdapter{q: queries}
	// A cfg built directly (not via config.Load(), e.g. bootstrap_test.go's
	// &config.Config{ServiceKey: "..."}) leaves RateLimit at its zero value
	// -- limit=0 would mean every single request is instantly rate-limited,
	// not "unconfigured". Only trust cfg.RateLimit when at least one field
	// was actually set; an all-zero struct falls back to the same defaults
	// config.Load() itself would have produced.
	rl := config.DefaultRateLimitConfig()
	if cfg != nil && cfg.RateLimit != (config.RateLimitConfig{}) {
		rl = cfg.RateLimit
	}
	s.setupLimiter = newRateLimiter(rl.Setup, time.Duration(rl.SetupWindowSeconds)*time.Second)
	s.loginIPLimiter = newRateLimiter(rl.LoginPerIP, time.Duration(rl.LoginPerIPWindowSeconds)*time.Second)
	s.loginAccountLimiter = newRateLimiter(rl.LoginPerAccount, time.Duration(rl.LoginPerAccountWindowSeconds)*time.Second)
	var gwURL, gwKey string
	if cfg != nil {
		gwURL, gwKey = cfg.Gateway.URL, cfg.Gateway.EpisodeReadServiceKey
	}
	s.gatewayClient = newHTTPGatewayEpisodeClient(gwURL, gwKey)

	// Tool credentials encryption: optional at startup, same convention as
	// the gateway proxy above -- an unset key does not fail server boot,
	// it just means CreateTool will fail closed per-request if a caller
	// submits credentials (see tools.go's CreateTool). A configured-but-
	// invalid key (wrong length/not hex) is a startup misconfiguration
	// worth surfacing loudly via logs rather than silently proceeding with
	// toolCreds == nil, so it's logged here rather than ignored.
	if cfg != nil && cfg.ToolCredentialsEncryptionKey != "" {
		tc, err := toolcreds.NewCipher(cfg.ToolCredentialsEncryptionKey)
		if err != nil {
			log.Printf("tool credentials encryption key is set but invalid, CreateTool will fail closed for any request with credentials: %v", err)
		} else {
			s.toolCreds = tc
		}
	}

	return s
}

// NewHandler creates a Server backed by a Store interface for unit testing.
// Handlers that reach s.queries will panic and return 500 until the Store
// interface is fully wired -- see TASK-035.
func NewHandler(s Store, authSvc *auth.Service) *Server {
	rl := config.DefaultRateLimitConfig()
	return &Server{
		storeIface:          s,
		authSvc:             authSvc,
		cfg:                 &config.Config{RateLimit: rl},
		setupLimiter:        newRateLimiter(rl.Setup, time.Duration(rl.SetupWindowSeconds)*time.Second),
		loginIPLimiter:      newRateLimiter(rl.LoginPerIP, time.Duration(rl.LoginPerIPWindowSeconds)*time.Second),
		loginAccountLimiter: newRateLimiter(rl.LoginPerAccount, time.Duration(rl.LoginPerAccountWindowSeconds)*time.Second),
	}
}

// WithGatewayClient overrides the gateway episode proxy client -- a
// test-injection point mirroring how NewHandler substitutes Store, and how
// eami-gateway's own NewReaderWithStore/NewHTTPHandler inject fakes (Brief
// 1). Returns s for chaining. Also backfills s.cfg.Gateway to non-empty
// placeholder values if unset, since the handlers treat an empty
// cfg.Gateway.URL/EpisodeReadServiceKey as "proxy not configured" and would
// otherwise 502 before ever reaching the injected client.
func (s *Server) WithGatewayClient(c GatewayEpisodeClient) *Server {
	s.gatewayClient = c
	if s.cfg == nil {
		s.cfg = &config.Config{}
	}
	if s.cfg.Gateway.URL == "" {
		s.cfg.Gateway.URL = "http://test-gateway.invalid"
	}
	if s.cfg.Gateway.EpisodeReadServiceKey == "" {
		s.cfg.Gateway.EpisodeReadServiceKey = "test-key"
	}
	return s
}

// Router is an alias for Handler, provided for test compatibility.
func (s *Server) Router() http.Handler { return s.Handler() }

// Handler builds and returns the Chi router with all routes registered.
//
// Role matrix:
//   admin    -- all routes
//   operator -- all except /v1/settings/* and /v1/users/*
//   approver -- ONLY /v1/approvals/*
//   viewer   -- GET requests only
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// This global middleware deliberately stays conservative even after
	// B-071 closed off eami-api's directly-published port (eami-proxy/Caddy
	// is now the only ingress in production): ClientIPFromRemoteAddr reads
	// only the TCP connection's actual source address, never a
	// caller-supplied X-Forwarded-For/X-Real-IP header, matching B-047's
	// original CVE-class-header-spoofing reasoning (replaces the deprecated
	// middleware.RealIP). Every consumer of middleware.GetClientIP keeps
	// this stricter behavior. B-070/B-071's rate limiters are the one
	// narrowly-scoped exception -- ratelimit.go's own clientKey() trusts
	// X-Forwarded-For directly (not via this middleware), specifically
	// because eami-proxy is now provably the sole path to the two
	// rate-limited route groups; see that function's own doc comment for
	// the full reasoning.
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.With(s.rateLimitLogin).Post("/v1/auth/login", s.Login)
	r.Post("/v1/auth/refresh", s.Refresh)

	// ── First-boot setup wizard (B-053 follow-up, bootstrap.go) ────────────────
	// Deliberately pre-auth -- no user exists yet to authenticate as. The real
	// gate is the console-only setup token Bootstrap validates internally, not
	// route-level auth. See bootstrap.go's package doc comment for the full
	// trust model.
	r.Get("/v1/setup/status", s.SetupStatus)
	r.Post("/v1/setup/token/validate", s.ValidateSetupToken)
	r.Post("/v1/setup/bootstrap", s.Bootstrap)

	// ── Collector write path (service key auth, no JWT) ───────────────────────
	r.With(s.requireServiceKey).Post("/v1/reports", s.IngestReports)
	r.With(s.requireServiceKey).Post("/v1/ingest/batch", s.IngestBatch)
	r.With(s.requireServiceKey).Post("/v1/internal/token-usage", s.IngestTokenUsage)
	r.With(s.requireServiceKey).Post("/v1/reports/paste-events", s.IngestPasteEvents)
	// eami-agent's own remote-config poll (B-165) -- read-only, but still
	// service-key gated like every other collector-facing route above: an
	// eami-agent instance authenticates as the fleet/collector identity,
	// never as a user.
	r.With(s.requireServiceKey).Get("/v1/agents/{agent_id}/config", s.AgentRemoteConfig)

	r.Group(func(r chi.Router) {
		r.Use(s.jwtMiddleware)

		// ── Admin-only ────────────────────────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole("admin"))
			r.Get("/v1/settings/org", s.GetOrgSettings)
			r.Put("/v1/settings/org", s.UpdateOrgSettings)
			r.Get("/v1/settings/notifications", s.GetNotificationConfig)
			r.Put("/v1/settings/notifications", s.UpdateNotificationConfig)
			r.Post("/v1/settings/notifications/test", s.TestNotificationChannel)
			r.Get("/v1/users", s.ListUsers)
			r.Post("/v1/users/invite", s.InviteUser)
			r.Put("/v1/users/{userId}/role", s.UpdateUserRole)
			r.Delete("/v1/users/{userId}", s.DeleteUser)
			// model_pricing (B-112) is a global, cross-org table (no
			// org_id column) -- gated admin-only rather than the looser
			// admin+operator gating agents/policies/tools use below,
			// since a write here affects every org's cost reporting, not
			// just the calling org's own resources.
			r.Post("/v1/admin/model-pricing", s.CreateModelPricing)
			r.Patch("/v1/admin/model-pricing/{model}", s.UpdateModelPricing)
			r.Delete("/v1/admin/model-pricing/{model}", s.DeleteModelPricing)
		})

		// ── Admin + operator: write access ────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole("admin", "operator"))
			r.Get("/v1/auth/api-keys", s.ListAPIKeys)
			r.Post("/v1/auth/api-keys", s.CreateAPIKey)
			r.Delete("/v1/auth/api-keys/{keyId}", s.RevokeAPIKey)
			r.Post("/v1/gateway/agents", s.CreateAgent)
			r.Patch("/v1/gateway/agents/{agentId}", s.UpdateAgent)
			r.Delete("/v1/gateway/agents/{agentId}", s.DeleteAgent)
			r.Put("/v1/gateway/agents/{agentId}/config", s.UpdateAgentConfig)
			// Endpoint <-> gateway agent identity link (B-164/B-165) --
			// the only write path for endpoints.gateway_agent_id.
			r.Patch("/v1/endpoints/{endpointId}/link-agent", s.LinkEndpointAgent)
			r.Post("/v1/gateway/policies", s.CreatePolicy)
			r.Put("/v1/gateway/policies/reorder", s.ReorderPolicies)
			r.Post("/v1/gateway/policies/reorder", s.ReorderPolicies)
			r.Patch("/v1/gateway/policies/{policyId}", s.UpdatePolicy)
			r.Delete("/v1/gateway/policies/{policyId}", s.DeletePolicy)
			r.Post("/v1/gateway/tools", s.CreateTool)
			r.Patch("/v1/gateway/tools/{toolId}", s.UpdateTool)
			r.Delete("/v1/gateway/tools/{toolId}", s.DeleteTool)
			r.Post("/v1/gateway/tools/{toolId}/test", s.TestTool)
			// OpenAPI-spec action discovery (B-075): stateless preview,
			// writes nothing -- generated actions only ever reach
			// gateway_tools via the existing action_paths PATCH above.
			r.Post("/v1/gateway/openapi/discover", s.DiscoverOpenAPI)
			// Multi-Hop Workflows (Thread B Brief 1, B-058): definition-only
			// CRUD, no execution. Same role gating as gateway_tools -- an
			// admin/operator can define a workflow, but nothing dispatches it.
			r.Post("/v1/gateway/workflows", s.CreateWorkflow)
			r.Patch("/v1/gateway/workflows/{workflowId}", s.UpdateWorkflow)
			r.Delete("/v1/gateway/workflows/{workflowId}", s.DeleteWorkflow)
			// Multi-Hop Workflows Brief 2 (B-059): static per-step params.
			r.Put("/v1/gateway/workflow-steps/{stepId}/params", s.PutWorkflowStepParams)
			r.Delete("/v1/gateway/nodes/{nodeId}", s.DeleteNode)
			r.Post("/v1/approvals", s.CreateApproval)
			// Alert rules (write)
			r.Post("/v1/alerts/rules", s.CreateAlertRule)
			r.Put("/v1/alerts/rules/{ruleId}", s.UpdateAlertRule)
			r.Delete("/v1/alerts/rules/{ruleId}", s.DeleteAlertRule)
			r.Post("/v1/alerts/rules/{ruleId}/test", s.TestAlertRule)
		})

		// ── Admin + operator + approver: decide approvals ─────────────────────
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole("admin", "operator", "approver"))
			r.Post("/v1/approvals/{approvalId}/decide", s.DecideApproval)
			r.Post("/v1/alerts/{alertId}/acknowledge", s.AcknowledgeAlert)
			r.Post("/v1/alerts/{alertId}/resolve", s.ResolveAlert)
		})

		// ── Admin + operator + viewer: read access ────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole("admin", "operator", "viewer"))
			r.Use(s.viewerReadOnly)
			r.Get("/v1/gateway/agents", s.ListAgents)
			r.Get("/v1/gateway/agents/{agentId}", s.GetAgent)
			r.Get("/v1/gateway/agents/{agentId}/config", s.GetAgentConfig)
			r.Get("/v1/gateway/policies", s.ListPolicies)
			r.Get("/v1/gateway/policies/{policyId}", s.GetPolicy)
			r.Get("/v1/gateway/tools", s.ListTools)
			r.Get("/v1/gateway/workflows", s.ListWorkflows)
			r.Get("/v1/gateway/workflows/{workflowId}", s.GetWorkflow)
			r.Get("/v1/gateway/workflow-steps/{stepId}/params", s.GetWorkflowStepParams)
			r.Get("/v1/gateway/nodes", s.ListNodes)
			r.Get("/v1/admin/model-pricing", s.ListModelPricing)
			r.Get("/v1/audit", s.ListAudit)
			r.Get("/v1/audit/export", s.ExportAudit)
			r.Get("/v1/audit/verify", s.VerifyAuditChain)
			// Alert rules + alerts (read)
			r.Get("/v1/alerts/rules", s.ListAlertRules)
			r.Get("/v1/alerts", s.ListAlerts)
			// FinOps (read)
			r.Get("/v1/finops/summary", s.FinOpsSummary)
			r.Get("/v1/finops/timeseries", s.FinOpsTimeSeries)
			// Memory episodes (B-002 Brief 3): the openapi.yaml-documented
			// /v1/memory/episodes* routes now serve full episode content via
			// the same org-isolated gateway proxy handlers Brief 2 built --
			// memory.go's old direct, unprotected episodes-table query is
			// retired (file deleted). {episodeId} fills a route openapi.yaml
			// already documented but memory.go never implemented.
			r.Get("/v1/memory/episodes", s.ListGatewayEpisodes)
			r.Get("/v1/memory/episodes/search", s.SearchGatewayEpisodes)
			r.Get("/v1/memory/episodes/{episodeId}", s.GetGatewayEpisode)
			// Gateway episode proxy (B-002 Brief 2) -- same handlers, kept
			// mounted here too (harmless, still secure; not used by the
			// frontend, which calls /v1/memory/episodes* above).
			r.Get("/v1/gateway/episodes", s.ListGatewayEpisodes)
			r.Get("/v1/gateway/episodes/search", s.SearchGatewayEpisodes)
			r.Get("/v1/gateway/episodes/{episodeId}", s.GetGatewayEpisode)
			// Discover (read)
			// /v1/endpoints — agent machine inventory (eami-agent discovery data)
			r.Get("/v1/endpoints", s.ListAgentEndpoints)
			r.Get("/v1/endpoints/{endpointId}", s.GetAgentEndpoint)
			// /v1/discover/endpoints — HTTP traffic observations (discovered_endpoints table)
			r.Get("/v1/discover/endpoints", s.ListEndpoints)
			r.Get("/v1/discover/endpoints/{endpointId}", s.GetEndpoint)
			// Paste events (B-038, read-only admin UI over B-032's paste_events)
			r.Get("/v1/paste-events", s.ListPasteEvents)
			r.Get("/v1/paste-events/timeseries", s.PasteEventsTimeSeries)
		})

		// ── All authenticated roles: approval + alert read ────────────────────
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole("admin", "operator", "approver", "viewer"))
			r.Use(s.viewerReadOnly)
			r.Get("/v1/approvals", s.ListApprovals)
			r.Get("/v1/approvals/{approvalId}", s.GetApproval)
		})
	})

	return r
}
