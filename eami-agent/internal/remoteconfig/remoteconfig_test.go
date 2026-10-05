package remoteconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Same inputs and outputs as eami-api's store.configVersionGolden, also
// recomputed independently (Python) when they were pinned. If this fails,
// the agent and the server disagree on the canonical form and every
// versioned config would be rejected as version_mismatch.
var golden = []struct {
	name string
	cfg  Config
	want string
}{
	{"defaults", Config{
		ScanIntervalSeconds: 300,
		EnabledScanners:     []string{"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser", "ai_processes", "gpu", "python_envs", "nodejs_ai"},
		ModelScanPaths:      []string{"/home", "/Users", `C:\Users`},
		ModelFileSizeMB:     100,
		MaxReportSizeBytes:  5242880,
	}, "c1:e13b658a7e5071ab4d196d1dc42eb1b32d15041f075bf12a4db95cac0cc8b8ad"},
	{"unsorted, duplicated, empty paths", Config{
		ScanIntervalSeconds: 3600,
		EnabledScanners:     []string{"gpu", "ai_apps", "gpu"},
		ModelScanPaths:      []string{},
		ModelFileSizeMB:     250,
		MaxReportSizeBytes:  1048576,
	}, "c1:d8d8341e266e8f725147e368c5d0bb8e34a9d8028d39f235b92b03fa1b79c958"},
	{"escaping", Config{
		ScanIntervalSeconds: 60,
		EnabledScanners:     nil,
		ModelScanPaths:      []string{`/opt/<m>&"q"`, "D:/Models"},
		ModelFileSizeMB:     1,
		MaxReportSizeBytes:  52428800,
	}, "c1:5cbc77f53148d018dfeb6a6b93b492f7d18a794e64b807f2e40b013c45e3d9f9"},
}

func TestVersion_GoldenVectorsMatchServer(t *testing.T) {
	for _, g := range golden {
		if got := g.cfg.Version(); got != g.want {
			t.Errorf("%s: Version() = %s, want %s", g.name, got, g.want)
		}
	}
}

func validConfig() Config {
	return Config{ScanIntervalSeconds: 300, EnabledScanners: []string{"ai_apps", "models"},
		ModelScanPaths: []string{"/srv/models"}, ModelFileSizeMB: 100, MaxReportSizeBytes: 5 << 20}
}

