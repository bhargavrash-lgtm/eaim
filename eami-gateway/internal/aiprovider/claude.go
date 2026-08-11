package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	claudeDefaultTimeout  = 30 * time.Second // matches toolrouter's defaultTimeout
	claudeMaxResponseSize = 10 * 1024 * 1024 // matches toolrouter's maxResponseSize
	claudeAPIVersion      = "2023-06-01"     // Anthropic's documented current stable Messages API version
	claudeMessagesURL     = "https://api.anthropic.com/v1/messages"
)

// ClaudeAdapter dispatches ai_provider tool calls to Anthropic's real
// Messages API. The first, complete implementation of Adapter -- not a
// special case with Adapter wrapped around it after the fact; a future
// GeminiAdapter/OpenAIAdapter is a new file satisfying the same interface,
// not a rework of anything here or in router.go.
type ClaudeAdapter struct {
	client *http.Client
}

// NewClaudeAdapter builds a ClaudeAdapter. Always dials via the real
// safeDialContext in production; tests use newClaudeAdapterWithDialer to
// reach a local httptest server without tripping the loopback block,
// mirroring toolrouter's identical New/newWithDialer split.
func NewClaudeAdapter() *ClaudeAdapter {
	return newClaudeAdapterWithDialer(safeDialContext)
}

func newClaudeAdapterWithDialer(dial dialContextFunc) *ClaudeAdapter {
	return &ClaudeAdapter{
		client: &http.Client{
			Timeout:   claudeDefaultTimeout,
			Transport: &http.Transport{DialContext: dial},
		},
	}
}

// claudeMessagesURLOverride is a test seam only -- production always
// dispatches to the real claudeMessagesURL. Never reassigned outside
// _test.go files, mirroring cmd/gateway/main.go's tokenUsageWriteFunc
// convention.
var claudeMessagesURLOverride string

func (a *ClaudeAdapter) Provider() string { return "claude" }

// Dispatch supports exactly one action, "messages" (Anthropic's Messages
// API, POST /v1/messages) -- any other action is a clean rejection, not a
// silent fallback. req.Params is sent to Claude as-is: the caller (the
// calling agent) is expected to supply a body already shaped like Claude's
// real Messages API request (model/messages/max_tokens/...) -- this
// adapter does not attempt to normalize a provider-agnostic request shape
// into Claude's own (confirmed unrealistic across providers without real
// per-provider translation work, out of this brief's scope).
func (a *ClaudeAdapter) Dispatch(ctx context.Context, creds Credentials, req Request) (Response, error) {
	if req.Action != "messages" {
		return Response{}, fmt.Errorf("aiprovider/claude: unsupported action %q (only \"messages\" is implemented)", req.Action)
	}
	if creds.APIKey == "" {
		return Response{}, errors.New("aiprovider/claude: no api_key configured for this connector")
	}

	bodyBytes, err := json.Marshal(req.Params)
	if err != nil {
		return Response{}, fmt.Errorf("aiprovider/claude: marshal request: %w", err)
	}

	targetURL := claudeMessagesURL
	if claudeMessagesURLOverride != "" {
		targetURL = claudeMessagesURLOverride
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return Response{}, fmt.Errorf("aiprovider/claude: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Claude's real auth scheme -- x-api-key + anthropic-version, NOT the
	// "Authorization: Bearer" toolrouter.Forward assumes for rest_api
	// tools. Confirmed via the Thread A investigation (Part A): every
	// provider's auth mechanism genuinely differs, this is not an
	// oversight to reconcile with toolrouter's header logic.
	httpReq.Header.Set("x-api-key", creds.APIKey)
	httpReq.Header.Set("anthropic-version", claudeAPIVersion)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("aiprovider/claude: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, claudeMaxResponseSize))
	if err != nil {
		return Response{}, fmt.Errorf("aiprovider/claude: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// raw is Claude's own error body (e.g. {"type":"error","error":{...}})
		// -- never creds.APIKey, which is never interpolated into any
		// returned error anywhere in this function.
		return Response{}, fmt.Errorf("aiprovider/claude: downstream error %d: %s", resp.StatusCode, string(raw))
	}

	return Response{StatusCode: resp.StatusCode, Body: json.RawMessage(raw)}, nil
}
