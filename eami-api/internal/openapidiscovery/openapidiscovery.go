// Package openapidiscovery parses an admin-supplied OpenAPI 3.x spec into
// candidate gateway_tools.action_paths entries (B-046's existing shape,
// extended with an optional input_schema field) for review before a real
// PATCH activates them. Purely definition-time: nothing in this package is
// ever called from eami-gateway's dispatch path.
//
// Untrusted-input surface: specs are admin-supplied (upload or URL) and
// parsed with github.com/getkin/kin-openapi. External $ref resolution is
// deliberately left disabled (the loader's zero-value default,
// IsExternalRefsAllowed=false) -- confirmed empirically, not assumed, that
// this rejects local-file, relative-path, and remote-URL $refs alike at
// parse time (see openapidiscovery_test.go). This closes both a
// local-file-read vector and a second, less obvious SSRF vector: a
// malicious spec's own $ref could otherwise make the SERVER fetch an
// attacker-chosen URL during parsing, independent of the one spec_url the
// caller explicitly asked to fetch.
package openapidiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// Bounds -- "a bounded OpenAPI-parsing library" per this brief's own
// assumptions. MaxSpecBytes applies to both an uploaded/pasted spec and a
// server-fetched one; maxActions caps how many operations are turned into
// candidate actions (a truncation warning is added, never a silent drop).
//
// MaxSpecBytes is exported so the HTTP layer (openapi_discover.go, a
// different package) can bound the raw request body via
// http.MaxBytesReader BEFORE json.Decode ever buffers it -- a real gap
// this brief's own mandatory security review found: FetchSpec already
// bounded a server-fetched spec via io.LimitReader, but the direct-paste
// path had no size limit until AFTER the full body was already read into
// memory. Both paths must share this one constant, not two independently
// maintained numbers that could drift apart.
const (
	MaxSpecBytes      = 5 << 20 // 5 MiB
	maxActions        = 300
	specFetchTimeout  = 8 * time.Second
	maxSpecFetchBytes = MaxSpecBytes
)

// allowedMethods mirrors eami-api/internal/api.allowedActionPathMethods
// exactly (a small, deliberate duplication -- the two packages don't share
// unexported symbols, and this list is 5 constants, not worth exporting
// just for this). Any operation using a method outside this set (HEAD,
// OPTIONS, TRACE, CONNECT) is skipped, since action_paths' own validation
// would reject it anyway if the admin tried to activate it.
var allowedMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// Action is one candidate action_paths entry, generated from a single
// OpenAPI operation.
type Action struct {
	Path          string         `json:"path"`
	Method        string         `json:"method"`
	Summary       string         `json:"summary,omitempty"`
	InputSchema   map[string]any `json:"input_schema,omitempty"`
	HasPathParams bool           `json:"has_path_params"`
}

// Result is the full response of parsing a spec: every generated action,
// plus any warnings (path-param actions that won't dispatch correctly
// today, truncation, non-fatal spec validation issues) the admin should see
// before deciding what to activate.
type Result struct {
	Actions  map[string]Action `json:"actions"`
	Warnings []string          `json:"warnings,omitempty"`
}

// DialContextFunc matches the signature needed for a custom
// http.Transport.DialContext -- deliberately identical to
// eami-api/internal/api's existing safeDialContext, so FetchSpec can be
// called with that exact function value (no adapter needed) to reuse
// TestTool's already-reviewed SSRF guard rather than duplicating it.
type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// FetchSpec retrieves specURL's body via a client dialing ONLY through
// dial (the caller's SSRF-guarded dialer -- this function has no default
// dialer of its own and will panic on a nil dial, deliberately: a caller
// forgetting to pass a real guard should fail loudly in tests, not
// silently dial the real network unguarded). Bounded by
// maxSpecFetchBytes/specFetchTimeout.
func FetchSpec(ctx context.Context, specURL string, dial DialContextFunc) ([]byte, error) {
	if dial == nil {
		panic("openapidiscovery: FetchSpec called with a nil dial func -- an SSRF-guarded dialer is required")
	}
	if !strings.HasPrefix(specURL, "http://") && !strings.HasPrefix(specURL, "https://") {
		return nil, fmt.Errorf("openapidiscovery: spec_url must be http:// or https://")
	}
	client := &http.Client{
		Timeout:   specFetchTimeout,
		Transport: &http.Transport{DialContext: dial},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, fmt.Errorf("openapidiscovery: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openapidiscovery: fetch spec_url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openapidiscovery: fetch spec_url: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openapidiscovery: read spec_url body: %w", err)
	}
	if len(data) > maxSpecFetchBytes {
		return nil, fmt.Errorf("openapidiscovery: spec exceeds %d byte limit", maxSpecFetchBytes)
	}
	return data, nil
}

