package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/agent_config_vectors.json is shared with eami-agent's
// remoteconfig tests (B-269 Slice 0, decision D10): one file, both modules
// must agree on every limit, version and path code.
type sharedVectors struct {
	Limits   map[string]int64 `json:"limits"`
	Versions []struct {
		Name   string `json:"name"`
		Config struct {
			ScanIntervalSeconds int32    `json:"scan_interval_seconds"`
			EnabledScanners     []string `json:"enabled_scanners"`
			ModelScanPaths      []string `json:"model_scan_paths"`
			ModelFileSizeMB     int32    `json:"model_file_size_mb"`
			MaxReportSizeBytes  int32    `json:"max_report_size_bytes"`
		} `json:"config"`
		Version string `json:"version"`
	} `json:"version_vectors"`
	PathCases []struct {
		Path        string `json:"path"`
		ServerBasic string `json:"server_basic"`
		ServerFull  string `json:"server_full"`
	} `json:"path_cases"`
	LongPath struct {
		Length      int    `json:"length"`
		ServerBasic string `json:"server_basic"`
		ServerFull  string `json:"server_full"`
	} `json:"long_path"`
}

func loadSharedVectors(t *testing.T) sharedVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "agent_config_vectors.json"))
	if err != nil {
		t.Fatalf("shared fixture: %v", err)
	}
	var v sharedVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("shared fixture: %v", err)
	}
	return v
}

// The server's constants equal the shared fixture's (the agent's test checks
// its own against the same file). max_walk_depth is agent-only.
func TestSharedFixture_LimitsMatchServerConstants(t *testing.T) {
	v := loadSharedVectors(t)
	got := map[string]int64{
		"max_model_scan_paths":      MaxModelScanPaths,
		"max_model_scan_path_bytes": MaxModelScanPathBytes,
		"max_enabled_scanners":      MaxEnabledScanners,
		"min_interval_seconds":      MinIntervalSeconds,
		"max_interval_seconds":      MaxIntervalSeconds,
		"min_report_size_bytes":     MinReportSizeBytes,
		"max_report_size_bytes":     MaxReportSizeBytes,
		"min_model_file_size_mb":    MinModelFileSizeMB,
		"max_model_file_size_mb":    MaxModelFileSizeMB,
	}
	for k, want := range got {
		if fv, ok := v.Limits[k]; !ok || fv != want {
			t.Errorf("%s: server %d, fixture %d (present %v)", k, want, fv, ok)
		}
	}
	if len(v.Limits) != len(got)+1 { // + max_walk_depth (agent-only)
		t.Errorf("fixture has %d limits; expected the server's %d plus max_walk_depth", len(v.Limits), len(got))
	}
}

func TestSharedFixture_VersionVectors(t *testing.T) {
	for _, vv := range loadSharedVectors(t).Versions {
		c := AgentConfig{
			ScanIntervalSeconds: vv.Config.ScanIntervalSeconds, EnabledScanners: vv.Config.EnabledScanners,
			ModelScanPaths: vv.Config.ModelScanPaths, ModelFileSizeMB: vv.Config.ModelFileSizeMB,
			MaxReportSizeBytes: vv.Config.MaxReportSizeBytes,
		}
		if got := c.Version(); got != vv.Version {
			t.Errorf("%s: %s, want %s", vv.Name, got, vv.Version)
		}
	}
}

func TestSharedFixture_PathCases(t *testing.T) {
	v := loadSharedVectors(t)
	for _, pc := range v.PathCases {
		if got := ValidateModelScanPaths([]string{pc.Path}); got != pc.ServerBasic {
			t.Errorf("%q: basic %q, want %q", pc.Path, got, pc.ServerBasic)
		}
		if got := ValidateModelScanPathsFull([]string{pc.Path}); got != pc.ServerFull {
			t.Errorf("%q: full %q, want %q", pc.Path, got, pc.ServerFull)
		}
	}
	long := "/" + strings.Repeat("a", v.LongPath.Length-1)
	if got := ValidateModelScanPaths([]string{long}); got != v.LongPath.ServerBasic {
		t.Errorf("long path basic %q", got)
	}
	if got := ValidateModelScanPathsFull([]string{long}); got != v.LongPath.ServerFull {
		t.Errorf("long path full %q", got)
	}
}

func TestPathWarnings(t *testing.T) {
	if w := PathWarnings([]string{"/srv/models"}); w == nil || len(w) != 0 {
		t.Fatalf("clean paths: %#v", w)
	}
	w := PathWarnings([]string{"/home", "/Users", `C:\\Users`, "/srv"})
	if len(w) != 1 || w[0] != CodePathProfileParent {
		t.Fatalf("legacy default: %v", w)
	}
	w = PathWarnings([]string{"/", "/home"})
	if len(w) != 2 || w[0] != CodePathProfileParent || w[1] != CodePathRoot {
		t.Fatalf("root + parent: %v", w)
	}
	if len(AgentConfigDefaults.ModelScanPaths) != 0 {
		t.Fatalf("new agents must default to no model paths (S4): %v", AgentConfigDefaults.ModelScanPaths)
	}
}
