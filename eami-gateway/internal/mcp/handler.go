// Package mcp implements the MCP SSE transport (ARCHITECTURE.md §3.3, ADR-004).
//
// Transport protocol:
//
//	GET  /v1/mcp/sse                      - open persistent SSE stream
//	POST /v1/mcp/messages?sessionId=<id>  - send JSON-RPC tool_call
//
// Session lifecycle:
//
//  1. Agent GETs /v1/mcp/sse with Bearer token.
//     Gateway validates token, resolves agent from registry, creates Session.
//     Sends SSE "endpoint" event: data: /v1/mcp/messages?sessionId=<id>
//
//  2. Agent POSTs JSON-RPC tool_call to /v1/mcp/messages?sessionId=<id>.
//     Gateway validates sessionId, evaluates policy, proxies or rejects.
//     Response arrives as SSE "message" event on the GET stream.
//
//  3. Token TTL expires or agent disconnects - session cancelled, stream closed.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
	policy "github.com/eami/policy"
)

// ActionContext is the normalised representation of a tool_call.
type ActionContext struct {
	// From JWT + registry lookup
	AgentID    string // JWT sub  (e.g. "agent:claude-support-01")
	AgentUUID  string // gateway_agents.id UUID
	AgentName  string // short name (JWT sub without "agent:" prefix)
	OrgID      string // gateway_agents.org_id UUID
	AgentScope string // declared scope (for scope-drift evaluation)

	// From tool_call params
	Tool       string
	Action     string
	Parameters map[string]any

	// From request context
	Environment string // "production" | "staging" | "development" | "unknown"
	SessionID   string

	// WorkflowRunID/StepIndex (B-093) are set only by workflow/executor.go's
	// runStep, for its own per-step call into the SAME dispatch() every
	// standalone MCP tool_call also uses -- empty/nil here for a standalone
	// call. Threaded through to audit_log so a governed call's workflow
	// context is recorded, not just implicit in workflow_run_steps (which
	// has no FK to/from audit_log at all).
	WorkflowRunID string
	StepIndex     *int32

	ReceivedAt time.Time
}

// ToPolicyContext converts an ActionContext to the policy library's type.
func (a ActionContext) ToPolicyContext() policy.ActionContext {
	return policy.ActionContext{
		AgentName:   a.AgentName,
		ToolName:    a.Tool,
		ActionType:  a.Action,
		Environment: a.Environment,
		Parameters:  a.Parameters,
		Scope:       a.AgentScope,
	}
}

// JSON-RPC envelope types.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	// Data carries extra context for specific error types (e.g. policy denials).
	// Omitted for generic errors.
	Data any `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// DecisionHandler is called with a validated ActionContext. Returns the proxy
// result or an error (which becomes a JSON-RPC error response).
// Return *PolicyDeniedError to produce a structured -32600 response.
type DecisionHandler func(ctx context.Context, ac ActionContext) (json.RawMessage, error)

// ToolDefinition is one MCP tool as returned by tools/list, matching the
// real spec's shape ({name, description, inputSchema}) so a real
// MCP-aware client library can parse it without special-casing this
// implementation (B-061). InputSchema is deliberately generic
// ({"type":"object"}) -- gateway_tools.action_paths (B-046) has no
// parameter schema (just path+method), so a richer schema would be
// fabricated, not derived from real data. See cmd/gateway/main.go's
// listGatewayTools for how these are built.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ListToolsHandler resolves the real, live set of tools available to
// orgID -- called with the session's server-resolved org (never client
// input), mirroring DecisionHandler's identical callback-injection
// pattern so internal/mcp stays DB-agnostic (all actual gateway_tools
// access lives in cmd/gateway/main.go, same separation of concerns
// DecisionHandler already established).
type ListToolsHandler func(ctx context.Context, orgID string) ([]ToolDefinition, error)

// toolsListResult is tools/list's JSON-RPC result payload. NextCursor is
// deliberately never populated (omitempty) -- real pagination isn't
// implemented at current scale; matching the spec's own optional-field
// shape is honest, a fabricated cursor would not be.
type toolsListResult struct {
	Tools      []ToolDefinition `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// Handler owns the SSE transport. Register its methods on the HTTP mux:
//
//	mux.HandleFunc("/v1/mcp/sse",      h.ServeSSE)
//	mux.HandleFunc("/v1/mcp/messages", h.ServeMessages)
type Handler struct {
	identity  *identity.Manager
	reg       *registry.Registry
	sessions  *SessionManager
	dispatch  DecisionHandler
	listTools ListToolsHandler
}

