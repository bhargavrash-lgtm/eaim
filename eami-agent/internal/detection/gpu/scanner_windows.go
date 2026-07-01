//go:build windows

package gpu

import "context"

// Scan detects GPUs on Windows. TODO: implement via WMI Win32_VideoController.
func Scan(_ context.Context) ([]GPU, error) { return nil, nil }
