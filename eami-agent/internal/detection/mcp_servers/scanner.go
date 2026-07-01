// Package mcp_servers detects configured MCP server configurations.
package mcp_servers

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// MCPServer describes a detected MCP server configuration.
type MCPServer struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
	Source  string `json:"source"` // claude_desktop, vscode, cursor
	Port    int    `json:"port,omitempty"`
	Active  bool   `json:"active"`
}

type claudeConfig struct {
	MCPServers map[string]mcpEntry `json:"mcpServers"`
}
type vscodeConfig struct {
	MCP struct {
		Servers map[string]mcpEntry `json:"servers"`
	} `json:"mcp"`
}
type mcpEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func parseClaudeConfig(path, source string) []MCPServer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg claudeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return toServers(cfg.MCPServers, source)
}

func parseCursorConfig(path, source string) []MCPServer { return parseClaudeConfig(path, source) }

func parseVSCodeConfig(path, source string) []MCPServer {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg vscodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return toServers(cfg.MCP.Servers, source)
}

func toServers(entries map[string]mcpEntry, source string) []MCPServer {
	out := make([]MCPServer, 0, len(entries))
	for name, e := range entries {
		out = append(out, MCPServer{
			Name: name, Command: e.Command,
			Args: strings.Join(e.Args, " "), Source: source,
		})
	}
	return out
}

func probePort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
