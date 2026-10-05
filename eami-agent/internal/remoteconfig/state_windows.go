//go:build windows

package remoteconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DefaultDir is %ProgramData%\EAMI\Agent, resolved through the known-folder
// API rather than the environment.
func DefaultDir() string {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil || base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "EAMI", "Agent")
}

// DefaultBase is %ProgramData% itself: OS-owned, trusted as-is, never
// created. Everything below it is checked on every use.
func DefaultBase() string {
	return filepath.Dir(filepath.Dir(DefaultDir()))
}

// trustedSIDs are the identities allowed to own or write the state: SYSTEM,
// BUILTIN\Administrators, and the agent process's own user (SYSTEM again
// for the service; the user when run interactively).
func trustedSIDs() ([]*windows.SID, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return []*windows.SID{system, admins, tu.User.Sid}, nil
}

func isTrusted(sid *windows.SID, trusted []*windows.SID) bool {
	for _, t := range trusted {
		if sid.Equals(t) {
			return true
		}
	}
	return false
}

// writeMask is every right that lets a holder change the object or its
// contents, its ACL or its owner.
const writeMask = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES | 0x40 /* FILE_DELETE_CHILD */ | windows.DELETE |
	windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL

const inheritOnlyACE = 0x08

// checkTrusted refuses a reparse point, the wrong object type, an owner
// outside trustedSIDs, a NULL DACL, or any allow entry that gives write
// access to anyone else. The default %ProgramData% ACL lets standard users
// create folders, so a pre-created or loosened directory is a real threat:
// it would let a user plant a config that steers the SYSTEM agent's scan.
func checkTrusted(path string, isDir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || isDir != fi.IsDir() {
		return fmt.Errorf("%s: not a plain %s", path, map[bool]string{true: "directory", false: "file"}[isDir])
	}
	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%s: no owner", path)
	}
	if !isTrusted(owner, trusted) {
		return fmt.Errorf("%s: untrusted owner %s", path, owner.String())
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%s: NULL DACL grants everyone full access", path)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&inheritOnlyACE != 0 {
			continue // applies to children only; each child is checked itself
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if uint32(ace.Mask)&writeMask != 0 && !isTrusted(sid, trusted) {
				return fmt.Errorf("%s: write access granted to %s", path, sid.String())
			}
		default:
			// Object or callback entries: not something the agent writes,
			// so their presence means someone else changed the ACL.
			return fmt.Errorf("%s: unexpected ACE type %d", path, ace.Header.AceType)
		}
	}
	return nil
}

// protectedSD is the descriptor every state directory gets: a protected
// DACL (nothing inherited from %ProgramData%, whose ACL lets standard users
// create folders) granting full control to SYSTEM, Administrators and the
// agent's own user only, inherited by everything created inside.
func protectedSD() (*windows.SECURITY_DESCRIPTOR, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + tu.User.Sid.String() + ")")
}

// ensureSecureDir makes one directory of the chain safe to use (security
// review H-1). A missing directory is created WITH its protected DACL in the
// same call, so there is no window in which a user could create something
// inside it. An existing one is never followed if it is a reparse point
// (junction, symlink) and never taken over if someone else owns it: the
// store is refused instead (state_untrusted). Only a loosened DACL on a
// directory a trusted identity owns, under an already-verified parent, is
// re-protected; nobody else can swap it while that happens.
func ensureSecureDir(dir string) error {
	sd, err := protectedSD()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	if err := windows.CreateDirectory(p, &sa); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
		return fmt.Errorf("%s: not a plain directory", dir)
	}
	if err := checkTrusted(dir, true); err == nil {
		return nil
	}
	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}
	cur, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if owner, _, err := cur.Owner(); err != nil || owner == nil || !isTrusted(owner, trusted) {
		return fmt.Errorf("%s: owned by an untrusted identity; refusing to use it", dir)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		return err
	}
	return checkTrusted(dir, true)
}

// restrictFile is a no-op on Windows: files inherit the directory's
// protected DACL, and checkTrusted verifies the result on load.
func restrictFile(*os.File) error { return nil }
