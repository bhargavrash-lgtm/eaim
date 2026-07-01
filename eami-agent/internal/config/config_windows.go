//go:build windows

package config

import "golang.org/x/sys/windows/registry"

type windowsRegistryReader struct{}

func (windowsRegistryReader) ReadString(key, value string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		return "", nil // key absent — not an error
	}
	defer k.Close()

	val, _, err := k.GetStringValue(value)
	if err == registry.ErrNotExist {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

func defaultRegistryReader() RegistryReader { return windowsRegistryReader{} }
