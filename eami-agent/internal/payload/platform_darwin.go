//go:build darwin

package payload

import (
	"os/exec"
	"strings"
)

func osVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return ""
	}
	return "macOS " + v
}
