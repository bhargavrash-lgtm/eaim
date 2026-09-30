package payload

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/eami/agent/internal/config"
)

// allScanners is every name Build gates on (must match the API's
// store.AllScanners).
var allScanners = []string{
	"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser",
	"ai_processes", "gpu", "python_envs", "nodejs_ai",
}

func TestRunScan_RecordsEachStatus(t *testing.T) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[string]string{}
	set := func(n, s string) { mu.Lock(); got[n] = s; mu.Unlock() }
	ran := map[string]bool{}
	mark := func(n string) { mu.Lock(); ran[n] = true; mu.Unlock() }

	ctx := context.Background()
	runScan(ctx, &wg, true, "ok", func() error { mark("ok"); return nil }, set)
	runScan(ctx, &wg, true, "err", func() error { mark("err"); return errors.New("boom") }, set)
	runScan(ctx, &wg, true, "panic", func() error { mark("panic"); panic("kaboom") }, set)
	runScan(ctx, &wg, false, "off", func() error { mark("off"); return nil }, set)
	wg.Wait()

	want := map[string]string{"ok": ScannerOK, "err": ScannerError, "panic": ScannerError, "off": ScannerDisabled}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("status[%s] = %q, want %q", n, got[n], w)
		}
	}
	if ran["off"] {
		t.Error("a disabled scanner's function ran")
	}
}

// Build records a status for every scanner, "disabled" exactly for the ones
// config excludes, and the field is serialised as scanner_status.
func TestBuild_ScannerStatusCoversEveryScanner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Detection.EnabledScanners = []string{"gpu", "ai_apps"}
	rep, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range rep.ScannerStatus {
		names = append(names, n)
	}
	sort.Strings(names)
	want := append([]string(nil), allScanners...)
	sort.Strings(want)
	if len(names) != len(want) {
		t.Fatalf("scanner_status keys = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("scanner_status keys = %v, want %v", names, want)
		}
	}
	for _, n := range allScanners {
		st := rep.ScannerStatus[n]
		enabled := n == "gpu" || n == "ai_apps"
		if !enabled && st != ScannerDisabled {
			t.Errorf("%s: status %q, want disabled", n, st)
		}
		if enabled && st != ScannerOK && st != ScannerError {
			t.Errorf("%s: status %q, want ok or error", n, st)
		}
	}
	b, _ := json.Marshal(rep)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["scanner_status"]; !ok {
		t.Fatal("report JSON has no scanner_status")
	}
}

// A scanner that returns nil after the scan deadline passed (several stop
// early on ctx.Done() with a partial list) must not be recorded as "ok".
func TestRunScan_DeadlinePassedIsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	var got string
	runScan(ctx, &wg, true, "late", func() error { return nil }, func(_, s string) { got = s })
	wg.Wait()
	if got != ScannerError {
		t.Fatalf("status after deadline = %q, want %q", got, ScannerError)
	}
}
