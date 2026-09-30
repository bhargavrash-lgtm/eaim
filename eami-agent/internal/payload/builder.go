// Package payload assembles the full endpoint Report by running all detection
// scanners in parallel with a shared 30-second context deadline.
package payload

import (
	"context"
	"os"
	"runtime"
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

	LocalModels       []models.LocalModel            `json:"local_models"`
	CloudClients      []cloud_clients.CloudClient    `json:"cloud_clients"`
	NetworkActivity   network_activity.ScanResult    `json:"network_activity"`
	AIProcesses       []ai_processes.AIProcess       `json:"ai_processes"`
	AIApps            []ai_apps.AIApp                `json:"ai_apps"`
	MCPServers        []mcp_servers.MCPServer        `json:"mcp_servers"`
	GPUs              []gpu.GPU                      `json:"gpus"`
	PythonEnvs        []python_envs.PythonEnv        `json:"python_envs"`
	NodeProjects      []nodejs_ai.NodeProject        `json:"node_projects"`
	BrowserExtensions []browser.BrowserExtension     `json:"browser_extensions"`

	// ScannerStatus records, per scanner name (the names DetectionConfig
	// gates on), what happened in this scan: ScannerOK, ScannerDisabled or
	// ScannerError. It is the only way to tell a scanner that found nothing
	// apart from one that was disabled or failed: all three otherwise
	// marshal their field as JSON null (master-sequence item 4). Reports
	// from agents that predate this field have no scanner_status at all.
	ScannerStatus map[string]string `json:"scanner_status"`
}

// Scanner status values carried in Report.ScannerStatus.
const (
	ScannerOK       = "ok"       // ran and returned without error (an empty result is a real "found nothing")
	ScannerDisabled = "disabled" // not enabled by config for this scan
	ScannerError    = "error"    // ran and failed: an error, a panic, or the scan deadline passed
)

const scanTimeout = 30 * time.Second

// runScan runs one scanner in its own goroutine (tracked by wg) if enabled,
// and records its status: ScannerDisabled without running it, ScannerOK if
// fn returns nil, ScannerError if fn returns an error or panics -- or if
// the scan deadline passed while it ran. Several scanners stop early on
// ctx.Done() and return a partial or empty list with a nil error; calling
// that "ok" would show a truncated result as a real "found nothing".
func runScan(ctx context.Context, wg *sync.WaitGroup, enabled bool, name string, fn func() error, setStatus func(name, status string)) {
	if !enabled {
		setStatus(name, ScannerDisabled)
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		status := ScannerError // stays "error" if fn panics
		defer func() {
			recover() //nolint:errcheck
			setStatus(name, status)
		}()
		if err := fn(); err == nil && ctx.Err() == nil {
			status = ScannerOK
		}
	}()
}

// Build runs all enabled scanners in parallel and assembles a Report.
// Scanners not listed in cfg.Detection.EnabledScanners are skipped;
// an empty list means all scanners are enabled (default).
func Build(cfg *config.Config) (*Report, error) {
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

	var wg sync.WaitGroup
	var mu sync.Mutex
	report.ScannerStatus = make(map[string]string, 10)
	setStatus := func(name, status string) {
		mu.Lock()
		report.ScannerStatus[name] = status
		mu.Unlock()
	}

	det := &cfg.Detection // shorthand

	// fn stores its own result (under mu) and returns the scanner's error.
	scan := func(name string, fn func() error) {
		runScan(ctx, &wg, det.IsEnabled(name), name, fn, setStatus)
	}

	scan("models", func() error {
		r, err := models.Scan(ctx, models.ScanOptions{
			MinSizeMB:      cfg.Detection.MinModelSizeMB,
			ExtraScanPaths: cfg.Detection.ModelFileScanPaths,
		})
		if err != nil {
			return err
		}
		mu.Lock(); report.LocalModels = r; mu.Unlock()
		return nil
	})
	scan("cloud_clients", func() error {
		r, err := cloud_clients.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.CloudClients = r; mu.Unlock()
		return nil
	})
	scan("network_activity", func() error {
		r, err := network_activity.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.NetworkActivity = r; mu.Unlock()
		return nil
	})
	scan("ai_processes", func() error {
		r, err := ai_processes.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.AIProcesses = r; mu.Unlock()
		return nil
	})
	scan("ai_apps", func() error {
		r, err := ai_apps.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.AIApps = r; mu.Unlock()
		return nil
	})
	scan("mcp_servers", func() error {
		r, err := mcp_servers.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.MCPServers = r; mu.Unlock()
		return nil
	})
	scan("gpu", func() error {
		r, err := gpu.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.GPUs = r; mu.Unlock()
		return nil
	})
	scan("python_envs", func() error {
		r, err := python_envs.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.PythonEnvs = r; mu.Unlock()
		return nil
	})
	scan("nodejs_ai", func() error {
		r, err := nodejs_ai.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.NodeProjects = r; mu.Unlock()
		return nil
	})
	scan("browser", func() error {
		r, err := browser.Scan(ctx)
		if err != nil {
			return err
		}
		mu.Lock(); report.BrowserExtensions = r; mu.Unlock()
		return nil
	})

	wg.Wait()
	return report, nil
}
