package remoteconfig

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/eami/agent/internal/config"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func localBase(fileLoaded bool) *config.Config {
	c := &config.Config{FileLoaded: fileLoaded}
	c.Agent.IntervalSecs = 300
	c.Detection.MinModelSizeMB = 100
	c.Detection.EnabledScanners = append([]string(nil), config.AllScanners...)
	c.Detection.ModelFileScanPaths = []string{"/local/models"}
	return c
}

func ok200(t *testing.T, c Config) FetchResult {
	return FetchResult{Status: http.StatusOK, Body: body(t, c, nil)}
}

func newManager(t *testing.T) (*Manager, Store) {
	t.Helper()
	s := newStore(t)
	return NewManager(localBase(true), &s, testID, quiet), s
}

func TestManager_StartsLocal(t *testing.T) {
	m := NewManager(localBase(false), nil, testID, quiet)
	if st := m.Status(); st.Source != SourceDefaults || st.Version != "" || st.MaxReportSize != 0 {
		t.Fatalf("no YAML: %+v", st)
	}
	m = NewManager(localBase(true), nil, testID, quiet)
	if st := m.Status(); st.Source != SourceLocal {
		t.Fatalf("YAML read: %+v", st)
	}
}

// Test (a) at unit level: replace, not merge.
func TestManager_ReplaceSwitchesScannersBackOnAndClearsPaths(t *testing.T) {
	m, _ := newManager(t)
	off := validConfig()
	off.EnabledScanners = []string{"ai_apps"} // models off
	off.ModelScanPaths = []string{"/srv/models"}
	m.Apply(ok200(t, off))
	if snap := m.Snapshot(); snap.Detection.IsEnabled("models") {
		t.Fatal("models still enabled after remote disable")
	}
	on := validConfig()
	on.EnabledScanners = append([]string{}, config.AllScanners...)
	on.ModelScanPaths = []string{}
	m.Apply(ok200(t, on))
	snap := m.Snapshot()
	if !snap.Detection.IsEnabled("models") {
		t.Fatal("models not re-enabled by a full list (merge semantics would keep it off)")
	}
	if len(snap.Detection.ModelFileScanPaths) != 0 {
		t.Fatalf("paths not cleared: %v", snap.Detection.ModelFileScanPaths)
	}
	if st := m.Status(); st.Source != SourceRemote || st.Version != on.Version() || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
}

// Decision D-a: an empty list is "no scanners", not "all".
func TestManager_EmptyScannerListDisablesEverything(t *testing.T) {
	m, _ := newManager(t)
	c := validConfig()
	c.EnabledScanners = []string{}
	m.Apply(ok200(t, c))
	snap := m.Snapshot()
	for _, name := range config.AllScanners {
		if snap.Detection.IsEnabled(name) {
			t.Fatalf("%s enabled under an empty remote list", name)
		}
	}
}

func TestManager_SnapshotIsPrivate(t *testing.T) {
	m, _ := newManager(t)
	m.Apply(ok200(t, validConfig()))
	snap := m.Snapshot()
	snap.Detection.EnabledScanners[0] = "mutated"
	snap.Detection.ModelFileScanPaths[0] = "/mutated"
	again := m.Snapshot()
	if again.Detection.EnabledScanners[0] == "mutated" || again.Detection.ModelFileScanPaths[0] == "/mutated" {
		t.Fatal("a snapshot shares memory with the manager")
	}
	if snap.Detection.MinModelSizeMB != validConfig().ModelFileSizeMB || snap.Agent.IntervalSecs != 300 {
		t.Fatalf("snapshot fields: %+v", snap)
	}
}

