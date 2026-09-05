// Command gateway is the eami-gateway entrypoint.
//
// Usage:
//
//	gateway --config /etc/eami-gateway/eami-gateway.yaml
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof" // registers /debug/pprof/* handlers on http.DefaultServeMux

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/config"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/policyloader"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/registry"
	"github.com/eami/gateway/internal/safego"
	"github.com/eami/gateway/internal/toolrouter"
	"github.com/eami/gateway/internal/workflow"
	policy "github.com/eami/policy"
)

// tokenHTTPClient is shared across fire-and-forget token usage writes.
var tokenHTTPClient = &http.Client{Timeout: 5 * time.Second}

// tokenUsageWriteFunc is a test seam: production always uses writeTokenUsage;
// tests substitute it to inject a panic without a real eami-api endpoint.
// Never reassigned outside _test.go files.
var tokenUsageWriteFunc = writeTokenUsage

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "eami-gateway.yaml", "path to gateway config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	initLogger(cfg.Log.Level, cfg.Log.Format)
	slog.Info("eami-gateway starting",
		"listen", cfg.ListenAddr,
		"policy_rules", cfg.Policy.RulesPath,
		"api_base", cfg.API.BaseURL,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	slog.Info("postgres connected")

	auditWriter, err := audit.NewWriter(ctx, pool)
	if err != nil {
		return fmt.Errorf("audit init: %w", err)
	}
	slog.Info("audit writer initialised")

	episodeRecorder := episode.New(pool)
	slog.Info("episode recorder ready")

	agentRegistry := registry.New(pool)
	slog.Info("agent registry ready")

	issuer := "eami-gateway:primary"
	idManager, err := identity.NewManagerWithDB(cfg.Token.KeypairPath, cfg.Token.DefaultTTLSeconds, issuer, pool)
	if err != nil {
		return fmt.Errorf("identity init: %w", err)
	}
	slog.Info("identity manager ready", "keypair_path", cfg.Token.KeypairPath)

	episodeReader := episode.NewReader(pool)
	// agentRegistry (*registry.Registry) satisfies episode.AgentResolver structurally.
	episodeHTTP := episode.NewHTTPHandler(episodeReader, idManager, agentRegistry, cfg.API.EpisodeReadServiceKey)
	slog.Info("episode read endpoint ready")

	// Load policies from the database. Hot-reloads on pg_notify "policy_reload"
	// so that changes made in the UI take effect without a gateway restart.
	// YAML file is a bootstrap fallback: used only when the DB returns 0 rules
	// (e.g. a fresh install with no policies created yet).
	pLoader := policyloader.New(pool)
	if loadErr := pLoader.Load(ctx); loadErr != nil {
		slog.Warn("policy DB load failed -- falling back to YAML", "err", loadErr)
		if yamlRules, yamlErr := loadPolicySet(cfg.Policy.RulesPath); yamlErr == nil {
			pLoader.Seed(yamlRules)
			slog.Info("policy engine: seeded from YAML fallback", "rule_count", len(yamlRules))
		}
	} else if pLoader.RuleCount() == 0 {
		if yamlRules, yamlErr := loadPolicySet(cfg.Policy.RulesPath); yamlErr == nil && len(yamlRules) > 0 {
			pLoader.Seed(yamlRules)
			slog.Info("policy engine: DB empty -- seeded from YAML", "rule_count", len(yamlRules))
		}
	}
	go pLoader.Listen(ctx)
	slog.Info("policy engine ready (DB-backed, live reload enabled)", "rules", pLoader.RuleCount())

	fwdProxy := proxy.New(proxy.Config{DownstreamURL: cfg.Proxy.DownstreamSSEAddr}, nil)
	slog.Info("proxy configured", "downstream", cfg.Proxy.DownstreamSSEAddr)

	// Dynamic rest_api tool routing (B-044). toolCipher is optional at
	// startup, same convention as eami-api's own construction of this exact
	// key (internal/api/router.go): unset does not fail boot, it just means
	// toolRouter.Forward will cleanly reject any resolved tool that has
	// stored credentials, per-request, rather than silently proceeding
	// without them. A configured-but-invalid key is a real misconfiguration,
	// logged loudly rather than silently ignored.
	var toolCipher *toolrouter.Cipher
	if cfg.API.ToolCredentialsEncryptionKey != "" {
		tc, cipherErr := toolrouter.NewCipher(cfg.API.ToolCredentialsEncryptionKey)
		if cipherErr != nil {
			slog.Error("tool credentials encryption key is set but invalid -- dynamic routing to any rest_api tool with stored credentials will fail closed", "err", cipherErr)
		} else {
			toolCipher = tc
		}
	}
	toolRouter := toolrouter.New(pool, toolCipher)
	slog.Info("tool router ready", "credentials_configured", toolCipher != nil)

	// AI Provider Connector (Thread A Model 1): ai_provider tool routing.
	// Reuses the exact same toolCipher instance above -- same key, same
	// B-022 decryption pattern, no new credential handling. The registry
	// is a plain map so a future provider is one new adapter file plus
	// one entry here, not a rework of resolution or dispatch wiring.
	aiProviderRegistry := map[string]aiprovider.Adapter{
		"claude": aiprovider.NewClaudeAdapter(),
	}
	aiProviderRouter := aiprovider.New(pool, toolCipher, aiProviderRegistry)
	slog.Info("ai provider router ready", "providers", len(aiProviderRegistry), "credentials_configured", toolCipher != nil)

	holdTimeout := time.Duration(cfg.Approval.ExpirySeconds) * time.Second
	approvalRouter := approval.New(
		pool,
		fwdProxy,
		holdTimeout,
		cfg.Approval.SlackWebhookURL,
		cfg.Approval.UIBaseURL,
		// An approved escalation resumes through the same dynamic
		// dispatch the original call would have used, instead of always
		// falling back to the static fwdProxy -- closes a real gap this
		// brief found live (see approval.Router.toolRouter's doc comment).
		toolRouter,
		aiProviderRouter,
	)
	slog.Info("approval router ready",
		"hold_timeout", holdTimeout,
		"slack_enabled", cfg.Approval.SlackWebhookURL != "",
	)

	// Start the LISTEN/NOTIFY loop. Stops when ctx is cancelled.
	go approvalRouter.Run(ctx)

	// Optional pprof listener — enabled only when GATEWAY_PPROF_ADDR is set.
	// Used by load tests (tests/load/gateway.js) to sample goroutine counts.
	if pprofAddr := os.Getenv("GATEWAY_PPROF_ADDR"); pprofAddr != "" {
		go func() {
			slog.Info("pprof listening", "addr", pprofAddr)
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				slog.Error("pprof server failed", "err", err)
			}
		}()
	}

	// Capture API config for the dispatcher.
	apiBaseURL := cfg.API.BaseURL
	apiServiceKey := cfg.API.ServiceKey

	// B-102: dispatch used to be an inline closure here -- not
	// independently constructable by any test (B-101), and its three
	// policy branches (plus Allow's own proxy-failure case, four return
	// points in total) each repeated their own copy of the post-dispatch
	// steps, which is exactly how B-099 happened (one return point simply
	// never got recordTokenUsage). Dispatcher.Dispatch (below,
	// package-level so it's testable from dispatcher_test.go without the
	// full server running) now converges every branch on one exit that
	// runs an explicit, literal hook list -- see Dispatcher's doc comment.
	dispatcher := NewDispatcher(
		toolRouter,
		aiProviderRouter,
		// pLoader itself, NOT pLoader.Evaluator() -- B-129: calling
		// .Evaluator() here would snapshot the rule set that exists at
		// this instant and freeze it for the process's entire lifetime.
		// *policyloader.Loader satisfies policy.EvaluatorSource, so
		// Dispatch() calls pLoader.Evaluator() itself, fresh, on every
		// dispatch, correctly observing every future pg_notify reload.
		pLoader,
		auditWriter,
		episodeRecorder,
		approvalRouter,
		fwdProxy,
		apiBaseURL,
		apiServiceKey,
		holdTimeout,
	)

	mcpHandler := mcp.NewHandler(idManager, agentRegistry, dispatcher.Dispatch, func(ctx context.Context, orgID string) ([]mcp.ToolDefinition, error) {
		return listGatewayTools(ctx, pool, orgID)
	})
	// B-098: api_keys (previously org-scoped only, enforced nowhere --
	// GetAPIKeyByHash had zero callers) now gates POST /v1/gateway/tokens.
	// Same Postgres pool as everything else above -- api_keys lives in the
	// same DB, not a new cross-service dependency.
	apiKeyValidator := identity.NewPostgresAPIKeyValidator(pool)
	tokenEvents := identity.NewPostgresTokenEventStore(pool)
	// B-107: IssueHandler no longer takes an AgentResolver -- its agent
	// lookup is now part of apiKeyValidator's own combined query
	// (ValidateAndResolveAgent). agentRegistry (*registry.Registry) is
	// still used, unmodified, by revokeHandler below.
	// B-119/B-120: both rate-limit thresholds are config-driven, mirroring
	// WorkflowRunPerAgent's own wiring immediately below. Bundled into one
	// named IssueRateLimits value (not four positional args) specifically
	// to avoid a same-typed-pair transposition mistake -- a real risk
	// flagged by this brief's own mandatory code review.
	issueHandler := identity.NewIssueHandler(idManager, apiKeyValidator, tokenEvents, identity.IssueRateLimits{
		PerAgentLimit:        cfg.RateLimit.TokenIssuePerAgent,
		PerAgentWindow:       time.Duration(cfg.RateLimit.TokenIssuePerAgentWindowSeconds) * time.Second,
		PreAuthMaxConcurrent: cfg.RateLimit.TokenIssuePreAuthMaxConcurrent,
	})
	revokeHandler := identity.NewRevokeHandler(idManager, agentRegistry, cfg.API.TokenRevokeServiceKey, tokenEvents)

	// Multi-Hop Workflows Brief 2 (B-059): executes a B-058-defined workflow
	// by calling dispatcher.Dispatch (the exact same Dispatcher above,
	// unmodified -- B-102 only changed dispatch from a closure to a method
	// value with the identical mcp.DecisionHandler signature) once per
	// step, in order -- reusing policy/TOCTOU-pinning/audit/episode logic
	// completely as-is. See internal/workflow's package doc.
	// pLoader itself, not pLoader.Evaluator() -- same B-129 reasoning as
	// the Dispatcher above; Executor.Run's ProjectedDecision preview must
	// re-read the live evaluator per step, not a startup snapshot.
	workflowExecutor := workflow.New(pool, dispatcher.Dispatch, pLoader)
	// agentRegistry (*registry.Registry) satisfies workflow.AgentResolver structurally.
	workflowHTTP := workflow.NewHTTPHandler(idManager, agentRegistry, workflowExecutor)
	slog.Info("workflow executor ready")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/gateway/tokens", issueHandler.HandleIssue)
	mux.HandleFunc("POST /v1/gateway/tokens/{jti}/revoke", revokeHandler.HandleRevoke)
	mux.HandleFunc("/.well-known/gateway-jwks.json", idManager.HandleJWKS)
	// MCP SSE transport (ADR-004):
	//   GET  /v1/mcp/sse      - persistent SSE stream per agent session
	//   POST /v1/mcp/messages - submit tool_call JSON-RPC per session
	mux.HandleFunc("/v1/mcp/sse", mcpHandler.ServeSSE)
	mux.HandleFunc("/v1/mcp/messages", mcpHandler.ServeMessages)
	// Episode read endpoint (ADR-019): serves full episode content to
	// eami-api's memory proxy (server-to-server) or a directly-authenticated
	// agent/desktop client. Never called by a browser — see internal/episode/http.go.
	mux.HandleFunc("GET /v1/gateway/episodes", episodeHTTP.ListEpisodes)
	mux.HandleFunc("GET /v1/gateway/episodes/search", episodeHTTP.SearchEpisodes)
	mux.HandleFunc("GET /v1/gateway/episodes/{id}", episodeHTTP.GetEpisode)
	// Workflow execution (B-059): synchronous, blocks until the run
	// finishes (including any escalation Hold()) -- see workflow/http.go.
	// B-070: wrapped with per-agent-identity rate limiting -- see
	// workflow/ratelimit.go's doc comment for why this needs its own
	// bearer-token peek rather than reusing chi-style middleware (this
	// module has no chi dependency at all; routing is stdlib ServeMux).
	mux.HandleFunc("POST /v1/gateway/workflows/{workflowId}/run", workflow.RateLimitRunMiddleware(
		idManager,
		agentRegistry,
		cfg.RateLimit.WorkflowRunPerAgent,
		time.Duration(cfg.RateLimit.WorkflowRunPerAgentWindowSeconds)*time.Second,
		workflowHTTP.HandleRun,
	))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
		// ReadHeaderTimeout covers the initial HTTP handshake.
		// WriteTimeout is omitted intentionally: SSE streams are long-lived and
		// must not be killed by a fixed write deadline.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received - draining connections...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown error", "err", err)
		}
		slog.Info("gateway stopped cleanly")
		return nil
	}
}

