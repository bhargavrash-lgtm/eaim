//go:build windows

package payload

import "golang.org/x/sys/windows/registry"

func osVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	product, _, _ := k.GetStringValue("ProductName")
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	if product == "" {
		return ""
	}
	if build != "" {
		return product + " (build " + build + ")"
	}
	return product
}
