//go:build !windows

package remoteconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_UnixLoosenedPermissionsAreUntrusted(t *testing.T) {
	s := newStore(t)
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(s.path()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %o, want 600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(s.Dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode %o, want 700", fi.Mode().Perm())
	}
	if err := os.Chmod(s.path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("world-readable file: ok=%v reason=%q", ok, reason)
	}
	if err := os.Chmod(s.path(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("world-writable dir: ok=%v reason=%q", ok, reason)
	}
	// The next save takes the directory back.
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); !ok || reason != "" {
		t.Fatalf("after re-secure: ok=%v reason=%q", ok, reason)
	}
}

func TestStore_UnixSymlinkIsUntrusted(t *testing.T) {
	s := newStore(t)
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err != nil {
		t.Fatal(err)
	}
	real := s.path() + ".real"
	if err := os.Rename(s.path(), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, s.path()); err != nil {
		t.Fatal(err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("symlinked state file: ok=%v reason=%q", ok, reason)
	}
}

// Security review H-1 (Unix counterpart): a symlinked directory anywhere in
// the chain is never followed, and its target stays untouched.
func TestStore_UnixSymlinkInChainIsRefused(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(base, "EAMI")); err != nil {
		t.Fatal(err)
	}
	s := Store{Base: base, Dir: filepath.Join(base, "EAMI", "Agent")}
	c := validConfig()
	if err := s.Save(testID, c, c.Version(), time.Now()); err == nil {
		t.Fatal("Save followed a symlink in the chain")
	}
	if _, err := os.Stat(filepath.Join(target, "Agent")); !os.IsNotExist(err) {
		t.Fatalf("the symlink target was written to: %v", err)
	}
	if _, _, reason, ok, _ := s.Load(testID); ok || reason != ReasonStateUntrusted {
		t.Fatalf("Load through a symlink: ok=%v reason=%q", ok, reason)
	}
	if err := s.Delete(); err == nil {
		t.Fatal("Delete followed a symlink in the chain")
	}
}
