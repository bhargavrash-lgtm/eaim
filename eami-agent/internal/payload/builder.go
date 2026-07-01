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
}

const scanTimeout = 30 * time.Second

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

	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { recover() }() //nolint:errcheck
			fn()
		}()
	}

	det := &cfg.Detection // shorthand

	if det.IsEnabled("models") {
		run(func() {
			r, err := models.Scan(ctx, models.ScanOptions{
				MinSizeMB:      cfg.Detection.MinModelSizeMB,
				ExtraScanPaths: cfg.Detection.ModelFileScanPaths,
			})
			if err == nil {
				mu.Lock(); report.LocalModels = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("cloud_clients") {
		run(func() {
			r, err := cloud_clients.Scan(ctx)
			if err == nil {
				mu.Lock(); report.CloudClients = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("network_activity") {
		run(func() {
			r, err := network_activity.Scan(ctx)
			if err == nil {
				mu.Lock(); report.NetworkActivity = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("ai_processes") {
		run(func() {
			r, err := ai_processes.Scan(ctx)
			if err == nil {
				mu.Lock(); report.AIProcesses = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("ai_apps") {
		run(func() {
			r, err := ai_apps.Scan(ctx)
			if err == nil {
				mu.Lock(); report.AIApps = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("mcp_servers") {
		run(func() {
			r, err := mcp_servers.Scan(ctx)
			if err == nil {
				mu.Lock(); report.MCPServers = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("gpu") {
		run(func() {
			r, err := gpu.Scan(ctx)
			if err == nil {
				mu.Lock(); report.GPUs = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("python_envs") {
		run(func() {
			r, err := python_envs.Scan(ctx)
			if err == nil {
				mu.Lock(); report.PythonEnvs = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("nodejs_ai") {
		run(func() {
			r, err := nodejs_ai.Scan(ctx)
			if err == nil {
				mu.Lock(); report.NodeProjects = r; mu.Unlock()
			}
		})
	}
	if det.IsEnabled("browser") {
		run(func() {
			r, err := browser.Scan(ctx)
			if err == nil {
				mu.Lock(); report.BrowserExtensions = r; mu.Unlock()
			}
		})
	}

	wg.Wait()
	return report, nil
}
