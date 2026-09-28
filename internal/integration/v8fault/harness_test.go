package v8fault

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
)

// spawnAndHardKillSpikeWorker is internal/spikeacceptance's own
// spk04_scenario.go helper of the identical name and behavior, copied
// rather than imported: that function is unexported (package-private) and
// SPK-04's own scenario file is itself full of _test.go-adjacent spike
// orchestration this package has no reason to depend on. The crash
// primitive it drives (cmd/spike-worker + internal/adapters/sqlite's
// exported crashworker.go) is the SAME real, proven mechanism either way —
// only the caller differs.
func spawnAndHardKillSpikeWorker(spikeWorkerPath, databasePath string, ttl time.Duration, mode string) (sqlite.CrashWorkerReady, error) {
	command := exec.Command(spikeWorkerPath)
	command.Env = append(os.Environ(),
		sqlite.CrashWorkerModeEnvironment+"="+mode,
		sqlite.CrashWorkerDBEnvironment+"="+databasePath,
		sqlite.CrashWorkerTTLEnvironment+"="+ttl.String(),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("create spike-worker stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("start spike-worker process: %w", err)
	}

	type scanResult struct {
		output string
		err    error
	}
	readyChannel := make(chan sqlite.CrashWorkerReady, 1)
	scanDone := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var output strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteByte('\n')
			if !strings.HasPrefix(line, sqlite.CrashWorkerReadyPrefix+" ") {
				continue
			}
			ready, parseErr := sqlite.ParseCrashWorkerReady(line)
			if parseErr != nil {
				scanDone <- scanResult{output: output.String(), err: parseErr}
				return
			}
			readyChannel <- ready
			return
		}
		scanDone <- scanResult{output: output.String(), err: scanner.Err()}
	}()

	var ready sqlite.CrashWorkerReady
	select {
	case ready = <-readyChannel:
	case result := <-scanDone:
		_ = command.Process.Kill()
		_ = command.Wait()
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spike-worker exited before READY: scan=%v stdout=%q stderr=%q", result.err, result.output, stderr.String())
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		return sqlite.CrashWorkerReady{}, fmt.Errorf("timeout waiting for spike-worker READY; stderr=%q", stderr.String())
	}

	// Process.Kill is intentionally used instead of context cancellation or a
	// protocol shutdown, so no defer in the worker can release its lease —
	// the closest real analogue to power loss this test process can produce.
	if err := command.Process.Kill(); err != nil {
		return ready, fmt.Errorf("hard-kill spike-worker: %w", err)
	}
	if err := command.Wait(); err == nil {
		return ready, fmt.Errorf("hard-killed spike-worker exited successfully, want forced termination")
	}
	return ready, nil
}

// realWorker is one real `aw worker` child process, started with no
// provider executables registered (deliberately: every crash-matrix fixture
// pins a fake AgentProfile/Command that never resolves to a real published
// definition — SPK-04's own established convention, crashworker_fixtures.go
// — so this test only ever needs the worker's own job-lease reaper to
// reclaim the crashed job; it is never meant to reach real dispatch).
type realWorker struct {
	cmd    *exec.Cmd
	stdout *lockedBuffer
	stderr *lockedBuffer
}

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

// startRealWorker brings up a real `aw worker` process against databasePath
// with a fast reaper pass — the mechanism SPK-04's own
// waitForExpiredJobRecovery manually drives via RecoverExpiredJobs, here
// left to the production binary's own real, continuously-polling loop.
func startRealWorker(t *testing.T, awBinary, databasePath, artifactRoot, workspaceRoot, workerID string) *realWorker {
	t.Helper()
	args := []string{
		"worker",
		"--db", databasePath, "--artifact-root", artifactRoot, "--workspace-root", workspaceRoot,
		"--worker-id", workerID,
		"--poll-interval", "100ms", "--reaper-interval", "150ms",
		"--projection-interval", "500ms", "--completion-interval", "500ms",
		"--sweep-interval", "5s",
		// workerpool.Config.RecoveryInterval (its own periodic
		// RecoverExpiredJobs pass — the generic, kind-agnostic scan that
		// actually reclaims a crashed worker's stale lease) has no CLI flag
		// of its own; cmd/aw/worker.go's own composition leaves it zero, so
		// workerpool.Config.Validate defaults it to LeaseTTL itself
		// (production default ~30s). --reaper-interval is a DIFFERENT
		// reaper entirely (runtime.RecoveryReaperJobKind, orphaned
		// attempts/cancellation intents) and never touches this. A short
		// --lease-ttl is therefore the only way to make the real worker's
		// own periodic lease-recovery scan run often enough for a test
		// budget — found live the first time boundaries 3/4/5 each timed
		// out waiting on a 30s-interval scan with only a 15s budget.
		// --lease-heartbeat must stay below --lease-ttl (workerpool.Config.
		// Validate rejects HeartbeatEvery >= LeaseTTL) — the production
		// default heartbeat interval is well above 1s, so it must be
		// lowered here too or the worker fails to start outright.
		"--lease-ttl", "1s", "--lease-heartbeat", "300ms",
	}
	cmd := exec.Command(awBinary, args...)
	w := &realWorker{stdout: &lockedBuffer{}, stderr: &lockedBuffer{}}
	cmd.Stdout = w.stdout
	cmd.Stderr = w.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start real aw worker: %v", err)
	}
	w.cmd = cmd
	t.Cleanup(func() {
		w.stop(t)
		if t.Failed() {
			t.Logf("real aw worker stdout:\n%s", w.stdout.String())
			t.Logf("real aw worker stderr:\n%s", w.stderr.String())
		}
	})
	return w
}

// stop sends the real worker a graceful termination signal and waits briefly
// — a no-op if it already exited. Never asserts a clean exit: this worker is
// deliberately fed a job it can never really finish (an unresolvable
// AgentProfile pin), so persistent retry/error logging right up to shutdown
// is expected, not a failure of this harness.
func (w *realWorker) stop(t *testing.T) {
	t.Helper()
	if w.cmd == nil || w.cmd.ProcessState != nil {
		return
	}
	_ = w.cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _ = w.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

// waitFor polls fn every interval until it returns true, failing the test
// with what if it never does within the deadline — mirrors
// internal/integration/v6accept's own waitFor helper exactly.
func waitFor(t *testing.T, what string, within, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}
