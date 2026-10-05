package remoteconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var testID = NewIdentity("agent-1", "https://collector.example:8888")

// newStore mirrors the real layout: Base is the trusted, pre-existing
// directory (like %ProgramData%), and Dir is two levels below it (like
// EAMI\Agent), so the whole-chain checks are exercised.
func newStore(t *testing.T) Store {
	t.Helper()
	base := t.TempDir()
	return Store{Base: base, Dir: filepath.Join(base, "EAMI", "Agent")}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != "" {
		t.Fatalf("absent file: ok=%v reason=%q", ok, reason)
	}
	c := validConfig()
	c.ModelScanPaths = []string{}
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, v, reason, ok, detail := s.Load(testID)
	if !ok || reason != "" || v != c.Version() || got.Version() != c.Version() {
		t.Fatalf("load: ok=%v reason=%q detail=%v got=%+v", ok, reason, detail, got)
	}
	if got.ModelScanPaths == nil || len(got.ModelScanPaths) != 0 {
		t.Fatalf("cleared paths must stay cleared: %#v", got.ModelScanPaths)
	}
}

// writeRaw replaces the state file's contents in place (keeping its
// protections), the way a tamperer with write access would.
func writeRaw(t *testing.T, s Store, raw []byte) {
	t.Helper()
	if err := os.WriteFile(s.path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func savedJSON(t *testing.T, s Store) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(s.path())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestStore_LoadRejects(t *testing.T) {
	c := validConfig()
	cases := []struct {
		name   string
		mutate func(t *testing.T, s Store)
		id     Identity
		want   string
	}{
		{"garbage", func(t *testing.T, s Store) { writeRaw(t, s, []byte("not json")) }, testID, ReasonStateCorrupt},
		{"oversized", func(t *testing.T, s Store) { writeRaw(t, s, make([]byte, MaxResponseBytes+10)) }, testID, ReasonStateCorrupt},
		{"hand-edited value (hash no longer matches)", func(t *testing.T, s Store) {
			m := savedJSON(t, s)
			m["config"].(map[string]any)["scan_interval_seconds"] = 61
			raw, _ := json.Marshal(m)
			writeRaw(t, s, raw)
		}, testID, ReasonStateCorrupt},
		{"unknown field", func(t *testing.T, s Store) {
			m := savedJSON(t, s)
			m["extra"] = true
			raw, _ := json.Marshal(m)
			writeRaw(t, s, raw)
		}, testID, ReasonStateCorrupt},
		{"hash-valid but out of bounds", func(t *testing.T, s Store) {
			bad := c.Clone()
			bad.ScanIntervalSeconds = 1
			m := savedJSON(t, s)
			m["config"].(map[string]any)["scan_interval_seconds"] = 1
			m["config_version"] = bad.Version()
			raw, _ := json.Marshal(m)
			writeRaw(t, s, raw)
		}, testID, ReasonStateInvalid},
		{"another identity", func(*testing.T, Store) {}, NewIdentity("agent-1", "https://other-collector"), ReasonStateStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, s)
			if _, _, reason, ok, _ := s.Load(tc.id); ok || reason != tc.want {
				t.Fatalf("ok=%v reason=%q, want %q", ok, reason, tc.want)
			}
		})
	}
}

func TestStore_DeleteIsIdempotent(t *testing.T) {
	s := newStore(t)
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != "" {
		t.Fatalf("after delete: ok=%v reason=%q", ok, reason)
	}
}
