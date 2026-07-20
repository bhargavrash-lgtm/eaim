// Command eami-agent is the Windows/macOS/Linux endpoint AI discovery agent.
// It runs as a background service and sends detection reports to eami-collector.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eami/agent/internal/collector"
	"github.com/eami/agent/internal/config"
	"github.com/eami/agent/internal/payload"
	"github.com/eami/agent/internal/service"
)

var (
	cfgPath    = flag.String("config", "eami-agent.yaml", "path to config file")
	serviceCmd = flag.String("service", "", "service command: install, start, stop, uninstall")
)

func main() {
	flag.Parse()

	// Handle --service sub-commands before loading config — they need only the
	// executable path and do not require a running agent configuration.
	switch *serviceCmd {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "service install: %v\n", err)
			os.Exit(1)
		}
		if err := service.Install(exe); err != nil {
			fmt.Fprintf(os.Stderr, "service install: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service installed:", service.ServiceName)
		return

	case "start":
		if err := service.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "service start: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service started:", service.ServiceName)
		return

	case "stop":
		if err := service.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "service stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service stopped:", service.ServiceName)
		return

	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "service uninstall: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service uninstalled:", service.ServiceName)
		return

	case "":
		// fall through — run the agent

	default:
		fmt.Fprintf(os.Stderr, "unknown --service command %q\n", *serviceCmd)
		flag.Usage()
		os.Exit(1)
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	run := func(ctx context.Context) {
		runLoop(ctx, cfg, log)
	}

	// Detect Windows SCM context vs. interactive terminal.
	isSvc, err := service.IsService()
	if err != nil {
		log.Warn("IsService check failed, assuming interactive", "err", err)
	}

	if isSvc {
		if err := service.RunAsService(run); err != nil {
			fmt.Fprintf(os.Stderr, "service run: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Interactive mode — log to stdout, respect SIGINT/SIGTERM.
	log.Info("eami-agent starting (interactive)",
		"interval_secs", cfg.Agent.IntervalSecs,
		"collector_url", cfg.Collector.URL,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	runLoop(ctx, cfg, log)
	fmt.Fprintln(os.Stderr, "eami-agent stopped")
}

// runLoop executes the scan-then-wait loop until ctx is cancelled.
// It is called both from interactive mode and from the Windows service handler.
func runLoop(ctx context.Context, cfg *config.Config, log *slog.Logger) {
	sender := collector.New(collector.Config{
		URL:            cfg.Collector.URL,
		APIKey:         cfg.Collector.APIKey,
		TimeoutSeconds: cfg.Collector.TimeoutSeconds,
	})

	// Derive agentID once; used for remote config polling.
	agentID := cfg.Agent.ID
	if agentID == "" {
		hostname, _ := os.Hostname()
		agentID = hostname
	}

	for {
		report, err := payload.Build(cfg)
		if err != nil {
			log.Error("scan error", "err", err)
		} else {
			log.Info("scan complete",
				"ai_apps", len(report.AIApps),
				"local_models", len(report.LocalModels),
				"cloud_clients", len(report.CloudClients),
				"active_connections", len(report.NetworkActivity.ActiveConnections),
				"ai_processes", len(report.AIProcesses),
			)

			if cfg.Collector.URL != "" {
				if err := sender.Send(ctx, report); err != nil {
					log.Warn("send failed", "err", err)
				} else {
					// Best-effort remote config poll after each successful send.
					if err := sender.FetchConfig(ctx, agentID, cfg); err != nil {
						log.Debug("remote config fetch", "err", err)
					}
				}
			} else {
				// Stdout mode — pretty-print for debugging.
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(report)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(cfg.Agent.IntervalSecs) * time.Second):
		}
	}
}
