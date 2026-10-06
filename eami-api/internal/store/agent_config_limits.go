package store

import "strings"

// Remote agent config bounds (B-293, decision D-g; B-269 Slice 0 / B-277).
// eami-agent's internal/remoteconfig enforces the same numbers on everything
// it receives or loads from disk; testdata/agent_config_vectors.json pins
// both modules to the same values and path codes (decision D10). The server
// checks them first so it never serves a config the agent would reject and
// keep last-known-good instead.
const (
	MaxModelScanPaths     = 32
	MaxModelScanPathBytes = 1024
	MaxEnabledScanners    = 32
	MinModelFileSizeMB    = 1
	MaxModelFileSizeMB    = 100000
	MinIntervalSeconds    = 60
	MaxIntervalSeconds    = 86400
	MinReportSizeBytes    = 1048576
	MaxReportSizeBytes    = 52428800
)

// Stable rejection codes (D10). The same strings the agent reports in
// config_error, plus the server-only ones. Never accompanied by the
// offending value.
const (
	CodeIntervalOutOfRange   = "interval_out_of_range"
	CodeReportSizeOutOfRange = "report_size_out_of_range"
	CodeModelSizeOutOfRange  = "model_size_out_of_range"
	CodeTooManyPaths         = "too_many_paths"
	CodePathEmpty            = "path_empty"
	CodePathTooLong          = "path_too_long"
	CodePathInvalidChars     = "path_invalid_chars"
	CodePathNetwork          = "path_network"
	CodePathNotAbsolute      = "path_not_absolute"
	CodePathRoot             = "path_root"
	CodePathNotNormalized    = "path_not_normalized"
	CodePathProfileParent    = "path_profile_parent" // server-only: /home, /Users, X:\Users (B-194)
	CodeTooManyScanners      = "too_many_scanners"
	CodeUnknownScanner       = "unknown_scanner"
)

// IsNetworkScanPath reports a UNC or device path (\\host\share, //host/x,
// \\?\..., \\.\...). The SYSTEM agent walking one would authenticate to that
// host as the machine account, so a config may never point it there
// (B-293 security review M-2). Matches eami-agent's scanpath.IsNetwork.
func IsNetworkScanPath(p string) bool {
	return len(p) >= 2 && (p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/')
}

// IsAbsoluteScanPath accepts the local POSIX and Windows absolute forms. A
// config is shared across platforms, so a path is checked by shape, not by
// the host OS. Network paths are not "absolute" here.
func IsAbsoluteScanPath(p string) bool {
	if IsNetworkScanPath(p) {
		return false
	}
	if len(p) >= 1 && p[0] == '/' {
		return true
	}
	return len(p) >= 3 && isASCIILetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func hasDrive(p string) bool { return len(p) >= 2 && isASCIILetter(p[0]) && p[1] == ':' }

// HasStrayColon reports a ':' anywhere but a leading drive letter: on
// Windows it selects an NTFS stream (C:\Users::$INDEX_ALLOCATION walks
// C:\Users). Matches eami-agent's scanpath.HasStrayColon.
func HasStrayColon(p string) bool {
	rest := p
	if hasDrive(p) {
		rest = p[2:]
	}
	return strings.Contains(rest, ":")
}

// IsNormalizedScanPath reports a path with no "." or ".." component and no
// component ending in a dot or a space (Windows drops those: C:\Users.
// walks C:\Users). Empty components are tolerated (the stored legacy
// C:\\Users). Matches eami-agent's scanpath.IsNormalized (B-269 Slice 0
// security review M-1).
func IsNormalizedScanPath(p string) bool {
	s := strings.ReplaceAll(p, "\\", "/")
	if hasDrive(s) {
		s = s[2:]
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" {
			continue
		}
		if c == "." || c == ".." || strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
			return false
		}
	}
	return true
}

// IsRootScanPath reports a filesystem root by shape ("/" or a drive root
// "C:\", "C:/", "C:"). Non-normal forms that would clean to a root are
// refused earlier (IsNormalizedScanPath). Matches eami-agent's scanpath.IsRoot.
func IsRootScanPath(p string) bool {
	if IsNetworkScanPath(p) {
		return false
	}
	s := strings.ReplaceAll(p, "\\", "/")
	if hasDrive(s) {
		return strings.Trim(s[2:], "/") == ""
	}
	return strings.HasPrefix(s, "/") && strings.Trim(s, "/") == ""
}

// IsProfileParentScanPath reports a whole-profile parent in any separator,
// case or trailing-slash form (including the doubled-backslash C:\\Users
// the baseline default stored): /home, /Users, X:\Users, and the aliases
// X:\Documents and Settings (a junction to X:\Users), /System/Volumes/Data/
// Users (the macOS firmlink) and /var/home (Fedora Silverblue). Walking one
// walks every user's files: the B-194 over-collection default.
func IsProfileParentScanPath(p string) bool {
	s := strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	s = strings.TrimRight(s, "/")
	if hasDrive(s) {
		rest := s[2:]
		return rest == "/users" || rest == "/documents and settings"
	}
	switch s {
	case "/home", "/users", "/system/volumes/data/users", "/var/home":
		return true
	}
	return false
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// ValidateModelScanPaths applies the rules every config route always
// enforces and returns "" or a code. nil (an absent field) is valid.
func ValidateModelScanPaths(paths []string) string {
	if len(paths) > MaxModelScanPaths {
		return CodeTooManyPaths
	}
	for _, p := range paths {
		if p == "" {
			return CodePathEmpty
		}
		if len(p) > MaxModelScanPathBytes {
			return CodePathTooLong
		}
		for i := 0; i < len(p); i++ {
			if p[i] < 0x20 || p[i] == 0x7f {
				return CodePathInvalidChars
			}
		}
		if IsNetworkScanPath(p) {
			return CodePathNetwork
		}
		if HasStrayColon(p) {
			return CodePathInvalidChars
		}
		if !IsNormalizedScanPath(p) {
			return CodePathNotNormalized
		}
		if IsRootScanPath(p) {
			return CodePathRoot
		}
		if !IsAbsoluteScanPath(p) {
			return CodePathNotAbsolute
		}
	}
	return ""
}

// ValidateModelScanPathsFull adds the rules applied when paths are changed
// or published (D9): no whole-profile parents.
func ValidateModelScanPathsFull(paths []string) string {
	if code := ValidateModelScanPaths(paths); code != "" {
		return code
	}
	for _, p := range paths {
		if IsProfileParentScanPath(p) {
			return CodePathProfileParent
		}
	}
	return ""
}

// PathWarnings lists the full-rule codes a stored path list would fail
// (S5: legacy paths stay accepted, but flagged). Sorted, de-duplicated,
// never nil.
func PathWarnings(paths []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range paths {
		code := ""
		switch {
		case IsRootScanPath(p):
			code = CodePathRoot
		case IsProfileParentScanPath(p):
			code = CodePathProfileParent
		}
		if code != "" && !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	if len(out) == 2 && out[0] > out[1] {
		out[0], out[1] = out[1], out[0]
	}
	return out
}
