package versionprobe

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V9-05 (gap G5): the probe runs in an environment its caller chooses, and its
// failures are typed.

const probeNeededVariable = "AW_V905_PROBE_NEEDS_THIS"

// TestProbe_InheritedEnvironment_DecidesWhetherTheProbeTargetCanRun: the probe
// target exits non-zero when a variable it needs is missing. With nothing
// inherited the probe fails with a typed NON_ZERO_EXIT carrying the exit code;
// naming the variable in the inherited allow-list makes the same executable
// report its version. The value reaches the child only through the
// supervisor — it is never an argument of Probe.
func TestProbe_InheritedEnvironment_DecidesWhetherTheProbeTargetCanRun(t *testing.T) {
	t.Setenv(probeNeededVariable, "a value the probe never sees as an argument")
	argv := []string{"-test.run=TestProbeHelper", "--", "require-env", probeNeededVariable}

	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-env-missing", os.Args[0], argv, nil, nil, 30*time.Second)
	var probeErr *ports.CapabilityProbeError
	if !errors.As(err, &probeErr) {
		t.Fatalf("err = %v (%T), want a *ports.CapabilityProbeError", err, err)
	}
	if probeErr.Reason != ports.CapabilityProbeNonZeroExit || probeErr.ExitCode != 7 {
		t.Fatalf("probeErr = %+v, want NON_ZERO_EXIT with exit code 7", probeErr)
	}

	version, err := Probe(context.Background(), process.NewSupervisor(), "probe-env-inherited", os.Args[0], argv, nil, []string{probeNeededVariable}, 30*time.Second)
	if err != nil {
		t.Fatalf("Probe with the variable inherited: %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", version)
	}
}

// TestProbe_InheritedNameThatIsNotSetInTheParentIsSimplyAbsent: an allow-listed
// name the worker does not have set is not an error of the probe itself — the
// target decides whether it can live without it.
func TestProbe_InheritedNameThatIsNotSetInTheParentIsSimplyAbsent(t *testing.T) {
	argv := []string{"-test.run=TestProbeHelper", "--", "require-env", "AW_V905_NEVER_SET_ANYWHERE"}
	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-env-unset", os.Args[0], argv, nil, []string{"AW_V905_NEVER_SET_ANYWHERE"}, 30*time.Second)
	var probeErr *ports.CapabilityProbeError
	if !errors.As(err, &probeErr) || probeErr.Reason != ports.CapabilityProbeNonZeroExit {
		t.Fatalf("err = %v, want a typed NON_ZERO_EXIT (the target needs a variable the parent does not have)", err)
	}
}

func TestProbe_FailuresAreTyped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		run     func() error
		reason  ports.CapabilityProbeReason
		exit    int
		message string
	}{
		{
			name: "non-zero exit",
			run: func() error {
				_, err := Probe(context.Background(), process.NewSupervisor(), "typed-exit", os.Args[0], []string{"-test.run=TestProbeHelper", "--", "fail"}, nil, nil, 30*time.Second)
				return err
			},
			reason: ports.CapabilityProbeNonZeroExit, exit: 1, message: "exited 1",
		},
		{
			name: "timeout",
			run: func() error {
				_, err := Probe(context.Background(), process.NewSupervisor(), "typed-timeout", os.Args[0], []string{"-test.run=TestProbeHelper", "--", "hang"}, nil, nil, 1500*time.Millisecond)
				return err
			},
			reason: ports.CapabilityProbeTimedOut, message: "timed out",
		},
		{
			name: "executable cannot be started",
			run: func() error {
				_, err := Probe(context.Background(), process.NewSupervisor(), "typed-missing", "definitely-not-a-real-executable-xyz", nil, nil, nil, 30*time.Second)
				return err
			},
			reason: ports.CapabilityProbeStartFailed, message: "definitely-not-a-real-executable-xyz",
		},
		{
			name: "empty output",
			run: func() error {
				_, err := Probe(context.Background(), process.NewSupervisor(), "typed-empty", os.Args[0], []string{"-test.run=TestProbeHelper", "--", "print", ""}, nil, nil, 30*time.Second)
				return err
			},
			reason: ports.CapabilityProbeEmptyOutput, message: ErrEmptyOutput.Error(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run()
			var probeErr *ports.CapabilityProbeError
			if !errors.As(err, &probeErr) {
				t.Fatalf("err = %v (%T), want a *ports.CapabilityProbeError", err, err)
			}
			if probeErr.Reason != test.reason || probeErr.ExitCode != test.exit {
				t.Fatalf("probeErr = %+v, want reason %s and exit code %d", probeErr, test.reason, test.exit)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("err = %q, want the message to keep saying %q", err.Error(), test.message)
			}
		})
	}
}

// TestProbe_EmptyOutputStaysReachableWithErrorsIs: the typed wrapper must not
// hide the sentinel callers already match.
func TestProbe_EmptyOutputStaysReachableWithErrorsIs(t *testing.T) {
	t.Parallel()
	_, err := Probe(context.Background(), process.NewSupervisor(), "typed-empty-is", os.Args[0], []string{"-test.run=TestProbeHelper", "--", "print", ""}, nil, nil, 30*time.Second)
	if !errors.Is(err, ErrEmptyOutput) {
		t.Fatalf("err = %v, want errors.Is(err, ErrEmptyOutput)", err)
	}
}
