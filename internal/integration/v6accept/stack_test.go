package v6accept

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// lockedBuffer is a bytes.Buffer safe for one writer goroutine (the
// supervisor copying a child's stdout) and one reader (the test polling it).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// childProcess is one real operating-system process started through the
// repository's own production ports.ProcessSupervisor (process-group aware:
// CTRL_BREAK on Windows, SIGTERM elsewhere, then a whole-tree kill after the
// grace period) — the same primitive the worker uses for provider CLIs, so
// the harness inherits its platform handling instead of re-deriving it.
type childProcess struct {
	name   string
	id     ports.ProcessID
	sup    *processadapter.Supervisor
	stdout *lockedBuffer
	stderr *lockedBuffer
	done   chan struct{}
	result ports.ProcessResult
	runErr error
}

// inheritedEnvironment is the minimal parent environment a child needs to
// find `git` and behave on Windows. Everything else is passed explicitly.
var inheritedEnvironment = []string{
	"PATH", "Path", "PATHEXT", "SystemRoot", "SYSTEMROOT", "windir", "ComSpec",
	"TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
}

const (
	childTimeout     = 60 * time.Minute
	childGracePeriod = 20 * time.Second
	childStopBound   = childGracePeriod + 15*time.Second
)

func startChild(t *testing.T, sup *processadapter.Supervisor, name, executable, workingDirectory string, argv []string, environment map[string]string) *childProcess {
	t.Helper()
	child := &childProcess{
		name: name, id: ports.ProcessID(name), sup: sup,
		stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, done: make(chan struct{}),
	}
	spec := ports.ProcessSpec{
		ID: child.id, Executable: executable, Argv: argv, WorkingDirectory: workingDirectory,
		Environment: environment, InheritedEnvironment: inheritedEnvironment,
		Timeout: childTimeout, GracePeriod: childGracePeriod, OutputLimitBytes: 16 << 20,
	}
	go func() {
		defer close(child.done)
		child.result, child.runErr = sup.Run(context.Background(), spec, child.stdout, child.stderr)
	}()
	return child
}