// Parse parses specBytes (JSON or YAML, kin-openapi auto-detects) into a
// Result. Never resolves an external $ref (local file, relative path, or
// remote URL) -- the loader is left at its default IsExternalRefsAllowed
// = false, which fails LoadFromData outright rather than silently
// resolving or ignoring the ref (verified empirically in
// openapidiscovery_test.go, not assumed from documentation).
func Parse(specBytes []byte) (*Result, error) {
	if len(specBytes) == 0 {
		return nil, errors.New("openapidiscovery: spec is empty")
	}
	if len(specBytes) > MaxSpecBytes {
		return nil, fmt.Errorf("openapidiscovery: spec exceeds %d byte limit", MaxSpecBytes)
	}

	loader := openapi3.NewLoader() // IsExternalRefsAllowed left false (default) -- deliberate, see package doc.
	doc, err := loader.LoadFromData(specBytes)
	if err != nil {
		return nil, fmt.Errorf("openapidiscovery: parse spec: %w", err)
	}

	result := &Result{Actions: map[string]Action{}}

	// Strict validation failure is a warning, not a hard rejection --
	// plenty of real-world specs have minor validation issues (an
	// undeclared example, a loose enum) while still being perfectly usable
	// for extracting real paths/methods/parameters. Rejecting those
	// outright would defeat the "paste a spec, see real actions populate"
	// goal for specs that are 95% fine. The admin still reviews every
	// generated action before anything activates, so a best-effort parse
	// of an imperfect spec is safe, not silently trusted.
	if verr := doc.Validate(loader.Context); verr != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("spec failed strict OpenAPI validation (%v) -- actions below were still extracted on a best-effort basis; review carefully", verr))
	}

	if doc.Paths == nil {
		result.Warnings = append(result.Warnings, "spec has no paths")
		return result, nil
	}

	paths := doc.Paths.Keys()
	sort.Strings(paths) // deterministic order: map iteration order isn't stable, and this feeds a numbered-suffix disambiguator below

	seenNames := map[string]int{}
	truncated := false
	for _, path := range paths {
		item := doc.Paths.Find(path)
		if item == nil {
			continue
		}
		for _, method := range allowedMethods {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}
			if len(result.Actions) >= maxActions {
				truncated = true
				break
			}
			name := actionName(op.OperationID, method, path, seenNames)
			hasPathParams := strings.Contains(path, "{")
			schema, schemaWarnings := buildInputSchema(op)
			action := Action{
				Path:          path,
				Method:        method,
				Summary:       op.Summary,
				InputSchema:   schema,
				HasPathParams: hasPathParams,
			}
			result.Actions[name] = action
			if hasPathParams {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"action %q (%s %s) has a path parameter -- the gateway's current dispatch mechanism does not substitute path parameters into the URL, so this action will not reach the intended endpoint until that's added; review before activating",
					name, method, path))
			}
			for _, w := range schemaWarnings {
				result.Warnings = append(result.Warnings, fmt.Sprintf("action %q: %s", name, w))
			}
		}
		if truncated {
			break
		}
	}
	if truncated {
		result.Warnings = append(result.Warnings, fmt.Sprintf("spec has more than %d operations; only the first %d (in path order) are shown", maxActions, maxActions))
	}

	return result, nil
}

