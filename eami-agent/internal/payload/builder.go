// Package payload assembles the full endpoint Report by running all detection
// scanners in parallel. Each scanner gets the same 30-second deadline, and
// Build never waits past it: a scanner still running is reported as an error
// and the report goes out with every other scanner's result (B-281).
package payload

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/eami/agent/internal/config"
	"github.com/eami/agent/internal/detection/ai_apps"
	"github.com/eami/agent/internal/detection/ai_processes"
	"github.com/eami/agent/internal/detection/browser"
	"github.com/eami/agent/internal/detection/cloud_clients"
	"github.com/eami/agent/internal/detection/gpu"
	"github.com/eami/agent/internal/detection/mcp_servers"
	"github.com/eami/agent/internal/detection/models"
	"github.com/eami/agent/internal/detection/network_activity"
	"github.com/eami/agent/internal/detection/nodejs_ai"
	"github.com/eami/agent/internal/detection/python_envs"
)

// Platform captures OS and hardware context of the reporting endpoint.
type Platform struct {
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	OSVersion string `json:"os_version,omitempty"`
}

// Report is the top-level payload sent to the collector on each scan cycle.
type Report struct {
	AgentID      string    `json:"agent_id"`
	Hostname     string    `json:"hostname"`
	CollectedAt  time.Time `json:"collected_at"`
	AgentVersion string    `json:"agent_version"`
	Platform     Platform  `json:"platform,omitempty"`

	LocalModels       []models.LocalModel         `json:"local_models"`
	CloudClients      []cloud_clients.CloudClient `json:"cloud_clients"`
	NetworkActivity   network_activity.ScanResult `json:"network_activity"`
	AIProcesses       []ai_processes.AIProcess    `json:"ai_processes"`
	AIApps            []ai_apps.AIApp             `json:"ai_apps"`
	MCPServers        []mcp_servers.MCPServer     `json:"mcp_servers"`
	GPUs              []gpu.GPU                   `json:"gpus"`
	PythonEnvs        []python_envs.PythonEnv     `json:"python_envs"`
	NodeProjects      []nodejs_ai.NodeProject     `json:"node_projects"`
	BrowserExtensions []browser.BrowserExtension  `json:"browser_extensions"`

	// ScannerStatus records, per scanner name (the names DetectionConfig
	// gates on), what happened in this scan: ScannerOK, ScannerDisabled or
	// ScannerError. It is the only way to tell a scanner that found nothing
	// apart from one that was disabled or failed: all three otherwise
	// marshal their field as JSON null (master-sequence item 4). Reports
	// from agents that predate this field have no scanner_status at all.
	ScannerStatus map[string]string `json:"scanner_status"`

	// ScannerErrors gives a short reason code for each scanner whose status
	// is ScannerError: ReasonTimeout, ReasonStillRunning, ReasonPanic or
	// ReasonError (B-285). Codes only -- raw error strings and panic values
	// can carry file paths or argument fragments, so the full detail stays
	// in the endpoint's local log (founder decision D1).
	ScannerErrors map[string]string `json:"scanner_errors,omitempty"`

	// The config this scan ran with (B-293), set by the agent loop from
	// remoteconfig.Manager.Status. ConfigVersion is "" unless a versioned
	// remote config was in force; ConfigSource is remote, persisted, local
	// or defaults; ConfigError is the last rejected config's reason code.
	// EnforceMaxSize never drops these or ScannerStatus.
	ConfigVersion string `json:"config_version"`
	ConfigSource  string `json:"config_source"`
	ConfigError   string `json:"config_error,omitempty"`
}

// Scanner status values carried in Report.ScannerStatus.
const (
	ScannerOK       = "ok"       // ran and returned without error (an empty result is a real "found nothing")
	ScannerDisabled = "disabled" // not enabled by config for this scan
	ScannerError    = "error"    // did not produce a trustworthy result; see Report.ScannerErrors
)

// Reason codes carried in Report.ScannerErrors.
const (
	ReasonTimeout      = "timeout"       // still running at, or returned only after, the scan deadline
	ReasonStillRunning = "still_running" // the previous scan's run of this scanner never returned, so it was not started again
	ReasonPanic        = "panic"         // the scanner panicked
	ReasonError        = "error"         // the scanner returned an error
	ReasonTooLarge     = "too_large"     // its result was dropped to keep the report under max_report_size_bytes (B-293)
)