// exited reports whether the process has already terminated.
func (c *childProcess) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// waitForStdoutLine polls stdout until a line containing marker appears, and
// fails the test — with both streams — if the process exits or the deadline
// passes first.
func (c *childProcess) waitForStdoutLine(t *testing.T, marker string, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(c.stdout.String(), "\n") {
			if strings.Contains(line, marker) {
				return strings.TrimSpace(line)
			}
		}
		if c.exited() {
			t.Fatalf("%s exited before announcing %q (exit=%d err=%v)\nstdout:\n%s\nstderr:\n%s",
				c.name, marker, c.result.ExitCode, c.runErr, c.stdout.String(), c.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never announced %q within %s\nstdout:\n%s\nstderr:\n%s", c.name, marker, within, c.stdout.String(), c.stderr.String())
	return ""
}

// stop asks the process to shut down the way an operator's Ctrl+C would and
// returns how it went. gracefulExit is true only when the process itself
// returned exit code 0 inside the grace period (a forced tree-kill is not a
// graceful exit and reports a non-zero code).
func (c *childProcess) stop(t *testing.T) (gracefulExit bool) {
	t.Helper()
	if c.exited() {
		return c.result.ExitCode == 0
	}
	if err := c.sup.Cancel(context.Background(), c.id); err != nil {
		t.Fatalf("cancel %s: %v", c.name, err)
	}
	select {
	case <-c.done:
	case <-time.After(childStopBound):
		t.Fatalf("%s did not stop within %s\nstdout:\n%s\nstderr:\n%s", c.name, childStopBound, c.stdout.String(), c.stderr.String())
	}
	return c.result.ExitCode == 0
}

// dumpOnFailure prints both streams when the test failed, so a red journey
// always carries the processes' own account of what happened.
func (c *childProcess) dumpOnFailure(t *testing.T) {
	t.Helper()
	if !t.Failed() {
		return
	}
	t.Logf("---- %s stdout ----\n%s", c.name, tail(c.stdout.String(), 6000))
	t.Logf("---- %s stderr ----\n%s", c.name, tail(c.stderr.String(), 12000))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-n:]
}

// stack is the pair of real processes plus the directories they share — the
// operator's installation. Only its own start/stop/restart touch the
// processes; every product interaction goes through s.api.
type stack struct {
	bin           binaries
	root          string
	dbPath        string
	artifactRoot  string
	workspaceRoot string
	sup           *processadapter.Supervisor
	serve         *childProcess
	worker        *childProcess
	api           *apiClient
	generation    int
}

// newStack lays out a clean installation directory. It starts nothing.
func newStack(t *testing.T) *stack {
	t.Helper()
	bin := builtBinaries(t)
	root := t.TempDir()
	s := &stack{
		bin:           bin,
		root:          root,
		dbPath:        filepath.Join(root, "aw.db"),
		artifactRoot:  filepath.Join(root, "artifacts"),
		workspaceRoot: filepath.Join(root, "workspaces"),
		sup:           processadapter.NewSupervisor(),
	}
	for _, dir := range []string{s.artifactRoot, s.workspaceRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	t.Cleanup(func() {
		// Best-effort teardown so a failed journey never leaks processes.
		for _, child := range []*childProcess{s.worker, s.serve} {
			if child == nil || child.exited() {
				continue
			}
			_ = child.sup.Cancel(context.Background(), child.id)
			select {
			case <-child.done:
			case <-time.After(childStopBound):
			}
		}
	})
	return s
}

// commonPathFlags are the three paths every aw process of one installation
// shares.
func (s *stack) commonPathFlags() []string {
	return []string{"--db", s.dbPath, "--artifact-root", s.artifactRoot, "--workspace-root", s.workspaceRoot}
}

// start brings the installation up the way an operator would: `aw serve`
// first (it creates and migrates the database), wait until it reports ready,
// then `aw worker` against the same database.
func (s *stack) start(t *testing.T) {
	t.Helper()
	s.generation++
	suffix := fmt.Sprintf("-g%d", s.generation)

	serveArgs := append([]string{"serve"}, s.commonPathFlags()...)
	serveArgs = append(serveArgs, "--host", "127.0.0.1", "--port", "0", "--claude-executable", s.bin.fakeClaude)
	s.serve = startChild(t, s.sup, "aw-serve"+suffix, s.bin.aw, s.root, serveArgs, nil)
	line := s.serve.waitForStdoutLine(t, `"address"`, 60*time.Second)
	var announced struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(line), &announced); err != nil || announced.Address == "" {
		t.Fatalf("aw serve announcement %q is not {\"address\":...}: %v", line, err)
	}
	s.api = newAPIClient(announced.Address)
	s.api.bootstrap(t)
	s.api.waitReady(t, 60*time.Second)

	workerArgs := append([]string{"worker"}, s.commonPathFlags()...)
	workerArgs = append(workerArgs,
		"--claude-executable", s.bin.fakeClaude,
		"--poll-interval", "100ms",
		"--projection-interval", "200ms",
		"--completion-interval", "300ms",
		"--env-allowlist", "AGENTKIT_HELPER_MODE,AGENTKIT_HELPER_OUTCOME",
	)
	workerEnv := map[string]string{
		"AGENTKIT_HELPER_MODE":    "outcome-success",
		"AGENTKIT_HELPER_OUTCOME": "done",
	}
	s.worker = startChild(t, s.sup, "aw-worker"+suffix, s.bin.aw, s.root, workerArgs, workerEnv)
	s.worker.waitForStdoutLine(t, `"workerId"`, 60*time.Second)
}

// stop shuts both processes down and reports whether each exited gracefully
// (exit code 0 inside the grace period). The worker goes first so no job is
// claimed while the API is already gone.
func (s *stack) stop(t *testing.T) (serveGraceful, workerGraceful bool) {
	t.Helper()
	workerGraceful = s.worker.stop(t)
	serveGraceful = s.serve.stop(t)
	s.worker.dumpOnFailure(t)
	s.serve.dumpOnFailure(t)
	return serveGraceful, workerGraceful
}

// restart is a clean stop followed by a fresh start on the SAME database,
// artifact root and workspace root; the new serve process binds a new
// ephemeral port and mints a new session token.
func (s *stack) restart(t *testing.T) {
	t.Helper()
	serveGraceful, workerGraceful := s.stop(t)
	if !serveGraceful || !workerGraceful {
		t.Fatalf("restart requires a graceful shutdown: serveGraceful=%v workerGraceful=%v", serveGraceful, workerGraceful)
	}
	s.start(t)
}
