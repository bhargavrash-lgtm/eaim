package payload

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eami/agent/internal/config"
	"github.com/eami/agent/internal/detection/ai_processes"
	"github.com/eami/agent/internal/detection/gpu"
	"github.com/eami/agent/internal/detection/models"
)

func bigReport() *Report {
	r := &Report{AgentID: "a", Hostname: "h", ConfigVersion: "c1:v", ConfigSource: "remote", ConfigError: "fetch_failed",
		ScannerStatus: map[string]string{"models": ScannerOK, "ai_processes": ScannerOK, "gpu": ScannerOK}}
	for i := 0; i < 2000; i++ {
		r.LocalModels = append(r.LocalModels, models.LocalModel{Name: strings.Repeat("m", 100)})
		r.AIProcesses = append(r.AIProcesses, ai_processes.AIProcess{Name: strings.Repeat("p", 50)})
	}
	r.GPUs = []gpu.GPU{{Name: "g"}}
	return r
}

func size(t *testing.T, r *Report) int64 {
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return int64(len(raw))
}

// Decision D-e: deterministic drop order, visible per scanner, and never
// the status or config fields.
func TestEnforceMaxSize_DropsInFixedOrderAndMarksEach(t *testing.T) {
	r := bigReport()
	withoutModels := bigReport()
	withoutModels.LocalModels = nil
	limit := size(t, withoutModels) + 50 // fits once models is gone

	dropped := EnforceMaxSize(r, limit)
	if len(dropped) != 1 || dropped[0] != "models" {
		t.Fatalf("dropped %v, want [models]", dropped)
	}
	if r.LocalModels != nil || r.AIProcesses == nil || r.GPUs == nil {
		t.Fatal("dropped the wrong sections")
	}
	if r.ScannerStatus["models"] != ScannerError || r.ScannerErrors["models"] != ReasonTooLarge {
		t.Fatalf("drop not marked: %v %v", r.ScannerStatus, r.ScannerErrors)
	}
	if size(t, r) > limit {
		t.Fatal("still over the limit")
	}

	// Smaller limit: models, then ai_processes, never anything out of order.
	r = bigReport()
	if got := EnforceMaxSize(r, 400); strings.Join(got, ",") != "models,ai_processes,gpu" {
		t.Fatalf("drop order %v", got)
	}
	if r.ConfigVersion != "c1:v" || r.ConfigSource != "remote" || r.ConfigError != "fetch_failed" || r.AgentID != "a" {
		t.Fatal("config or identity fields were dropped")
	}
	if len(r.ScannerStatus) != 3 {
		t.Fatalf("scanner_status lost entries: %v", r.ScannerStatus)
	}
}

func TestEnforceMaxSize_NoCapOrUnderCapIsUntouched(t *testing.T) {
	r := bigReport()
	if got := EnforceMaxSize(r, 0); got != nil || r.LocalModels == nil {
		t.Fatal("a zero cap dropped data")
	}
	if got := EnforceMaxSize(r, size(t, r)); got != nil || r.ScannerErrors != nil {
		t.Fatal("a report exactly at the cap was changed")
	}
}

// Every scanner the builder runs has a place in DropOrder and in
// config.AllScanners, and nothing else does.
func TestScannerNameListsAgree(t *testing.T) {
	cfg := &config.Config{}
	cfg.Detection.EnabledScanners = []string{}
	rep, err := BuildWith(cfg, quiet)
	if err != nil {
		t.Fatal(err)
	}
	var built []string
	for name, st := range rep.ScannerStatus {
		built = append(built, name)
		if st != ScannerDisabled {
			t.Errorf("%s ran under an empty scanner list (status %s)", name, st)
		}
	}
	norm := func(in []string) string {
		c := append([]string(nil), in...)
		sort.Strings(c)
		return strings.Join(c, ",")
	}
	if norm(built) != norm(config.AllScanners) || norm(DropOrder) != norm(config.AllScanners) {
		t.Fatalf("builder %v / DropOrder %v / AllScanners %v", norm(built), norm(DropOrder), norm(config.AllScanners))
	}
}

// Code review L3: an empty result isn't dropped (it costs nothing), and a
// scanner already in error keeps its own reason.
func TestEnforceMaxSize_SkipsEmptyAndFailedScanners(t *testing.T) {
	r := bigReport()
	r.ScannerStatus["models"] = ScannerError
	r.ScannerErrors = map[string]string{"models": ReasonTimeout}
	r.ScannerStatus["python_envs"] = ScannerOK
	r.PythonEnvs = nil
	dropped := EnforceMaxSize(r, 400)
	for _, d := range dropped {
		if d == "models" || d == "python_envs" {
			t.Fatalf("dropped %s (%v)", d, dropped)
		}
	}
	if r.ScannerErrors["models"] != ReasonTimeout || r.ScannerStatus["python_envs"] != ScannerOK {
		t.Fatalf("relabelled: %v %v", r.ScannerStatus, r.ScannerErrors)
	}
}

// Code review M1 (B-269 Slice 0): a scanner that stops at the deadline and
// returns the context's error is a timeout, not an error.
func TestCollect_DeadlineErrorIsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var mu sync.Mutex
	sp := scannerSpec{name: "t-deadline-err", enabled: true, fn: func(ctx context.Context, _ func(func())) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	status, reasons := collect(ctx, []scannerSpec{sp}, &mu, quiet)
	if status["t-deadline-err"] != ScannerError || reasons["t-deadline-err"] != ReasonTimeout {
		t.Fatalf("status %v reasons %v, want error/timeout", status, reasons)
	}
}
