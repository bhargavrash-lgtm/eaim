// Package browser detects AI-related browser extensions in Chrome and Edge.
// Only extensions whose IDs appear in knownExtensions are reported.
package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// BrowserExtension describes a detected AI-related browser extension.
type BrowserExtension struct {
	Browser     string    `json:"browser"`             // "chrome" | "edge"
	Name        string    `json:"name"`
	ExtensionID string    `json:"extension_id"`
	Version     string    `json:"version,omitempty"`
	UserPath    string    `json:"user_path,omitempty"` // anonymised: username only
	DetectedAt  time.Time `json:"detected_at"`
}

// ExtensionDir is a (browser, extensions-root) pair supplied to the scanner.
// Path should point to the Extensions/ directory that contains one sub-directory
// per installed extension ID.
type ExtensionDir struct {
	Browser  string // "chrome" | "edge"
	Path     string // absolute path to .../Extensions/
	UserPath string // anonymised user identifier (e.g. username)
}

// knownExtensions maps Chrome/Edge extension IDs to their human-readable names.
// Add new entries here to expand detection coverage.
var knownExtensions = map[string]string{
	"ppmdhllojcbgkbmcfkhdkklblickdbno": "Claude (Anthropic)",
	"bbnkhnaphfggfomhfnnbhiplfflgolge": "ChatGPT (OpenAI)",
	"hlgbmkakpnlgpfkipbiiknlibooaoeag": "Perplexity AI",
	"camppjleccjaphfdbohjdohecfnoikec":  "Merlin AI",
	"ddlbpiadoechcolndfekhkjhkjebnakj":  "Compose AI",
	"ofpnmcalabcbjgholdjcjblkibolbppb":  "Monica AI",
	"kbfnbcaeplbcioakkpcpgfkobkghlhen":  "Grammarly",
}

// Option is a functional option for Scanner.
type Option func(*Scanner)

// WithBaseDirs overrides the platform extension directories. Used in tests to
// inject a temporary directory tree without touching the real filesystem.
func WithBaseDirs(dirs []ExtensionDir) Option {
	return func(s *Scanner) {
		s.dirsFunc = func() []ExtensionDir { return dirs }
	}
}

// Scanner scans browser extension directories for known AI extensions.
type Scanner struct {
	dirsFunc func() []ExtensionDir
}

// New returns a Scanner initialised with platform-default extension directories.
func New(opts ...Option) *Scanner {
	s := &Scanner{dirsFunc: platformDirs}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Scan returns all detected AI browser extensions. Missing Chrome/Edge
// installations are silently skipped; only context errors are propagated.
func (s *Scanner) Scan(ctx context.Context) ([]BrowserExtension, error) {
	var result []BrowserExtension
	for _, dir := range s.dirsFunc() {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		for id, fallbackName := range knownExtensions {
			ext, ok := scanExtension(
				filepath.Join(dir.Path, id),
				dir.Browser, id, fallbackName, dir.UserPath,
			)
			if ok {
				result = append(result, ext)
			}
		}
	}
	return result, nil
}

// Scan is a package-level convenience wrapper.
func Scan(ctx context.Context) ([]BrowserExtension, error) {
	return New().Scan(ctx)
}

// scanExtension probes a single extension directory (one known ID under one
// browser profile). Returns the populated struct and true if the extension is
// installed; false if the directory does not exist.
func scanExtension(extDir, brws, id, fallbackName, userPath string) (BrowserExtension, bool) {
	entries, err := os.ReadDir(extDir)
	if err != nil {
		// Directory absent means extension not installed — not an error.
		return BrowserExtension{}, false
	}

	// Chrome/Edge store each version in a sub-directory named "{ver}_0".
	// Find the first such directory.
	var versionDir string
	for _, e := range entries {
		if e.IsDir() {
			versionDir = e.Name()
			break
		}
	}

	name := fallbackName
	version := versionDir // raw dir name as fallback (e.g. "1.5.2_0")

	if versionDir != "" {
		manifestPath := filepath.Join(extDir, versionDir, "manifest.json")
		if n, v, ok := readManifest(manifestPath); ok {
			if n != "" {
				name = n
			}
			if v != "" {
				version = v
			}
		}
	}

	return BrowserExtension{
		Browser:     brws,
		Name:        name,
		ExtensionID: id,
		Version:     version,
		UserPath:    userPath,
		DetectedAt:  time.Now().UTC(),
	}, true
}

// extManifest is the minimal subset of a browser extension manifest.json.
type extManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// readManifest parses name and version from a manifest.json file.
// Returns ("", "", false) on any error; the caller uses its fallback values.
func readManifest(path string) (name, version string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var m extManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", false
	}
	return m.Name, m.Version, true
}
