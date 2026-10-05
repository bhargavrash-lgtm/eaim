package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// B-293 (item 8a): the config wire format always carries the full config
// plus its content-hash version, empty lists are real values, and Endpoint
// Detail shows which config the agent runs versus the one it should.

type b293ConfigResp struct {
	ScanIntervalSeconds int32     `json:"scan_interval_seconds"`
	ModelScanPaths      *[]string `json:"model_scan_paths"`
	EnabledScanners     *[]string `json:"enabled_scanners"`
	MaxReportSizeBytes  int32     `json:"max_report_size_bytes"`
	ModelFileSizeMB     int32     `json:"model_file_size_mb"`
	ConfigVersion       string    `json:"config_version"`
}

func decodeB293(t *testing.T, resp *http.Response, want int, what string) b293ConfigResp {
	t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s: status %d, want %d: %s", what, resp.StatusCode, want, raw)
	}
	var out b293ConfigResp
	if want == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if out.ModelScanPaths == nil || out.EnabledScanners == nil {
			t.Fatalf("%s: a list was null or missing: %s", what, raw)
		}
	}
	return out
}

func (r b293ConfigResp) version() string {
	return store.AgentConfig{
		ScanIntervalSeconds: r.ScanIntervalSeconds, ModelScanPaths: *r.ModelScanPaths,
		EnabledScanners: *r.EnabledScanners, MaxReportSizeBytes: r.MaxReportSizeBytes,
		ModelFileSizeMB: r.ModelFileSizeMB,
	}.Version()
}

func TestAgentConfig_B293_FullConfigEmptyListsAndBounds_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b293-config")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@b293.test", "admin")
	agent := seedConfigAgent(t, env, ctx, orgID, "b293-agent")
	path := fmt.Sprintf("/v1/gateway/agents/%s/config", agent)

	got := decodeB293(t, env.do(t, http.MethodGet, path, admin, nil), http.StatusOK, "GET default")
	if got.ModelFileSizeMB != 100 || got.ConfigVersion != got.version() || !strings.HasPrefix(got.ConfigVersion, "c1:") {
		t.Fatalf("default config: %+v", got)
	}

	// [] is a value for both lists; absent still means unchanged.
	put := decodeB293(t, env.do(t, http.MethodPut, path, admin, map[string]any{
		"model_scan_paths": []string{}, "enabled_scanners": []string{}, "model_file_size_mb": 250,
	}), http.StatusOK, "PUT empty lists")
	if len(*put.ModelScanPaths) != 0 || len(*put.EnabledScanners) != 0 || put.ModelFileSizeMB != 250 || put.ScanIntervalSeconds != 300 {
		t.Fatalf("after PUT []: %+v", put)
	}
	if put.ConfigVersion == got.ConfigVersion || put.ConfigVersion != put.version() {
		t.Fatalf("version did not follow the content: %s -> %s", got.ConfigVersion, put.ConfigVersion)
	}
	var dbScanners, dbPaths, dbSize int
	if err := env.pool.QueryRow(ctx, `SELECT cardinality(enabled_scanners), cardinality(model_scan_paths), model_file_size_mb FROM agent_configs WHERE agent_id=$1`, agent).
		Scan(&dbScanners, &dbPaths, &dbSize); err != nil {
		t.Fatal(err)
	}
	if dbScanners != 0 || dbPaths != 0 || dbSize != 250 {
		t.Fatalf("stored: scanners %d paths %d size %d", dbScanners, dbPaths, dbSize)
	}
	again := decodeB293(t, env.do(t, http.MethodGet, path, admin, nil), http.StatusOK, "GET after PUT")
	if again.ConfigVersion != put.ConfigVersion {
		t.Fatal("an unchanged config read back with a different version")
	}
	untouched := decodeB293(t, env.do(t, http.MethodPut, path, admin, map[string]any{"scan_interval_seconds": 600}), http.StatusOK, "PUT interval only")
	if len(*untouched.EnabledScanners) != 0 || untouched.ModelFileSizeMB != 250 {
		t.Fatalf("an absent field changed: %+v", untouched)
	}

	// The agent's own bounds, checked server-side first.
	many := make([]string, store.MaxModelScanPaths+1)
	for i := range many {
		many[i] = fmt.Sprintf("/p%d", i)
	}
	for name, body := range map[string]map[string]any{
		"model size 0":        {"model_file_size_mb": 0},
		"model size too big":  {"model_file_size_mb": 100001},
		"relative path":       {"model_scan_paths": []string{"models"}},
		"control char":        {"model_scan_paths": []string{"/a\nb"}},
		"empty path":          {"model_scan_paths": []string{""}},
		"too long path":       {"model_scan_paths": []string{"/" + strings.Repeat("a", store.MaxModelScanPathBytes)}},
		"too many paths":      {"model_scan_paths": many},
		"unknown scanner":     {"enabled_scanners": []string{"nope"}},
		"interval below 60":   {"scan_interval_seconds": 59},
		"report size too big": {"max_report_size_bytes": 60 << 20},
	} {
		resp := env.do(t, http.MethodPut, path, admin, body)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, resp.StatusCode, raw)
		}
		if name == "relative path" && strings.Contains(string(raw), "models") && !strings.Contains(string(raw), "model_scan_paths") {
			t.Errorf("%s: the error echoed the input: %s", name, raw)
		}
	}
	// Both local absolute forms are valid on every platform.
	decodeB293(t, env.do(t, http.MethodPut, path, admin, map[string]any{"model_scan_paths": []string{"/srv/m", `C:\Models`, "D:/m"}}), http.StatusOK, "PUT mixed absolute paths")
	// Network paths never: the SYSTEM agent would authenticate to that host (security review M-2).
	for _, p := range []string{`\\nas\m`, "//nas/m", `\\?\C:\x`, `\\.\pipe\x`} {
		resp := env.do(t, http.MethodPut, path, admin, map[string]any{"model_scan_paths": []string{p}})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("network path %q: status %d, want 400", p, resp.StatusCode)
		}
	}
}

