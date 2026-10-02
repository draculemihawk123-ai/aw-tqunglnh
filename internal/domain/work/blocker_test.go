package work

import (
	"testing"
	"time"
)

// TestBlockerType_RunFailed_IsValidResolvableAndNotWaivable pins ADR-033
// decision 2 (V9-06): RUN_FAILED is a member of the closed BlockerType set,
// ResolveWorkItemBlocker has authority over it in RESOLVED mode, and it can
// never be WAIVED ("Muốn bỏ việc thì dùng CancelWorkItem").
func TestBlockerType_RunFailed_IsValidResolvableAndNotWaivable(t *testing.T) {
	if BlockerRunFailed != "RUN_FAILED" {
		t.Fatalf("BlockerRunFailed = %q, want the wire/DB value RUN_FAILED", BlockerRunFailed)
	}
	if !BlockerRunFailed.IsValid() {
		t.Fatal("RUN_FAILED is not a valid BlockerType")
	}
	if !BlockerRunFailed.ResolvableViaCommand() {
		t.Fatal("RUN_FAILED must be resolvable via ResolveWorkItemBlocker (mode RESOLVED)")
	}
	if BlockerRunFailed.Waivable() {
		t.Fatal("RUN_FAILED must not be waivable")
	}
}

// TestBlockerType_AuthorityMatrix locks the whole closed table, so adding a
// type (as V9-06 did) cannot silently change another type's waive/resolve
// authority.
func TestBlockerType_AuthorityMatrix(t *testing.T) {
	tests := []struct {
		blockerType BlockerType
		waivable    bool
		resolvable  bool
	}{
		{BlockerRunCancelled, true, true},
		{BlockerRunFailed, false, true},
		{BlockerCompletionPolicyFailed, true, true},
		{BlockerScopeExpansionRequired, false, false},
		{BlockerIsolationEnforcementUnavailable, false, true},
		{BlockerAdapterBuildDrift, false, true},
		{BlockerCapabilityRequirementUnsatisfied, false, true},
		{BlockerWriteCapabilityOrGrantMissing, false, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.blockerType), func(t *testing.T) {
			if !tt.blockerType.IsValid() {
				t.Fatalf("%s is not valid", tt.blockerType)
			}
			if got := tt.blockerType.Waivable(); got != tt.waivable {
				t.Fatalf("Waivable() = %v, want %v", got, tt.waivable)
			}
			if got := tt.blockerType.ResolvableViaCommand(); got != tt.resolvable {
				t.Fatalf("ResolvableViaCommand() = %v, want %v", got, tt.resolvable)
			}
		})
	}
	if len(knownBlockerTypes) != len(tests) {
		t.Fatalf("knownBlockerTypes has %d entries, this table covers %d — add the new type here", len(knownBlockerTypes), len(tests))
	}
	for _, unknown := range []BlockerType{"", "RUN_SUCCEEDED", "run_failed", "BOGUS"} {
		if unknown.IsValid() {
			t.Fatalf("%q must not be a valid BlockerType", unknown)
		}
	}
}

// TestNewWorkItemBlocker_RunFailed builds an OPEN RUN_FAILED blocker carrying
// its source Run, the shape internal/app/runtime's transitionRunToFailedTx
// opens, and rejects an unknown type.
func TestNewWorkItemBlocker_RunFailed(t *testing.T) {
	openedAt := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	blocker, err := NewWorkItemBlocker(
		"run-1-run-failed-blocker", "project-1", "work-item-1", BlockerRunFailed,
		"run-1", "", "", "workflow run run-1 failed (RUN_FAILED)", openedAt,
	)
	if err != nil {
		t.Fatalf("NewWorkItemBlocker(RUN_FAILED): %v", err)
	}
	if blocker.Type != BlockerRunFailed || blocker.State != BlockerOpen || blocker.SourceRunID != "run-1" || blocker.Version != 1 {
		t.Fatalf("blocker = %+v, want an OPEN version-1 RUN_FAILED blocker sourced from run-1", blocker)
	}

	if _, err := NewWorkItemBlocker("b-1", "project-1", "work-item-1", BlockerType("RUN_EXPLODED"), "run-1", "", "", "reason", openedAt); err == nil {
		t.Fatal("NewWorkItemBlocker accepted an unknown blocker type")
	}
}