// Tests (b) and (c) at unit level: persisted config is in force before the
// first fetch succeeds, and a failed fetch keeps it.
func TestManager_PersistedSurvivesRestart(t *testing.T) {
	m, s := newManager(t)
	c := validConfig()
	c.EnabledScanners = []string{"gpu"}
	m.Apply(ok200(t, c))

	restarted := NewManager(localBase(true), &s, testID, quiet)
	restarted.LoadPersisted()
	if st := restarted.Status(); st.Source != SourcePersisted || st.Version != c.Version() {
		t.Fatalf("after restart: %+v", st)
	}
	if snap := restarted.Snapshot(); !snap.Detection.IsEnabled("gpu") || snap.Detection.IsEnabled("models") {
		t.Fatal("persisted scanner list not in force")
	}
	restarted.Apply(FetchResult{Err: io.ErrUnexpectedEOF})
	if st := restarted.Status(); st.Source != SourcePersisted || st.Error != ReasonFetchFailed || st.Version != c.Version() {
		t.Fatalf("after a failed fetch: %+v", st)
	}
	restarted.Apply(ok200(t, c))
	if st := restarted.Status(); st.Source != SourceRemote || st.Error != "" {
		t.Fatalf("after a good fetch: %+v", st)
	}
}

// Test (f) at unit level: a hostile response never replaces last-known-good.
func TestManager_RejectionsKeepLastKnownGood(t *testing.T) {
	m, s := newManager(t)
	good := validConfig()
	m.Apply(ok200(t, good))
	hostile := good.Clone()
	hostile.ScanIntervalSeconds = 1
	for _, r := range []FetchResult{
		ok200(t, hostile), // hash-valid, out of bounds
		{Status: http.StatusOK, Body: []byte(`{"config_version":"c1:00","scan_interval_seconds":"x"}`)},
		{Status: http.StatusOK, TooLarge: true},
		{Status: http.StatusInternalServerError},
	} {
		m.Apply(r)
		if st := m.Status(); st.Version != good.Version() || st.Error == "" {
			t.Fatalf("after %+v: %+v", r.Status, st)
		}
	}
	if _, v, _, ok, _ := s.Load(testID); !ok || v != good.Version() {
		t.Fatal("a rejected config reached the state file")
	}
}

// Decision D-c with the founder's guard: one 404 changes nothing; the
// second consecutive one deletes the saved config and reverts to local.
func TestManager_TwoConsecutive404sRevertToLocal(t *testing.T) {
	m, s := newManager(t)
	c := validConfig()
	m.Apply(ok200(t, c))
	m.Apply(FetchResult{Status: http.StatusNotFound})
	if st := m.Status(); st.Source != SourceRemote || st.Version != c.Version() {
		t.Fatalf("after one 404: %+v", st)
	}
	if _, err := os.Stat(s.path()); err != nil {
		t.Fatal("saved config deleted after a single 404")
	}
	m.Apply(ok200(t, c)) // anything else resets the count
	m.Apply(FetchResult{Status: http.StatusNotFound})
	if st := m.Status(); st.Source != SourceRemote {
		t.Fatalf("non-consecutive 404s reverted: %+v", st)
	}
	m.Apply(FetchResult{Status: http.StatusNotFound})
	if st := m.Status(); st.Source != SourceLocal || st.Version != "" {
		t.Fatalf("after two consecutive 404s: %+v", st)
	}
	if _, err := os.Stat(s.path()); !os.IsNotExist(err) {
		t.Fatalf("saved config not deleted: %v", err)
	}
	if snap := m.Snapshot(); snap.Detection.ModelFileScanPaths[0] != "/local/models" {
		t.Fatal("local config not back in force")
	}
}

// A state file for another identity is never loaded, so a 404 can't
// delete it either: the guard applies to config tied to this identity.
func TestManager_404sLeaveAnotherIdentitysFile(t *testing.T) {
	s := newStore(t)
	other := NewIdentity("agent-1", "https://old-collector")
	c := validConfig()
	if err := s.Save(other, c, c.Version(), m0()); err != nil {
		t.Fatal(err)
	}
	m := NewManager(localBase(true), &s, testID, quiet)
	m.LoadPersisted()
	if st := m.Status(); st.Source != SourceLocal || st.Error != ReasonStateStale {
		t.Fatalf("stale file: %+v", st)
	}
	m.Apply(FetchResult{Status: http.StatusNotFound})
	m.Apply(FetchResult{Status: http.StatusNotFound})
	if _, err := os.Stat(s.path()); err != nil {
		t.Fatal("another identity's file was deleted")
	}
}

