package nmlauncher

import (
	"errors"
	"testing"
)

func withParentProcessNameFunc(t *testing.T, fn func() (string, error)) {
	t.Helper()
	orig := parentProcessNameFunc
	parentProcessNameFunc = fn
	t.Cleanup(func() { parentProcessNameFunc = orig })
}

func TestIsKnownBrowser(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Windows -- confirmed against this real development machine's
		// actual running chrome.exe/msedge.exe processes (B-037).
		{"chrome.exe", true},
		{"CHROME.EXE", true},
		{"msedge.exe", true},
		{"msedgewebview2.exe", true}, // B-037: real Edge WebView2 process, confirmed running here
		{"chromium", true},           // generic Chromium build name, seen on multiple platforms

		// macOS -- per-channel bundle executable names (B-037).
		{"Google Chrome", true}, // macOS `ps -o comm=` style
		{"Google Chrome Beta", true},
		{"Google Chrome Dev", true},
		{"Google Chrome Canary", true},
		{"Microsoft Edge", true},
		{"Microsoft Edge Beta", true},
		{"Microsoft Edge Dev", true},
		{"Microsoft Edge Canary", true},

		// Linux -- per-channel binary names (B-037).
		{"google-chrome", true},
		{"google-chrome-stable", true},
		{"google-chrome-beta", true},
		{"google-chrome-unstable", true},
		{"chromium-browser", true},
		{"microsoft-edge", true},
		{"microsoft-edge-stable", true},
		{"microsoft-edge-beta", true},
		{"microsoft-edge-dev", true},

		{"evil.exe", false},
		{"powershell.exe", false},
		{"brave.exe", false}, // B-037: deliberately not added -- see package doc, scope kept to Chrome/Edge
		{"", false},
		{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", true}, // full path, base name matches
	}
	for _, c := range cases {
		if got := isKnownBrowser(c.name); got != c.want {
			t.Errorf("isKnownBrowser(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestVerifyLaunchedByBrowser_RecognizedParent_Allowed(t *testing.T) {
	withParentProcessNameFunc(t, func() (string, error) { return "chrome.exe", nil })
	r := VerifyLaunchedByBrowser()
	if !r.Allowed {
		t.Fatalf("want Allowed=true for a recognized browser parent, got %+v", r)
	}
}

// TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked (B-037): restored
// to fail-closed after the dev-testing-only fail-open weakening. A
// determined, genuinely unrecognized parent must be refused -- this is
// the confused-deputy protection the whole package exists to provide.
func TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked(t *testing.T) {
	withParentProcessNameFunc(t, func() (string, error) { return "evil.exe", nil })
	r := VerifyLaunchedByBrowser()
	if r.Allowed {
		t.Fatalf("want Allowed=false for an unrecognized parent process, got %+v", r)
	}
	if r.Warn {
		t.Fatalf("want Warn=false for a refusal (Warn only applies to the undetermined-parent fail-open case), got %+v", r)
	}
}

func TestVerifyLaunchedByBrowser_InconclusiveCheck_FailsOpen(t *testing.T) {
	withParentProcessNameFunc(t, func() (string, error) { return "", errors.New("simulated: cannot determine parent") })
	r := VerifyLaunchedByBrowser()
	if !r.Allowed {
		t.Fatalf("want Allowed=true when the parent process cannot be determined at all (fail open), got %+v", r)
	}
}

func TestVerifyLaunchedByBrowser_SkipEnvVar_AlwaysAllowed(t *testing.T) {
	t.Setenv(skipCheckEnvVar, "1")
	withParentProcessNameFunc(t, func() (string, error) { return "evil.exe", nil })
	r := VerifyLaunchedByBrowser()
	if !r.Allowed {
		t.Fatalf("want Allowed=true when %s is set, regardless of parent, got %+v", skipCheckEnvVar, r)
	}
}
