//go:build !linux && !windows

package process

import "syscall"

// setParentDeathSignal has no equivalent outside Linux (macOS has no PR_SET_PDEATHSIG); the Run app runner reaps leftovers instead.
func setParentDeathSignal(*syscall.SysProcAttr) {}