func TestEndpointDetail_B293_AppliedAndExpectedConfig_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b293-endpoint")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@b293e.test", "viewer")
	agent := seedConfigAgent(t, env, ctx, orgID, "b293-linked")

	seedEP := func(host string) uuid.UUID {
		var id uuid.UUID
		if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, $2, $2) RETURNING id`, orgID, host).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	report := func(ep uuid.UUID, body string) {
		if _, err := env.pool.Exec(ctx, `INSERT INTO endpoint_reports (endpoint_id, org_id, collected_at, report) VALUES ($1, $2, now(), $3::jsonb)`, ep, orgID, body); err != nil {
			t.Fatal(err)
		}
	}
	type detail struct {
		Applied  *string `json:"applied_config_version"`
		Source   *string `json:"config_source"`
		Error    *string `json:"config_error"`
		Expected *string `json:"expected_config_version"`
	}
	get := func(ep uuid.UUID) detail {
		t.Helper()
		resp := env.do(t, http.MethodGet, "/v1/endpoints/"+ep.String(), viewer, nil)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET endpoint: %d %s", resp.StatusCode, raw)
		}
		var d detail
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	cfg, err := store.New(env.pool).GetAgentConfig(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	want := cfg.Version()

	// Linked, reporting the expected version.
	linked := seedEP("b293-linked-ep")
	if _, err := env.pool.Exec(ctx, `UPDATE endpoints SET gateway_agent_id=$1 WHERE id=$2`, agent, linked); err != nil {
		t.Fatal(err)
	}
	report(linked, fmt.Sprintf(`{"config_version":%q,"config_source":"remote"}`, want))
	if d := get(linked); str(d.Applied) != want || str(d.Expected) != want || str(d.Source) != "remote" || d.Error != nil {
		t.Fatalf("in sync: applied %s expected %s source %s error %s", str(d.Applied), str(d.Expected), str(d.Source), str(d.Error))
	}
	// The server config changes: a visible mismatch until the agent polls.
	if _, err := env.pool.Exec(ctx, `UPDATE agent_configs SET scan_interval_seconds=900 WHERE agent_id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	if d := get(linked); str(d.Applied) != want || str(d.Expected) == want {
		t.Fatalf("mismatch not visible: applied %s expected %s", str(d.Applied), str(d.Expected))
	}

	// Unlinked, reporting persisted config with a rejection code.
	unlinked := seedEP("b293-unlinked-ep")
	report(unlinked, fmt.Sprintf(`{"config_version":%q,"config_source":"persisted","config_error":"interval_out_of_range"}`, want))
	if d := get(unlinked); d.Expected != nil || str(d.Source) != "persisted" || str(d.Error) != "interval_out_of_range" {
		t.Fatalf("unlinked: %+v", d)
	}

	// An older agent: none of the fields -> null ("not known").
	old := seedEP("b293-old-ep")
	report(old, `{"scanner_status":{"gpu":"ok"}}`)
	if d := get(old); d.Applied != nil || d.Source != nil || d.Error != nil {
		t.Fatalf("old agent: %+v", d)
	}

	// Agent-supplied text never passes through raw.
	hostile := seedEP("b293-hostile-ep")
	report(hostile, `{"config_version":"c1:<script>","config_source":"remote; rm -rf /","config_error":"/home/alice/secret: permission denied"}`)
	if d := get(hostile); d.Applied != nil || str(d.Source) != "unrecognised" || str(d.Error) != "unrecognised" {
		t.Fatalf("hostile report: applied %s source %s error %s", str(d.Applied), str(d.Source), str(d.Error))
	}
	// An unversioned remote config reports "" -- shown as "", not dropped.
	legacy := seedEP("b293-legacy-ep")
	report(legacy, `{"config_version":"","config_source":"remote"}`)
	if d := get(legacy); d.Applied == nil || *d.Applied != "" {
		t.Fatalf("unversioned: %+v", d)
	}
}
