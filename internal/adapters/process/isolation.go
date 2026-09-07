package process

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// ErrIsolationEnforcementUnavailable is IsolationChecker's own fixed
// answer for policy.IsolationTierEnforcedIsolated: this codebase has no
// real OS-level filesystem/network sandbox for a spawned child process
// today, so ENFORCED_ISOLATED can never be honestly satisfied (ADR-013,
// ADR-023). A caller MUST treat this as fail-closed and MUST NOT call
// Supervisor.Run for the rejected attempt — ADR-023's own "không bao giờ
// auto-downgrade" forbids silently substituting OPERATOR_TRUSTED_LOCAL
// instead.
var ErrIsolationEnforcementUnavailable = errors.New("process: real OS-level isolation enforcement is not available for ENFORCED_ISOLATED")

// IsolationChecker is the production ports.IsolationEnforcementChecker
// (V5-05's own scope, ADR-013/ADR-023). It is a static, I/O-free fact
// about this Supervisor implementation, confirmed with the user before
// writing this file: OPERATOR_TRUSTED_LOCAL is the only tier this
// environment can run today, at the trust level ADR-013 itself documents
// (operator-granted, not least-privilege — internal/app/scopeguard's own
// ValidateDiffs, already built and wired into internal/app/worker's
// finalizer, is this codebase's post-hoc diff check for that tier, not
// this checker's concern). A Windows Job Object or a Unix process group
// (see processtree.go) can manage and kill a process tree, but neither
// blocks filesystem or network access on its own, so neither is sufficient
// to claim ENFORCED_ISOLATED.
type IsolationChecker struct{}

// NewIsolationChecker returns the production isolation-enforcement
// checker. It holds no state and does no I/O.
func NewIsolationChecker() IsolationChecker { return IsolationChecker{} }

var _ ports.IsolationEnforcementChecker = IsolationChecker{}

func (IsolationChecker) VerifyEnforceable(_ context.Context, tier policy.IsolationTier) error {
	switch tier {
	case policy.IsolationTierOperatorTrustedLocal:
		return nil
	case policy.IsolationTierEnforcedIsolated:
		return ErrIsolationEnforcementUnavailable
	default:
		return fmt.Errorf("process: unsupported isolation tier %q", tier)
	}
}