// resolveDynamicTool looks up tool within org's gateway_tools (B-044),
// returning the row only when it's a usable rest_api entry -- nil in every
// other case (empty org/tool, no matching row, a non-rest_api row, or a
// genuine DB error resolving it). nil signals dispatch to fall back to the
// existing static fwdProxy exactly as before B-044: dynamic routing is
// strictly additive/opt-in per registered rest_api tool, never a new way
// for an existing, previously-working call to break. Extracted from
// dispatch's closure body so it's unit-testable (main_test.go) without
// needing the full MCP/SSE request machinery.
func resolveDynamicTool(ctx context.Context, tr *toolrouter.Router, orgID, tool string) *toolrouter.ToolRow {
	if orgID == "" || tool == "" {
		return nil
	}
	row, err := tr.Resolve(ctx, orgID, tool)
	if err != nil {
		if !errors.Is(err, toolrouter.ErrNotFound) {
			// A genuine DB error resolving the tool is not the same as
			// "not found" -- log it, but still fall through to static
			// forwarding rather than failing the whole call over a lookup
			// that was never required before B-044.
			slog.Warn("toolrouter: resolve failed -- falling back to static proxy", "tool", tool, "err", err)
		}
		return nil
	}
	if row.Type != "rest_api" {
		return nil
	}
	return row
}

