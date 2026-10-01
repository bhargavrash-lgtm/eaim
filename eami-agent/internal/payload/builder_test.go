package payload

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eami/agent/internal/config"
)

// allScanners is every name Build gates on (must match the API's
// store.AllScanners).
var allScanners = []string{
	"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser",
	"ai_processes", "gpu", "python_envs", "nodejs_ai",
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func okSpec(name string) scannerSpec {
	return scannerSpec{name: name, enabled: true, fn: func(context.Context, func(func())) error { return nil }}
}

func TestCollect_RecordsEachStatusAndReason(t *testing.T) {
	ran := atomic.Bool{}
	specs := []scannerSpec{
		okSpec("t1-ok"),
		{name: "t1-err", enabled: true, fn: func(context.Context, func(func())) error { return errors.New("boom") }},
		{name: "t1-panic", enabled: true, fn: func(context.Context, func(func())) error { panic("kaboom") }},
		{name: "t1-off", enabled: false, fn: func(context.Context, func(func())) error { ran.Store(true); return nil }},
	}
	var mu sync.Mutex
	status, reasons := collect(context.Background(), specs, &mu, quiet)

	want := map[string]string{"t1-ok": ScannerOK, "t1-err": ScannerError, "t1-panic": ScannerError, "t1-off": ScannerDisabled}
	for n, w := range want {
		if status[n] != w {
			t.Errorf("status[%s] = %q, want %q", n, status[n], w)
		}
	}
	wantReason := map[string]string{"t1-err": ReasonError, "t1-panic": ReasonPanic}
	if len(reasons) != len(wantReason) {
		t.Errorf("reasons = %v, want %v", reasons, wantReason)
	}
	for n, w := range wantReason {
		if reasons[n] != w {
			t.Errorf("reasons[%s] = %q, want %q", n, reasons[n], w)
		}
	}
	if ran.Load() {
		t.Error("a disabled scanner's function ran")
	}
}

// A scanner that returns nil after the deadline passed (several stop early on
// ctx.Done() with a partial list) must not be recorded as "ok".
func TestCollect_DeadlinePassedIsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var mu sync.Mutex
	status, reasons := collect(ctx, []scannerSpec{okSpec("t2-late")}, &mu, quiet)
	if status["t2-late"] != ScannerError || reasons["t2-late"] != ReasonTimeout {
		t.Fatalf("status=%q reason=%q, want error/timeout", status["t2-late"], reasons["t2-late"])
	}
}

// B-281: a scanner that never returns must not block the report. collect
// returns at the deadline, the hung scanner is error/timeout, every other
// scanner's committed result is kept, and the hung scanner's late write is
// discarded.
func TestCollect_HungScannerDoesNotBlockAndLateWriteIsDropped(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string // stands in for the report fields
	specs := []scannerSpec{
		{name: "t3-good", enabled: true, fn: func(_ context.Context, commit func(func())) error {
			commit(func() { got = append(got, "good") })
			return nil
		}},
		{name: "t3-hung", enabled: true, fn: func(_ context.Context, commit func(func())) error {
			<-release // blocks like a syscall that ignores ctx
			commit(func() { got = append(got, "late") })
			return nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	status, reasons := collect(ctx, specs, &mu, quiet)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("collect blocked for %v on a hung scanner", took)
	}
	if status["t3-good"] != ScannerOK {
		t.Errorf("good scanner status = %q, want ok", status["t3-good"])
	}
	if status["t3-hung"] != ScannerError || reasons["t3-hung"] != ReasonTimeout {
		t.Errorf("hung scanner status=%q reason=%q, want error/timeout", status["t3-hung"], reasons["t3-hung"])
	}

	// Let the straggler finish; its write must not land.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		inFlight.Lock()
		running := inFlight.running["t3-hung"]
		inFlight.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("report fields = %v, want only the good scanner's write (late write must be dropped)", got)
	}
}

// B-281: across cycles, a scanner whose previous run never returned is not
// started again -- at most one stuck goroutine per scanner, ever.
func TestCollect_StillRunningScannerIsNotRelaunched(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var launches atomic.Int32
	hung := scannerSpec{name: "t4-hung", enabled: true, fn: func(context.Context, func(func())) error {
		launches.Add(1)
		<-release
		return nil
	}}
	for cycle := 1; cycle <= 5; cycle++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		var mu sync.Mutex
		status, reasons := collect(ctx, []scannerSpec{hung, okSpec("t4-ok")}, &mu, quiet)
		cancel()
		wantReason := ReasonTimeout
		if cycle > 1 {
			wantReason = ReasonStillRunning
		}
		if status["t4-hung"] != ScannerError || reasons["t4-hung"] != wantReason {
			t.Fatalf("cycle %d: hung status=%q reason=%q, want error/%s", cycle, status["t4-hung"], reasons["t4-hung"], wantReason)
		}
		if status["t4-ok"] != ScannerOK {
			t.Fatalf("cycle %d: other scanner status = %q, want ok", cycle, status["t4-ok"])
		}
	}
	if n := launches.Load(); n != 1 {
		t.Fatalf("hung scanner launched %d times across 5 cycles, want 1", n)
	}
}

// Build records a status for every scanner, "disabled" exactly for the ones
// config excludes, and serialises scanner_status (and scanner_errors only
// when something failed).
func TestBuild_ScannerStatusCoversEveryScanner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Detection.EnabledScanners = []string{"gpu", "ai_apps"}
	rep, err := BuildWith(cfg, quiet)
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
		if st == ScannerError && rep.ScannerErrors[n] == "" {
			t.Errorf("%s: status error without a reason code", n)
		}
	}
	b, _ := json.Marshal(rep)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["scanner_status"]; !ok {
		t.Fatal("report JSON has no scanner_status")
	}
}