const scanTimeout = 30 * time.Second

// inFlight records scanners whose goroutine has not returned yet, across scan
// cycles. A Go goroutine cannot be stopped from outside, so a scanner blocked
// in a syscall (a hung filesystem, a FIFO) stays blocked; without this, every
// cycle would start another copy and the stuck goroutines -- each holding an
// OS thread while blocked in a syscall -- would pile up without bound. With
// it, at most one goroutine per scanner name can ever be stuck (B-281).
var inFlight = struct {
	sync.Mutex
	running map[string]bool
}{running: map[string]bool{}}

// scannerSpec is one scanner to run. fn returns the scanner's error and
// stores its result only through commit.
type scannerSpec struct {
	name    string
	enabled bool
	fn      func(ctx context.Context, commit func(func())) error
}

type scanOutcome struct {
	status string // ScannerOK or ScannerError
	reason string // set when status is ScannerError
}

// collect runs specs in parallel and returns each scanner's status and, for
// errors, its reason code. It never waits past ctx's deadline: a scanner
// still running then is ReasonTimeout. Results are written only through
// commit, which applies a write under mu only while collect has not yet
// returned, so a straggler finishing later can never touch the report being
// sent.
func collect(ctx context.Context, specs []scannerSpec, mu *sync.Mutex, log *slog.Logger) (status, reasons map[string]string) {
	status = make(map[string]string, len(specs))
	reasons = map[string]string{}
	closed := false
	commit := func(write func()) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			write()
		}
	}
	fail := func(name, reason string) {
		status[name] = ScannerError
		reasons[name] = reason
	}

	type done struct {
		name string
		out  scanOutcome
	}
	results := make(chan done, len(specs)) // buffered: a straggler's send never blocks
	pending := map[string]bool{}

	for _, sp := range specs {
		if !sp.enabled {
			status[sp.name] = ScannerDisabled
			continue
		}
		inFlight.Lock()
		if inFlight.running[sp.name] {
			inFlight.Unlock()
			fail(sp.name, ReasonStillRunning)
			log.Warn("scanner not started: its previous run has not returned", "scanner", sp.name)
			continue
		}
		inFlight.running[sp.name] = true
		inFlight.Unlock()
		pending[sp.name] = true

		go func(sp scannerSpec) {
			out := scanOutcome{status: ScannerError, reason: ReasonPanic} // if fn panics
			defer func() {
				if p := recover(); p != nil {
					// Full detail stays local (B-285, founder decision D1).
					log.Error("scanner panicked", "scanner", sp.name, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				}
				inFlight.Lock()
				delete(inFlight.running, sp.name)
				inFlight.Unlock()
				results <- done{sp.name, out}
			}()
			err := sp.fn(ctx, commit)
			switch {
			case err != nil:
				out = scanOutcome{ScannerError, ReasonError}
				log.Warn("scanner returned an error", "scanner", sp.name, "err", err)
			case ctx.Err() != nil:
				// Several scanners stop early on ctx.Done() and return a
				// partial or empty list with a nil error: not "ok".
				out = scanOutcome{ScannerError, ReasonTimeout}
				log.Warn("scanner finished after the scan deadline; its result is not trusted", "scanner", sp.name)
			default:
				out = scanOutcome{status: ScannerOK}
			}
		}(sp)
	}

	for len(pending) > 0 {
		select {
		case d := <-results:
			delete(pending, d.name)
			status[d.name] = d.out.status
			if d.out.status == ScannerError {
				reasons[d.name] = d.out.reason
			}
		case <-ctx.Done():
			// Take any result that is already waiting before declaring the
			// rest timed out: select picks randomly when both are ready, and a
			// scanner whose data is in the report must not be called "timeout".
		drain:
			for {
				select {
				case d := <-results:
					delete(pending, d.name)
					status[d.name] = d.out.status
					if d.out.status == ScannerError {
						reasons[d.name] = d.out.reason
					}
				default:
					break drain
				}
			}
			for name := range pending {
				fail(name, ReasonTimeout)
				log.Warn("scanner still running at the scan deadline; reporting without it", "scanner", name)
			}
			pending = nil
		}
	}

	mu.Lock()
	closed = true
	mu.Unlock()
	return status, reasons
}

