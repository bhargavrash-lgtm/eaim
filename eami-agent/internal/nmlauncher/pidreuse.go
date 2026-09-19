package nmlauncher

import (
	"fmt"
	"time"
)

// verifyNotReusedPID is the pure decision logic behind the PID-reuse fix
// documented on parentProcessName (Windows) -- kept platform-independent
// and seam-free so it can be unit-tested directly with synthetic
// timestamps, without needing to manipulate real OS processes.
//
// A genuine parent must have been created strictly before the process it
// spawned (it had to still be running to call CreateProcess). If the
// process currently occupying ppid has a creation time at or after
// ownCreation, that PID was necessarily vacated by the real parent
// exiting and reused by an unrelated, later process -- its name must not
// be trusted as the real parent's.
func verifyNotReusedPID(ppid int, parentCreation, ownCreation time.Time) error {
	if !parentCreation.Before(ownCreation) {
		return fmt.Errorf("parent process id %d was created at %s, at or after this process's own creation at %s -- the real parent already exited and this PID was reused by an unrelated process", ppid, parentCreation, ownCreation)
	}
	return nil
}
