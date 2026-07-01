//go:build windows

package mcp_servers

import (
	"context"
	"os"
	"path/filepath"
)

func Scan(_ context.Context) ([]MCPServer, error) {
	var out []MCPServer
	appData := os.Getenv("APPDATA")
	home := os.Getenv("USERPROFILE")
	if appData != "" {
		out = append(out, parseClaudeConfig(filepath.Join(appData, "Claude", "claude_desktop_config.json"), "claude_desktop")...)
		out = append(out, parseVSCodeConfig(filepath.Join(appData, "Code", "User", "settings.json"), "vscode")...)
	}
	if home != "" {
		out = append(out, parseCursorConfig(filepath.Join(home, ".cursor", "mcp.json"), "cursor")...)
	}
	return out, nil
}
