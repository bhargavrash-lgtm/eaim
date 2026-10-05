package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ConfigVersionPrefix names the canonicalisation scheme, so it can change
// later without two schemes producing comparable-looking versions.
const ConfigVersionPrefix = "c1:"

// canonicalAgentConfig is the hashed form of a remote agent config (B-293).
// Field order is part of the contract (keys alphabetical); it must match
// eami-agent's internal/remoteconfig canonical form byte for byte. Both
// modules pin it with the same golden test vectors.
type canonicalAgentConfig struct {
	EnabledScanners     []string `json:"enabled_scanners"`
	MaxReportSizeBytes  int64    `json:"max_report_size_bytes"`
	ModelFileSizeMB     int64    `json:"model_file_size_mb"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	ScanIntervalSeconds int64    `json:"scan_interval_seconds"`
}

// sortedUnique returns a sorted, de-duplicated copy, never nil: order and
// repeats carry no meaning in either list, and nil must hash like [].
func sortedUnique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Version is the config's content hash (B-293): "c1:" + hex(sha256) of the
// canonical JSON. The same config always has the same version, so re-saving
// an unchanged config never looks like a change. It proves integrity in
// transit or at rest, not authenticity: whoever serves a config can compute
// its version, so it is never a security control on its own.
func (c AgentConfig) Version() string {
	canon := canonicalAgentConfig{
		EnabledScanners:     sortedUnique(c.EnabledScanners),
		MaxReportSizeBytes:  int64(c.MaxReportSizeBytes),
		ModelFileSizeMB:     int64(c.ModelFileSizeMB),
		ModelScanPaths:      sortedUnique(c.ModelScanPaths),
		ScanIntervalSeconds: int64(c.ScanIntervalSeconds),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(canon) // cannot fail: plain strings and integers
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return ConfigVersionPrefix + hex.EncodeToString(sum[:])
}
