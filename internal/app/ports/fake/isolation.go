package fake

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// IsolationEnforcementChecker is a fixed, deterministic
// ports.IsolationEnforcementChecker for tests: zero value reports every
// tier enforceable, sufficient for any test that does not itself care
// about isolation admission — set Err to exercise the "enforcement
// unavailable" fail-closed path regardless of which tier was asked about.
type IsolationEnforcementChecker struct {
	Err error
}

var _ ports.IsolationEnforcementChecker = IsolationEnforcementChecker{}

func (c IsolationEnforcementChecker) VerifyEnforceable(context.Context, policy.IsolationTier) error {
	return c.Err
}
