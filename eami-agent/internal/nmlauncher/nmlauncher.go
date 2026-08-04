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
// point of defense-in-depth.
//
// This fails OPEN, with a loud warning, only when the parent process name
// genuinely cannot be determined at all (platform limitation,
// permissions, sandboxing) -- an inconclusive check must not silently
// disable the feature. If the parent name CAN be determined and it isn't
// in knownBrowserExecutables, this fails CLOSED (B-037, restored after a
// dev-testing-only weakening -- see below).
//
// History (B-037, 2026-08-04): real-world B1 manual testing (2026-08-03)
// hit a live false positive here -- a real user's real browser produced
// an immediate refusal, breaking the primary paste-flush path. That
// session's fix was to fail open with a warning for ANY unrecognized
// parent, unblocking the user's own testing that night, but explicitly
// flagged (BACKLOG.md B-037, at the time) as a dev-testing convenience,
// not a defensible production posture -- it silently disabled this
// check's entire security value for anything not on the list, which
// defeats its purpose (the confused-deputy protection it exists for is
// exactly the "not a recognized browser" case).
//
// B-037 investigated the actual gap rather than assuming a short list
// was the problem, per its own task brief:
//   - Confirmed (Chromium release-channel documentation, cross-checked
//     against this development machine's real running chrome.exe/
//     msedge.exe processes) that Chrome and Edge use the SAME executable
//     name across every channel on Windows (stable/beta/dev/canary all
//     report as "chrome.exe"/"msedge.exe") -- so Windows channel
//     variation was never actually the gap the original incident
//     suggested. That specific hypothesis is refuted, not just untested.
//   - Confirmed (Chromium's own native-messaging-host launch mechanism,
//     Chromium 113+, 2023-era change) that a browser launches an .exe
//     native host directly -- no cmd.exe or other intermediary process
//     -- for any reasonably current browser, ruling out an
//     intermediary-process theory for a normally-updated install.
//   - Confirmed real per-channel process/bundle name differences DO
//     exist on Linux (google-chrome-stable/-beta/-unstable,
//     microsoft-edge-stable/-beta/-dev -- distinct binaries per channel,
//     unlike Windows) and macOS (each channel ships as a separate .app
//     bundle with its own executable name) -- these were genuinely
//     missing and are now added below, though still not exercised on a
//     live Linux/macOS host (this development machine remains Windows).
//   - The exact parent process name from the original August 2026
//     incident was never captured (the earlier fix reproduced the
//     failure PATTERN with a synthetic non-browser test parent, never
//     inspected the user's actual real parent at the moment of failure)
//     and cannot be recovered retroactively. Leading hypothesis, given
//     the above: the user's real default browser is a different
//     Chromium-family browser (Brave/Opera/Vivaldi/etc.), not Chrome or
//     Edge -- deliberately NOT added to knownBrowserExecutables here,
//     since B-037 scoped this fix to genuinely completing Chrome/Edge
//     coverage, not widening browser-family scope (a separate decision).
//     If a similar refusal recurs, the logged Reason string now names
//     the exact detected parent -- checking that name against this
//     hypothesis is the first thing to try, not re-diagnosing from
//     scratch.
//
// Operators can still force skipping the check entirely via
// EAMI_NM_SKIP_PARENT_CHECK=1 (matches this codebase's established
// fail-closed-with-documented-override convention, e.g. B-023's SSRF
// guard, B-025's fail-closed secret validation) for genuine edge cases
// this allowlist still doesn't cover.
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
// This scoping decision was re-confirmed during B-037 (kept Chrome/Edge
// only, not widened to other Chromium-family browsers) rather than
// carried over unexamined.
//
// Windows entries are confirmed complete for every release channel:
// Chrome and Edge both use the identical executable name across
// stable/beta/dev/canary (verified against Chromium's own release-channel
// documentation and against this development machine's real running
// chrome.exe/msedge.exe processes) -- there is no per-channel Windows
// variant to add. Linux and macOS entries below close a real gap: those
// two platforms DO use distinct per-channel process/bundle names, unlike
// Windows, confirmed via research but not exercised on a live Linux/macOS
// host in this session (flagged, not silently assumed correct).
var knownBrowserExecutables = []string{
	// Windows -- one name per browser covers every channel.
	"chrome",         // Chrome, all channels (stable/beta/dev/canary all report as chrome.exe)
	"msedge",         // Edge, all channels (stable/beta/dev/canary all report as msedge.exe)
	"msedgewebview2", // Edge WebView2 runtime -- a real, distinct Microsoft Edge-family
	// process, confirmed running on this development machine. Accepted on
	// name-recognition grounds alone, same as every other entry in this
	// list (security review, B-037): WebView2 is a general UI-embedding
	// runtime used by many unrelated first- and third-party Windows apps,
	// not exclusively a native-messaging-capable browser context, and
	// whether a WebView2 host enforces the same allowed_origins check a
	// full browser does is NOT verified here -- this entry carries the
	// same (already-documented, already-accepted) rename-based limitation
	// as the rest of the list, no stronger and no weaker.

	"chromium", // generic Chromium build name, seen across multiple platforms, not Windows-specific

	// macOS -- `ps -o comm=`/`/proc`-equivalent reports the app bundle's
	// executable name, which differs per channel (unlike Windows).
	"google chrome",         // stable
	"google chrome beta",    // researched, not verified on a live macOS host
	"google chrome dev",     // researched, not verified on a live macOS host
	"google chrome canary",  // researched, not verified on a live macOS host
	"microsoft edge",        // stable
	"microsoft edge beta",   // researched, not verified on a live macOS host
	"microsoft edge dev",    // researched, not verified on a live macOS host
	"microsoft edge canary", // researched, not verified on a live macOS host

	// Linux -- distribution packages install a channel-specific binary
	// name (unlike Windows, where one binary name covers every channel).
	"google-chrome", // some distros symlink the default/stable binary to this bare name
	"google-chrome-stable",
	"google-chrome-beta",
	"google-chrome-unstable", // Chrome's Dev channel is packaged as "unstable" on Linux
	"chromium-browser",
	"microsoft-edge", // some distros symlink the default/stable binary to this bare name
	"microsoft-edge-stable",
	"microsoft-edge-beta",
	"microsoft-edge-dev",
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
	// Warn marks a Reason worth logging at WARN rather than INFO: the
	// parent process could not be determined at all, so the caller
	// proceeded with reduced assurance rather than a clean pass. Never
	// set when Allowed is false (a refusal is always logged as ERROR by
	// the caller, per main.go).
	Warn bool
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
			Warn:    true,
			Reason:  "could not determine parent process (" + err.Error() + ") -- proceeding without this check",
		}
	}

	if isKnownBrowser(name) {
		return Result{Allowed: true, Reason: "parent process recognized as a browser: " + name}
	}

	// Determined, and NOT on the list -- fail CLOSED (B-037, restored;
	// see the package doc comment for the investigation that led here).
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
