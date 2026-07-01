//go:build darwin

package gpu

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

func Scan(ctx context.Context) ([]GPU, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "system_profiler", "SPDisplaysDataType", "-json").Output()
	if err != nil {
		return nil, err
	}
	var sp struct {
		SPDisplaysDataType []struct {
			Name   string `json:"sppci_model"`
			VRAM   string `json:"spdisplays_vram"`
			Vendor string `json:"spdisplays_vendor"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(out, &sp); err != nil {
		return nil, err
	}
	var gpus []GPU
	for _, d := range sp.SPDisplaysDataType {
		name := d.Name
		if name == "" {
			name = d.Vendor
		}
		gpus = append(gpus, GPU{Name: name, VRAMBytes: parseVRAM(d.VRAM), Source: "system_profiler"})
	}
	return gpus, nil
}

func parseVRAM(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var val int64
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		val = val*10 + int64(s[i]-'0')
		i++
	}
	unit := strings.TrimSpace(s[i:])
	switch strings.ToUpper(unit) {
	case "GB":
		return val * 1024 * 1024 * 1024
	case "MB":
		return val * 1024 * 1024
	}
	return 0
}
