//go:build !windows

package config

func defaultRegistryReader() RegistryReader { return NoopRegistryReader{} }
