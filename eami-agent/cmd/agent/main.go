// Command eami-agent is the Windows/macOS/Linux endpoint AI discovery agent.
// It runs as a background service and sends detection reports to eami-collector.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eami/agent/internal/collector"
	"github.com/eami/agent/internal/config"
	"github.com/eami/agent/internal/nativemsg"
	"github.com/eami/agent/internal/nmlauncher"
	"github.com/eami/agent/internal/nmregister"
	"github.com/eami/agent/internal/payload"
	"github.com/eami/agent/internal/service"
)

var (
	cfgPath        = flag.String("config", "eami-agent.yaml", "path to config file")
	serviceCmd     = flag.String("service", "", "service command: install, start, stop, uninstall")
	nativeHostFlag = flag.Bool("native-messaging-host", false, "run as a browser native-messaging host (invoked by the browser itself; not for interactive use)")
	registerNMCmd  = flag.String("register-native-messaging", "", "native messaging manifest registration: install, uninstall")
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

	// Handle --register-native-messaging before loading config — like
	// --service, it needs only the executable path, not a running agent
	// configuration.
	if *registerNMCmd != "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "register-native-messaging: %v\n", err)
			os.Exit(1)
		}
		switch *registerNMCmd {
		case "install":
			if err := nmregister.Install(exe); err != nil {
				fmt.Fprintf(os.Stderr, "register-native-messaging install: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Native messaging host registered")
			return
		case "uninstall":
			if err := nmregister.Uninstall(exe); err != nil {
				fmt.Fprintf(os.Stderr, "register-native-messaging uninstall: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Native messaging host unregistered")
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown --register-native-messaging command %q\n", *registerNMCmd)
			os.Exit(1)
		}
	}

	// In --native-messaging-host mode, stdout is exclusively the native
	// messaging protocol stream (length-prefixed JSON) -- the browser
	// parses every byte written there as protocol framing. A single log
	// line written to stdout (even something as routine as an INFO
	// message) desyncs that framing and breaks the session, since the
	// reader on the other end has no way to distinguish log text from a
	// length prefix. Logging must go to stderr in this mode instead, same
	// as any well-behaved native messaging host; every other mode keeps
	// logging to stdout unchanged, matching this codebase's existing
	// convention.
	// A real browser launches eami-agent per the native-messaging
	// manifest's "path" field, which names a launcher (see
	// nmregister.LauncherBaseName's doc comment) rather than the real
	// binary directly, because Chrome/Edge's manifest schema has no way
	// to supply --native-messaging-host as an argument. Detect that
	// invocation by name, in addition to the explicit flag (used by
	// --service-independent manual/test invocations).
	nativeHost := *nativeHostFlag || invokedAsNativeMessagingLauncher()

	logWriter := io.Writer(os.Stdout)
	if nativeHost {
		logWriter = os.Stderr
	}
	log := slog.New(slog.NewTextHandler(logWriter, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// --native-messaging-host is a wholly separate process invocation: the
	// browser spawns this process fresh per connection and talks to it
	// exclusively over stdin/stdout. It never runs alongside, or shares
	// process state with, the scan-then-wait poll loop below — main
	// returns as soon as the native messaging session ends, so this mode
	// cannot affect the poll cycle's behavior in any way.
	if nativeHost {
		runNativeMessagingHost(cfg, log)
		return
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

// runNativeMessagingHost runs eami-agent as a browser native-messaging
// host (see internal/nativemsg): a fresh process the browser spawns per
// connection, communicating exclusively over stdin/stdout. Returns as
// soon as the session ends (browser closes the pipe, or a fatal I/O
// error) — there is no long-running state here, and no interaction with
// runLoop's scan-then-wait cycle at all.
// invokedAsNativeMessagingLauncher reports whether this process was
// invoked under nmregister.LauncherBaseName -- the hard-linked/aliased
// copy of this binary that the native-messaging manifest's "path" field
// actually names (see that constant's doc comment for why a real browser
// can never pass --native-messaging-host as an argument directly). Checks
// both os.Args[0] (how the invocation was literally named on the command
// line) and the resolved os.Executable() path, since which one reflects
// the launcher name can vary by platform/shell.
func invokedAsNativeMessagingLauncher() bool {
	if baseNameMatches(os.Args[0]) {
		return true
	}
	if exe, err := os.Executable(); err == nil && baseNameMatches(exe) {
		return true
	}
	return false
}

func baseNameMatches(path string) bool {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return strings.EqualFold(base, nmregister.LauncherBaseName)
}

func runNativeMessagingHost(cfg *config.Config, log *slog.Logger) {
	// Defense-in-depth (flagged by mandatory security review): native
	// messaging's own manifest restriction is enforced by the browser
	// before it launches this process, not by this process itself --
	// without any check here, any local process could invoke this mode
	// directly and have it forward fabricated data using the agent's own
	// already-configured collector credentials (a confused-deputy
	// problem). This is a best-effort mitigation, not a hard guarantee
	// (see internal/nmlauncher's package doc for why), and can be
	// disabled via EAMI_NM_SKIP_PARENT_CHECK=1 if it proves too strict in
	// some environment.
	if r := nmlauncher.VerifyLaunchedByBrowser(); !r.Allowed {
		log.Error("native messaging host refusing to start", "reason", r.Reason)
		os.Exit(1)
	} else if r.Reason != "" {
		if r.Warn {
			log.Warn("native messaging host parent-process check", "result", r.Reason)
		} else {
			log.Info("native messaging host parent-process check", "result", r.Reason)
		}
	}

	sender := collector.New(collector.Config{
		URL:            cfg.Collector.URL,
		APIKey:         cfg.Collector.APIKey,
		TimeoutSeconds: cfg.Collector.TimeoutSeconds,
	})

	agentID := cfg.Agent.ID
	hostname, _ := os.Hostname()
	if agentID == "" {
		agentID = hostname
	}

	if err := nativemsg.RunHost(context.Background(), os.Stdin, os.Stdout, sender, agentID, hostname, log); err != nil {
		log.Error("native messaging host exited with error", "err", err)
		os.Exit(1)
	}
}
