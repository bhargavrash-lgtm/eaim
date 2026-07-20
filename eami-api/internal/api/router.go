package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/eami/api/internal/alerting"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	queries     *store.Queries
	authSvc     *auth.Service
	alertEngine *alerting.Engine
	cfg         *config.Config
	storeIface  Store // set when constructed via NewHandler (testing)
}

// NewServer creates a Server with the given dependencies.
func NewServer(queries *store.Queries, authSvc *auth.Service, engine *alerting.Engine, cfg *config.Config) *Server {
	s := &Server{queries: queries, authSvc: authSvc, alertEngine: engine, cfg: cfg}
	s.storeIface = &queriesAdapter{q: queries}
	return s
}

// NewHandler creates a Server backed by a Store interface for unit testing.
// Handlers that reach s.queries will panic and return 500 until the Store
// interface is fully wired -- see TASK-035.
func NewHandler(s Store, authSvc *auth.Service) *Server {
	return &Server{storeIface: s, authSvc: authSvc}
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
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Post("/v1/auth/login", s.Login)
	r.Post("/v1/auth/refresh", s.Refresh)

	// ── Collector write path (service key auth, no JWT) ───────────────────────
	r.With(s.requireServiceKey).Post("/v1/reports", s.IngestReports)
	r.With(s.requireServiceKey).Post("/v1/ingest/batch", s.IngestBatch)
	r.With(s.requireServiceKey).Post("/v1/internal/token-usage", s.IngestTokenUsage)

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
			r.Post("/v1/gateway/policies", s.CreatePolicy)
			r.Put("/v1/gateway/policies/reorder", s.ReorderPolicies)
			r.Post("/v1/gateway/policies/reorder", s.ReorderPolicies)
			r.Patch("/v1/gateway/policies/{policyId}", s.UpdatePolicy)
			r.Delete("/v1/gateway/policies/{policyId}", s.DeletePolicy)
			r.Post("/v1/gateway/tools", s.CreateTool)
			r.Delete("/v1/gateway/tools/{toolId}", s.DeleteTool)
			r.Post("/v1/gateway/tools/{toolId}/test", s.TestTool)
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
			r.Get("/v1/gateway/nodes", s.ListNodes)
			r.Get("/v1/audit", s.ListAudit)
			r.Get("/v1/audit/export", s.ExportAudit)
			r.Get("/v1/audit/verify", s.VerifyAuditChain)
			// Alert rules + alerts (read)
			r.Get("/v1/alerts/rules", s.ListAlertRules)
			r.Get("/v1/alerts", s.ListAlerts)
			// FinOps (read)
			r.Get("/v1/finops/summary", s.FinOpsSummary)
			r.Get("/v1/finops/timeseries", s.FinOpsTimeSeries)
			// Memory episodes (stubs - episode recorder not yet built)
			r.Get("/v1/memory/episodes", s.ListMemoryEpisodes)
			r.Get("/v1/memory/episodes/search", s.SearchMemoryEpisodes)
			// Discover (read)
			// /v1/endpoints — agent machine inventory (eami-agent discovery data)
			r.Get("/v1/endpoints", s.ListAgentEndpoints)
			r.Get("/v1/endpoints/{endpointId}", s.GetAgentEndpoint)
			// /v1/discover/endpoints — HTTP traffic observations (discovered_endpoints table)
			r.Get("/v1/discover/endpoints", s.ListEndpoints)
			r.Get("/v1/discover/endpoints/{endpointId}", s.GetEndpoint)
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
