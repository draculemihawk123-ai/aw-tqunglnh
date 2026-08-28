package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
)

type Supervisor struct {
	mu     sync.Mutex
	active map[ports.ProcessID]context.CancelFunc
}

func NewSupervisor() *Supervisor {
	return &Supervisor{active: make(map[ports.ProcessID]context.CancelFunc)}
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
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	if err := s.reserve(spec.ID, cancel); err != nil {
		cancel()
		return result, err
	}
	defer func() {
		cancel()
		s.release(spec.ID)
	}()

	environment, err := buildEnvironment(spec.InheritedEnvironment, spec.Environment)
	if err != nil {
		return result, err
	}

	// Never replace this with a shell invocation. Go passes each argv element
	// directly to the target executable on both Windows and Linux.
	command := exec.CommandContext(runCtx, spec.Executable, spec.Argv...)
	command.Dir = spec.WorkingDirectory
	command.Env = environment
	command.Stdin = bytes.NewReader(spec.Stdin)
	command.Stdout = stdout
	command.Stderr = stderr

	result.StartedAt = time.Now().UTC()
	if err := command.Start(); err != nil {
		result.FinishedAt = time.Now().UTC()
		if runCtx.Err() != nil {
			setCancellationResult(&result, runCtx.Err())
		}
		return result, fmt.Errorf("start executable %q: %w", spec.Executable, err)
	}

	waitErr := command.Wait()
	result.FinishedAt = time.Now().UTC()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}

	if runCtx.Err() != nil {
		setCancellationResult(&result, runCtx.Err())
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

func (s *Supervisor) Cancel(_ context.Context, id ports.ProcessID) error {
	s.mu.Lock()
	cancel, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotRunning, id)
	}
	cancel()
	return nil
}

func (s *Supervisor) reserve(id ports.ProcessID, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[id]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, id)
	}
	s.active[id] = cancel
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
