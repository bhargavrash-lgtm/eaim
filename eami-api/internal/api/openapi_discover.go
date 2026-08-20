package api

import (
	"encoding/json"
	"net/http"

	"github.com/eami/api/internal/openapidiscovery"
)

// openAPIDiscoverRequest is POST /v1/gateway/openapi/discover's body.
// Exactly one of SpecURL/SpecContent must be present -- a URL to fetch
// server-side (through the same SSRF-guarded dialer TestTool already uses)
// or the raw spec text (JSON or YAML) the admin pasted/uploaded directly.
type openAPIDiscoverRequest struct {
	SpecURL     string `json:"spec_url"`
	SpecContent string `json:"spec_content"`
}

// DiscoverOpenAPI handles POST /v1/gateway/openapi/discover (B-075).
// Stateless: parses an admin-supplied OpenAPI spec and returns candidate
// action_paths entries for review -- writes nothing to the database. Not
// yet documented in api/openapi.yaml (Architect-EAMI-owned, out of this
// task's scope), matching B-045/B-046's own precedent of shipping an
// undocumented route and flagging the spec gap rather than touching that
// file.
//
// This is real untrusted-input surface (an admin-supplied spec, optionally
// fetched from an admin-supplied URL) -- see internal/openapidiscovery's
// package doc for how $ref resolution and fetch bounds are handled.
func (s *Server) DiscoverOpenAPI(w http.ResponseWriter, r *http.Request) {
	// Bound the raw request body BEFORE json.Decode ever buffers it --
	// found by this brief's own mandatory security review: FetchSpec
	// already bounds a server-fetched spec via io.LimitReader, but a
	// directly-pasted spec_content had no size limit until AFTER the full
	// body was already read into memory (Parse's own check ran too late to
	// prevent the read itself). The 2x factor over openapidiscovery.
	// MaxSpecBytes covers the JSON envelope's own escaping overhead
	// (quotes/backslashes/\uXXXX) around the spec text -- Parse's own
	// exact MaxSpecBytes check on the decoded spec_content still applies
	// afterward, this is only the coarser wire-level bound.
	r.Body = http.MaxBytesReader(w, r.Body, openapidiscovery.MaxSpecBytes*2)

	var body openAPIDiscoverRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON or request body too large")
		return
	}

	hasURL := body.SpecURL != ""
	hasContent := body.SpecContent != ""
	if hasURL == hasContent {
		writeError(w, http.StatusBadRequest, "bad_request", "exactly one of spec_url or spec_content is required")
		return
	}

	specBytes := []byte(body.SpecContent)
	if hasURL {
		// Same dial-override pattern TestTool (tool_connectivity.go) already
		// uses: nil in production means the real safeDialContext; tests
		// inject an unrestricted dialer to reach a local httptest server,
		// which safeDialContext's loopback/private-address block would
		// otherwise correctly refuse.
		dial := dialContextFunc(safeDialContext)
		if s.toolDialOverride != nil {
			dial = s.toolDialOverride
		}
		fetched, err := openapidiscovery.FetchSpec(r.Context(), body.SpecURL, openapidiscovery.DialContextFunc(dial))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "failed to fetch spec_url: "+err.Error())
			return
		}
		specBytes = fetched
	}

	result, err := openapidiscovery.Parse(specBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "failed to parse OpenAPI spec: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
