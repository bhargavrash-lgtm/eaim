// Package aiprovider resolves an incoming tool_call's parsed tool name to
// an org-scoped gateway_tools row of type "ai_provider" and dispatches it
// to the named external AI provider (Claude first) via a small, real
// per-provider Adapter interface -- not a Claude-only integration with an
// interface bolted on top.
//
// Scope (AI Provider Connector, Thread A Model 1): ai_provider-type rows
// only. A tool name that resolves to no row, or to a row of a different
// type, is left entirely to the caller (cmd/gateway/main.go's dispatch) to
// fall back to its existing resolution order -- this package never decides
// that fallback itself, mirroring toolrouter's identical convention for
// rest_api (see toolrouter/router.go's own doc comment).
//
// Deliberately does not import eami-gateway/internal/toolrouter's Resolve/
// Forward/dial machinery: toolrouter.Resolve's SELECT doesn't carry the new
// provider/audit_mode columns this package needs, and toolrouter is
// read-only scope for this brief (its rest_api dispatch logic is frozen).
// toolrouter.Cipher and toolrouter.Credentials ARE reused directly via
// import, unmodified -- both are already exported, decrypt-only, and this
// package's credentials are sealed by the exact same B-022 cipher/key any
// other gateway_tools row's credentials are.
package aiprovider

import (
	"context"
	"encoding/json"
)

// Request is one dispatched call to a provider, translated from the
// agent's own tool_call (Tool = the gateway_tools row's name, e.g.
// "claude"; Action/Params below map 1:1 onto ac.Action/ac.Parameters).
type Request struct {
	// Action is the provider action requested, e.g. "messages". Each
	// Adapter defines its own valid action set and rejects anything else
	// cleanly -- there is no shared action vocabulary across providers.
	Action string
	// Params is the caller-supplied request body, in the target
	// provider's own native wire shape (e.g. Claude's Messages API body:
	// model/messages/max_tokens/...). This package does not attempt to
	// normalize request shapes across providers -- confirmed unrealistic
	// without real per-provider translation work, out of this brief's
	// scope (see the Thread A investigation, Part A).
	Params map[string]any
}

// Response is a provider's reply, already fully buffered -- streaming is
// explicitly out of scope for this brief (Model 2 territory).
type Response struct {
	StatusCode int
	Body       json.RawMessage
}

// Adapter is the real, common interface every AI provider integration
// implements. Claude's implementation (claude.go) is the first complete
// instance of it, not a special case with this interface wrapped around it
// after the fact -- a future Gemini/OpenAI adapter is a new file
// satisfying this same interface plus one registry entry, not a rework of
// dispatch, resolution, or governance wiring.
type Adapter interface {
	// Provider returns this adapter's canonical identifier, matching the
	// gateway_tools.provider value that selects it (e.g. "claude").
	Provider() string

	// Dispatch translates req into the provider's real wire request using
	// creds, calls it, and translates the response back. Every failure
	// path -- unsupported action, missing/invalid credentials, a
	// downstream error -- must be a clean returned error, never a panic
	// and never a response that echoes creds back in any form.
	Dispatch(ctx context.Context, creds Credentials, req Request) (Response, error)
}

// Credentials is the decrypted credential material available to an
// Adapter. Decoded from the exact same stored JSON shape toolrouter's own
// Credentials type reads (eami-api's CreateTool stores the client's raw
// submitted credentials JSON unchanged, per B-022 -- {"api_key": "..."}
// for any api_key-auth tool, this type included) -- kept as this
// package's own type rather than importing toolrouter.Credentials so a
// provider-specific field could be added later (e.g. a Vertex AI
// service-account JSON blob) without widening toolrouter's own
// REST-tool-shaped struct with fields no AI provider needs.
type Credentials struct {
	APIKey string `json:"api_key"`
}
