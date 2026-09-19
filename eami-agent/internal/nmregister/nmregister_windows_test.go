//go:build windows

package nmregister

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// withTestRegistryKeys temporarily points registryKeys at a throwaway
// HKLM path instead of the real Chrome/Edge NativeMessagingHosts keys --
// this package has no existing tests specifically because Install/
// Uninstall write to real, currently-load-bearing registry locations;
// this seam (registryKeys is already a package-level var, not a const)
// lets EnsureRegistered/registrationCorrect/Install be tested for real,
// including real registry reads/writes, without ever touching this
// machine's actual native-messaging registration. Cleans up the
// throwaway keys and restores the real ones on test completion.
func withTestRegistryKeys(t *testing.T) {
	t.Helper()
	orig := registryKeys
	testKeys := []string{
		`Software\EAMI-TEST-nmregister\Chrome`,
		`Software\EAMI-TEST-nmregister\Edge`,
	}
	registryKeys = testKeys
	t.Cleanup(func() {
		registryKeys = orig
		for _, k := range testKeys {
			_ = registry.DeleteKey(registry.LOCAL_MACHINE, k)
		}
		_ = registry.DeleteKey(registry.LOCAL_MACHINE, `Software\EAMI-TEST-nmregister`)
	})
}

// fakeExe creates a throwaway "installed binary" in a temp directory,
// matching Install's real expected shape (a real file Install can hard
// link to) without touching the real C:\Program Files\EAMI\Agent install.
func fakeExe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "eami-agent.exe")
	if err := os.WriteFile(exe, []byte("fake binary content v1"), 0755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	return exe
}

func TestEnsureRegistered_FreshState_InstallsCorrectly(t *testing.T) {
	withTestRegistryKeys(t)
	exe := fakeExe(t)

	if err := EnsureRegistered(exe); err != nil {
		t.Fatalf("EnsureRegistered on fresh state: %v", err)
	}
	ok, err := registrationCorrect(exe)
	if err != nil || !ok {
		t.Fatalf("registrationCorrect after fresh EnsureRegistered: ok=%v err=%v", ok, err)
	}
}

func TestEnsureRegistered_AlreadyCorrect_NoOp(t *testing.T) {
	withTestRegistryKeys(t)
	exe := fakeExe(t)

	if err := Install(exe); err != nil {
		t.Fatalf("Install: %v", err)
	}
	manifestPath := filepath.Join(filepath.Dir(exe), manifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if err := EnsureRegistered(exe); err != nil {
		t.Fatalf("EnsureRegistered on already-correct state: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("EnsureRegistered rewrote an already-correct manifest -- expected a true no-op")
	}
}

// TestEnsureRegistered_BrokenHardlink_Repairs is the centerpiece test: it
// reproduces the exact failure mode this fix exists for -- the launcher
// hard link pointing at stale, disconnected content after the real binary
// was replaced (e.g. by an MSI file-replace that didn't preserve the
// link), rather than the current one.
func TestEnsureRegistered_BrokenHardlink_Repairs(t *testing.T) {
	withTestRegistryKeys(t)
	exe := fakeExe(t)

	if err := Install(exe); err != nil {
		t.Fatalf("Install: %v", err)
	}
	launcherPath := filepath.Join(filepath.Dir(exe), LauncherBaseName+".exe")

	// Simulate the binary being replaced with new content via a fresh file
	// (new inode), not an in-place overwrite -- exactly what breaks a hard
	// link. The launcher still points at the OLD inode after this.
	if err := os.Remove(exe); err != nil {
		t.Fatalf("remove exe to simulate replacement: %v", err)
	}
	if err := os.WriteFile(exe, []byte("fake binary content v2 -- different inode"), 0755); err != nil {
		t.Fatalf("rewrite exe: %v", err)
	}

	ok, err := registrationCorrect(exe)
	if err != nil {
		t.Fatalf("registrationCorrect: %v", err)
	}
	if ok {
		t.Fatal("expected registrationCorrect to detect the broken hard link, got ok=true")
	}

	if err := EnsureRegistered(exe); err != nil {
		t.Fatalf("EnsureRegistered repair: %v", err)
	}

	ok, err = registrationCorrect(exe)
	if err != nil || !ok {
		t.Fatalf("registrationCorrect after repair: ok=%v err=%v", ok, err)
	}
	exeInfo, _ := os.Stat(exe)
	launcherInfo, _ := os.Stat(launcherPath)
	if !os.SameFile(exeInfo, launcherInfo) {
		t.Fatal("launcher still not hard-linked to the current exe after repair")
	}
}

func TestEnsureRegistered_MissingManifest_Repairs(t *testing.T) {
	withTestRegistryKeys(t)
	exe := fakeExe(t)

	if err := Install(exe); err != nil {
		t.Fatalf("Install: %v", err)
	}
	manifestPath := filepath.Join(filepath.Dir(exe), manifestFileName)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	if ok, _ := registrationCorrect(exe); ok {
		t.Fatal("expected registrationCorrect to detect the missing manifest")
	}
	if err := EnsureRegistered(exe); err != nil {
		t.Fatalf("EnsureRegistered repair: %v", err)
	}
	if ok, err := registrationCorrect(exe); err != nil || !ok {
		t.Fatalf("registrationCorrect after repair: ok=%v err=%v", ok, err)
	}
}

func TestEnsureRegistered_StaleRegistryValue_Repairs(t *testing.T) {
	withTestRegistryKeys(t)
	exe := fakeExe(t)

	if err := Install(exe); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Simulate a stale registry entry pointing at some other path
	// entirely (e.g. left over from an old install directory).
	if err := writeDefaultValue(registryKeys[0], `C:\some\stale\path.json`); err != nil {
		t.Fatalf("corrupt registry value: %v", err)
	}

	if ok, _ := registrationCorrect(exe); ok {
		t.Fatal("expected registrationCorrect to detect the stale registry value")
	}
	if err := EnsureRegistered(exe); err != nil {
		t.Fatalf("EnsureRegistered repair: %v", err)
	}
	if ok, err := registrationCorrect(exe); err != nil || !ok {
		t.Fatalf("registrationCorrect after repair: ok=%v err=%v", ok, err)
	}
}
