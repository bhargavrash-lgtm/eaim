//go:build linux

package ai_processes

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func Scan(ctx context.Context) ([]AIProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("ai_processes: read /proc: %w", err)
	}
	now := time.Now().UTC()
	var results []AIProcess
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSpace(readFile(fmt.Sprintf("/proc/%d/comm", pid)))
		cmdline := strings.ReplaceAll(readFile(fmt.Sprintf("/proc/%d/cmdline", pid)), "\x00", " ")
		cmdline = strings.TrimSpace(cmdline)
		exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if !IsAIProcess(name, cmdline, exe) {
			continue
		}
		results = append(results, AIProcess{PID: pid, Name: name, ExePath: exe, CommandLine: cmdline, DetectedAt: now})
	}
	return results, nil
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
