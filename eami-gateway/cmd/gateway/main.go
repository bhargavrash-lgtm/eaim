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
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof" // registers /debug/pprof/* handlers on http.DefaultServeMux

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/eami/gateway/internal/approval"
	"github.com/eami/gateway/internal/audit"
	"github.com/eami/gateway/internal/config"
	"github.com/eami/gateway/internal/episode"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/mcp"
	"github.com/eami/gateway/internal/policyloader"
	"github.com/eami/gateway/internal/proxy"
	"github.com/eami/gateway/internal/registry"
	policy "github.com/eami/policy"
)

// tokenHTTPClient is shared across fire-and-forget token usage writes.
var tokenHTTPClient = &http.Client{Timeout: 5 * time.Second}

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

	holdTimeout := time.Duration(cfg.Approval.ExpirySeconds) * time.Second
	approvalRouter := approval.New(
		pool,
		fwdProxy,
		holdTimeout,
		cfg.Approval.SlackWebhookURL,
		cfg.Approval.UIBaseURL,
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

	// Capture API config for the dispatch closure.
	apiBaseURL := cfg.API.BaseURL
	apiServiceKey := cfg.API.ServiceKey

	dispatch := func(reqCtx context.Context, ac mcp.ActionContext) (json.RawMessage, error) {
		start := time.Now()
		decision, evalErr := pLoader.Evaluator().Evaluate(reqCtx, ac.ToPolicyContext())
		if evalErr != nil {
			// Semantic evaluation errors are non-fatal; log and default to allow.
			slog.Warn("policy eval error — defaulting to allow", "err", evalErr)
			decision.Action = policy.ActionAllow
		}
		latencyMS := time.Since(start).Milliseconds()

		// PolicyID is *string in Decision; dereference once for the audit entry.
		policyID := ""
		if decision.PolicyID != nil {
			policyID = *decision.PolicyID
		}

		orgID, _ := uuid.Parse(ac.OrgID)
		agentID, _ := uuid.Parse(ac.AgentUUID)
		auditEntry := audit.Entry{
			OrgID:      orgID,
			AgentID:    agentID,
			AgentName:  ac.AgentName,
			ToolName:   ac.Tool,
			Action:     ac.Action,
			Parameters: ac.Parameters,
			LatencyMS:  latencyMS,
			PolicyID:   policyID,
			Timestamp:  ac.ReceivedAt,
		}

		switch decision.Action {
		case policy.ActionDeny:
			auditEntry.Decision = "denied"
			_ = auditWriter.Write(reqCtx, auditEntry)
			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{
					ToolName:  ac.Tool,
					Action:    ac.Action,
					Params:    ac.Parameters,
					Decision:  "blocked",
					Timestamp: ac.ReceivedAt,
				}},
				"blocked",
			)
			// Return a typed error so the MCP handler builds a structured -32600 response.
			return nil, &mcp.PolicyDeniedError{
				Reason:   decision.Reason,
				PolicyID: policyID,
			}

		case policy.ActionEscalate:
			// Write "escalated" audit entry before blocking on the approval waiter.
			auditEntry.Decision = "escalated"
			_ = auditWriter.Write(reqCtx, auditEntry)

			approvalReq := approval.Request{
				OrgID:      ac.OrgID,
				AgentID:    ac.AgentUUID,
				AgentName:  ac.AgentName,
				Tool:       ac.Tool,
				Action:     ac.Action,
				Parameters: ac.Parameters,
				SessionID:  ac.SessionID,
			}
			approvalID, submitErr := approvalRouter.Submit(reqCtx, approvalReq)
			if submitErr != nil {
				return nil, fmt.Errorf("approval submit: %w", submitErr)
			}
			slog.Info("dispatch: holding for approval decision",
				"approval_id", approvalID,
				"agent", ac.AgentName,
				"hold_timeout", holdTimeout,
			)
			result, holdErr := approvalRouter.Hold(reqCtx, approvalID, approvalReq)
			outcome := "success"
			if holdErr != nil {
				outcome = "failed"
			}
			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{
					ToolName:  ac.Tool,
					Action:    ac.Action,
					Params:    ac.Parameters,
					Result:    result,
					Decision:  "escalated",
					Timestamp: ac.ReceivedAt,
				}},
				outcome,
			)
			return result, holdErr

		default: // policy.ActionAllow
			tr, proxyErr := fwdProxy.Forward(reqCtx, proxy.ToolRequest{
				ToolName:  ac.Tool,
				Action:    ac.Action,
				Params:    ac.Parameters,
				SessionID: ac.SessionID,
			})
			if proxyErr != nil {
				auditEntry.Decision = "denied"
				_ = auditWriter.Write(reqCtx, auditEntry)
				go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
					[]episode.Step{{
						ToolName:  ac.Tool,
						Action:    ac.Action,
						Params:    ac.Parameters,
						Decision:  "allowed",
						Timestamp: ac.ReceivedAt,
					}},
					"failed",
				)
				return nil, fmt.Errorf("proxy error: %w", proxyErr)
			}
			auditEntry.Decision = "allowed"
			if writeErr := auditWriter.Write(reqCtx, auditEntry); writeErr != nil {
				slog.Error("audit write failed", "err", writeErr)
			}

			// Fire-and-forget: write token usage to eami-api for FinOps.
			// Must not block or affect the MCP response latency.
			tu := extractTokenUsage(tr.Body, ac)
			go func() {
				if err := writeTokenUsage(context.Background(), apiBaseURL, apiServiceKey, tu); err != nil {
					slog.Warn("token usage write failed", "agent", ac.AgentName, "err", err)
				}
			}()

			go episodeRecorder.Record(context.Background(), ac.OrgID, ac.AgentUUID, ac.AgentName,
				[]episode.Step{{
					ToolName:  ac.Tool,
					Action:    ac.Action,
					Params:    ac.Parameters,
					Result:    tr.Body,
					Decision:  "allowed",
					Timestamp: ac.ReceivedAt,
				}},
				"success",
			)

			return tr.Body, nil
		}
	}

	mcpHandler := mcp.NewHandler(idManager, agentRegistry, dispatch)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/gateway/tokens", idManager.HandleIssue)
	mux.HandleFunc("/.well-known/gateway-jwks.json", idManager.HandleJWKS)
	// MCP SSE transport (ADR-004):
	//   GET  /v1/mcp/sse      - persistent SSE stream per agent session
	//   POST /v1/mcp/messages - submit tool_call JSON-RPC per session
	mux.HandleFunc("/v1/mcp/sse", mcpHandler.ServeSSE)
	mux.HandleFunc("/v1/mcp/messages", mcpHandler.ServeMessages)
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

// tokenUsagePayload is the body sent to POST /v1/internal/token-usage on eami-api.
type tokenUsagePayload struct {
	OrgID        string `json:"org_id"`
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name"`
	Model        string `json:"model"`        // from MCP response; "" if absent
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	RecordedAt   string `json:"recorded_at"` // RFC3339
}

// extractTokenUsage parses an MCP proxy result for token counts and model name.
// If parsing fails or fields are absent, counts default to 0 and model to "".
// This never returns an error — the caller must not block on this.
func extractTokenUsage(result json.RawMessage, ac mcp.ActionContext) tokenUsagePayload {
	p := tokenUsagePayload{
		OrgID:      ac.OrgID,
		AgentID:    ac.AgentUUID,
		AgentName:  ac.AgentName,
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
		} `json:"usage"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return p // non-fatal: return with zero counts
	}
	p.Model = resp.Model
	p.InputTokens = resp.Usage.InputTokens
	p.OutputTokens = resp.Usage.OutputTokens
	return p
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