// body builds a versioned response; mutate edits the decoded map first.
func body(t *testing.T, c Config, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"agent_id": "x", "updated_at": "2026-10-05T00:00:00Z",
		"scan_interval_seconds": c.ScanIntervalSeconds, "enabled_scanners": c.EnabledScanners,
		"model_scan_paths": c.ModelScanPaths, "model_file_size_mb": c.ModelFileSizeMB,
		"max_report_size_bytes": c.MaxReportSizeBytes, "config_version": c.Version(),
	}
	if mutate != nil {
		mutate(m)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseResponse_VersionedReplacesWhole(t *testing.T) {
	c := validConfig()
	c.EnabledScanners, c.ModelScanPaths = []string{}, []string{}
	u, reason := ParseResponse(body(t, c, nil))
	if reason != "" || !u.Versioned || u.Version != c.Version() {
		t.Fatalf("got %+v reason %q", u, reason)
	}
	if u.Config.EnabledScanners == nil || len(u.Config.EnabledScanners) != 0 || len(u.Config.ModelScanPaths) != 0 {
		t.Fatalf("empty lists must stay empty, not absent: %+v", u.Config)
	}
}

// Every rejection names its own reason code (decision D-g: never generic).
func TestParseResponse_RejectionReasons(t *testing.T) {
	long := "/" + strings.Repeat("a", MaxModelScanPathBytes)
	many := make([]string, MaxModelScanPaths+1)
	for i := range many {
		many[i] = "/p" + strings.Repeat("x", i)
	}
	scanners := make([]string, MaxEnabledScanners+1)
	for i := range scanners {
		scanners[i] = "s" + strings.Repeat("x", i)
	}
	set := func(field string, v any) func(map[string]any) {
		return func(m map[string]any) { m[field] = v }
	}
	// withConfig re-hashes, so the bounds check (not the hash) must catch it:
	// a hash-valid but hostile config.
	withConfig := func(edit func(*Config)) []byte {
		c := validConfig()
		edit(&c)
		return body(t, c, nil)
	}
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"not json", []byte(`{"config_version":`), ReasonMalformedJSON},
		{"wrong type", body(t, validConfig(), set("scan_interval_seconds", "300")), ReasonWrongType},
		{"list as string", body(t, validConfig(), set("model_scan_paths", "/x")), ReasonWrongType},
		{"missing field", body(t, validConfig(), func(m map[string]any) { delete(m, "model_scan_paths") }), ReasonIncomplete},
		{"null field", body(t, validConfig(), set("enabled_scanners", nil)), ReasonIncomplete},
		{"version mismatch", body(t, validConfig(), set("scan_interval_seconds", 301)), ReasonVersionMismatch},
		{"interval 1 s (hash-valid)", withConfig(func(c *Config) { c.ScanIntervalSeconds = 1 }), ReasonIntervalOutOfRange},
		{"interval huge (hash-valid)", withConfig(func(c *Config) { c.ScanIntervalSeconds = 1 << 40 }), ReasonIntervalOutOfRange},
		{"report size 0 (hash-valid)", withConfig(func(c *Config) { c.MaxReportSizeBytes = 0 }), ReasonReportSizeOutOfRange},
		{"model size 0 (hash-valid)", withConfig(func(c *Config) { c.ModelFileSizeMB = 0 }), ReasonModelSizeOutOfRange},
		{"too many paths (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = many }), ReasonTooManyPaths},
		{"empty path (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{""} }), ReasonPathEmpty},
		{"long path (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{long} }), ReasonPathTooLong},
		{"relative path (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{"models"} }), ReasonPathNotAbsolute},
		{"control char (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{"/a\nb"} }), ReasonPathInvalidChars},
		{"NUL (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{"/a\x00"} }), ReasonPathInvalidChars},
		{"traversal (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{"../../etc"} }), ReasonPathNotAbsolute},
		{"UNC (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{`\\attacker\share`} }), ReasonPathNetwork},
		{"// network (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{"//attacker/x"} }), ReasonPathNetwork},
		{"device path (hash-valid)", withConfig(func(c *Config) { c.ModelScanPaths = []string{`\\?\C:\x`} }), ReasonPathNetwork},
		{"model size too big (hash-valid)", withConfig(func(c *Config) { c.ModelFileSizeMB = MaxModelFileSizeMB + 1 }), ReasonModelSizeOutOfRange},
		{"too many scanners (hash-valid)", withConfig(func(c *Config) { c.EnabledScanners = scanners }), ReasonTooManyScanners},
	}
	for _, tc := range cases {
		if _, reason := ParseResponse(tc.raw); reason != tc.want {
			t.Errorf("%s: reason %q, want %q", tc.name, reason, tc.want)
		}
	}
}

func TestParseResponse_RootPathIsHashValidAndInBounds(t *testing.T) {
	// "/" is well-formed: restricting which roots may be walked is B-277's
	// path allowlist, deliberately not part of this slice. Pinned so the
	// limitation stays visible.
	c := validConfig()
	c.ModelScanPaths = []string{"/"}
	if _, reason := ParseResponse(body(t, c, nil)); reason != "" {
		t.Fatalf("reason %q", reason)
	}
}

// An older server (no config_version): exactly the pre-B-293 merge.
func TestParseResponse_LegacyMergesNonEmptyOnly(t *testing.T) {
	u, reason := ParseResponse([]byte(`{"agent_id":"x","scan_interval_seconds":0,"enabled_scanners":[],"model_scan_paths":["/m"],"max_report_size_bytes":1}`))
	if reason != "" || u.Versioned {
		t.Fatalf("got %+v %q", u, reason)
	}
	if u.Has.Interval || u.Has.Scanners || !u.Has.Paths || u.Config.ModelScanPaths[0] != "/m" {
		t.Fatalf("legacy merge applied the wrong fields: %+v", u)
	}
	legacy := `{"scan_interval_seconds":%d,"enabled_scanners":["gpu"],"model_scan_paths":[],"max_report_size_bytes":1048576%s}`
	if _, reason := ParseResponse([]byte(fmt.Sprintf(legacy, 5, ""))); reason != ReasonIntervalOutOfRange {
		t.Fatalf("legacy interval below the floor: reason %q", reason)
	}
	if u, reason := ParseResponse([]byte(fmt.Sprintf(legacy, 300, `,"config_version":""`))); reason != "" || u.Versioned {
		t.Fatalf("empty config_version is unversioned: reason %q", reason)
	}
	if _, reason := ParseResponse([]byte(fmt.Sprintf(legacy, 300, `,"config_version":null`))); reason != "" {
		t.Fatalf("null config_version is unversioned: reason %q", reason)
	}
	if _, reason := ParseResponse([]byte(`{"config_version":null,"scan_interval_seconds":60,"enabled_scanners":[],"model_scan_paths":["../../etc"],"max_report_size_bytes":1}`)); reason != ReasonPathNotAbsolute {
		t.Fatalf("legacy path still bounds-checked: reason %q", reason)
	}
}

// A body that isn't a config at all -- {} or null from a captive portal or
// proxy -- never counts as an older server's config (security review L-1).
func TestParseResponse_EmptyBodyIsNotALegacyConfig(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"agent_id":"x"}`, `{"scan_interval_seconds":300}`} {
		if _, reason := ParseResponse([]byte(raw)); reason != ReasonIncomplete {
			t.Errorf("%s: reason %q, want incomplete", raw, reason)
		}
	}
}

func TestKnownScanners_DropsUnknownNames(t *testing.T) {
	got, dropped := KnownScanners([]string{"gpu", "future_scanner", "ai_apps"}, []string{"ai_apps", "gpu"})
	if !dropped || len(got) != 2 || got[0] != "gpu" || got[1] != "ai_apps" {
		t.Fatalf("got %v dropped %v", got, dropped)
	}
}

func TestIsAbsoluteScanPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/home": true, `C:\Users`: true, "c:/x": true,
		`\\srv\share`: false, "//srv/share": false, `\\?\C:\x`: false, `\\.\pipe\x`: false,
		"models": false, `C:`: false, `C:Users`: false, "": false, "./x": false,
	} {
		if IsAbsoluteScanPath(p) != want {
			t.Errorf("%q: got %v", p, !want)
		}
	}
}
