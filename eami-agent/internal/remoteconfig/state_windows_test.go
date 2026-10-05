//go:build windows

package remoteconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// grantEveryoneWrite adds an allow-write entry for Everyone, the shape of a
// directory a standard user pre-created or loosened under %ProgramData%.
func grantEveryoneWrite(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStore_WindowsDACL(t *testing.T) {
	s := newStore(t)
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The saved directory carries the protected DACL and passes the check.
	if err := checkTrusted(s.Dir, true); err != nil {
		t.Fatalf("secured dir untrusted: %v", err)
	}
	if err := checkTrusted(s.path(), false); err != nil {
		t.Fatalf("state file untrusted: %v", err)
	}

	// Loosened file: Everyone can write -> ignored.
	grantEveryoneWrite(t, s.path())
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("Everyone-writable file: ok=%v reason=%q", ok, reason)
	}

	// Loosened directory: ignored too, then the next save re-secures it.
	if err := os.Remove(s.path()); err != nil {
		t.Fatal(err)
	}
	grantEveryoneWrite(t, s.Dir)
	if err := checkTrusted(s.Dir, true); err == nil {
		t.Fatal("Everyone-writable dir passed the trust check")
	}
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); !ok || reason != "" {
		t.Fatalf("after re-secure: ok=%v reason=%q", ok, reason)
	}
}

// Security review H-1: a junction anywhere between %ProgramData% and the
// state directory is never followed -- not to create, re-ACL, read or
// delete. The junction target must stay untouched.
func TestStore_WindowsJunctionInChainIsRefused(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "EAMI")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
	s := Store{Base: base, Dir: filepath.Join(base, "EAMI", "Agent")}
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err == nil {
		t.Fatal("Save followed a junction in the chain")
	}
	if _, err := os.Stat(filepath.Join(target, "Agent")); !os.IsNotExist(err) {
		t.Fatalf("the junction target was written to: %v", err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("Load through a junction: ok=%v reason=%q", ok, reason)
	}
	if err := s.Delete(); err == nil {
		t.Fatal("Delete followed a junction in the chain")
	}
}

func TestDefaultDir_Windows(t *testing.T) {
	if DefaultBase() == "" || filepath.Join(DefaultBase(), "EAMI", "Agent") != DefaultDir() {
		t.Fatalf("DefaultBase %q / DefaultDir %q", DefaultBase(), DefaultDir())
	}
	d := DefaultDir()
	if d == "" || !(len(d) > 3 && d[1] == ':') {
		t.Fatalf("DefaultDir = %q", d)
	}
}
