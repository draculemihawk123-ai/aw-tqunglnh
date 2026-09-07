// Package versionprobe is the shared "capability/version probe" shape
// V5-06 builds for the Claude adapter and V5-07 reuses unchanged for
// Codex (docs/design/07-v5-execution-evidence.md: V5-07 "cùng contract/
// semantics như Claude" — the roadmap itself requires this, not a
// speculative abstraction). Before this package existed,
// ports.AgentCapabilities.TestedCLIVersion was a hardcoded Go constant,
// never verified against the executable actually configured
// (cmd/agentkit/adapter.go's own doc comment named this exact gap as
// deferred to V5-06/07). Probe closes it: a real, separate, minimal
// invocation of the configured executable (e.g. "--version"), entirely
// independent of the adapter's own task-execution wire protocol.
package versionprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrEmptyOutput is returned when the probed executable exited zero but
// produced no usable version string — a silent, empty success is not
// evidence of a real, working CLI.
var ErrEmptyOutput = errors.New("versionprobe: executable produced no version output")

// Probe spawns executable with argv via supervisor and returns the
// trimmed stdout as the observed version — fail-closed: a spawn error, a
// non-zero exit, a timeout/cancellation, or empty output are all reported
// as errors, never papered over with a stale or guessed value.
//
// argv is intentionally separate from whatever argv the adapter's own
// task-execution protocol builds (e.g. Claude's "-p --output-format
// stream-json ...") — a version probe is a distinct, minimal invocation
// mode most real CLIs support even when no task is running.
//
// id must be unique among Supervisor.Run calls that could be in flight
// concurrently on the same Supervisor — this package holds no adapter
// identity of its own to derive one from, so the caller mints it. env is
// passed through to the spawned process as-is (nil in production; a test
// fixture uses it to route the probe through whatever env-gated re-exec
// contract the caller's own test infrastructure needs).
func Probe(ctx context.Context, supervisor ports.ProcessSupervisor, id ports.ProcessID, executable string, argv []string, env map[string]string, timeout time.Duration) (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("versionprobe: get working directory: %w", err)
	}

	var stdout strings.Builder
	result, err := supervisor.Run(ctx, ports.ProcessSpec{
		ID:               id,
		Executable:       executable,
		Argv:             argv,
		Environment:      env,
		WorkingDirectory: workingDirectory,
		Timeout:          timeout,
	}, &stdout, io.Discard)
	if err != nil {
		return "", fmt.Errorf("versionprobe: run %q: %w", executable, err)
	}
	if result.TimedOut {
		return "", fmt.Errorf("versionprobe: %q timed out after %s", executable, timeout)
	}
	if result.Cancelled {
		return "", fmt.Errorf("versionprobe: %q was cancelled", executable)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("versionprobe: %q exited %d", executable, result.ExitCode)
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		return "", ErrEmptyOutput
	}
	return version, nil
}
