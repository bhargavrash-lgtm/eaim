//go:build windows

package nmlauncher

import "testing"

// TestParentProcessName_RealLookup_Windows exercises the actual
// toolhelp-snapshot-based Windows implementation against this machine's
// real process tree -- not just the seam-injected unit tests above. It
// doesn't assert which process is the parent (that varies by how `go
// test` itself is invoked -- typically go.exe or a test runner, not a
// browser), only that the real Win32 lookup mechanism works end-to-end
// without error and returns a plausible, non-empty executable name.
func TestParentProcessName_RealLookup_Windows(t *testing.T) {
	name, err := parentProcessName()
	if err != nil {
		t.Fatalf("parentProcessName: %v", err)
	}
	if name == "" {
		t.Fatal("parentProcessName returned an empty name with no error")
	}
	t.Logf("resolved real parent process executable name: %q", name)
}
