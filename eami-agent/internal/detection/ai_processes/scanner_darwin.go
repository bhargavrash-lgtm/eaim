//go:build darwin

package ai_processes

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func Scan(ctx context.Context) ([]AIProcess, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "ps", "-axo", "pid=,comm=,command=").Output()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var results []AIProcess
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.SplitN(strings.TrimSpace(scanner.Text()), " ", 3)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		name := fields[1]
		var cmdline string
		if len(fields) == 3 {
			cmdline = fields[2]
		}
		if !IsAIProcess(name, cmdline, "") {
			continue
		}
		results = append(results, AIProcess{PID: pid, Name: name, CommandLine: cmdline, DetectedAt: now})
	}
	return results, scanner.Err()
}
