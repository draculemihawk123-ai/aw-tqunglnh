//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsProcessTree assigns a child to a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so every descendant it spawns
// terminates together with it, and creates the child in its own console
// process group (CREATE_NEW_PROCESS_GROUP, set in configure) so a CTRL_
// BREAK graceful signal can target it without also hitting this worker's
// own console group.
type windowsProcessTree struct {
	mu  sync.Mutex
	job windows.Handle // 0 (unset) until bind succeeds
}

func newProcessTree() processTree { return &windowsProcessTree{} }

func (t *windowsProcessTree) configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func (t *windowsProcessTree) bind(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("process: create job object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("process: configure job object: %w", err)
	}

	// Opening a fresh handle by pid (os.Process exposes no usable handle
	// of its own) has one accepted, unavoidable-without-far-more-invasive
	// spawn changes race: if the process already exited AND its pid was
	// reused by an unrelated process before this OpenProcess call, that
	// unrelated process would be assigned to the job instead. This window
	// is a handful of instructions right after a successful Start, not the
	// seconds-to-minutes a real attempt runs for.
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("process: open process %d: %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(processHandle)

	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("process: assign process %d to job object: %w", cmd.Process.Pid, err)
	}

	t.mu.Lock()
	t.job = job
	t.mu.Unlock()
	return nil
}

func (t *windowsProcessTree) signalGraceful(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	// CTRL_BREAK targets every process sharing this console process group
	// (CREATE_NEW_PROCESS_GROUP in configure made the child's own pid its
	// group id). Only a process that installed a console control handler
	// observes this as "graceful" — a plain CLI with none simply dies, no
	// worse an outcome than the force path below.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}

func (t *windowsProcessTree) kill(cmd *exec.Cmd) error {
	t.mu.Lock()
	job := t.job
	t.mu.Unlock()
	if job != 0 {
		return windows.TerminateJobObject(job, 1)
	}
	// bind never succeeded (or was never called) — fall back to killing
	// only the direct child; any grandchildren it spawned outside the job
	// are not reachable this way.
	if cmd.Process == nil {
		return errNoProcess
	}
	return cmd.Process.Kill()
}

func (t *windowsProcessTree) close() {
	t.mu.Lock()
	job := t.job
	t.job = 0
	t.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}
