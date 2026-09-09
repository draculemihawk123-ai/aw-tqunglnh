//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// unixProcessTree groups a child as its own process-group leader
// (Setpgid, no explicit Pgid — the kernel makes the group id equal the
// child's own pid), so signaling the negated pid reaches every descendant
// that stayed in the group.
type unixProcessTree struct{}

func newProcessTree() processTree { return unixProcessTree{} }

func (unixProcessTree) configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (unixProcessTree) bind(*exec.Cmd) error { return nil }

func (unixProcessTree) signalGraceful(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGTERM)
}

func (unixProcessTree) kill(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

// quiesced signals 0 to the negated pgid — the standard "does this process
// (or, negated, this process group) still exist" probe: it delivers no
// actual signal, only performs the kernel's own existence/permission check.
// ESRCH means no member of the group exists anymore (empty, quiesced); a
// nil error means at least one member is still alive. Run's own wait
// goroutine holds the single legitimate Wait() call for cmd, so a member
// that already exited is a zombie, not yet reaped — its pid cannot have
// been reused by an unrelated process, so there is no TOCTOU race here
// either.
func (unixProcessTree) quiesced(cmd *exec.Cmd) (bool, error) {
	if cmd.Process == nil {
		return false, errNoProcess
	}
	err := syscall.Kill(-cmd.Process.Pid, 0)
	if err == nil {
		return false, nil
	}
	if err == syscall.ESRCH {
		return true, nil
	}
	return false, err
}

func (unixProcessTree) close() {}

// signalGroup targets the whole process group Setpgid created. Run's own
// wait goroutine holds the single legitimate Wait() call for cmd, so the
// process (if it has already exited by the time this runs) is a zombie,
// not yet reaped — its pid cannot have been reused by an unrelated
// process, so there is no TOCTOU race between checking cmd.Process and
// signaling it here.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}
