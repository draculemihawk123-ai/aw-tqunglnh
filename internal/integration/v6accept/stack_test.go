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

// hardKill (V6-14A) is the harness's own real crash primitive: it
// terminates the whole process tree IMMEDIATELY through
// processadapter.Supervisor.HardKill — no CTRL_BREAK/SIGTERM courtesy
// signal, no grace period — the closest real-world analogue to power loss
// or `kill -9` this test process can produce. Unlike stop (above), which
// asks nicely and only escalates to a forced kill after childGracePeriod
// elapses unanswered, hardKill proves a fault-matrix scenario's own crash
// genuinely interrupted whatever was in flight rather than giving the
// child a chance to finish or checkpoint cleanly on its way out — the
// entire point of a crash-recovery test. A no-op if the process already
// exited on its own.
func (c *childProcess) hardKill(t *testing.T) {
	t.Helper()
	if c.exited() {
		return
	}
	if err := c.sup.HardKill(context.Background(), c.id); err != nil {
		t.Fatalf("hard kill %s: %v", c.name, err)
	}
	select {
	case <-c.done:
	case <-time.After(childStopBound):
		t.Fatalf("%s did not die within %s after a hard kill\nstdout:\n%s\nstderr:\n%s", c.name, childStopBound, c.stdout.String(), c.stderr.String())
	}
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
	// principalConfigPath (V6-14A, scenario 8 — role downgrade mid-flight)
	// is passed as `aw serve --principal-config` on every FUTURE startServe
	// call when non-empty — ADR-028's only sanctioned way to change which
	// actor/roles a process trusts, so "downgrading a role" in this suite
	// always means restarting serve under a DIFFERENT trusted principal
	// file, never a live role-mutation call (none exists). Empty keeps the
	// default local-operator/[operator] principal every other stage relies
	// on.
	principalConfigPath string
	// workerLeaseTTL/workerLeaseHeartbeat (V6-14A) override `aw worker
	// --lease-ttl/--lease-heartbeat` on every FUTURE startWorker call when
	// non-zero — several crash scenarios need to observe a durable JOB
	// lease (workerpool's own claim lease, not any command-level write
	// lease) genuinely expire and be reclaimed by a fresh worker before
	// the crashed job's own retry can run, and the production default
	// (30s/10s, config.Defaults) would make every one of those scenarios
	// slow without buying this suite anything real: the mechanism under
	// test is "does reclaim/retry happen at all", not "how many seconds
	// does the default TTL happen to be" — mirrors
	// internal/integration/v5accept's own established precedent
	// (v5AcceptFixture.startPool's 2s/2s) of using small-but-still-real
	// lease timing for a crash-recovery test. Zero keeps `aw worker`'s own
	// production defaults.
	workerLeaseTTL, workerLeaseHeartbeat time.Duration
	// localCommitWriteLeaseTTL (V6-14A, scenario 3 — crash after Git
	// commit) overrides `aw worker --local-commit-write-lease-ttl` on
	// every FUTURE startWorker call when non-zero — the SEPARATE, longer-
	// lived write lease internal/app/releasesetcommit's own worker holds
	// across its real `git commit` call (production default 2 minutes,
	// cmd/aw/worker.go's own localCommitWriteLeaseTTL constant). A crash
	// scenario that needs a fresh worker to reclaim and retry an
	// interrupted local commit would otherwise have to wait out that full
	// 2 minutes for no real test value. Zero keeps the production default.
	localCommitWriteLeaseTTL time.Duration
	// projectionRebuildBatchSize (V6-14A, scenarios 4/5 — projection
	// rebuild crash before/after cutover) overrides `aw worker
	// --projection-rebuild-batch-size` on every FUTURE startWorker call
	// when non-zero — forces a rebuild of even a modest journal through
	// several observable BUILDING/CUTTING_OVER rounds instead of
	// finishing inside a single, externally-unobservable job claim. Zero
	// keeps the production default (500).
	projectionRebuildBatchSize int
	// workerPollInterval (V6-14A, scenario 4) overrides `aw worker
	// --poll-interval` on every FUTURE startWorker call when non-zero —
	// the 100ms this stack otherwise always passes is real wasted-poll
	// budget for a scenario racing to observe a durable job genuinely
	// being claimed and worked. Zero keeps this stack's own 100ms
	// default.
	workerPollInterval time.Duration
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

// nextSuffix mints a fresh, monotonically increasing name suffix for one
// child process start — shared across BOTH serve and worker so every real
// OS-level process this stack ever spawns (across every restart, graceful
// or crashed) gets its own unique processadapter.Supervisor id, never
// reusing one still cooling down. V6-14A needs this to be callable
// independently for serve and worker (startServeOnly/startWorkerOnly below
// no longer always start together the way the original V6-14 start did).
func (s *stack) nextSuffix() string {
	s.generation++
	return fmt.Sprintf("-g%d", s.generation)
}

// startServe brings up a real `aw serve` process against this
// installation's database (creating/migrating it if this is the first
// start), waits for its readiness announcement, then mints a fresh
// s.api bound to its (newly assigned, possibly different) ephemeral port
// and session token. Split out of start (V6-14A) so a fault scenario can
// restart ONLY the API process — e.g. a receipt-commit or attachment-put
// crash, both HTTP/application-layer concerns the worker never touches —
// without tearing down an unrelated, still-healthy worker.
func (s *stack) startServe(t *testing.T) {
	t.Helper()
	suffix := s.nextSuffix()
	serveArgs := append([]string{"serve"}, s.commonPathFlags()...)
	serveArgs = append(serveArgs, "--host", "127.0.0.1", "--port", "0", "--claude-executable", s.bin.fakeClaude)
	if s.principalConfigPath != "" {
		serveArgs = append(serveArgs, "--principal-config", s.principalConfigPath)
	}
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
}

// startWorker brings up a real `aw worker` process against this
// installation's database — see startServe's own doc comment for why this
// is split out (V6-14A).
func (s *stack) startWorker(t *testing.T) {
	t.Helper()
	suffix := s.nextSuffix()
	pollInterval := "100ms"
	if s.workerPollInterval > 0 {
		pollInterval = s.workerPollInterval.String()
	}
	workerArgs := append([]string{"worker"}, s.commonPathFlags()...)
	workerArgs = append(workerArgs,
		"--claude-executable", s.bin.fakeClaude,
		"--poll-interval", pollInterval,
		"--projection-interval", "200ms",
		"--completion-interval", "300ms",
		"--env-allowlist", "AGENTKIT_HELPER_MODE,AGENTKIT_HELPER_OUTCOME",
	)
	if s.workerLeaseTTL > 0 {
		workerArgs = append(workerArgs, "--lease-ttl", s.workerLeaseTTL.String())
	}
	if s.workerLeaseHeartbeat > 0 {
		workerArgs = append(workerArgs, "--lease-heartbeat", s.workerLeaseHeartbeat.String())
	}
	if s.localCommitWriteLeaseTTL > 0 {
		workerArgs = append(workerArgs, "--local-commit-write-lease-ttl", s.localCommitWriteLeaseTTL.String())
	}
	if s.projectionRebuildBatchSize > 0 {
		workerArgs = append(workerArgs, "--projection-rebuild-batch-size", fmt.Sprintf("%d", s.projectionRebuildBatchSize))
	}
	workerEnv := map[string]string{
		"AGENTKIT_HELPER_MODE":    "outcome-success",
		"AGENTKIT_HELPER_OUTCOME": "done",
	}
	s.worker = startChild(t, s.sup, "aw-worker"+suffix, s.bin.aw, s.root, workerArgs, workerEnv)
	s.worker.waitForStdoutLine(t, `"workerId"`, 60*time.Second)
}

// start brings the installation up the way an operator would: `aw serve`
// first (it creates and migrates the database), wait until it reports ready,
// then `aw worker` against the same database.
func (s *stack) start(t *testing.T) {
	t.Helper()
	s.startServe(t)
	s.startWorker(t)
}

// stopServeOnly gracefully stops the serve process alone and reports
// whether it exited gracefully (exit code 0 inside the grace period). A
// no-op reporting true if serve is already stopped/never started.
func (s *stack) stopServeOnly(t *testing.T) (graceful bool) {
	t.Helper()
	if s.serve == nil {
		return true
	}
	graceful = s.serve.stop(t)
	s.serve.dumpOnFailure(t)
	return graceful
}

// stopWorkerOnly gracefully stops the worker process alone — see
// stopServeOnly's own doc comment.
func (s *stack) stopWorkerOnly(t *testing.T) (graceful bool) {
	t.Helper()
	if s.worker == nil {
		return true
	}
	graceful = s.worker.stop(t)
	s.worker.dumpOnFailure(t)
	return graceful
}

// stop shuts both processes down and reports whether each exited gracefully
// (exit code 0 inside the grace period). The worker goes first so no job is
// claimed while the API is already gone.
func (s *stack) stop(t *testing.T) (serveGraceful, workerGraceful bool) {
	t.Helper()
	workerGraceful = s.stopWorkerOnly(t)
	serveGraceful = s.stopServeOnly(t)
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

// restartWorkerOnly (V6-14A) gracefully stops and restarts ONLY the worker
// process, leaving serve (and its already-minted session token) untouched
// — for a fault scenario that needs the worker to observe whatever a
// PRIOR worker generation left on disk, without disturbing an in-flight
// HTTP conversation.
func (s *stack) restartWorkerOnly(t *testing.T) {
	t.Helper()
	if graceful := s.stopWorkerOnly(t); !graceful {
		t.Fatalf("restartWorkerOnly requires a graceful shutdown of the prior worker")
	}
	s.startWorker(t)
}

// restartServeOnly (V6-14A) gracefully stops and restarts ONLY the serve
// process — see restartWorkerOnly's own doc comment. The worker is left
// running throughout: `aw worker` never talks to `aw serve` over HTTP (it
// only touches the shared SQLite database, artifact store and Git
// worktrees directly), so restarting serve alone never disturbs it.
func (s *stack) restartServeOnly(t *testing.T) {
	t.Helper()
	if graceful := s.stopServeOnly(t); !graceful {
		t.Fatalf("restartServeOnly requires a graceful shutdown of the prior serve process")
	}
	s.startServe(t)
}

// hardKillWorker (V6-14A) immediately terminates the CURRENT worker process
// tree with no grace period (childProcess.hardKill's own doc comment) and
// leaves it dead — the caller decides when/whether to bring a fresh one up
// via startWorker, so it can first assert whatever crash-observation it
// needs against the now-stopped-cold state.
func (s *stack) hardKillWorker(t *testing.T) {
	t.Helper()
	s.worker.hardKill(t)
}

// hardKillServe (V6-14A) is hardKillWorker's own twin for the serve
// process.
func (s *stack) hardKillServe(t *testing.T) {
	t.Helper()
	s.serve.hardKill(t)
}
