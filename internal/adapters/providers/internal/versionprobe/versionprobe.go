// Package versionprobe is the shared "capability/version probe" shape
// V5-06 builds for the Claude adapter and V5-07 reuses unchanged for
// Codex (docs/design/07-v5-execution-evidence.md: V5-07 "cùng contract/
// semantics như Claude" — the roadmap itself requires this, not a
// speculative abstraction). Before this package existed,
// ports.AgentCapabilities.TestedCLIVersion was a hardcoded Go constant,
// never verified against the executable actually configured
// (cmd/aw/adapter.go's own doc comment named this exact gap as
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
// Every failure is a *ports.CapabilityProbeError (V9-05): a typed Reason
// (and the exit code of a non-zero exit) a caller reads with errors.As
// instead of matching text. Its Err keeps the same message this function
// has always returned, and ErrEmptyOutput is still reachable with
// errors.Is.
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
//
// inherited (V9-05, gap G5) is the explicit allow-list of parent-environment
// variable NAMES the probe process inherits — ports.ProcessSpec's
// InheritedEnvironment, so values are read by the supervisor at spawn time
// and never pass through here. nil keeps the probe's environment empty
// apart from env, which is what every caller did before V9-05. A caller that
// wants the probe to run in the environment an agent would get passes the
// worker's `--env-allowlist`; that is how `aw doctor` finds out that a
// provider executable cannot run in it.
func Probe(ctx context.Context, supervisor ports.ProcessSupervisor, id ports.ProcessID, executable string, argv []string, env map[string]string, inherited []string, timeout time.Duration) (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("versionprobe: get working directory: %w", err)
	}

	var stdout strings.Builder
	result, err := supervisor.Run(ctx, ports.ProcessSpec{
		ID:                   id,
		Executable:           executable,
		Argv:                 argv,
		Environment:          env,
		InheritedEnvironment: inherited,
		WorkingDirectory:     workingDirectory,
		Timeout:              timeout,
	}, &stdout, io.Discard)
	if err != nil {
		return "", &ports.CapabilityProbeError{
			Reason: ports.CapabilityProbeStartFailed,
			Err:    fmt.Errorf("versionprobe: run %q: %w", executable, err),
		}
	}
	if result.TimedOut {
		return "", &ports.CapabilityProbeError{
			Reason: ports.CapabilityProbeTimedOut,
			Err:    fmt.Errorf("versionprobe: %q timed out after %s", executable, timeout),
		}
	}
	if result.Cancelled {
		return "", &ports.CapabilityProbeError{
			Reason: ports.CapabilityProbeCancelled,
			Err:    fmt.Errorf("versionprobe: %q was cancelled", executable),
		}
	}
	if result.ExitCode != 0 {
		return "", &ports.CapabilityProbeError{
			Reason: ports.CapabilityProbeNonZeroExit, ExitCode: result.ExitCode,
			Err: fmt.Errorf("versionprobe: %q exited %d", executable, result.ExitCode),
		}
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		return "", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeEmptyOutput, Err: ErrEmptyOutput}
	}
	return version, nil
}