// Build runs all enabled scanners in parallel and assembles a Report.
// Scanners not listed in cfg.Detection.EnabledScanners are skipped; an
// empty list runs none (B-293: config.Load fills in the full list when the
// YAML doesn't set one, so "all" is always explicit).
func Build(cfg *config.Config) (*Report, error) {
	return BuildWith(cfg, slog.Default())
}

// BuildWith is Build with the agent's logger, which receives full scanner
// error and panic detail (never put in the report).
func BuildWith(cfg *config.Config, log *slog.Logger) (*Report, error) {
	if log == nil {
		// A nil logger would panic inside collect's deferred recover, which
		// nothing catches: never let logging take the agent down.
		log = slog.Default()
	}
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	hostname, _ := os.Hostname()
	agentID := cfg.Agent.ID
	if agentID == "" {
		agentID = hostname
	}

	report := &Report{
		AgentID:     agentID,
		Hostname:    hostname,
		CollectedAt: time.Now().UTC(),
		Platform: Platform{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			OSVersion: osVersion(),
		},
	}

	det := &cfg.Detection // shorthand
	spec := func(name string, fn func(ctx context.Context, commit func(func())) error) scannerSpec {
		return scannerSpec{name: name, enabled: det.IsEnabled(name), fn: fn}
	}
	specs := []scannerSpec{
		spec("models", func(ctx context.Context, commit func(func())) error {
			r, err := models.Scan(ctx, models.ScanOptions{
				MinSizeMB:      cfg.Detection.MinModelSizeMB,
				ExtraScanPaths: cfg.Detection.ModelFileScanPaths,
			})
			if err == nil {
				commit(func() { report.LocalModels = r })
			}
			return err
		}),
		spec("cloud_clients", func(ctx context.Context, commit func(func())) error {
			r, err := cloud_clients.Scan(ctx)
			if err == nil {
				commit(func() { report.CloudClients = r })
			}
			return err
		}),
		spec("network_activity", func(ctx context.Context, commit func(func())) error {
			r, err := network_activity.Scan(ctx)
			if err == nil {
				commit(func() { report.NetworkActivity = r })
			}
			return err
		}),
		spec("ai_processes", func(ctx context.Context, commit func(func())) error {
			r, err := ai_processes.Scan(ctx)
			if err == nil {
				commit(func() { report.AIProcesses = r })
			}
			return err
		}),
		spec("ai_apps", func(ctx context.Context, commit func(func())) error {
			r, err := ai_apps.Scan(ctx)
			if err == nil {
				commit(func() { report.AIApps = r })
			}
			return err
		}),
		spec("mcp_servers", func(ctx context.Context, commit func(func())) error {
			r, err := mcp_servers.Scan(ctx)
			if err == nil {
				commit(func() { report.MCPServers = r })
			}
			return err
		}),
		spec("gpu", func(ctx context.Context, commit func(func())) error {
			r, err := gpu.Scan(ctx)
			if err == nil {
				commit(func() { report.GPUs = r })
			}
			return err
		}),
		spec("python_envs", func(ctx context.Context, commit func(func())) error {
			r, err := python_envs.Scan(ctx)
			if err == nil {
				commit(func() { report.PythonEnvs = r })
			}
			return err
		}),
		spec("nodejs_ai", func(ctx context.Context, commit func(func())) error {
			r, err := nodejs_ai.Scan(ctx)
			if err == nil {
				commit(func() { report.NodeProjects = r })
			}
			return err
		}),
		spec("browser", func(ctx context.Context, commit func(func())) error {
			r, err := browser.Scan(ctx)
			if err == nil {
				commit(func() { report.BrowserExtensions = r })
			}
			return err
		}),
	}

	var mu sync.Mutex
	status, reasons := collect(ctx, specs, &mu, log)
	report.ScannerStatus = status
	if len(reasons) > 0 {
		report.ScannerErrors = reasons
	}
	return report, nil
}
