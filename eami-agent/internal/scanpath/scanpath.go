// Package scanpath holds the agent's path-shape rules for model scan paths
// (B-293 bounds, B-269 Slice 0 / B-277). They are checked by shape, the same
// on every OS: one config serves a mixed fleet, and a path written for
// another OS simply doesn't exist locally. eami-api's store package enforces
// the same rules; testdata/agent_config_vectors.json pins both.
//
// Shape is not enough on its own: Windows silently rewrites some forms and a
// path can be a link. So paths must also be in normal form (IsNormalized),
// and the scanner resolves links before walking (models.resolveScanRoot).
package scanpath

import "strings"

// IsNetwork reports a UNC or device path (\\host\share, //host/x, \\?\...,
// \\.\...). The SYSTEM/root agent walking one would authenticate to that
// host as the machine account.
func IsNetwork(p string) bool {
	return len(p) >= 2 && (p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/')
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// hasDrive reports a leading "X:".
func hasDrive(p string) bool { return len(p) >= 2 && isLetter(p[0]) && p[1] == ':' }

// IsAbsolute accepts the local POSIX and Windows absolute forms. Network
// paths are not "absolute" here.
func IsAbsolute(p string) bool {
	if IsNetwork(p) {
		return false
	}
	if len(p) >= 1 && p[0] == '/' {
		return true
	}
	return len(p) >= 3 && hasDrive(p) && (p[2] == '\\' || p[2] == '/')
}

// HasStrayColon reports a ':' anywhere but a leading drive letter. On
// Windows it selects an NTFS stream (C:\Users::$INDEX_ALLOCATION walks
// C:\Users); a fleet-wide config can't allow it.
func HasStrayColon(p string) bool {
	rest := p
	if hasDrive(p) {
		rest = p[2:]
	}
	return strings.Contains(rest, ":")
}

// IsNormalized reports a path with no "." or ".." component and no
// component ending in a dot or a space (Windows drops those, so
// C:\Users. walks C:\Users). Empty components (doubled separators) are
// tolerated: a stored legacy default holds C:\\Users.
func IsNormalized(p string) bool {
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

// IsRoot reports a filesystem root by shape: "/" (any number of slashes) or
// a drive root ("C:\", "C:/", "C:"). Callers check IsNormalized first, so
// "/." and "/.." never reach here as roots; resolved links are checked by
// the scanner against the real root.
func IsRoot(p string) bool {
	if IsNetwork(p) {
		return false
	}
	s := strings.ReplaceAll(p, "\\", "/")
	if hasDrive(s) {
		s = s[2:]
		return strings.Trim(s, "/") == ""
	}
	return strings.HasPrefix(s, "/") && strings.Trim(s, "/") == ""
}
