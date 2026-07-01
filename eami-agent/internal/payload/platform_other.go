//go:build !windows && !darwin && !linux

package payload

func osVersion() string { return "" }
