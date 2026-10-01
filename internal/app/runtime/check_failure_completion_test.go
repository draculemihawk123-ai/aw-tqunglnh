package runtime_test

// V9-02 (ADR-031 decision 5): completion is unchanged. Only the LATEST
// activation of each node counts and only SUCCEEDED/PASS/NOT_APPLICABLE
// satisfies a required evidence kind, so a loop that goes fail -> fix -> pass
// completes, and a check whose latest activation failed does not — even though
// its NodeRun is SUCCEEDED (it took the failureOutcome).

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// seedCheckActivation creates one SUCCEEDED activation of node "check" at
// activationSequence — a NodeRun that took the failureOutcome or the success
// outcome, it does not matter which for completion — with its SUCCEEDED
// attempt and one COMMAND_EXECUTION evidence row carrying verdict.
func seedCheckActivation(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun, activationSequence uint64, verdict string) {
	t.Helper()
	ctx := context.Background()
	suffix := string(rune('a' + activationSequence%26))
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID("noderun-check-"+suffix), run.ID, "check", activationSequence, uint32(activationSequence), nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunSucceeded
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}
		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID("attempt-check-"+suffix), nodeRun.ID, 1, "profile-hash", "test-provider", nil,
		)
		if err != nil {
			return err
		}
		attempt.State = runtimedomain.ExecutionAttemptSucceeded
		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt); err != nil {
			return err
		}
		evidence, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(string(attempt.ID)+":"+runtimedomain.EvidenceKindCommandExecution), run.ProjectID, run.WorkItemID, run.ID,
			nodeRun.ID, attempt.ID, runtimedomain.EvidenceKindCommandExecution, verdict, []string{"artifact-check-" + suffix},
			workspace.RevisionSet{}, "policy-version-1", time.Date(2026, 1, 1, 0, 0, int(activationSequence), 0, time.UTC),
		)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(ctx, evidence)
		return err
	}); err != nil {
		t.Fatalf("seed check activation %d: %v", activationSequence, err)
	}
}

func commandExecutionCompletionPolicy() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution}},
	}
}

func evaluateCompletion(t *testing.T, uow *fake.UnitOfWork, ids interface{ NewID() string }, run runtimedomain.WorkflowRun) runtime.CompletionOutcome {
	t.Helper()
	cmd := evaluateCompletionCandidateCmd("idem-eval-check")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(context.Background(), uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	return result.Outcome
}

// fail -> fix -> pass: the first activation's FAILED evidence is superseded by
// the second's SUCCEEDED, so the loop completes.
func TestCompletion_FailFixPassLoopCompletes(t *testing.T) {
	doc := commandExecutionCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedCheckActivation(t, uow, run, 100, runtimedomain.EvidenceVerdictFailed)
	seedCheckActivation(t, uow, run, 101, runtimedomain.EvidenceVerdictSucceeded)

	if outcome := evaluateCompletion(t, uow, ids, run); outcome != runtime.CompletionOutcomePass {
		t.Fatalf("outcome = %s, want PASS: the latest activation of the check passed", outcome)
	}
}

// A run whose LAST check failed does not satisfy completion, however many
// earlier activations passed.
func TestCompletion_LatestCheckFailedDoesNotSatisfyCompletion(t *testing.T) {
	doc := commandExecutionCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedCheckActivation(t, uow, run, 100, runtimedomain.EvidenceVerdictSucceeded)
	seedCheckActivation(t, uow, run, 101, runtimedomain.EvidenceVerdictFailed)

	if outcome := evaluateCompletion(t, uow, ids, run); outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("outcome = %s, want BLOCK: the latest activation of the check failed", outcome)
	}
}

func TestCompletion_OnlyFailedCheckDoesNotSatisfyCompletion(t *testing.T) {
	doc := commandExecutionCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedCheckActivation(t, uow, run, 100, runtimedomain.EvidenceVerdictFailed)

	if outcome := evaluateCompletion(t, uow, ids, run); outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("outcome = %s, want BLOCK: FAILED is not a passing verdict", outcome)
	}
}
