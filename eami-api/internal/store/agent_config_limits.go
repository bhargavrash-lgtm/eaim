package store

// Remote agent config bounds (B-293, decision D-g; the agent-side slice of
// B-277). eami-agent's internal/remoteconfig enforces the same numbers on
// everything it receives or loads from disk, so keep the two in sync. The
// server checks them first so it never serves a config the agent would
// reject and keep last-known-good instead.
const (
	MaxModelScanPaths     = 32
	MaxModelScanPathBytes = 1024
	MaxEnabledScanners    = 32
	MinModelFileSizeMB    = 1
	MaxModelFileSizeMB    = 100000
)

// IsAbsoluteScanPath reports whether p is absolute in either the POSIX or
// the Windows form. A config is shared across platforms (the default holds
// /home, /Users and C:\Users), so a path is checked by shape, not by the
// host OS; a path for another OS simply doesn't exist there.
func IsAbsoluteScanPath(p string) bool {
	if IsNetworkScanPath(p) {
		return false
	}
	if len(p) >= 1 && p[0] == '/' {
		return true
	}
	if len(p) >= 3 && isASCIILetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}

// IsNetworkScanPath reports a UNC or device path (\\host\share, //host/x,
// \\?\..., \\.\...). The SYSTEM agent walking one would authenticate to that
// host as the machine account, so a config may never point it there
// (B-293 security review M-2). Matches eami-agent's remoteconfig.
func IsNetworkScanPath(p string) bool {
	return len(p) >= 2 && (p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/')
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// ValidateModelScanPaths returns "" when paths is within bounds, or a fixed
// message (never echoing the input). nil is valid (absent field).
func ValidateModelScanPaths(paths []string) string {
	if len(paths) > MaxModelScanPaths {
		return "model_scan_paths has too many entries (max 32)"
	}
	for _, p := range paths {
		if len(p) == 0 || len(p) > MaxModelScanPathBytes {
			return "model_scan_paths entries must be 1–1024 bytes"
		}
		for i := 0; i < len(p); i++ {
			if p[i] < 0x20 || p[i] == 0x7f {
				return "model_scan_paths entries must not contain control characters"
			}
		}
		if IsNetworkScanPath(p) {
			return "model_scan_paths entries must be local paths, not network (UNC) paths"
		}
		if !IsAbsoluteScanPath(p) {
			return "model_scan_paths entries must be absolute paths"
		}
	}
	return ""
}
