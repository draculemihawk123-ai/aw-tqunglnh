//go:build !windows

package workspacereconcile_test

import "syscall"

// ownProcessGroup starts a test process in its own process group, the way the Run app host starts a real one.
func ownProcessGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
