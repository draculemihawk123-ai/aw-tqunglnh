package ports

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// TestClassifyJobKind_ControlAllowList proves the exact five real Kind
// constants confirmed with the user classify CONTROL — see this package's
// own controlJobKinds doc comment for why each one is in this set.
func TestClassifyJobKind_ControlAllowList(t *testing.T) {
	for _, kind := range []string{"CANCEL_RUN_COORDINATOR", "WORKSPACE_RECONCILIATION", "WORKSPACE_SET_RELEASE", "RECOVERY_REAPER", "ARTIFACT_SWEEP"} {
		if got := ClassifyJobKind(kind); got != JobClassControl {
			t.Fatalf("ClassifyJobKind(%q) = %q, want %q", kind, got, JobClassControl)
		}
	}
}

// TestClassifyJobKind_EverythingElseDefaultsRunWork proves the fail-closed
// default: the doc's own typo'd WORKSPACE_RECONCILE, a completely unknown
// Kind, and every ordinary real Kind this codebase already produces all
// classify RUN_WORK.
func TestClassifyJobKind_EverythingElseDefaultsRunWork(t *testing.T) {
	kinds := []string{
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

// TestValidateJobScope_RecoveryReaper_RequiresNoProjectOrRun proves
// RECOVERY_REAPER's own installation-global exemption (V4-13, confirmed
// with the user): blank ProjectID and blank RunID together are the only
// combination ValidateJobScope accepts for this kind — a real ProjectID or
// a real RunID is rejected individually.
func TestValidateJobScope_RecoveryReaper_RequiresNoProjectOrRun(t *testing.T) {
	if err := ValidateJobScope("RECOVERY_REAPER", "", ""); err != nil {
		t.Fatalf("ValidateJobScope(RECOVERY_REAPER, blank, blank) = %v, want nil", err)
	}
	if err := ValidateJobScope("RECOVERY_REAPER", project.ProjectID("project-1"), ""); err == nil {
		t.Fatal("ValidateJobScope(RECOVERY_REAPER, non-blank ProjectID, blank) = nil, want error")
	}
	if err := ValidateJobScope("RECOVERY_REAPER", "", "run-1"); err == nil {
		t.Fatal("ValidateJobScope(RECOVERY_REAPER, blank, non-blank RunID) = nil, want error")
	}
	if err := ValidateJobScope("RECOVERY_REAPER", project.ProjectID("project-1"), "run-1"); err == nil {
		t.Fatal("ValidateJobScope(RECOVERY_REAPER, non-blank ProjectID, non-blank RunID) = nil, want error")
	}
}

// TestValidateJobScope_ArtifactSweep_RequiresNoProjectOrRun mirrors
// TestValidateJobScope_RecoveryReaper_RequiresNoProjectOrRun exactly for
// ARTIFACT_SWEEP's own identical installation-global exemption (V5-14).
func TestValidateJobScope_ArtifactSweep_RequiresNoProjectOrRun(t *testing.T) {
	if err := ValidateJobScope("ARTIFACT_SWEEP", "", ""); err != nil {
		t.Fatalf("ValidateJobScope(ARTIFACT_SWEEP, blank, blank) = %v, want nil", err)
	}
	if err := ValidateJobScope("ARTIFACT_SWEEP", project.ProjectID("project-1"), ""); err == nil {
		t.Fatal("ValidateJobScope(ARTIFACT_SWEEP, non-blank ProjectID, blank) = nil, want error")
	}
	if err := ValidateJobScope("ARTIFACT_SWEEP", "", "run-1"); err == nil {
		t.Fatal("ValidateJobScope(ARTIFACT_SWEEP, blank, non-blank RunID) = nil, want error")
	}
	if err := ValidateJobScope("ARTIFACT_SWEEP", project.ProjectID("project-1"), "run-1"); err == nil {
		t.Fatal("ValidateJobScope(ARTIFACT_SWEEP, non-blank ProjectID, non-blank RunID) = nil, want error")
	}
}

// TestValidateJobScope_EveryOtherKind_RequiresProjectID proves the
// exemption is narrow: every OTHER CONTROL kind (not just RUN_WORK ones)
// still requires a real ProjectID — CONTROL-ness and
// installation-global-ness are independent axes (this file's own
// installationGlobalJobKinds doc comment).
func TestValidateJobScope_EveryOtherKind_RequiresProjectID(t *testing.T) {
	kinds := []string{
		"CANCEL_RUN_COORDINATOR", "WORKSPACE_RECONCILIATION", "WORKSPACE_SET_RELEASE", // other CONTROL kinds
		"EXECUTE_NODE", "ADVANCE_RUN", "SCHEDULE_NODE_RUN", "SOME_UNKNOWN_FUTURE_KIND", // RUN_WORK kinds
	}
	for _, kind := range kinds {
		if err := ValidateJobScope(kind, "", ""); err == nil {
			t.Fatalf("ValidateJobScope(%q, blank ProjectID, blank RunID) = nil, want error", kind)
		}
		if err := ValidateJobScope(kind, project.ProjectID("project-1"), ""); err != nil {
			t.Fatalf("ValidateJobScope(%q, non-blank ProjectID, blank RunID) = %v, want nil", kind, err)
		}
	}
}
