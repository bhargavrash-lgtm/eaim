//go:build windows

// Package winutil provides helpers for Windows-specific operations.
package winutil

import "golang.org/x/sys/windows/registry"

// ReadHKLMString reads a REG_SZ value from HKLM\<key>\<valueName>.
// Returns ("", nil) when the key or value is absent.
func ReadHKLMString(key, valueName string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		return "", nil
	}
	defer k.Close()
	val, _, err := k.GetStringValue(valueName)
	if err == registry.ErrNotExist {
		return "", nil
	}
	return val, err
}
