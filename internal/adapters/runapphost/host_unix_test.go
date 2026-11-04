//go:build linux

package runapphost

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The leader of a Run app process group died (PR_SET_PDEATHSIG) but a grandchild still runs in its group — the java under mvnw:
// ReapSession still ends the group.
func TestReapSession_EndsTheGroupOfADeadLeader(t *testing.T) {
	root := t.TempDir()
	h := New(root)
	pidFile := filepath.Join(t.TempDir(), "child")
	leader := exec.Command("sh", "-c", "sleep 60 & echo $! > "+pidFile+"; exit 0")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.RecordProcess("s1", "backend", leader.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = leader.Wait() // the leader is gone (reaped); its sleep stays in the group
	var child int
	for i := 0; i < 50 && child == 0; i++ {
		data, _ := os.ReadFile(pidFile)
		child, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 || syscall.Kill(child, 0) != nil {
		t.Fatalf("the grandchild is not running (pid %d)", child)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	if killed := h.ReapSession("s1"); len(killed) != 1 || !strings.HasPrefix(killed[0], "backend (pid ") {
		t.Fatalf("killed = %v", killed)
	}
	for i := 0; i < 50; i++ {
		if _, ok := startTime(child); !ok {
			return
		}
		state, _ := os.ReadFile("/proc/" + strconv.Itoa(child) + "/stat")
		if strings.Contains(string(state), ") Z ") {
			return // dead, waiting for its parent (init) to reap it
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the grandchild still runs")
}

func TestPortHolder_NamesTheListeningProcess(t *testing.T) {
	// The test process itself listens on a free port.
	l, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(interface{ String() string }).String()
	p, _ := strconv.Atoi(port[strings.LastIndexByte(port, ':')+1:])
	if got := (Host{}).PortHolder(p); !strings.Contains(got, "(pid "+strconv.Itoa(os.Getpid())+")") {
		t.Fatalf("PortHolder = %q", got)
	}
}

func listenLocal() (interface {
	Close() error
	Addr() net.Addr
}, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
