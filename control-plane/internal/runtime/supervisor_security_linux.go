package runtime

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// RestrictPrivilegeGain is inherited across fork/exec, preventing application
// code from acquiring privileges through setuid binaries or file capabilities.
func RestrictPrivilegeGain() error {
	// PR_SET_NO_NEW_PRIVS is a thread attribute. A single Prctl only protects
	// whichever Go thread happens to execute it; os/exec may fork on another.
	// Apply it to all runtime threads, including those created concurrently.
	// This intentionally fails closed for CGO builds (ENOTSUP), where Go cannot
	// enumerate foreign threads. Cube guest binaries must use CGO_ENABLED=0.
	_, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// ProtectSupervisorProcess disables same-UID proc environ/memory and ptrace
// access. Exec resets dumpability, so every supervisor startup must reapply it.
// This does not prevent same-UID signals or establish a separate guest UID.
func ProtectSupervisorProcess() error {
	if err := RestrictPrivilegeGain(); err != nil {
		return err
	}
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
