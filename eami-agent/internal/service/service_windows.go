//go:build windows

package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const stopTimeout = 10 * time.Second

// Install registers the agent binary as a Windows service with auto-start.
func Install(exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service: %q already installed", ServiceName)
	}

	s, err = m.CreateService(ServiceName, exePath,
		mgr.Config{
			DisplayName: DisplayName,
			Description: Description,
			StartType:   mgr.StartAutomatic,
		})
	if err != nil {
		return fmt.Errorf("service: create: %w", err)
	}
	defer s.Close()

	// Register event log source — best-effort, non-fatal.
	_ = eventlog.InstallAsEventCreate(ServiceName,
		eventlog.Error|eventlog.Warning|eventlog.Info)
	return nil
}

// Start starts the already-installed Windows service.
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service: open %q: %w", ServiceName, err)
	}
	defer s.Close()

	return s.Start()
}

// Stop sends a Stop control to the running service.
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service: open %q: %w", ServiceName, err)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	return err
}

// Uninstall marks the service for deletion and removes the event log source.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service: open %q: %w", ServiceName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("service: delete: %w", err)
	}
	_ = eventlog.Remove(ServiceName)
	return nil
}

// RunAsService executes run under the Windows SCM and blocks until Stop is received.
// Panics if called outside a Windows service context.
func RunAsService(run RunFn) error {
	return svc.Run(ServiceName, &handler{run: run})
}

// IsService reports whether this process is running inside the Windows SCM
// (as opposed to an interactive terminal).
func IsService() (bool, error) {
	return svc.IsWindowsService()
}

// handler implements svc.Handler for the eami-agent scan loop.
type handler struct {
	run RunFn
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	elog, _ := eventlog.Open(ServiceName)
	if elog != nil {
		defer elog.Close()
		_ = elog.Info(1, ServiceName+" starting")
	}

	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.run(ctx)
	}()

	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	paused := false
	for cr := range req {
		switch cr.Cmd {
		case svc.Interrogate:
			state := svc.Running
			if paused {
				state = svc.Paused
			}
			changes <- svc.Status{State: state, Accepts: accepts}

		case svc.Pause:
			paused = true
			changes <- svc.Status{State: svc.Paused, Accepts: accepts}

		case svc.Continue:
			paused = false
			changes <- svc.Status{State: svc.Running, Accepts: accepts}

		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			select {
			case <-done:
			case <-time.After(stopTimeout):
				// Scan loop did not finish in time — force exit.
				if elog != nil {
					_ = elog.Warning(1, ServiceName+": stop timeout exceeded, forcing exit")
				}
				os.Exit(1)
			}
			if elog != nil {
				_ = elog.Info(1, ServiceName+" stopped")
			}
			return false, 0
		}
	}

	cancel()
	return false, 0
}
