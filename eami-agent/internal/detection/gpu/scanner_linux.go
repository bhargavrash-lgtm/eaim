//go:build linux

package gpu

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"os/exec"
)

func Scan(ctx context.Context) ([]GPU, error) {
	if gpus := nvidiaGPUs(ctx); len(gpus) > 0 {
		return gpus, nil
	}
	return sysfsGPUs(), nil
}

func nvidiaGPUs(ctx context.Context) []GPU {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx,
		"nvidia-smi", "--query-gpu=name,memory.total,driver_version",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var gpus []GPU
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		parts := strings.SplitN(strings.TrimSpace(scanner.Text()), ", ", 3)
		if len(parts) < 1 {
			continue
		}
		g := GPU{Name: strings.TrimSpace(parts[0]), Source: "nvidia-smi"}
		if len(parts) >= 2 {
			g.VRAMBytes = parseInt64(strings.TrimSpace(parts[1])) * 1024 * 1024
		}
		if len(parts) >= 3 {
			g.DriverVersion = strings.TrimSpace(parts[2])
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func sysfsGPUs() []GPU {
	entries, err := os.ReadDir("/sys/class/drm")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var gpus []GPU
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "card") {
			continue
		}
		name := sysRead(filepath.Join("/sys/class/drm", e.Name(), "device", "product_name"))
		if name == "" {
			name = e.Name()
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		gpus = append(gpus, GPU{Name: name, Source: "sysfs"})
	}
	return gpus
}

func sysRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func parseInt64(s string) int64 {
	var v int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			v = v*10 + int64(c-'0')
		}
	}
	return v
}
