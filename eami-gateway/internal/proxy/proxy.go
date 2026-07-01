// Package proxy forwards allowed tool calls to the configured downstream MCP
// server and returns the response to the calling agent.
//
// Wire format (downstream request):
//
//	POST <DownstreamURL>/v1/mcp/tool_call
//	Content-Type: application/json
//	{"tool":"<name>","action":"<action>","params":{...},"session_id":"<id>"}
//
// The downstream response body is returned unchanged as ToolResponse.Body.
// A non-2xx status is treated as an error.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultTimeout  = 30 * time.Second
	maxResponseSize = 10 * 1024 * 1024 // 10 MB
)

// Config holds the proxy configuration.
// Exported so callers can construct a testable Proxy via New.
type Config struct {
	DownstreamURL string // base URL of the downstream MCP server
}

// ToolRequest is the semantic payload for a downstream tool call.
type ToolRequest struct {
	ToolName  string
	Action    string
	Params    map[string]any
	SessionID string // optional; forwarded when non-empty
}

// ToolResponse is the result returned by the downstream server.
type ToolResponse struct {
	Status int             // HTTP status code from the downstream
	Body   json.RawMessage // raw response body, unchanged
}

// Proxy forwards ToolRequest payloads to a downstream MCP server.
type Proxy struct {
	downstreamURL string
	client        *http.Client
}

// New creates a Proxy from an explicit Config and HTTP client.
// Pass a non-nil client to inject a test double (e.g. httptest server client).
// If client is nil, a default client with a 30-second timeout is used.
func New(cfg Config, client *http.Client) *Proxy {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Proxy{
		downstreamURL: cfg.DownstreamURL,
		client:        client,
	}
}

// NewWithAddr creates a Proxy targeting addr using the default HTTP client.
// Convenience wrapper for callers that don't need to inject a client.
func NewWithAddr(addr string) *Proxy {
	return New(Config{DownstreamURL: addr}, nil)
}

// downstreamRequest is the JSON body sent to the downstream server.
type downstreamRequest struct {
	Tool      string         `json:"tool"`
	Action    string         `json:"action"`
	Params    map[string]any `json:"params,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

// Forward sends a tool call to the downstream server and returns its raw response.
// Returns an error if the downstream responds with a non-2xx status or is unreachable.
func (p *Proxy) Forward(ctx context.Context, req ToolRequest) (ToolResponse, error) {
	reqBody := downstreamRequest{
		Tool:      req.ToolName,
		Action:    req.Action,
		Params:    req.Params,
		SessionID: req.SessionID,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("proxy: marshal request: %w", err)
	}

	url := p.downstreamURL + "/v1/mcp/tool_call"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return ToolResponse{}, fmt.Errorf("proxy: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("proxy: downstream request failed (tool=%s action=%s): %w",
			req.ToolName, req.Action, err)
	}
	defer resp.Body.Close()

	slog.Debug("proxy: downstream response",
		"tool", req.ToolName,
		"action", req.Action,
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
	)

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return ToolResponse{}, fmt.Errorf("proxy: read downstream response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return ToolResponse{}, fmt.Errorf("proxy: downstream error %d (tool=%s action=%s): %s",
			resp.StatusCode, req.ToolName, req.Action, string(raw))
	}

	return ToolResponse{Status: resp.StatusCode, Body: json.RawMessage(raw)}, nil
}