// listGatewayTools builds the real tools/list result (B-061) for orgID:
// one mcp.ToolDefinition per named action of every org-scoped rest_api
// gateway_tools row, mirroring resolveDynamicTool's org-scoping discipline
// exactly (orgID is always server-resolved from the JWT by the caller,
// never client-supplied). Only type='rest_api' rows are considered --
// ai_provider connectors dispatch via a completely different mechanism
// with no action_paths concept, and mcp/database rows have no working
// direct-dispatch path in this file today; representing either as
// zero-action tool entries would be misleading, not honestly empty, so
// they're excluded from the query itself rather than filtered after the
// fact. A rest_api row with no action_paths set contributes zero entries
// too: such a tool accepts any action name via B-044's flat-POST
// fallback, so there is no fixed list to honestly enumerate.
func listGatewayTools(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]mcp.ToolDefinition, error) {
	if orgID == "" {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT name, action_paths
		FROM gateway_tools
		WHERE org_id = $1 AND type = 'rest_api'
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("listGatewayTools: query: %w", err)
	}
	defer rows.Close()

	var defs []mcp.ToolDefinition
	for rows.Next() {
		var name string
		var actionPathsRaw []byte
		if err := rows.Scan(&name, &actionPathsRaw); err != nil {
			return nil, fmt.Errorf("listGatewayTools: scan: %w", err)
		}
		if len(actionPathsRaw) == 0 {
			continue
		}
		var actionPaths map[string]toolrouter.ActionPathEntry
		if err := json.Unmarshal(actionPathsRaw, &actionPaths); err != nil {
			slog.Warn("listGatewayTools: malformed action_paths -- skipping tool", "tool", name, "err", err)
			continue
		}
		for action, entry := range actionPaths {
			// InputSchema (B-075): use the real schema derived from an
			// OpenAPI spec when the mapping carries one; a manually-defined
			// mapping (B-046, no input_schema) keeps B-061's original
			// honest fallback -- {"type":"object"} is still true (any
			// object is technically accepted at dispatch, since
			// toolrouter.Forward never validates parameters against a
			// schema), just not richly descriptive.
			schema := entry.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			defs = append(defs, mcp.ToolDefinition{
				Name:        name + "/" + action,
				Description: fmt.Sprintf("%s %s on connector %q", entry.Method, entry.Path, name),
				InputSchema: schema,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listGatewayTools: rows: %w", err)
	}
	return defs, nil
}

// resolveAIProviderTool looks up tool within org's gateway_tools,
// restricted to type='ai_provider' (AI Provider Connector, Thread A Model
// 1). Deliberately NOT symmetric with resolveDynamicTool any more (B-168):
// that function's fail-open contract (any non-ErrNotFound error falls
// through to the static proxy) is legitimate there because a real
// pre-existing "before B-044" path exists for rest_api tools to fall back
// to -- there is no equivalent "before" for ai_provider connectors
// (fwdProxy was never built to speak to any AI provider, has no
// credential injection, and cannot parse a Claude-shaped response), so
// falling through there didn't preserve any real working behavior --  it
// just silently rerouted raw, unauthenticated prompt content to a
// legacy, protocol-incompatible endpoint with zero governance applied
// (found live during B-167's own security review; see BACKLOG.md's B-168
// entry for the full blast-radius analysis).
//
// Returns (nil, nil) for empty org/tool or a genuine "not an ai_provider
// connector" (ErrNotFound) -- both cases correctly still fall through to
// the existing rest_api/static-proxy resolution, exactly as before this
// fix; a real error (anything else) is now returned to the caller
// instead of silently discarded, so dispatcher.go can fail the whole
// call closed rather than treating "the DB hiccuped" identically to
// "this tool genuinely isn't an ai_provider connector."
//
// Blast-radius note (code-review finding, precision not correctness):
// dispatcher.go only calls this when resolveDynamicTool already returned
// nil -- which covers a genuinely unregistered tool name AND any
// mcp/database-type gateway_tools row, not only an actual ai_provider
// connector experiencing a transient fault. A real error here now
// hard-rejects all of those cases alike, not just ai_provider-bound
// calls -- the correct, intended tradeoff (never a weaker outcome than a
// clean rejection when the resolver itself can't be trusted), just wider
// in practice than "ai_provider calls only."
func resolveAIProviderTool(ctx context.Context, apr *aiprovider.Router, orgID, tool string) (*aiprovider.ToolRow, error) {
	if orgID == "" || tool == "" {
		return nil, nil
	}
	row, err := apr.Resolve(ctx, orgID, tool)
	if err != nil {
		if errors.Is(err, aiprovider.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

// tokenUsagePayload is the body sent to POST /v1/internal/token-usage on eami-api.
type tokenUsagePayload struct {
	OrgID        string `json:"org_id"`
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name"`
	Model        string `json:"model"`     // from MCP response; "" if absent
	ToolName     string `json:"tool_name"` // ac.Tool -- the connector this call dispatched through (B-108)
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	// CacheCreation5mTokens/CacheCreation1hTokens/CacheReadTokens (B-111)
	// are Anthropic Messages API prompt-caching counters, distinct from
	// InputTokens/OutputTokens -- Anthropic's own documented invariant is
	// that usage.input_tokens excludes cache tokens entirely (three
	// non-overlapping counters), so these are always additive, never
	// double-counted against InputTokens. Raw counts only -- no cost is
	// computed here or at write time; finops.go prices them at query time
	// from the current model_pricing rates.
	CacheCreation5mTokens int    `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int    `json:"cache_creation_1h_tokens"`
	CacheReadTokens       int    `json:"cache_read_tokens"`
	RecordedAt            string `json:"recorded_at"` // RFC3339
}

// extractTokenUsage parses an MCP proxy result for token counts and model name.
// If parsing fails or fields are absent, counts default to 0 and model to "".
// This never returns an error — the caller must not block on this.
func extractTokenUsage(result json.RawMessage, ac mcp.ActionContext) tokenUsagePayload {
	p := tokenUsagePayload{
		OrgID:     ac.OrgID,
		AgentID:   ac.AgentUUID,
		AgentName: ac.AgentName,
		// ToolName is set unconditionally from ac.Tool, independent of
		// whether result parses -- it's always known at the call site,
		// unlike Model/token counts, which live inside the (possibly
		// unparseable) downstream response body.
		ToolName:   ac.Tool,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(result) == 0 {
		return p
	}
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			// CacheCreationInputTokens (B-111) is the flat total Anthropic
			// always documents -- equals the sum of the CacheCreation
			// sub-object's two fields when that sub-object is present.
			// Used as a fallback total when the sub-object is absent (see
			// below), and as a cross-check when it's present.
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			// CacheCreation is Anthropic's documented per-TTL breakdown.
			// Docs only showed it for a mixed 5m+1h example; live-verified
			// (B-111, 2026-08-26, real claude-haiku-4-5-20251001 dispatch,
			// a pure 1h-only cache_control, no 5m mixed in) that it's ALSO
			// present for a single-TTL response: real usage was
			// {"cache_creation_input_tokens":4812,"cache_creation":
			// {"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":4812}}
			// -- so this struct's zero value never has to double as
			// "absent" in the case that's actually been observed live.
			CacheCreation struct {
				Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
				Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return p // non-fatal: return with zero counts
	}
	p.Model = resp.Model
	p.InputTokens = resp.Usage.InputTokens
	p.OutputTokens = resp.Usage.OutputTokens
	p.CacheReadTokens = resp.Usage.CacheReadInputTokens

	switch {
	case resp.Usage.CacheCreation.Ephemeral5mInputTokens > 0 || resp.Usage.CacheCreation.Ephemeral1hInputTokens > 0:
		// Breakdown present -- trust it directly, the exact split.
		p.CacheCreation5mTokens = resp.Usage.CacheCreation.Ephemeral5mInputTokens
		p.CacheCreation1hTokens = resp.Usage.CacheCreation.Ephemeral1hInputTokens
	case resp.Usage.CacheCreationInputTokens > 0:
		// Defensive fallback only -- B-111's live verification (see
		// above) found the breakdown object present even for a pure
		// single-TTL (1h-only) real dispatch, so this branch wasn't
		// observed to be reachable against the real API. Kept in case
		// some other response path (a different API version, a
		// provider-side omission) ever produces a flat total without
		// the breakdown -- attributed to 5m (Anthropic's default TTL
		// when cache_control omits "ttl") rather than silently
		// dropping real cache-write spend.
		p.CacheCreation5mTokens = resp.Usage.CacheCreationInputTokens
	}
	return p
}

// recordTokenUsage extracts usage from a successful dispatch result and
// fire-and-forget writes it to eami-api for FinOps. Shared by the
// immediate-Allow and escalate-then-approved dispatch paths (B-099) so a
// call routed through approval isn't silently excluded from cost tracking
// the way it was before this fix -- the two paths previously diverged
// silently because each duplicated its own copy of this step and only one
// copy was ever written. Must not block or affect dispatch latency.
func recordTokenUsage(apiBaseURL, apiServiceKey string, body json.RawMessage, ac mcp.ActionContext) {
	tu := extractTokenUsage(body, ac)
	go safeWriteTokenUsage(apiBaseURL, apiServiceKey, tu)
}

// safeWriteTokenUsage runs tokenUsageWriteFunc with panic recovery. Call via
// `go safeWriteTokenUsage(...)` from the dispatch path — a panic writing one
// event's token usage must not crash the gateway process.
func safeWriteTokenUsage(apiBaseURL, apiServiceKey string, tu tokenUsagePayload) {
	safego.Guard("token-usage-writer", func() {
		if err := tokenUsageWriteFunc(context.Background(), apiBaseURL, apiServiceKey, tu); err != nil {
			slog.Warn("token usage write failed", "agent", tu.AgentName, "err", err)
		}
	})
}

// writeTokenUsage POSTs a token usage record to the eami-api internal endpoint.
// Returns an error for logging; the caller must not block on this result.
func writeTokenUsage(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
	if apiBase == "" {
		return nil // no API configured; skip silently
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("token_usage: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		apiBase+"/v1/internal/token-usage",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("token_usage: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if serviceKey != "" {
		req.Header.Set("X-Service-Key", serviceKey)
	}
	resp, err := tokenHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("token_usage: POST failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("token_usage: eami-api returned %d", resp.StatusCode)
	}
	return nil
}

// loadPolicySet reads and unmarshals the YAML rules file.
// PolicySet in eami-policy is []Rule, so we try a wrapper struct first,
// then fall back to a bare list.
func loadPolicySet(path string) ([]policy.Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("policy rules file not found; all actions allowed by default", "path", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	var wrapper struct {
		Rules []policy.Rule `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse policy file: %w", err)
	}
	if len(wrapper.Rules) > 0 {
		return wrapper.Rules, nil
	}
	var rules []policy.Rule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("parse policy file (list form): %w", err)
	}
	return rules, nil
}

func initLogger(level, format string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
