//go:build linux

package process

import "syscall"

// setParentDeathSignal (V12-30): the kernel sends SIGKILL to the child when the thread that started it exits — in practice when the
// worker dies, even by SIGKILL. It reaches the direct child only; the rest of its process group is reaped by the Run app runner when
// the worker starts again.
func setParentDeathSignal(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }
