package runtime

// TerminationReason is ExecutionAttempt's own closed enum explaining why a
// terminal Attempt left RUNNING/QUEUED (ADR-020 §22 "ma trận state-reason";
// docs/architecture/04-go-core-spec.md §4.5). It is a runtime-owned type,
// deliberately distinct from apperror.Code: an admission failure can
// produce both an AppError.Code (why the command/dispatch failed) and a
// TerminationReason (why the Attempt itself ended) and the two are never
// interchangeable.
type TerminationReason string

const (
	// TerminationReasonProcessExitBeforeOutcomeCommit marks an attempt whose
	// worker was killed after its external/child process had already exited
	// but before any outcome was durably committed. The attempt's actual
	// success or failure cannot be inferred from that exit code alone. This
	// predates ADR-020 (it is SPK-04/SPK-09's own crash-classification
	// primitive, internal/app/worker.ClassifyInterruptedAttempt) and is kept
	// as-is: V4-13's recovery coordinator is the task that wires real
	// durable state through that primitive and decides whether it still
	// needs its own value or should emit one of the ADR-020 reasons below
	// instead — not V4-01's schema-only scope to redecide.
	TerminationReasonProcessExitBeforeOutcomeCommit TerminationReason = "PROCESS_EXIT_BEFORE_OUTCOME_COMMIT"

	// The remaining constants are ADR-020's own closed "ma trận state-reason"
	// table, ratified independently of any single caller — the same
	// "pin the whole reference enum up front" treatment DefinitionKind and
	// CommandScope already got. Which reason is valid for which transition
	// is enforced by whichever task first builds a real caller for that
	// transition (V4-05 COMPLETED/EXECUTION_FAILED/DEADLINE_EXCEEDED, V4-06
	// retry exhaustion, V4-08 wait cancellation, V4-12A SCOPE_EXPANSION_REQUIRED,
	// V4-12B RUN_CANCELLED/RUN_CANCELLED_BEFORE_START, V4-13 LEASE_LOST/
	// OWNERSHIP_LOST_MUTATING/RECONCILIATION_REQUIRED) — not this file.

	// TerminationReasonCompleted: RUNNING -> SUCCEEDED.
	TerminationReasonCompleted TerminationReason = "COMPLETED"
	// TerminationReasonExecutionFailed: RUNNING -> FAILED (executor/process failure).
	TerminationReasonExecutionFailed TerminationReason = "EXECUTION_FAILED"
	// TerminationReasonOutcomeRejected: RUNNING -> FAILED (proposed outcome
	// failed deterministic validation, HE-14-M07).
	TerminationReasonOutcomeRejected TerminationReason = "OUTCOME_REJECTED"
	// TerminationReasonScopeViolation: RUNNING -> FAILED (attempt touched
	// scope outside its pinned effective scope).
	TerminationReasonScopeViolation TerminationReason = "SCOPE_VIOLATION"
	// TerminationReasonDeadlineExceeded: RUNNING -> TIMED_OUT.
	TerminationReasonDeadlineExceeded TerminationReason = "DEADLINE_EXCEEDED"
	// TerminationReasonRunCancelled: RUNNING -> CANCELLED.
	TerminationReasonRunCancelled TerminationReason = "RUN_CANCELLED"
	// TerminationReasonRunCancelledBeforeStart: QUEUED -> CANCELLED (run
	// cancelled before this attempt ever started; StartedAt stays nil, spawn
	// count stays zero).
	TerminationReasonRunCancelledBeforeStart TerminationReason = "RUN_CANCELLED_BEFORE_START"
	// TerminationReasonLeaseLost: RUNNING -> LOST (read-only attempt,
	// interrupted, no side effect was ever possible).
	TerminationReasonLeaseLost TerminationReason = "LEASE_LOST"
	// TerminationReasonOwnershipLostMutating: RUNNING -> INDETERMINATE
	// (mutating attempt lost ownership; side effect cannot be ruled out).
	TerminationReasonOwnershipLostMutating TerminationReason = "OWNERSHIP_LOST_MUTATING"
	// TerminationReasonReconciliationRequired: RUNNING -> INDETERMINATE
	// (mutating attempt requires workspace reconciliation before anything
	// downstream can rely on it).
	TerminationReasonReconciliationRequired TerminationReason = "RECONCILIATION_REQUIRED"
	// TerminationReasonScopeExpansionRequired: RUNNING -> BLOCKED (runtime
	// blocker group — ADR-011 scope amendment flow).
	TerminationReasonScopeExpansionRequired TerminationReason = "SCOPE_EXPANSION_REQUIRED"
	// TerminationReasonIsolationEnforcementUnavailable: QUEUED -> BLOCKED
	// (admission blocker group).
	TerminationReasonIsolationEnforcementUnavailable TerminationReason = "ISOLATION_ENFORCEMENT_UNAVAILABLE"
	// TerminationReasonAdapterBuildDrift: QUEUED -> BLOCKED (admission
	// blocker group).
	TerminationReasonAdapterBuildDrift TerminationReason = "ADAPTER_BUILD_DRIFT"
	// TerminationReasonCapabilityRequirementUnsatisfied: QUEUED -> BLOCKED
	// (admission blocker group).
	TerminationReasonCapabilityRequirementUnsatisfied TerminationReason = "CAPABILITY_REQUIREMENT_UNSATISFIED"
	// TerminationReasonWriteCapabilityOrGrantMissing: QUEUED -> BLOCKED
	// (admission blocker group).
	TerminationReasonWriteCapabilityOrGrantMissing TerminationReason = "WRITE_CAPABILITY_OR_GRANT_MISSING"
)

// knownTerminationReasons is ADR-020's closed set plus the pre-existing
// SPK-04/SPK-09 crash-classification reason — the exhaustive membership
// IsValid checks against. It is deliberately not exported: callers compare
// by value or call IsValid, never range over the set themselves.
var knownTerminationReasons = map[TerminationReason]struct{}{
	TerminationReasonProcessExitBeforeOutcomeCommit:   {},
	TerminationReasonCompleted:                        {},
	TerminationReasonExecutionFailed:                  {},
	TerminationReasonOutcomeRejected:                  {},
	TerminationReasonScopeViolation:                   {},
	TerminationReasonDeadlineExceeded:                 {},
	TerminationReasonRunCancelled:                     {},
	TerminationReasonRunCancelledBeforeStart:          {},
	TerminationReasonLeaseLost:                        {},
	TerminationReasonOwnershipLostMutating:            {},
	TerminationReasonReconciliationRequired:           {},
	TerminationReasonScopeExpansionRequired:           {},
	TerminationReasonIsolationEnforcementUnavailable:  {},
	TerminationReasonAdapterBuildDrift:                {},
	TerminationReasonCapabilityRequirementUnsatisfied: {},
	TerminationReasonWriteCapabilityOrGrantMissing:    {},
}

// IsValid reports whether r is a member of the closed TerminationReason set.
// It does not check that r is the *correct* reason for any particular
// transition — that per-transition validity is owned by whichever task
// implements the transition (see the constants' own doc comments above).
func (r TerminationReason) IsValid() bool {
	_, ok := knownTerminationReasons[r]
	return ok
}
