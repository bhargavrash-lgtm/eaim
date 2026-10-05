//go:build !windows

package remoteconfig

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// DefaultDir is where the state file lives on this OS, or "" where the
// agent has no place for it (persistence then stays off).
func DefaultDir() string {
	switch runtime.GOOS {
	case "linux":
		return "/var/lib/eami-agent" // also systemd's StateDirectory=eami-agent
	case "darwin":
		return "/Library/Application Support/EAMI/Agent"
	}
	return ""
}

// DefaultBase is the OS-owned directory DefaultDir sits under; it is
// trusted as-is and never created.
func DefaultBase() string {
	switch runtime.GOOS {
	case "linux":
		return "/var/lib"
	case "darwin":
		return "/Library/Application Support"
	}
	return ""
}

// checkTrusted refuses anything the agent's own identity doesn't exclusively
// control: a symlink, the wrong file type, another owner, or any group or
// other permission bit. A planted or loosened state file would otherwise
// steer the root agent's filesystem walk across restarts (B-277's class).
func checkTrusted(path string, isDir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if isDir && !fi.Mode().IsDir() || !isDir && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a plain %s", path, map[bool]string{true: "directory", false: "file"}[isDir])
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no ownership information", path)
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s: owned by uid %d, not %d", path, st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s: mode %o allows group or other access", path, fi.Mode().Perm())
	}
	return nil
}

// ensureSecureDir makes one directory of the chain safe to use: created
// 0700 if missing (one level only; the caller walks the chain from Base,
// so a missing parent is never created implicitly), and an existing one is
// never followed if it is a symlink or owned by anyone else. Only a loosened
// mode on a directory the agent already owns is tightened back to 0700.
func ensureSecureDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.Mode().IsDir() {
		return fmt.Errorf("%s: not a plain directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s: not owned by the agent", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return checkTrusted(dir, true)
}

func restrictFile(f *os.File) error { return f.Chmod(0o600) }
