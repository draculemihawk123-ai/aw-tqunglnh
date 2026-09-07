package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// IsolationEnforcementChecker is ADR-023's own required admission
// dependency: given the policy.IsolationTier a node's PERMISSION-category
// Policy pin resolved (internal/app/runtime.ScheduleExecutableNodeRun's own
// resolveProfile step), it reports whether THIS environment can actually
// enforce that tier right now — a simple pass/fail check because ADR-023's
// own contract is binary: either real OS-level enforcement exists for
// ENFORCED_ISOLATED, or admission must fail closed with a typed error
// (mapped by the caller to errorcode.CodeIsolationEnforcementUnavailable /
// runtime.TerminationReasonIsolationEnforcementUnavailable /
// work.BlockerIsolationEnforcementUnavailable — all three already exist
// from earlier phases) BEFORE ProcessSupervisor.Run is ever called. A
// non-nil error here must never be papered over by silently proceeding
// under OPERATOR_TRUSTED_LOCAL instead — ADR-023's own "không bao giờ auto-
// downgrade".
//
// VerifyEnforceable performs no I/O in this codebase's only implementation
// today (Alpha has no real OS-level filesystem/network sandbox), but takes
// a ctx for the same reason RuntimeExecutionConfigProvider.Resolve does: a
// future implementation that actually probes OS capability (namespaces,
// seccomp, a container runtime) may need one.
//
// Implementations: internal/app/ports/fake.IsolationEnforcementChecker
// (tests); internal/adapters/process.IsolationChecker (production, V5-05's
// own scope) — the only implementation this codebase currently has, and it
// always rejects ENFORCED_ISOLATED honestly rather than claiming
// enforcement that does not exist.
//
// V5-05 builds this checker and proves its own contract (process spawn
// count stays 0 when it rejects) directly; wiring it into the real Attempt
// admission/BLOCKED state machine is V5-08's own scope ("isolation profile
// admission theo ADR-023").
type IsolationEnforcementChecker interface {
	VerifyEnforceable(ctx context.Context, tier policy.IsolationTier) error
}