// NewHandler creates a Handler.
func NewHandler(
	idm *identity.Manager,
	reg *registry.Registry,
	dispatch DecisionHandler,
	listTools ListToolsHandler,
) *Handler {
	return &Handler{
		identity:  idm,
		reg:       reg,
		sessions:  NewSessionManager(),
		dispatch:  dispatch,
		listTools: listTools,
	}
}

// ServeSSE opens a persistent SSE stream for an AI agent.
func (h *Handler) ServeSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	claims, err := h.parseBearer(r)
	if err != nil {
		http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	agentName := strings.TrimPrefix(claims.Subject, "agent:")
	agentRec, err := h.reg.LookupByName(r.Context(), agentName)
	if err != nil {
		slog.Warn("mcp/sse: agent lookup failed", "agent", agentName, "err", err)
		http.Error(w, "agent not registered or suspended: "+err.Error(), http.StatusForbidden)
		return
	}

	tokenExpiry := claims.ExpiresAt.Time
	sess, err := h.sessions.Create(claims, agentRec, tokenExpiry)
	if err != nil {
		http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer h.sessions.Close(sess.ID)

	slog.Info("mcp/sse: session opened",
		"session", sess.ID,
		"agent", agentName,
		"expires", tokenExpiry.Format(time.RFC3339),
	)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send endpoint event so the agent knows where to POST messages.
	sseWrite(w, flusher, "endpoint", fmt.Sprintf("/v1/mcp/messages?sessionId=%s", sess.ID))

	for {
		select {
		case evt := <-sess.events:
			sseWrite(w, flusher, evt.Event, evt.Data)
		case <-sess.Done():
			sseWrite(w, flusher, "error", `{"message":"session expired"}`)
			slog.Info("mcp/sse: session expired", "session", sess.ID)
			return
		case <-r.Context().Done():
			slog.Info("mcp/sse: client disconnected", "session", sess.ID)
			return
		}
	}
}

// ServeMessages receives JSON-RPC tool_call requests from the agent.
func (h *Handler) ServeMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "missing sessionId query parameter", http.StatusBadRequest)
		return
	}
	sess := h.sessions.Get(sessionID)
	if sess == nil {
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sessRPCError(sess, nil, -32700, "parse error: "+err.Error())
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// tools/list (B-061) is handled as its own early branch, before the
	// tool_call gate below -- everything from here down (the params
	// parsing, buildActionContext, the dispatch goroutine) is completely
	// unmodified tool_call logic, untouched by this brief.
	if req.Method == "tools/list" {
		h.serveToolsList(w, r, sess, req)
		return
	}

	if req.Method != "tool_call" {
		sessRPCError(sess, req.ID, -32601, "method not found: "+req.Method)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		sessRPCError(sess, req.ID, -32602, "invalid params: "+err.Error())
		w.WriteHeader(http.StatusAccepted)
		return
	}

	ac := buildActionContext(sess, params, r)

	slog.Info("mcp/messages: tool_call",
		"session", sessionID,
		"agent", ac.AgentName,
		"tool", ac.Tool,
		"action", ac.Action,
	)

	// Respond 202 immediately; result arrives via SSE stream.
	w.WriteHeader(http.StatusAccepted)

	// dispatch runs in a detached goroutine (the result arrives later via
	// SSE, not as this handler's own response), but net/http cancels
	// r.Context() the moment ServeHTTP returns -- which happens right
	// after the 202 write above, before dispatch (including a
	// potentially minutes-long approval Hold()) has any real chance to
	// run. context.WithoutCancel keeps this goroutine's context alive
	// for its own natural lifetime (dispatch/Submit/Hold already have
	// their own internal timeouts) while still carrying any values the
	// original request context set. Found live (B-039): every ESCALATE
	// decision's Submit() failed with "context canceled" until this was
	// fixed -- a real, separate root cause from the three in
	// internal/approval/router.go, not previously known.
	dispatchCtx := context.WithoutCancel(r.Context())
	go func() {
		result, err := h.dispatch(dispatchCtx, ac)
		if err != nil {
			slog.Warn("mcp/messages: rejected", "agent", ac.AgentName, "err", err)
			// Policy denials get a structured JSON-RPC error (code -32600 + data).
			// All other errors use generic code -32000.
			var pde *PolicyDeniedError
			if errors.As(err, &pde) {
				sessPolicyDenied(sess, req.ID, pde)
			} else {
				sessRPCError(sess, req.ID, -32000, err.Error())
			}
			return
		}
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(result)}
		data, _ := json.Marshal(resp)
		_ = sess.Send(sseEvent{Event: "message", Data: string(data)})
	}()
}

