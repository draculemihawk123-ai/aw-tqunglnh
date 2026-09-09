package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var (
	ErrAlreadyRunning = errors.New("process id is already running")
	ErrNotRunning     = errors.New("process id is not running")
	// errExplicitCancel marks a termination as caller-requested (Cancel)
	// rather than deadline-caused, for setCancellationResult's own
	// TimedOut/Cancelled classification.
	errExplicitCancel = errors.New("process: cancel requested")
)

// defaultGracePeriod backstops any caller that leaves
// ports.ProcessSpec.GracePeriod at zero: how long a graceful signal gets to
// work before Supervisor force-kills the whole process tree.
const defaultGracePeriod = 5 * time.Second

type Supervisor struct {
	mu     sync.Mutex
	active map[ports.ProcessID]*activeProcess
}

// activeProcess is what Run registers under spec.ID for the duration of one
// spawn. cancelRequested is closed exactly once by Cancel to signal
// "terminate the tree" — independent of, and racing against, the run's own
// timeout deadline in Run's own select.
type activeProcess struct {
	cancelRequested chan struct{}
	cancelOnce      sync.Once
}

func newActiveProcess() *activeProcess {
	return &activeProcess{cancelRequested: make(chan struct{})}
}

func (p *activeProcess) requestCancel() {
	p.cancelOnce.Do(func() { close(p.cancelRequested) })
}

func NewSupervisor() *Supervisor {
	return &Supervisor{active: make(map[ports.ProcessID]*activeProcess)}
}

func (s *Supervisor) Run(
	ctx context.Context,
	spec ports.ProcessSpec,
	stdout io.Writer,
	stderr io.Writer,
) (ports.ProcessResult, error) {
	result := ports.ProcessResult{ID: spec.ID, ExitCode: -1}
	if err := validateSpec(spec); err != nil {
		return result, err
	}

	outputLimit := spec.OutputLimitBytes
	if outputLimit <= 0 {
		outputLimit = defaultOutputLimitBytes
	}
	stdoutBound := newBoundedWriter(stdout, outputLimit)
	stderrBound := newBoundedWriter(stderr, outputLimit)

	grace := spec.GracePeriod
	if grace <= 0 {
		grace = defaultGracePeriod
	}

	environment, err := buildEnvironment(spec.InheritedEnvironment, spec.Environment)
	if err != nil {
		return result, err
	}

	deadline, cancelDeadline := context.WithTimeout(ctx, spec.Timeout)
	defer cancelDeadline()

	// Never replace this with a shell invocation. Go passes each argv element
	// directly to the target executable on both Windows and Linux.
	command := exec.Command(spec.Executable, spec.Argv...)
	command.Dir = spec.WorkingDirectory
	command.Env = environment
	command.Stdin = bytes.NewReader(spec.Stdin)
	command.Stdout = stdoutBound
	command.Stderr = stderrBound
	tree := newProcessTree()
	tree.configure(command)
	defer tree.close()

	proc := newActiveProcess()
	if err := s.reserve(spec.ID, proc); err != nil {
		return result, err
	}
	defer s.release(spec.ID)

	result.StartedAt = time.Now().UTC()
	if err := command.Start(); err != nil {
		result.FinishedAt = time.Now().UTC()
		if deadline.Err() != nil {
			setCancellationResult(&result, deadline.Err())
		}
		return result, fmt.Errorf("start executable %q: %w", spec.Executable, err)
	}
	if bindErr := tree.bind(command); bindErr != nil {
		// Best-effort: this run degrades to direct single-process
		// signaling (see processTree's own doc comment) instead of
		// failing an already-started attempt over a tree-management
		// hiccup unrelated to the work itself.
		_ = bindErr
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	var waitErr error
	var terminationCause error
	select {
	case waitErr = <-waitDone:
		// Finished on its own — nothing to escalate.
	case <-deadline.Done():
		terminationCause = deadline.Err()
	case <-proc.cancelRequested:
		terminationCause = errExplicitCancel
	}

	if terminationCause != nil {
		_ = tree.signalGraceful(command)
		select {
		case waitErr = <-waitDone:
			// Exited on its own within the grace period.
		case <-time.After(grace):
			_ = tree.kill(command)
			waitErr = <-waitDone
		}
	}

	result.FinishedAt = time.Now().UTC()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	result.OutputTruncated = stdoutBound.truncated || stderrBound.truncated
	// Confirmed on EVERY exit path, not only cancellation/timeout below —
	// V5-08B's own normal-exit quiescence postcondition (see
	// ports.ProcessResult.TreeQuiesced's own doc comment).
	result.TreeQuiesced = confirmTreeQuiesced(tree, command)

	if terminationCause != nil {
		setCancellationResult(&result, terminationCause)
		return result, nil
	}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return result, nil
	}
	return result, fmt.Errorf("wait for executable %q: %w", spec.Executable, waitErr)
}

