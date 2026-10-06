package remoteconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eami/agent/internal/detection/models"
)

// testdata/agent_config_vectors.json is shared with eami-api's store tests
// (B-269 Slice 0, decision D10): one file, both modules must agree.
type vectors struct {
	Limits map[string]int64 `json:"limits"`
	Versions []struct {
		Name   string `json:"name"`
		Config struct {
			ScanIntervalSeconds int64    `json:"scan_interval_seconds"`
			EnabledScanners     []string `json:"enabled_scanners"`
			ModelScanPaths      []string `json:"model_scan_paths"`
			ModelFileSizeMB     int64    `json:"model_file_size_mb"`
			MaxReportSizeBytes  int64    `json:"max_report_size_bytes"`
		} `json:"config"`
		Version string `json:"version"`
	} `json:"version_vectors"`
	PathCases []struct {
		Path  string `json:"path"`
		Agent string `json:"agent"`
	} `json:"path_cases"`
	LongPath struct {
		Length int    `json:"length"`
		Agent  string `json:"agent"`
	} `json:"long_path"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "agent_config_vectors.json"))
	if err != nil {
		t.Fatalf("shared fixture: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("shared fixture: %v", err)
	}
	return v
}

// The agent's constants equal the shared fixture's (the server's test
// checks its own against the same file).
func TestSharedFixture_LimitsMatchAgentConstants(t *testing.T) {
	v := loadVectors(t)
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
		"max_walk_depth":            models.MaxWalkDepth,
	}
	if len(got) != len(v.Limits) {
		t.Fatalf("fixture has %d limits, agent checks %d", len(v.Limits), len(got))
	}
	for k, want := range v.Limits {
		if got[k] != want {
			t.Errorf("%s: agent %d, fixture %d", k, got[k], want)
		}
	}
}

func TestSharedFixture_VersionVectors(t *testing.T) {
	for _, vv := range loadVectors(t).Versions {
		c := Config{
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
	v := loadVectors(t)
	for _, pc := range v.PathCases {
		if got := checkPaths([]string{pc.Path}); got != pc.Agent {
			t.Errorf("%q: agent code %q, want %q", pc.Path, got, pc.Agent)
		}
	}
	long := "/" + strings.Repeat("a", v.LongPath.Length-1)
	if got := checkPaths([]string{long}); got != v.LongPath.Agent {
		t.Errorf("long path: %q, want %q", got, v.LongPath.Agent)
	}
}
