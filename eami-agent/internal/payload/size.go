package payload

import (
	"encoding/json"

	"github.com/eami/agent/internal/detection/network_activity"
)

// DropOrder is the fixed order in which EnforceMaxSize drops scanner
// results when a report is over max_report_size_bytes (B-293, decision
// D-e). It is deterministic -- the same report always loses the same
// sections -- and goes from the sections that can grow without bound
// (file walks, process lists, project trees) to the small, high-value ones.
var DropOrder = []string{
	"models",           // local_models: one entry per large file; an extra-paths walk can be huge (B-194)
	"ai_processes",     // full command lines
	"nodejs_ai",        // node_projects: one entry per project found
	"python_envs",      // one entry per environment
	"network_activity", // connections and DNS cache hits
	"browser",          // browser_extensions
	"mcp_servers",
	"ai_apps",
	"cloud_clients",
	"gpu",
}

// clearSection empties one scanner's result. Report metadata, ScannerStatus,
// ScannerErrors and the config fields are never touched.
func clearSection(r *Report, name string) bool {
	switch name {
	case "models":
		had := len(r.LocalModels) > 0
		r.LocalModels = nil
		return had
	case "ai_processes":
		had := len(r.AIProcesses) > 0
		r.AIProcesses = nil
		return had
	case "nodejs_ai":
		had := len(r.NodeProjects) > 0
		r.NodeProjects = nil
		return had
	case "python_envs":
		had := len(r.PythonEnvs) > 0
		r.PythonEnvs = nil
		return had
	case "network_activity":
		had := len(r.NetworkActivity.ActiveConnections) > 0 || len(r.NetworkActivity.DNSCacheHits) > 0
		r.NetworkActivity = network_activity.ScanResult{}
		return had
	case "browser":
		had := len(r.BrowserExtensions) > 0
		r.BrowserExtensions = nil
		return had
	case "mcp_servers":
		had := len(r.MCPServers) > 0
		r.MCPServers = nil
		return had
	case "ai_apps":
		had := len(r.AIApps) > 0
		r.AIApps = nil
		return had
	case "cloud_clients":
		had := len(r.CloudClients) > 0
		r.CloudClients = nil
		return had
	case "gpu":
		had := len(r.GPUs) > 0
		r.GPUs = nil
		return had
	}
	return false
}

// EnforceMaxSize keeps the report's JSON (uncompressed, as marshalled for
// sending) at or under maxBytes by dropping whole scanner results in
// DropOrder. Each dropped scanner is marked visibly: scanner_status
// "error", scanner_errors "too_large" -- never silent data loss. maxBytes
// <= 0 means no cap. If the report is still too large with every scanner
// result dropped, it is sent anyway and the server decides. It returns the
// scanners it dropped, in order.
func EnforceMaxSize(r *Report, maxBytes int64) []string {
	if maxBytes <= 0 || fits(r, maxBytes) {
		return nil
	}
	var dropped []string
	for _, name := range DropOrder {
		// Only a scanner that ran cleanly and found something is dropped: an
		// empty result costs nothing, and a scanner already marked error
		// (timeout, panic, ...) keeps its own reason.
		if r.ScannerStatus[name] != ScannerOK || !clearSection(r, name) {
			continue
		}
		if r.ScannerErrors == nil {
			r.ScannerErrors = map[string]string{}
		}
		r.ScannerStatus[name] = ScannerError
		r.ScannerErrors[name] = ReasonTooLarge
		dropped = append(dropped, name)
		if fits(r, maxBytes) {
			break
		}
	}
	return dropped
}

func fits(r *Report, maxBytes int64) bool {
	raw, err := json.Marshal(r)
	return err == nil && int64(len(raw)) <= maxBytes
}
