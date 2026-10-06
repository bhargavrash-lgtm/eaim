// Package remoteconfig applies the server's remote agent config correctly
// (B-293, master-sequence item 8a): a versioned response replaces the
// config whole, last-known-good is persisted outside the admin's YAML, and
// every value is bounds-checked before it can steer a scan.
//
// The config_version is a content hash. It proves the config arrived (or
// was stored) intact; it does not prove who produced it -- anyone serving a
// config can compute its hash. The bounds checks carry the security weight.
package remoteconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/eami/agent/internal/scanpath"
)

// Config is the remotely controlled part of the agent's configuration.
type Config struct {
	ScanIntervalSeconds int64
	EnabledScanners     []string
	ModelScanPaths      []string
	ModelFileSizeMB     int64
	MaxReportSizeBytes  int64
}

// Clone returns a deep copy (the lists are never shared with a running scan).
func (c Config) Clone() Config {
	c.EnabledScanners = append([]string{}, c.EnabledScanners...)
	c.ModelScanPaths = append([]string{}, c.ModelScanPaths...)
	return c
}

// Reason codes. Short, fixed, and the only thing that leaves the endpoint
// about a rejected config: raw errors stay in the local log (B-285, the
// standing review check). eami-api's endpoint_applied_config.go allowlists
// exactly these.
const (
	ReasonFetchFailed          = "fetch_failed"
	ReasonResponseTooLarge     = "response_too_large"
	ReasonMalformedJSON        = "malformed_json"
	ReasonWrongType            = "wrong_type"
	ReasonIncomplete           = "incomplete"
	ReasonVersionMismatch      = "version_mismatch"
	ReasonIntervalOutOfRange   = "interval_out_of_range"
	ReasonReportSizeOutOfRange = "report_size_out_of_range"
	ReasonModelSizeOutOfRange  = "model_size_out_of_range"
	ReasonTooManyPaths         = "too_many_paths"
	ReasonPathEmpty            = "path_empty"
	ReasonPathTooLong          = "path_too_long"
	ReasonPathNotAbsolute      = "path_not_absolute"
	ReasonPathInvalidChars     = "path_invalid_chars"
	ReasonPathNetwork          = "path_network"
	ReasonPathRoot             = "path_root"
	ReasonPathNotNormalized    = "path_not_normalized"
	ReasonTooManyScanners      = "too_many_scanners"
	ReasonStateUntrusted       = "state_untrusted"
	ReasonStateCorrupt         = "state_corrupt"
	ReasonStateInvalid         = "state_invalid"
	ReasonStateStale           = "state_stale"
	ReasonStateWriteFailed     = "state_write_failed"
)

// Bounds (decision D-g; the agent-side slice of B-277). They match
// eami-api's store.AgentConfigLimits and PUT validation.
const (
	// MaxResponseBytes caps the config response and the state file. 256 KiB
	// fits the largest config the server accepts even fully escaped (32
	// paths of 1024 bytes, each byte JSON-escaped up to 6x, code review L6),
	// so a valid config is never rejected as too large.
	MaxResponseBytes      = 256 << 10
	MinIntervalSeconds    = 60
	MaxIntervalSeconds    = 86400
	MinReportSizeBytes    = 1 << 20
	MaxReportSizeBytes    = 50 << 20
	MinModelFileSizeMB    = 1
	MaxModelFileSizeMB    = 100000
	MaxModelScanPaths     = 32
	MaxModelScanPathBytes = 1024
	MaxEnabledScanners    = 32
)

// VersionPrefix names the canonicalisation scheme.
const VersionPrefix = "c1:"