// quiescencePollInterval/quiescencePollBound bound confirmTreeQuiesced's own
// retry loop: the direct child is already reaped by Wait() by the time this
// runs, so a well-behaved tree with no orphaned descendants is quiesced
// immediately (the first poll succeeds); these only matter for the brief
// OS-level lag between a kill syscall returning and the kernel actually
// tearing down every descendant, or for a genuinely-still-alive orphan
// (which polling can never fix — the bound exists precisely so that case
// reports false promptly instead of hanging).
const (
	quiescencePollInterval = 20 * time.Millisecond
	quiescencePollBound    = 2 * time.Second
)

// confirmTreeQuiesced polls tree.quiesced until it reports true or
// quiescencePollBound elapses. Called on every exit path in Run (normal
// completion included), never only after signalGraceful/kill — a parent
// that exits cleanly while a descendant keeps running/writing must report
// false here exactly like an orphan surviving a forced kill would.
func confirmTreeQuiesced(tree processTree, cmd *exec.Cmd) bool {
	deadline := time.Now().Add(quiescencePollBound)
	for {
		if quiesced, err := tree.quiesced(cmd); err == nil && quiesced {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(quiescencePollInterval)
	}
}

func (s *Supervisor) Cancel(_ context.Context, id ports.ProcessID) error {
	s.mu.Lock()
	proc, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotRunning, id)
	}
	proc.requestCancel()
	return nil
}

func (s *Supervisor) reserve(id ports.ProcessID, proc *activeProcess) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[id]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, id)
	}
	s.active[id] = proc
	return nil
}

func (s *Supervisor) release(id ports.ProcessID) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

func validateSpec(spec ports.ProcessSpec) error {
	if spec.ID == "" {
		return errors.New("process id is required")
	}
	if strings.TrimSpace(spec.Executable) == "" {
		return errors.New("process executable is required")
	}
	if strings.TrimSpace(spec.WorkingDirectory) == "" {
		return errors.New("process working directory is required")
	}
	if !filepath.IsAbs(spec.WorkingDirectory) {
		return fmt.Errorf("process working directory %q must be an absolute path", spec.WorkingDirectory)
	}
	info, err := os.Stat(spec.WorkingDirectory)
	if err != nil {
		return fmt.Errorf("process working directory %q: %w", spec.WorkingDirectory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("process working directory %q is not a directory", spec.WorkingDirectory)
	}
	if spec.Timeout <= 0 {
		return errors.New("process timeout must be greater than zero")
	}
	for _, argument := range spec.Argv {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("process argv cannot contain NUL")
		}
	}
	return nil
}

func buildEnvironment(inherited []string, explicit map[string]string) ([]string, error) {
	type entry struct {
		key   string
		value string
	}
	values := make(map[string]entry, len(inherited)+len(explicit))

	put := func(key, value string) error {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("invalid environment entry %q", key)
		}
		normalized := key
		if runtime.GOOS == "windows" {
			normalized = strings.ToUpper(key)
		}
		values[normalized] = entry{key: key, value: value}
		return nil
	}

	for _, key := range inherited {
		value, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		if err := put(key, value); err != nil {
			return nil, err
		}
	}
	for key, value := range explicit {
		if err := put(key, value); err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		item := values[key]
		result = append(result, item.key+"="+item.value)
	}
	return result, nil
}

func setCancellationResult(result *ports.ProcessResult, cause error) {
	if errors.Is(cause, context.DeadlineExceeded) {
		result.TimedOut = true
		return
	}
	result.Cancelled = true
}

var _ ports.ProcessSupervisor = (*Supervisor)(nil)