// serveToolsList handles the "tools/list" JSON-RPC method (B-061): writes
// 202 immediately and sends the real result via the same SSE "message"
// mechanism tool_call already uses, for full transport uniformity -- a
// real client is already listening on one stream for everything,
// regardless of which method it called. orgID is always resolved
// server-side from the session's own registry-verified agent (never
// client input), the identical source buildActionContext already trusts
// for tool_call.
func (h *Handler) serveToolsList(w http.ResponseWriter, r *http.Request, sess *Session, req jsonRPCRequest) {
	w.WriteHeader(http.StatusAccepted)

	orgID := ""
	if sess.Agent != nil {
		orgID = sess.Agent.OrgID
	}

	// Same context.WithoutCancel reasoning as tool_call immediately above
	// (B-039): this handler has already returned (202 written) by the
	// time this goroutine runs, and net/http cancels r.Context() the
	// instant ServeHTTP returns -- using r.Context() directly (unwrapped)
	// here would fail nearly every real DB query with "context canceled",
	// not just long-held ones, since ServeMessages returns essentially
	// immediately after this function starts the goroutine.
	listCtx := context.WithoutCancel(r.Context())
	go func() {
		tools, err := h.listTools(listCtx, orgID)
		if err != nil {
			slog.Warn("mcp/messages: tools/list failed", "session", sess.ID, "org", orgID, "err", err)
			sessRPCError(sess, req.ID, -32000, err.Error())
			return
		}
		if tools == nil {
			tools = []ToolDefinition{}
		}
		resultRaw, err := json.Marshal(toolsListResult{Tools: tools})
		if err != nil {
			sessRPCError(sess, req.ID, -32000, "failed to encode tools/list result")
			return
		}
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(resultRaw)}
		data, _ := json.Marshal(resp)
		_ = sess.Send(sseEvent{Event: "message", Data: string(data)})
	}()
}

// parseBearer extracts and validates the Bearer token from the Authorization header.
func (h *Handler) parseBearer(r *http.Request) (*identity.Claims, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("missing Bearer token")
	}
	return h.identity.Validate(auth[7:])
}

func buildActionContext(sess *Session, params toolCallParams, r *http.Request) ActionContext {
	tool, action, _ := splitToolAction(params.Name)
	agentName := strings.TrimPrefix(sess.Claims.Subject, "agent:")
	env := r.Header.Get("X-Environment")
	if env == "" {
		env = "unknown"
	}
	orgID, agentUUID, agentScope := "", "", ""
	if sess.Agent != nil {
		orgID = sess.Agent.OrgID
		agentUUID = sess.Agent.ID
		agentScope = sess.Agent.Scope
	}
	return ActionContext{
		AgentID:     sess.Claims.Subject,
		AgentUUID:   agentUUID,
		AgentName:   agentName,
		OrgID:       orgID,
		AgentScope:  agentScope,
		Tool:        tool,
		Action:      action,
		Parameters:  params.Arguments,
		Environment: env,
		SessionID:   sess.ID,
		ReceivedAt:  time.Now().UTC(),
	}
}

func splitToolAction(name string) (tool, action, sep string) {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i], name[i+1:], "/"
	}
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:], "."
	}
	return "", name, ""
}

// sseWrite writes one SSE event and flushes immediately.
func sseWrite(w http.ResponseWriter, f http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	f.Flush()
}

// sessRPCError sends a generic JSON-RPC error as an SSE "message" event.
func sessRPCError(sess *Session, id any, code int, msg string) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	data, _ := json.Marshal(resp)
	_ = sess.Send(sseEvent{Event: "message", Data: string(data)})
}

// sessPolicyDenied sends a structured JSON-RPC -32600 error for policy denials.
// The error includes a "data" object with "reason" and "policy_id" so MCP clients
// can surface a useful message to the user and to observability tooling.
func sessPolicyDenied(sess *Session, id any, e *PolicyDeniedError) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    -32600,
			Message: "Request denied by policy",
			Data: map[string]string{
				"reason":    e.Reason,
				"policy_id": e.PolicyID,
			},
		},
	}
	data, _ := json.Marshal(resp)
	_ = sess.Send(sseEvent{Event: "message", Data: string(data)})
}
