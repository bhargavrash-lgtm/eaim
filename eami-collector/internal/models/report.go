// Package models defines the EndpointReport schema as received from agents.
package models

import "time"

// EndpointReport is the top-level payload POSTed by an eami-agent.
// Fields must stay in sync with api/openapi.yaml (maintained by Architect-EAMI).
type EndpointReport struct {
	AgentID      string          `json:"agent_id"`
	Hostname     string          `json:"hostname"`
	CollectedAt  time.Time       `json:"collected_at"`
	AgentVersion string          `json:"agent_version,omitempty"`
	Platform     Platform        `json:"platform,omitempty"`

	LocalModels     []LocalModel     `json:"local_models,omitempty"`
	CloudClients    []CloudClient    `json:"cloud_clients,omitempty"`
	NetworkActivity NetworkActivity  `json:"network_activity,omitempty"`
	AIProcesses     []AIProcess      `json:"ai_processes,omitempty"`
	AIApps          []AIApp          `json:"ai_apps,omitempty"`
	MCPServers      []MCPServer      `json:"mcp_servers,omitempty"`
	GPUs            []GPU            `json:"gpus,omitempty"`
	PythonEnvs      []PythonEnv      `json:"python_envs,omitempty"`
	NodeProjects    []NodeProject    `json:"node_projects,omitempty"`
}

// Platform captures OS / arch context.
type Platform struct {
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	OSVersion string `json:"os_version,omitempty"`
}

// LocalModel is a model file or Ollama entry.
type LocalModel struct {
	Name         string    `json:"name"`
	Source       string    `json:"source"`
	FilePath     string    `json:"file_path,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at,omitempty"`
	ModelType    string    `json:"model_type,omitempty"`
	Architecture string    `json:"architecture,omitempty"`
}

// CloudClient is a configured AI cloud credential.
type CloudClient struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
	KeyPrefix  string `json:"key_prefix,omitempty"`
	Source     string `json:"source"`
}

// NetworkActivity holds active connections and DNS cache hits.
type NetworkActivity struct {
	ActiveConnections []Connection `json:"active_connections,omitempty"`
	DNSCacheHits      []DNSHit     `json:"dns_cache_hits,omitempty"`
}

type Connection struct {
	RemoteHost  string    `json:"remote_host"`
	RemotePort  uint16    `json:"remote_port"`
	LocalPort   uint16    `json:"local_port"`
	ProcessName string    `json:"process_name,omitempty"`
	PID         uint32    `json:"pid,omitempty"`
	State       string    `json:"state"`
	DetectedAt  time.Time `json:"detected_at"`
}

type DNSHit struct {
	Hostname   string    `json:"hostname"`
	DetectedAt time.Time `json:"detected_at"`
}

// AIProcess is a running process identified as AI tooling.
type AIProcess struct {
	PID         int       `json:"pid"`
	Name        string    `json:"name"`
	ExePath     string    `json:"exe_path,omitempty"`
	CommandLine string    `json:"command_line,omitempty"`
	DetectedAt  time.Time `json:"detected_at"`
}

// AIApp is an installed AI application.
type AIApp struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path"`
	Source  string `json:"source"`
}

// MCPServer is a detected MCP server configuration.
type MCPServer struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
	Source  string `json:"source"`
	Port    int    `json:"port,omitempty"`
	Active  bool   `json:"active"`
}

// GPU is a detected GPU device.
type GPU struct {
	Name          string `json:"name"`
	VRAMBytes     int64  `json:"vram_bytes,omitempty"`
	DriverVersion string `json:"driver_version,omitempty"`
	Source        string `json:"source"`
}

// PythonEnv is a Python environment with AI packages.
type PythonEnv struct {
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	AIPackages []string  `json:"ai_packages"`
	DetectedAt time.Time `json:"detected_at"`
}

// NodeProject is a Node.js project with AI packages.
type NodeProject struct {
	Path       string    `json:"path"`
	AIPackages []string  `json:"ai_packages"`
	DetectedAt time.Time `json:"detected_at"`
}
