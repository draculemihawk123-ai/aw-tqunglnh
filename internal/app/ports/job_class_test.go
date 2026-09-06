package ports

import "testing"

// TestClassifyJobKind_ControlAllowList proves the exact three real Kind
// constants confirmed with the user classify CONTROL — see this package's
// own controlJobKinds doc comment for why each one is (or, for
// RECOVERY_REAPER, is deliberately not) in this set.
func TestClassifyJobKind_ControlAllowList(t *testing.T) {
	for _, kind := range []string{"CANCEL_RUN_COORDINATOR", "WORKSPACE_RECONCILIATION", "WORKSPACE_SET_RELEASE"} {
		if got := ClassifyJobKind(kind); got != JobClassControl {
			t.Fatalf("ClassifyJobKind(%q) = %q, want %q", kind, got, JobClassControl)
		}
	}
}

// TestClassifyJobKind_EverythingElseDefaultsRunWork proves the fail-closed
// default: RECOVERY_REAPER (deliberately excluded — no real durable job
// exists for it today), the doc's own typo'd WORKSPACE_RECONCILE, a
// completely unknown Kind, and every ordinary real Kind this codebase
// already produces all classify RUN_WORK.
func TestClassifyJobKind_EverythingElseDefaultsRunWork(t *testing.T) {
	kinds := []string{
		"RECOVERY_REAPER",
		"WORKSPACE_RECONCILE", // the design doc's own naming drift, not the real constant
		"SOME_UNKNOWN_FUTURE_KIND",
		"REPOSITORY_PROBE",
		"WORKSPACE_PROVISION",
		"SCOPE_EXPANSION_RECONCILE",
		"ADVANCE_RUN",
		"SCHEDULE_NODE_RUN",
		"EXECUTE_NODE",
		"REQUEST_SCOPE_EXPANSION",
		"WAIT_TIMER",
		"APPROVAL_TIMER",
		"BASELINE_EVIDENCE",
		"",
	}
	for _, kind := range kinds {
		if got := ClassifyJobKind(kind); got != JobClassRunWork {
			t.Fatalf("ClassifyJobKind(%q) = %q, want %q", kind, got, JobClassRunWork)
		}
	}
}
