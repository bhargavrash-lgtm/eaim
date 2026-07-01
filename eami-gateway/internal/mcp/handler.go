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

// Handler owns the SSE transport. Register its methods on the HTTP mux:
//
//	mux.HandleFunc("/v1/mcp/sse",      h.ServeSSE)
//	mux.HandleFunc("/v1/mcp/messages", h.ServeMessages)
type Handler struct {
	identity *identity.Manager
	reg      *registry.Registry
	sessions *SessionManager
	dispatch DecisionHandler
}

// NewHandler creates a Handler.
func NewHandler(
	idm *identity.Manager,
	reg *registry.Registry,
	dispatch DecisionHandler,
) *Handler {
	return &Handler{
		identity: idm,
		reg:      reg,
		sessions: NewSessionManager(),
		dispatch: dispatch,
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

	go func() {
		result, err := h.dispatch(r.Context(), ac)
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
