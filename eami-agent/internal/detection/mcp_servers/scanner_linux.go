//go:build linux

package mcp_servers

import (
	"context"
	"os"
	"path/filepath"
)

func Scan(_ context.Context) ([]MCPServer, error) {
	home, _ := os.UserHomeDir()
	var out []MCPServer
	out = append(out, parseClaudeConfig(filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), "claude_desktop")...)
	out = append(out, parseCursorConfig(filepath.Join(home, ".cursor", "mcp.json"), "cursor")...)
	out = append(out, parseVSCodeConfig(filepath.Join(home, ".config", "Code", "User", "settings.json"), "vscode")...)
	return out, nil
}
