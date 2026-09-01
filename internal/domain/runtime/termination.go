package runtime

// TerminationReason is a small, closed enum explaining machine-verifiably
// why an ExecutionAttempt left RUNNING during crash recovery. It is not
// exhaustive of every conceivable reason — add a value only when a real
// caller needs it, never speculatively.
type TerminationReason string

const (
	// TerminationReasonProcessExitBeforeOutcomeCommit marks an attempt whose
	// worker was killed after its external/child process had already exited
	// but before any outcome was durably committed. The attempt's actual
	// success or failure cannot be inferred from that exit code alone.
	TerminationReasonProcessExitBeforeOutcomeCommit TerminationReason = "PROCESS_EXIT_BEFORE_OUTCOME_COMMIT"
)
