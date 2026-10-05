package api

import (
	"encoding/json"
	"regexp"
)

// B-293: which remote config an endpoint's agent is actually running, from
// its latest report (ordered by server received_at, B-284). The agent puts
// config_version, config_source and config_error in every report; reports
// from older agents carry none of them, so all three stay null ("not
// known"), never a guess.
//
// config_source and config_error are agent-supplied text shown back to
// admins, so only known codes pass through (the standing "no raw text
// across a trust boundary" check); anything else becomes "unrecognised".
// The lists must match eami-agent's internal/remoteconfig codes.

var configVersionPattern = regexp.MustCompile(`^c1:[0-9a-f]{64}$`)

var knownConfigSources = map[string]bool{
	"remote": true, "persisted": true, "local": true, "defaults": true,
}

var knownConfigErrors = map[string]bool{
	"fetch_failed": true, "response_too_large": true, "malformed_json": true,
	"wrong_type": true, "incomplete": true, "version_mismatch": true,
	"interval_out_of_range": true, "report_size_out_of_range": true,
	"model_size_out_of_range": true, "too_many_paths": true, "path_too_long": true,
	"path_empty": true, "path_not_absolute": true, "path_invalid_chars": true, "path_network": true,
	"too_many_scanners": true, "state_untrusted": true, "state_corrupt": true,
	"state_invalid": true, "state_stale": true, "state_write_failed": true,
}

const unrecognisedCode = "unrecognised"

// appliedConfig is the part of the latest report this file reads.
type appliedConfig struct {
	Version *string `json:"config_version"`
	Source  *string `json:"config_source"`
	Error   *string `json:"config_error"`
}

// parseAppliedConfig extracts the three fields from a raw report. A field
// absent from the report stays nil. A version is kept only when it is a
// well-formed c1 hash or "" (an unversioned remote config, shown as such);
// anything else is dropped to nil.
func parseAppliedConfig(report json.RawMessage) appliedConfig {
	var a appliedConfig
	if len(report) == 0 || json.Unmarshal(report, &a) != nil {
		return appliedConfig{}
	}
	if a.Version != nil && *a.Version != "" && !configVersionPattern.MatchString(*a.Version) {
		a.Version = nil
	}
	a.Source = allowlisted(a.Source, knownConfigSources)
	if a.Error != nil && *a.Error == "" {
		a.Error = nil
	}
	a.Error = allowlisted(a.Error, knownConfigErrors)
	return a
}

func allowlisted(v *string, known map[string]bool) *string {
	if v == nil || known[*v] {
		return v
	}
	u := unrecognisedCode
	return &u
}
