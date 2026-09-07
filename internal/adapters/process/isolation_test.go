package process

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func TestIsolationChecker_EnforcedIsolated_AlwaysRejected(t *testing.T) {
	t.Parallel()
	checker := NewIsolationChecker()
	err := checker.VerifyEnforceable(context.Background(), policy.IsolationTierEnforcedIsolated)
	if !errors.Is(err, ErrIsolationEnforcementUnavailable) {
		t.Fatalf("VerifyEnforceable(ENFORCED_ISOLATED) = %v, want ErrIsolationEnforcementUnavailable", err)
	}
}

func TestIsolationChecker_OperatorTrustedLocal_Enforceable(t *testing.T) {
	t.Parallel()
	checker := NewIsolationChecker()
	if err := checker.VerifyEnforceable(context.Background(), policy.IsolationTierOperatorTrustedLocal); err != nil {
		t.Fatalf("VerifyEnforceable(OPERATOR_TRUSTED_LOCAL) = %v, want nil", err)
	}
}

func TestIsolationChecker_UnknownTier_Rejected(t *testing.T) {
	t.Parallel()
	checker := NewIsolationChecker()
	err := checker.VerifyEnforceable(context.Background(), policy.IsolationTier("SOMETHING_ELSE"))
	if err == nil {
		t.Fatal("expected an error for an unknown isolation tier")
	}
	if errors.Is(err, ErrIsolationEnforcementUnavailable) {
		t.Fatal("an unknown tier must not be conflated with the known ENFORCED_ISOLATED rejection")
	}
}

// spySupervisor counts Run calls without ever actually spawning anything —
// ADR-023's own contract test bar: "Contract test MUST assert process spawn
// count bằng 0" when a pinned tier cannot be enforced.
type spySupervisor struct {
	calls int
}

func (s *spySupervisor) Run(context.Context, ports.ProcessSpec, io.Writer, io.Writer) (ports.ProcessResult, error) {
	s.calls++
	return ports.ProcessResult{}, nil
}

func (s *spySupervisor) Cancel(context.Context, ports.ProcessID) error { return nil }

var _ ports.ProcessSupervisor = (*spySupervisor)(nil)

// TestIsolationChecker_RejectionMeansNoSpawn is ADR-023's own contract test
// ("Contract test MUST assert process spawn count bằng 0"): a caller that
// checks VerifyEnforceable before ever calling ProcessSupervisor.Run must
// see zero spawns when the pinned tier cannot be enforced, and must never
// substitute a different tier to make the spawn happen anyway (ADR-023's
// own "không bao giờ auto-downgrade" — this test proves the checker gives
// a caller no way to launder that substitution through it).
func TestIsolationChecker_RejectionMeansNoSpawn(t *testing.T) {
	t.Parallel()
	checker := NewIsolationChecker()
	supervisor := &spySupervisor{}

	runIfEnforceable := func(tier policy.IsolationTier) error {
		if err := checker.VerifyEnforceable(context.Background(), tier); err != nil {
			return err
		}
		_, err := supervisor.Run(context.Background(), ports.ProcessSpec{}, nil, nil)
		return err
	}

	if err := runIfEnforceable(policy.IsolationTierEnforcedIsolated); !errors.Is(err, ErrIsolationEnforcementUnavailable) {
		t.Fatalf("runIfEnforceable(ENFORCED_ISOLATED) error = %v, want ErrIsolationEnforcementUnavailable", err)
	}
	if supervisor.calls != 0 {
		t.Fatalf("supervisor.calls = %d, want 0 — ENFORCED_ISOLATED must never reach ProcessSupervisor.Run", supervisor.calls)
	}

	if err := runIfEnforceable(policy.IsolationTierOperatorTrustedLocal); err != nil {
		t.Fatalf("runIfEnforceable(OPERATOR_TRUSTED_LOCAL) error = %v, want nil", err)
	}
	if supervisor.calls != 1 {
		t.Fatalf("supervisor.calls = %d, want 1 after an enforceable tier", supervisor.calls)
	}
}
