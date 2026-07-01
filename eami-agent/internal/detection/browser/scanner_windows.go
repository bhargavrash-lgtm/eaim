//go:build windows

package browser

import (
	"os"
	"path/filepath"
)

// platformDirs returns Chrome and Edge extension directories for every user
// profile under C:\Users\*, excluding system pseudo-accounts.
func platformDirs() []ExtensionDir {
	userEntries, err := os.ReadDir(`C:\Users`)
	if err != nil {
		return nil
	}

	skip := map[string]bool{
		"Public": true, "Default": true, "Default User": true, "All Users": true,
	}

	var dirs []ExtensionDir
	for _, u := range userEntries {
		if !u.IsDir() || skip[u.Name()] {
			continue
		}
		username := u.Name()
		local := filepath.Join(`C:\Users`, username, "AppData", "Local")

		for _, b := range []struct{ browser, subPath string }{
			{"chrome", filepath.Join("Google", "Chrome", "User Data", "Default", "Extensions")},
			{"edge", filepath.Join("Microsoft", "Edge", "User Data", "Default", "Extensions")},
		} {
			dirs = append(dirs, ExtensionDir{
				Browser:  b.browser,
				Path:     filepath.Join(local, b.subPath),
				UserPath: username,
			})
		}
	}
	return dirs
}