// buildInputSchema synthesizes one JSON-Schema-shaped object per action,
// combining the operation's parameters (path/query/header) and its
// application/json request body, matching mcp.ToolDefinition.InputSchema's
// existing {"type":"object", ...} shape (B-061) so tools/list can use this
// directly with zero shape changes -- only the CONTENT of an existing
// field becomes real instead of the generic {"type":"object"} B-061
// shipped with.
//
// Returns collision warnings alongside the schema: a code-review finding
// on this brief caught that a request-body property sharing a name with a
// path/query/header parameter (a real, if uncommon, shape -- e.g. a query
// param and a body field both called "format") was silently overwritten
// by the body property with no signal at all. Parameters are processed
// first and kept on a name collision (never overwritten) -- both because a
// path/query/header parameter's placement is unambiguous and load-bearing
// for the dispatch-limitation warning above, while a same-named body
// field is comparatively recoverable (rename it in the spec, or edit the
// generated schema after review) -- but the collision itself is always
// reported, never silently resolved either way.
func buildInputSchema(op *openapi3.Operation) (map[string]any, []string) {
	properties := map[string]any{}
	var required []string
	var warnings []string

	for _, pRef := range op.Parameters {
		if pRef == nil || pRef.Value == nil {
			continue
		}
		p := pRef.Value
		if p.Name == "" {
			continue
		}
		var schema map[string]any
		if p.Schema != nil && p.Schema.Value != nil {
			schema = schemaToMap(p.Schema.Value)
		} else {
			schema = map[string]any{"type": "string"}
		}
		if p.Description != "" {
			schema["description"] = p.Description
		}
		// "in" (path/query/header) isn't a standard JSON-Schema keyword --
		// included anyway, since it's the only place this information
		// (where a real REST call would place this value) survives into
		// the generated schema at all, and it's directly relevant to the
		// path-parameter dispatch limitation warned about separately.
		schema["in"] = p.In
		properties[p.Name] = schema
		if p.Required {
			required = append(required, p.Name)
		}
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		mt := op.RequestBody.Value.GetMediaType("application/json")
		if mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
			body := mt.Schema.Value
			if isObjectType(body) && len(body.Properties) > 0 {
				// Merge the body's own declared properties directly into
				// the top level -- a real client of this MCP tool sees one
				// flat set of arguments, matching how toolrouter.Forward
				// already sends the whole arguments map as the dispatch
				// body's "params" (see this package's own doc comment on
				// the path-parameter limitation for the parallel caveat).
				for name, propRef := range body.Properties {
					if propRef == nil || propRef.Value == nil {
						continue
					}
					if _, exists := properties[name]; exists {
						warnings = append(warnings, fmt.Sprintf(
							"both a parameter and a request body property are named %q -- keeping the parameter's schema; the body property's own schema was NOT merged in and this action's input_schema may be incomplete, review before activating",
							name))
						continue
					}
					properties[name] = schemaToMap(propRef.Value)
				}
				required = append(required, body.Required...)
			} else if _, exists := properties["body"]; exists {
				warnings = append(warnings, `a parameter is already named "body" -- the request body's own schema was NOT added under that name and this action's input_schema is incomplete, review before activating`)
			} else {
				// A non-object body (an array, a scalar, or an object with
				// no declared properties) can't be flattened into named
				// top-level properties -- represented honestly as a single
				// "body" property carrying the real schema, rather than
				// fabricating field names that aren't in the spec.
				properties["body"] = schemaToMap(body)
				if op.RequestBody.Value.Required {
					required = append(required, "body")
				}
			}
		}
	}

	out := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		out["required"] = dedupeStrings(required)
	}
	return out, warnings
}

func isObjectType(s *openapi3.Schema) bool {
	if s.Type == nil {
		return false
	}
	types := *s.Type
	return len(types) == 1 && types[0] == "object"
}

// schemaToMap converts a resolved *openapi3.Schema to a plain
// map[string]any by round-tripping through its own real JSON marshaling
// (matching the wire shape a real MCP client would see in inputSchema),
// rather than hand-rolling a JSON-Schema conversion.
func schemaToMap(s *openapi3.Schema) map[string]any {
	b, err := json.Marshal(s)
	if err != nil {
		return map[string]any{"type": "string"}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{"type": "string"}
	}
	return m
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

var actionNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
var repeatedUnderscore = regexp.MustCompile(`_+`)

// actionName derives a stable action_paths key: the spec's own
// operationId if present (preferred -- it's the author's own intended
// identifier), else a slug synthesized from method+path. Collisions
// (e.g. two operations sanitizing to the same slug) get a numeric suffix
// rather than silently overwriting one action with another.
func actionName(operationID, method, path string, seen map[string]int) string {
	base := sanitizeActionName(operationID)
	if base == "" {
		base = sanitizeActionName(strings.ToLower(method) + "_" + path)
	}
	if base == "" {
		base = strings.ToLower(method)
	}
	n := seen[base]
	seen[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, n+1)
}

func sanitizeActionName(s string) string {
	s = actionNameSanitizer.ReplaceAllString(s, "_")
	s = repeatedUnderscore.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}
