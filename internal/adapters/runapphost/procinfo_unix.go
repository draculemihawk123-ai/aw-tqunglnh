//go:build !windows

package runapphost

import (
	"syscall"
	"time"
)

// killTree ends the process group a Run app process leads (the supervisor started it with Setpgid): SIGTERM, then SIGKILL after 3 s.
func killTree(pid int) {
	if pid <= 1 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	for i := 0; i < 30; i++ {
		if syscall.Kill(-pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// orphanedGroup reports whether the process group a dead leader pid led still has members. The kernel does not hand out a pid that
// is still some process group's id, so a group with that id while no process has the pid is the one the recorded leader led.
func orphanedGroup(pid int) bool {
	if pid <= 1 {
		return false
	}
	if _, ok := startTime(pid); ok {
		return false // the pid is in use by a process (ours was checked by start time already)
	}
	return syscall.Kill(-pid, 0) == nil
}