// Test (e) at unit level: an older server's response merges exactly as
// before B-293 and is never persisted.
func TestManager_UnversionedResponseMergesLikeBefore(t *testing.T) {
	m, s := newManager(t)
	legacy, _ := json.Marshal(map[string]any{
		"agent_id": "x", "scan_interval_seconds": 600, "enabled_scanners": []string{},
		"model_scan_paths": []string{"/remote"}, "max_report_size_bytes": 1048576, "updated_at": "t",
	})
	m.Apply(FetchResult{Status: http.StatusOK, Body: legacy})
	snap := m.Snapshot()
	if snap.Agent.IntervalSecs != 600 || snap.Detection.ModelFileScanPaths[0] != "/remote" {
		t.Fatalf("legacy merge not applied: %+v", snap)
	}
	if len(snap.Detection.EnabledScanners) != len(config.AllScanners) {
		t.Fatal("legacy [] cleared the scanner list (it must mean 'unchanged')")
	}
	if st := m.Status(); st.Source != SourceRemote || st.Version != "" || st.MaxReportSize != 0 {
		t.Fatalf("legacy status: %+v", st)
	}
	if _, err := os.Stat(s.path()); !os.IsNotExist(err) {
		t.Fatal("an unversioned config was persisted")
	}
}

func TestManager_MaxReportSizeOnlyFromVersionedConfig(t *testing.T) {
	m, _ := newManager(t)
	c := validConfig()
	m.Apply(ok200(t, c))
	if st := m.Status(); st.MaxReportSize != c.MaxReportSizeBytes {
		t.Fatalf("status %+v", st)
	}
}

// Security review M-1: the saved config is the server's, unfiltered, so its
// hash still matches after a restart even when this build ignores a name.
func TestManager_PersistedConfigWithUnknownScannerSurvivesRestart(t *testing.T) {
	m, s := newManager(t)
	c := validConfig()
	c.EnabledScanners = []string{"gpu", "scanner_from_the_future"}
	m.Apply(ok200(t, c))
	restarted := NewManager(localBase(true), &s, testID, quiet)
	restarted.LoadPersisted()
	st := restarted.Status()
	if st.Source != SourcePersisted || st.Version != c.Version() || st.Error != "" {
		t.Fatalf("after restart: %+v", st)
	}
	if snap := restarted.Snapshot(); len(snap.Detection.EnabledScanners) != 1 || !snap.Detection.IsEnabled("gpu") {
		t.Fatalf("filtered list after restart: %v", snap.Detection.EnabledScanners)
	}
}

// Code review M2: a legacy merge puts a remote config in force without a
// state file of this identity's, so two 404s must not delete the stale file
// that belongs to another identity.
func Test404sAfterLegacyMergeLeaveAnotherIdentitysFile(t *testing.T) {
	s := newStore(t)
	c := validConfig()
	if err := s.Save(NewIdentity("agent-1", "https://old-collector"), c, c.Version(), m0()); err != nil {
		t.Fatal(err)
	}
	m := NewManager(localBase(true), &s, testID, quiet)
	m.LoadPersisted()
	legacy, _ := json.Marshal(map[string]any{"scan_interval_seconds": 600, "enabled_scanners": []string{"gpu"},
		"model_scan_paths": []string{}, "max_report_size_bytes": 1048576})
	m.Apply(FetchResult{Status: http.StatusOK, Body: legacy})
	m.Apply(FetchResult{Status: http.StatusNotFound})
	m.Apply(FetchResult{Status: http.StatusNotFound})
	if st := m.Status(); st.Source != SourceLocal {
		t.Fatalf("not reverted to local: %+v", st)
	}
	if _, err := os.Stat(s.path()); err != nil {
		t.Fatal("another identity's file was deleted after a legacy merge")
	}
}

func TestManager_UnknownScannerNamesIgnored(t *testing.T) {
	m, _ := newManager(t)
	c := validConfig()
	c.EnabledScanners = []string{"gpu", "scanner_from_the_future"}
	m.Apply(ok200(t, c))
	snap := m.Snapshot()
	if len(snap.Detection.EnabledScanners) != 1 || !snap.Detection.IsEnabled("gpu") {
		t.Fatalf("got %v", snap.Detection.EnabledScanners)
	}
	if st := m.Status(); st.Version != c.Version() || st.Error != "" {
		t.Fatalf("status %+v (the server's version is still the one in force)", st)
	}
}

func m0() time.Time { return time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC) }
