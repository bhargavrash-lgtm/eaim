// Package gpu detects GPU hardware available on the endpoint.
package gpu

// GPU describes a detected GPU device.
type GPU struct {
	Name          string `json:"name"`
	VRAMBytes     int64  `json:"vram_bytes,omitempty"`
	DriverVersion string `json:"driver_version,omitempty"`
	Source        string `json:"source"` // nvidia-smi, system_profiler, wmi, sysfs
}
