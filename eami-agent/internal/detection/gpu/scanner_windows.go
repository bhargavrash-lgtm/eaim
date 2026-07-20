//go:build windows

package gpu

import (
	"context"
	"encoding/json"
	"os/exec"
)

// wmiGPU is the JSON shape emitted by PowerShell's ConvertTo-Json for one
// Win32_VideoController instance.
type wmiGPU struct {
	Name          string      `json:"Name"`
	AdapterRAM    interface{} `json:"AdapterRAM"`    // uint32 in WMI; null for some virtual adapters
	DriverVersion string      `json:"DriverVersion"` // e.g. "31.0.101.2115"
}

// Scan detects GPUs on Windows using WMI Win32_VideoController via PowerShell.
// The call is non-fatal — errors return an empty slice rather than propagating,
// because GPU absence should never block the rest of the agent report.
//
// Note: AdapterRAM is a uint32 in WMI, so values above ~4 GB wrap around.
// Modern GPUs (e.g. RTX 3090 with 24 GB) will report a clamped value.
// This is a WMI limitation; use DXGI or OpenCL for accurate VRAM on large GPUs.
func Scan(ctx context.Context) ([]GPU, error) {
	cmd := exec.CommandContext(ctx,
		"powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-WmiObject Win32_VideoController | `+
			`Select-Object Name,AdapterRAM,DriverVersion | `+
			`ConvertTo-Json -Compress`,
	)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, nil
	}

	// PowerShell emits a JSON object for a single result, an array for multiple.
	var gpus []GPU
	switch out[0] {
	case '[':
		var entries []wmiGPU
		if json.Unmarshal(out, &entries) != nil {
			return nil, nil
		}
		for _, e := range entries {
			if g := wmiGPUToGPU(e); g != nil {
				gpus = append(gpus, *g)
			}
		}
	default:
		var e wmiGPU
		if json.Unmarshal(out, &e) != nil {
			return nil, nil
		}
		if g := wmiGPUToGPU(e); g != nil {
			gpus = append(gpus, *g)
		}
	}
	return gpus, nil
}

func wmiGPUToGPU(e wmiGPU) *GPU {
	if e.Name == "" {
		return nil
	}
	var vram int64
	if f, ok := e.AdapterRAM.(float64); ok {
		vram = int64(f)
	}
	return &GPU{
		Name:          e.Name,
		VRAMBytes:     vram,
		DriverVersion: e.DriverVersion,
		Source:        "wmi",
	}
}
