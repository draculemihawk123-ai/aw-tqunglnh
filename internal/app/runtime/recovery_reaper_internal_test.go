package runtime

import "testing"

// TestDecideRecoveryNextAction is V5-13's own budget contract, confirmed
// with the user verbatim (2026-09-11): RETRY and FRESH_START share one
// AttemptNumber/MaxAttempts counter, and a replacement Attempt that would
// exceed it always ESCALATEs with RecoveryReasonAttemptsExhausted —
// checked identically for the mutating (would-be FRESH_START) and
// non-mutating (would-be RETRY) branch alike. Table-driven and pure (no
// database, no real observed mutation) since decideRecoveryNextAction
// itself takes only already-classified facts.
func TestDecideRecoveryNextAction(t *testing.T) {
	const classificationReason = "LEASE_LOST"

	tests := []struct {
		name                string
		mutationObserved    bool
		hasUsableCheckpoint bool
		runCancelling       bool
		budgetRemains       bool
		wantAction          RecoveryNextAction
		wantReason          string
	}{
		{
			name:          "non-mutating, budget remains, run not cancelling -> RETRY",
			budgetRemains: true, wantAction: RecoveryActionRetry, wantReason: classificationReason,
		},
		{
			name:          "non-mutating, run cancelling -> ESCALATE with classification reason",
			runCancelling: true, budgetRemains: true, wantAction: RecoveryActionEscalate, wantReason: classificationReason,
		},
		{
			name:          "non-mutating, budget exhausted -> ESCALATE with RECOVERY_ATTEMPTS_EXHAUSTED",
			budgetRemains: false, wantAction: RecoveryActionEscalate, wantReason: RecoveryReasonAttemptsExhausted,
		},
		{
			name:             "mutating, budget exhausted -> ESCALATE with RECOVERY_ATTEMPTS_EXHAUSTED, even with a usable checkpoint",
			mutationObserved: true, hasUsableCheckpoint: true, budgetRemains: false,
			wantAction: RecoveryActionEscalate, wantReason: RecoveryReasonAttemptsExhausted,
		},
		{
			name:             "mutating, budget remains, usable checkpoint -> FRESH_START",
			mutationObserved: true, hasUsableCheckpoint: true, budgetRemains: true,
			wantAction: RecoveryActionFreshStart, wantReason: classificationReason,
		},
		{
			name:             "mutating, budget remains, no usable checkpoint -> ESCALATE with classification reason",
			mutationObserved: true, hasUsableCheckpoint: false, budgetRemains: true,
			wantAction: RecoveryActionEscalate, wantReason: classificationReason,
		},
		{
			name:             "mutating, budget exhausted takes priority over run cancelling",
			mutationObserved: true, hasUsableCheckpoint: true, runCancelling: true, budgetRemains: false,
			wantAction: RecoveryActionEscalate, wantReason: RecoveryReasonAttemptsExhausted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, reason := decideRecoveryNextAction(test.mutationObserved, test.hasUsableCheckpoint, test.runCancelling, test.budgetRemains, classificationReason)
			if action != test.wantAction || reason != test.wantReason {
				t.Fatalf("decideRecoveryNextAction(...) = (%s, %s), want (%s, %s)", action, reason, test.wantAction, test.wantReason)
			}
		})
	}
}