// canonical is the hashed form; key order is alphabetical and must match
// eami-api's store.canonicalAgentConfig byte for byte (both modules pin it
// with the same golden vectors).
type canonical struct {
	EnabledScanners     []string `json:"enabled_scanners"`
	MaxReportSizeBytes  int64    `json:"max_report_size_bytes"`
	ModelFileSizeMB     int64    `json:"model_file_size_mb"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	ScanIntervalSeconds int64    `json:"scan_interval_seconds"`
}

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

// Version is "c1:" + hex(sha256(canonical JSON)).
func (c Config) Version() string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(canonical{
		EnabledScanners:     sortedUnique(c.EnabledScanners),
		MaxReportSizeBytes:  c.MaxReportSizeBytes,
		ModelFileSizeMB:     c.ModelFileSizeMB,
		ModelScanPaths:      sortedUnique(c.ModelScanPaths),
		ScanIntervalSeconds: c.ScanIntervalSeconds,
	})
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return VersionPrefix + hex.EncodeToString(sum[:])
}

// IsNetworkScanPath, IsAbsoluteScanPath and IsRootScanPath are the shared
// path-shape rules (internal/scanpath; eami-api's store matches them).
func IsNetworkScanPath(p string) bool  { return scanpath.IsNetwork(p) }
func IsAbsoluteScanPath(p string) bool { return scanpath.IsAbsolute(p) }
func IsRootScanPath(p string) bool     { return scanpath.IsRoot(p) }

// checkPaths returns "" or the specific reason code for the first bad path.
func checkPaths(paths []string) string {
	if len(paths) > MaxModelScanPaths {
		return ReasonTooManyPaths
	}
	for _, p := range paths {
		if p == "" {
			return ReasonPathEmpty
		}
		if len(p) > MaxModelScanPathBytes {
			return ReasonPathTooLong
		}
		for i := 0; i < len(p); i++ {
			if p[i] < 0x20 || p[i] == 0x7f {
				return ReasonPathInvalidChars
			}
		}
		if IsNetworkScanPath(p) {
			return ReasonPathNetwork
		}
		// Forms Windows silently rewrites (B-269 Slice 0 security review
		// M-1): an NTFS stream suffix, or a component ending in "." or " ".
		if scanpath.HasStrayColon(p) {
			return ReasonPathInvalidChars
		}
		if !scanpath.IsNormalized(p) {
			return ReasonPathNotNormalized
		}
		// A filesystem root means walking the whole disk (B-277; B-269
		// Slice 0, decision D9). Rejected outright, so the whole config is
		// refused and last-known-good stays in force.
		if IsRootScanPath(p) {
			return ReasonPathRoot
		}
		if !IsAbsoluteScanPath(p) {
			return ReasonPathNotAbsolute
		}
	}
	return ""
}

// Validate checks every bound on a complete config: "" or a reason code.
func Validate(c Config) string {
	switch {
	case c.ScanIntervalSeconds < MinIntervalSeconds || c.ScanIntervalSeconds > MaxIntervalSeconds:
		return ReasonIntervalOutOfRange
	case c.MaxReportSizeBytes < MinReportSizeBytes || c.MaxReportSizeBytes > MaxReportSizeBytes:
		return ReasonReportSizeOutOfRange
	case c.ModelFileSizeMB < MinModelFileSizeMB || c.ModelFileSizeMB > MaxModelFileSizeMB:
		return ReasonModelSizeOutOfRange
	case len(c.EnabledScanners) > MaxEnabledScanners:
		return ReasonTooManyScanners
	}
	return checkPaths(c.ModelScanPaths)
}

// wire is the response body. Pointers tell a missing or null field apart
// from a zero value; unknown fields are allowed (forward compatibility).
type wire struct {
	ConfigVersion       *string   `json:"config_version"`
	ScanIntervalSeconds *int64    `json:"scan_interval_seconds"`
	EnabledScanners     *[]string `json:"enabled_scanners"`
	ModelScanPaths      *[]string `json:"model_scan_paths"`
	ModelFileSizeMB     *int64    `json:"model_file_size_mb"`
	MaxReportSizeBytes  *int64    `json:"max_report_size_bytes"`
}

// Update is a parsed, checked response.
//   - Versioned: Config is complete and replaces the current config whole.
//   - Not versioned (an older server): only the fields marked in Has apply,
//     merged onto the current config exactly as before B-293.
type Update struct {
	Versioned bool
	Version   string
	Config    Config
	Has       struct{ Interval, Scanners, Paths bool }
}

func decodeReason(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return ReasonWrongType
	}
	return ReasonMalformedJSON
}

// ParseResponse turns a 200 body into an Update, or returns a reason code
// and nothing to apply. len(body) must already be capped by the caller.
func ParseResponse(body []byte) (Update, string) {
	var w wire
	if err := json.Unmarshal(body, &w); err != nil {
		return Update{}, decodeReason(err)
	}
	if w.ConfigVersion == nil || *w.ConfigVersion == "" {
		return parseLegacy(w)
	}
	// Versioned: replace semantics apply only to a complete response that
	// proves it is the whole config. Any gap is rejected, never read as
	// "empty" (the main compatibility risk).
	if w.ScanIntervalSeconds == nil || w.EnabledScanners == nil || w.ModelScanPaths == nil ||
		w.ModelFileSizeMB == nil || w.MaxReportSizeBytes == nil {
		return Update{}, ReasonIncomplete
	}
	c := Config{
		ScanIntervalSeconds: *w.ScanIntervalSeconds,
		EnabledScanners:     append([]string{}, (*w.EnabledScanners)...),
		ModelScanPaths:      append([]string{}, (*w.ModelScanPaths)...),
		ModelFileSizeMB:     *w.ModelFileSizeMB,
		MaxReportSizeBytes:  *w.MaxReportSizeBytes,
	}
	if c.Version() != *w.ConfigVersion {
		return Update{}, ReasonVersionMismatch
	}
	if reason := Validate(c); reason != "" {
		return Update{}, reason
	}
	return Update{Versioned: true, Version: *w.ConfigVersion, Config: c}, ""
}

// parseLegacy keeps the pre-B-293 merge exactly: a field applies only when
// non-zero / non-empty, and max_report_size_bytes stays ignored. What does
// apply is still bounds-checked. A pre-B-293 server always sends these four
// fields, so a body without them -- {} or null from a captive portal or a
// misbehaving proxy -- isn't a config at all (security review L-1).
func parseLegacy(w wire) (Update, string) {
	if w.ScanIntervalSeconds == nil || w.EnabledScanners == nil || w.ModelScanPaths == nil || w.MaxReportSizeBytes == nil {
		return Update{}, ReasonIncomplete
	}
	var u Update
	if w.ScanIntervalSeconds != nil && *w.ScanIntervalSeconds > 0 {
		if *w.ScanIntervalSeconds < MinIntervalSeconds || *w.ScanIntervalSeconds > MaxIntervalSeconds {
			return Update{}, ReasonIntervalOutOfRange
		}
		u.Has.Interval, u.Config.ScanIntervalSeconds = true, *w.ScanIntervalSeconds
	}
	if w.EnabledScanners != nil && len(*w.EnabledScanners) > 0 {
		if len(*w.EnabledScanners) > MaxEnabledScanners {
			return Update{}, ReasonTooManyScanners
		}
		u.Has.Scanners, u.Config.EnabledScanners = true, append([]string{}, (*w.EnabledScanners)...)
	}
	if w.ModelScanPaths != nil && len(*w.ModelScanPaths) > 0 {
		if reason := checkPaths(*w.ModelScanPaths); reason != "" {
			return Update{}, reason
		}
		u.Has.Paths, u.Config.ModelScanPaths = true, append([]string{}, (*w.ModelScanPaths)...)
	}
	return u, ""
}

// KnownScanners filters names to the ones this agent build runs. A name it
// doesn't know (a newer server) is dropped, not fatal (decision D-b's other
// half). It returns the filtered list and whether anything was dropped.
func KnownScanners(names, known []string) ([]string, bool) {
	ok := make(map[string]bool, len(known))
	for _, k := range known {
		ok[k] = true
	}
	out := make([]string, 0, len(names))
	dropped := false
	for _, n := range names {
		if ok[n] {
			out = append(out, n)
		} else {
			dropped = true
		}
	}
	return out, dropped
}
