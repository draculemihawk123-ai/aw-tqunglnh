//go:build linux

package process

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestConfigure_KillWithParentSetsTheParentDeathSignalOnLinux(t *testing.T) {
	cmd := exec.Command("true")
	unixProcessTree{}.configure(cmd, true)
	if !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("SysProcAttr = %+v", cmd.SysProcAttr)
	}
	plain := exec.Command("true")
	unixProcessTree{}.configure(plain, false)
	if plain.SysProcAttr.Pdeathsig != 0 {
		t.Fatal("an agent or command process must not get a parent death signal")
	}
}
