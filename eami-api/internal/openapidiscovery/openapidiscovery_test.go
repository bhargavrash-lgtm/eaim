package openapidiscovery

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validSpec = `
openapi: 3.0.0
info:
  title: Test API
  version: "1"
paths:
  /pets:
    get:
      operationId: listPets
      parameters:
        - name: limit
          in: query
          required: false
          schema:
            type: integer
      responses:
        '200':
          description: ok
    post:
      operationId: createPet
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name:
                  type: string
                  description: the pet's name
                tag:
                  type: string
      responses:
        '201':
          description: created
  /pets/{petId}:
    get:
      operationId: getPetById
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: ok
  /noop:
    trace:
      operationId: shouldBeSkipped
      responses:
        '200':
          description: ok
  /no-operation-id:
    get:
      responses:
        '200':
          description: ok
`

// ── Parse: correctness ───────────────────────────────────────────────────────

func TestParse_ValidSpec_GeneratesExpectedActions(t *testing.T) {
	result, err := Parse([]byte(validSpec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(result.Actions) != 4 {
		t.Fatalf("len(Actions) = %d, want 4 (trace method must be skipped): %+v", len(result.Actions), result.Actions)
	}

	list, ok := result.Actions["listPets"]
	if !ok {
		t.Fatal("expected action \"listPets\" (from operationId), not found")
	}
	if list.Method != "GET" || list.Path != "/pets" {
		t.Errorf("listPets = %+v, want method=GET path=/pets", list)
	}
	if list.HasPathParams {
		t.Error("listPets.HasPathParams = true, want false (no {} in /pets)")
	}
	props, _ := list.InputSchema["properties"].(map[string]any)
	if _, ok := props["limit"]; !ok {
		t.Errorf("listPets.InputSchema.properties missing \"limit\" query param: %+v", list.InputSchema)
	}

	create, ok := result.Actions["createPet"]
	if !ok {
		t.Fatal("expected action \"createPet\", not found")
	}
	createProps, _ := create.InputSchema["properties"].(map[string]any)
	if _, ok := createProps["name"]; !ok {
		t.Errorf("createPet.InputSchema.properties missing body property \"name\" (should be flattened to top level): %+v", create.InputSchema)
	}
	if _, ok := createProps["tag"]; !ok {
		t.Errorf("createPet.InputSchema.properties missing body property \"tag\": %+v", create.InputSchema)
	}
	req, _ := create.InputSchema["required"].([]string)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("createPet.InputSchema.required = %v, want [\"name\"] (from the body schema's own required)", req)
	}

	getByID, ok := result.Actions["getPetById"]
	if !ok {
		t.Fatal("expected action \"getPetById\", not found")
	}
	if !getByID.HasPathParams {
		t.Error("getPetById.HasPathParams = false, want true (/pets/{petId} has a path parameter)")
	}
	idProps, _ := getByID.InputSchema["properties"].(map[string]any)
	petID, ok := idProps["petId"].(map[string]any)
	if !ok {
		t.Fatalf("getPetById.InputSchema.properties missing \"petId\": %+v", getByID.InputSchema)
	}
	if petID["in"] != "path" {
		t.Errorf("petId schema[\"in\"] = %v, want \"path\"", petID["in"])
	}

	if _, ok := result.Actions["shouldBeSkipped"]; ok {
		t.Error("a TRACE-method operation must be skipped (not in allowedMethods), but it was generated")
	}
}

func TestParse_NoOperationID_FallsBackToMethodPathSlug(t *testing.T) {
	result, err := Parse([]byte(validSpec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	found := false
	for name, a := range result.Actions {
		if a.Path == "/no-operation-id" && a.Method == "GET" {
			found = true
			if name == "" {
				t.Error("fallback action name is empty")
			}
			t.Logf("fallback name for GET /no-operation-id = %q", name)
		}
	}
	if !found {
		t.Fatal("expected an action for GET /no-operation-id even with no operationId")
	}
}

func TestParse_PathParamAction_ProducesWarning(t *testing.T) {
	result, err := Parse([]byte(validSpec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "getPetById") && strings.Contains(w, "path parameter") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a path-parameter warning mentioning getPetById, got warnings: %v", result.Warnings)
	}
}

// TestParse_ParamAndBodyPropertySameName_ParamKeptWithWarning proves the
// code-review fix: a request body property sharing a name with a
// path/query/header parameter no longer silently overwrites the
// parameter's schema -- the parameter's schema (with its "in" tag) is
// kept, and a warning is emitted naming the exact collision.
func TestParse_ParamAndBodyPropertySameName_ParamKeptWithWarning(t *testing.T) {
	spec := `
openapi: 3.0.0
info: {title: collide, version: "1"}
paths:
  /widgets:
    post:
      operationId: createWidget
      parameters:
        - name: format
          in: query
          required: false
          schema:
            type: string
            enum: [json, xml]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                format:
                  type: integer
      responses:
        '200': {description: ok}
`
	result, err := Parse([]byte(spec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, ok := result.Actions["createWidget"]
	if !ok {
		t.Fatal("expected action createWidget")
	}
	props, _ := a.InputSchema["properties"].(map[string]any)
	format, _ := props["format"].(map[string]any)
	if format == nil {
		t.Fatalf("expected a \"format\" property, got %+v", a.InputSchema)
	}
	if format["in"] != "query" {
		t.Errorf(`properties["format"]["in"] = %v, want "query" (the parameter's schema must be kept, not overwritten by the body property)`, format["in"])
	}
	if format["type"] != "string" {
		t.Errorf(`properties["format"]["type"] = %v, want "string" (the parameter's type, not the body property's "integer")`, format["type"])
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, `"format"`) && strings.Contains(w, "createWidget") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning naming the format collision on createWidget, got warnings: %v", result.Warnings)
	}
}

func TestParse_DuplicateSlugs_Disambiguated(t *testing.T) {
	spec := `
openapi: 3.0.0
info: {title: dup, version: "1"}
paths:
  /a:
    get:
      responses: {'200': {description: ok}}
  /a-:
    get:
      responses: {'200': {description: ok}}
`
	result, err := Parse([]byte(spec))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("len(Actions) = %d, want 2 distinct actions even though both paths sanitize to the same slug base: %+v", len(result.Actions), result.Actions)
	}
}

// ── Parse: bounds and rejection ──────────────────────────────────────────────

func TestParse_EmptySpec_Rejected(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Error("Parse(nil): want error, got nil")
	}
}

func TestParse_OversizedSpec_Rejected(t *testing.T) {
	huge := make([]byte, MaxSpecBytes+1)
	if _, err := Parse(huge); err == nil {
		t.Error("Parse(oversized): want error, got nil")
	}
}

func TestParse_MalformedSpec_RejectedCleanly(t *testing.T) {
	if _, err := Parse([]byte("this is not a valid openapi spec { [ garbage")); err == nil {
		t.Error("Parse(malformed): want error, got nil")
	}
}

// ── Parse: untrusted-input $ref safety (empirically verified, not assumed) ──

func TestParse_RejectsLocalFileRef(t *testing.T) {
	spec := `
openapi: 3.0.0
info: {title: probe, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: 'file:///etc/passwd#/definitions/Foo'
`
	_, err := Parse([]byte(spec))
	if err == nil {
		t.Fatal("Parse with a file:// $ref: want error, got nil -- local file refs must be rejected, not silently resolved")
	}
	if !strings.Contains(err.Error(), "disallowed external reference") {
		t.Errorf("error = %v, want it to mention the disallowed external reference", err)
	}
}

func TestParse_RejectsRelativePathRef(t *testing.T) {
	spec := `
openapi: 3.0.0
info: {title: probe, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '../../../../../../etc/passwd'
`
	if _, err := Parse([]byte(spec)); err == nil {
		t.Fatal("Parse with a relative-path $ref: want error, got nil")
	}
}

func TestParse_RejectsRemoteHTTPRef(t *testing.T) {
	spec := `
openapi: 3.0.0
info: {title: probe, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: 'http://169.254.169.254/latest/meta-data/'
`
	if _, err := Parse([]byte(spec)); err == nil {
		t.Fatal("Parse with a remote http $ref: want error, got nil -- this is a real SSRF vector distinct from the spec_url fetch itself (a malicious spec's own $ref making the server fetch an attacker URL during parsing)")
	}
}

// ── FetchSpec ─────────────────────────────────────────────────────────────────

// TestFetchSpec_UsesProvidedDialer proves FetchSpec actually dials through
// the caller-supplied dial function, not some other path -- the real proof
// that a caller's SSRF guard is genuinely wired in, not just present as an
// unused parameter.
func TestFetchSpec_UsesProvidedDialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"openapi":"3.0.0"}`))
	}))
	defer srv.Close()

	dialCalled := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCalled = true
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}

	data, err := FetchSpec(context.Background(), srv.URL, dial)
	if err != nil {
		t.Fatalf("FetchSpec: %v", err)
	}
	if !dialCalled {
		t.Error("FetchSpec did not invoke the provided dial function at all -- the SSRF guard would never actually run")
	}
	if string(data) != `{"openapi":"3.0.0"}` {
		t.Errorf("FetchSpec body = %q, want the real server response", data)
	}
}

// TestFetchSpec_DialRejection_PropagatesAsError proves a dial function that
// refuses a connection (the real shape safeDialContext's rejection takes)
// actually surfaces as a FetchSpec error, not silently swallowed/ignored.
func TestFetchSpec_DialRejection_PropagatesAsError(t *testing.T) {
	rejectAll := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("connections to loopback/link-local/private addresses are not permitted")
	}
	_, err := FetchSpec(context.Background(), "http://127.0.0.1:1/spec.json", rejectAll)
	if err == nil {
		t.Fatal("FetchSpec with a rejecting dialer: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("error = %v, want it to surface the dialer's own rejection reason", err)
	}
}

func TestFetchSpec_NilDial_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("FetchSpec with a nil dial func: want a panic (fail loudly), got none")
		}
	}()
	_, _ = FetchSpec(context.Background(), "http://example.com/spec.json", nil)
}

func TestFetchSpec_NonHTTPScheme_Rejected(t *testing.T) {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Fatal("dial should never be reached for a non-http(s) scheme URL")
		return nil, nil
	}
	if _, err := FetchSpec(context.Background(), "file:///etc/passwd", dial); err == nil {
		t.Error("FetchSpec with a file:// URL: want error, got nil")
	}
}

func TestFetchSpec_OversizedResponse_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxSpecFetchBytes+1))
	}))
	defer srv.Close()

	var d net.Dialer
	_, err := FetchSpec(context.Background(), srv.URL, d.DialContext)
	if err == nil {
		t.Error("FetchSpec with an oversized response: want error, got nil")
	}
}

func TestFetchSpec_NonOKStatus_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var d net.Dialer
	_, err := FetchSpec(context.Background(), srv.URL, d.DialContext)
	if err == nil {
		t.Error("FetchSpec against a 404: want error, got nil")
	}
}
