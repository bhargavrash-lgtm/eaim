// Package nmlauncher provides defense-in-depth verification that
// eami-agent's native-messaging host mode was actually launched by a
// browser, not an arbitrary local process.
//
// Native messaging's real enforcement point is the browser itself: it
// reads the manifest's allowed_origins/allowed_extensions and refuses to
// launch the host for any extension not listed there. That check happens
// entirely on the browser's side, before the host process even starts --
// nothing in the native messaging protocol gives the host process itself
// a way to cryptographically verify who launched it (this is a universal
// property of the mechanism, true of every native messaging host, not a
// gap specific to this implementation). Without any check at all, though,
// any local process could invoke `eami-agent --native-messaging-host`
// directly and have it forward fabricated data using the agent's own
// already-configured collector credentials -- a confused-deputy problem
// flagged explicitly by this task's mandatory security review.
//
// VerifyLaunchedByBrowser is a best-effort mitigation: it checks whether
// the immediate parent process is a recognized browser executable. It is
// NOT bulletproof -- a sufficiently capable local attacker could rename
// their process or launch it as a child of a real browser process -- but
// it meaningfully raises the bar above "no check at all", which is the
// point of defense-in-depth. If the parent process name genuinely cannot
// be determined (platform limitation, permissions, sandboxing), this
// fails OPEN with a loud warning rather than breaking the feature
// entirely over an inconclusive check; if the parent CAN be determined
// and is clearly not a browser, it fails CLOSED. Operators can force
// skipping this check via EAMI_NM_SKIP_PARENT_CHECK=1 if it proves too
// strict in some environment (matches this codebase's established
// fail-closed-with-documented-override convention, e.g. B-023's SSRF
// guard, B-025's fail-closed secret validation).
package nmlauncher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// knownBrowserExecutables lists recognized browser process names (base
// filename only, extension-stripped, matched case-insensitively). Scoped
// to Chrome and Edge only, matching this codebase's existing
// browser-detection scope (internal/detection/browser only covers
// Chrome/Edge, not Firefox) -- consistent, not an arbitrary new list.
var knownBrowserExecutables = []string{
	"chrome",        // Windows/Linux Chrome, and macOS `ps -o comm=` may report just this
	"google chrome", // macOS: full "Google Chrome" process name
	"google-chrome", // Linux Chrome package binary name
	"chromium",
	"chromium-browser",
	"msedge",         // Windows Edge
	"microsoft edge", // macOS: full "Microsoft Edge" process name
	"microsoft-edge", // Linux Edge package binary name
}

const skipCheckEnvVar = "EAMI_NM_SKIP_PARENT_CHECK"

// parentProcessNameFunc is a test seam: tests override this to inject a
// fake parent process name/error without needing real OS process
// introspection or a live browser parent. Production always uses the
// real per-OS parentProcessName (build-tagged: parentproc_windows.go,
// parentproc_linux.go, parentproc_darwin.go, parentproc_other.go).
var parentProcessNameFunc = parentProcessName

// Result describes the outcome of VerifyLaunchedByBrowser, for logging.
type Result struct {
	// Allowed is whether the caller should proceed.
	Allowed bool
	// Reason is a short, log-friendly explanation.
	Reason string
}

// VerifyLaunchedByBrowser checks the immediate parent process and decides
// whether this invocation should be allowed to proceed as a native
// messaging host.
func VerifyLaunchedByBrowser() Result {
	if os.Getenv(skipCheckEnvVar) != "" {
		return Result{Allowed: true, Reason: skipCheckEnvVar + " set -- parent-process check skipped by operator"}
	}

	name, err := parentProcessNameFunc()
	if err != nil {
		// Could not determine the parent at all -- fail OPEN, loudly.
		// An inconclusive check must not silently disable the feature on
		// platforms/environments where process introspection doesn't work.
		return Result{
			Allowed: true,
			Reason:  "could not determine parent process (" + err.Error() + ") -- proceeding without this check",
		}
	}

	if isKnownBrowser(name) {
		return Result{Allowed: true, Reason: "parent process recognized as a browser: " + name}
	}

	return Result{
		Allowed: false,
		Reason:  fmt.Sprintf("parent process %q is not a recognized browser -- refusing to run as a native messaging host (set %s=1 to override)", name, skipCheckEnvVar),
	}
}

func isKnownBrowser(exeName string) bool {
	base := filepath.Base(exeName)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToLower(strings.TrimSpace(base))
	for _, known := range knownBrowserExecutables {
		if base == known {
			return true
		}
	}
	return false
}
