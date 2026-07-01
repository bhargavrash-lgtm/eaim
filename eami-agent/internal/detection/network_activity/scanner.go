// Package network_activity detects active outbound TCP connections to known
// AI API endpoints. On Windows it uses the IP Helper API (iphlpapi.dll) via
// golang.org/x/sys/windows — pure Go, no CGO.
package network_activity

import (
	"bufio"
	"context"
	"strings"
	"time"
)

// KnownAIHosts lists hostnames (or hostname suffixes) for known AI API endpoints.
var KnownAIHosts = []string{
	"api.openai.com",
	"api.anthropic.com",
	"generativelanguage.googleapis.com",
	"api.cohere.com",
	"api.mistral.ai",
	"inference.ai.azure.com",
	"bedrock-runtime.amazonaws.com",
}

type ConnectionState string

const (
	StateEstablished ConnectionState = "ESTABLISHED"
	StateTimeWait    ConnectionState = "TIME_WAIT"
	StateCloseWait   ConnectionState = "CLOSE_WAIT"
)

// Connection describes a single detected outbound TCP connection.
type Connection struct {
	RemoteHost  string          `json:"remote_host"`
	RemotePort  uint16          `json:"remote_port"`
	LocalPort   uint16          `json:"local_port"`
	ProcessName string          `json:"process_name"`
	PID         uint32          `json:"pid"`
	State       ConnectionState `json:"state"`
	DetectedAt  time.Time       `json:"detected_at"`
}

// DNSHit represents a recent DNS lookup to a known AI hostname.
type DNSHit struct {
	Hostname   string    `json:"hostname"`
	DetectedAt time.Time `json:"detected_at"`
}

// ScanResult holds the combined output of the network scanner.
type ScanResult struct {
	ActiveConnections []Connection `json:"active_connections"`
	DNSCacheHits      []DNSHit     `json:"dns_cache_hits"`
}

// Scan checks for active or recent outbound connections to known AI API endpoints.
func Scan(ctx context.Context) (ScanResult, error) { return platformScan(ctx) }

// MatchesKnownAIHost returns true if hostname matches any entry in KnownAIHosts.
// Exported for testing.
func MatchesKnownAIHost(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, known := range KnownAIHosts {
		if h == known || strings.HasSuffix(h, "."+known) {
			return true
		}
		if known == "bedrock-runtime.amazonaws.com" &&
			strings.Contains(h, "bedrock-runtime") &&
			strings.HasSuffix(h, ".amazonaws.com") {
			return true
		}
	}
	return false
}

// ParseDNSCache parses `ipconfig /displaydns` (or dscacheutil) text output
// and returns hits for known AI hostnames. Exported for testing.
func ParseDNSCache(output string) []DNSHit {
	now := time.Now().UTC()
	seen := map[string]bool{}
	var hits []DNSHit
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostname := strings.ToLower(strings.TrimSpace(parts[1]))
		if hostname == "" || seen[hostname] {
			continue
		}
		if MatchesKnownAIHost(hostname) {
			seen[hostname] = true
			hits = append(hits, DNSHit{Hostname: hostname, DetectedAt: now})
		}
	}
	return hits
}
