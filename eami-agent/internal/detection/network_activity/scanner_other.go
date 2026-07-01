//go:build !windows && !darwin && !linux

package network_activity

import "context"

func platformScan(_ context.Context) (ScanResult, error) { return ScanResult{}, nil }
