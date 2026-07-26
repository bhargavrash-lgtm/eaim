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
		{"chrome.exe", true},
		{"CHROME.EXE", true},
		{"msedge.exe", true},
		{"chromium", true},
		{"google-chrome", true},
		{"Google Chrome", true}, // macOS `ps -o comm=` style
		{"Microsoft Edge", true},
		{"evil.exe", false},
		{"powershell.exe", false},
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

func TestVerifyLaunchedByBrowser_UnrecognizedParent_Blocked(t *testing.T) {
	withParentProcessNameFunc(t, func() (string, error) { return "evil.exe", nil })
	r := VerifyLaunchedByBrowser()
	if r.Allowed {
		t.Fatalf("want Allowed=false for an unrecognized parent process, got %+v", r)
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
