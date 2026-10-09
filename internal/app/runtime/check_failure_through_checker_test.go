package runtime_test

// V11-04 (ADR-037) — what a diagnosing CHECKER between a failing check and the
// maker reads, and what the maker reads after it:
//
//	implement (MAKER) -> check (COMMAND, failureOutcome failed) --failed--> debug (CHECKER) --diagnosed--> implement
//
// Before this change `debug` was activated by a failing check but its prompt
// carried no checkFailures (a CHECKER's prompt never rendered them), and the
// maker after `debug` got only debug's report. Driven against the fake store
// with real scheduling, finalization and request assembly, like
// check_failure_context_test.go.

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const failingStderr = "--- FAIL: TestAdd (0.00s)\n    add_test.go:12: want 3, got 2\nFAIL\n"

// failThroughDiagnosis walks maker -> failing check and returns the check's
// attempt id and the diagnosing checker's NodeRun id.
func failThroughDiagnosis(t *testing.T, loop *makerCheckLoop) (checkAttempt, debugNodeRunID string) {
	t.Helper()
	_, checkNodeRunID := loop.runMaker()
	checkAttempt, finalized := loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}, Stderr: failingStderr,
	})
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "debug" {
		t.Fatalf("finalize check = %+v, want the failureOutcome edge to debug", finalized)
	}
	return checkAttempt, finalized.AdvanceResult.NextNodeRunID
}

func TestDiagnosingCheckerAfterFailingCheck_ReceivesTheCheckFailures(t *testing.T) {
	loop := newMakerCheckLoopWithDiagnosis(t, workflow.NodeCommand, true)
	checkAttempt, debugNodeRunID := failThroughDiagnosis(t, loop)
	debugAttempt, _ := loop.start(debugNodeRunID)

	failures := decodeCheckFailures(t, loop.prompt(debugNodeRunID, debugAttempt))
	if len(failures) != 1 {
		t.Fatalf("debug checkFailures = %+v, want exactly the failing check", failures)
	}
	failure := failures[0]
	wantEvidence := checkAttempt + ":COMMAND_EXECUTION"
	if failure.CheckNode != "check" || len(failure.EvidenceIDs) != 1 || failure.EvidenceIDs[0] != wantEvidence {
		t.Fatalf("failure = %+v, want check node check and evidence %s", failure, wantEvidence)
	}
	if !strings.Contains(failure.Why, "want 3, got 2") || !strings.Contains(failure.What, "exited with code 1") {
		t.Fatalf("failure = %+v, want the command's own output", failure)
	}
}

func TestMakerAfterDiagnosingChecker_ReceivesBothTheDiagnosisAndTheOriginalCheckFailure(t *testing.T) {
	loop := newMakerCheckLoopWithDiagnosis(t, workflow.NodeCommand, true)
	checkAttempt, debugNodeRunID := failThroughDiagnosis(t, loop)

	debugAttempt, makerNodeRunID := loop.finalizeReviewer(debugNodeRunID, "diagnosed", "implement")
	diagnosisEvidence := loop.recordAgentOutput(debugNodeRunID, debugAttempt, "Root cause: add() drops the carry.")

	makerAttempt, _ := loop.start(makerNodeRunID)
	prompt := loop.prompt(makerNodeRunID, makerAttempt)

	feedback := decodeReviewerFeedback(t, prompt)
	if len(feedback) != 1 || feedback[0].ReviewNode != "debug" || len(feedback[0].EvidenceIDs) != 1 || feedback[0].EvidenceIDs[0] != diagnosisEvidence {
		t.Fatalf("reviewerFeedback = %+v, want the diagnosis from %s", feedback, diagnosisEvidence)
	}
	failures := decodeCheckFailures(t, prompt)
	if len(failures) != 1 || failures[0].CheckNode != "check" || len(failures[0].EvidenceIDs) != 1 ||
		failures[0].EvidenceIDs[0] != checkAttempt+":COMMAND_EXECUTION" || !strings.Contains(failures[0].Why, "want 3, got 2") {
		t.Fatalf("checkFailures = %+v, want the original failing check %s as well as the diagnosis", failures, checkAttempt)
	}
}

// The check failure only goes through the CHECKER the failing check sent the run
// to: a reviewer reached by an ordinary edge, and the maker it sends back, get no
// checkFailures.
func TestCheckerReachedWithoutAFailingCheck_HasNoCheckFailures(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeAgent)
	_, reviewNodeRunID := loop.runMaker()
	reviewAttempt, _ := loop.start(reviewNodeRunID)
	if failures := decodeCheckFailures(t, loop.prompt(reviewNodeRunID, reviewAttempt)); len(failures) != 0 {
		t.Fatalf("reviewer checkFailures = %+v, want none: no check failed before it", failures)
	}
}

// A passing check followed (on the next loop) by the same diagnosing checker does
// not resurrect an earlier failure.
func TestDiagnosingCheckerAfterTheNextFailure_ReceivesOnlyTheLatestFailure(t *testing.T) {
	loop := newMakerCheckLoopWithDiagnosis(t, workflow.NodeCommand, true)
	_, debugNodeRunID := failThroughDiagnosis(t, loop)
	_, makerNodeRunID := loop.finalizeReviewer(debugNodeRunID, "diagnosed", "implement")

	makerAttempt, lease := loop.start(makerNodeRunID)
	// complete the second maker and fail the check a second time
	finalized := finalizeResult(t, loop.uow, loop.ids, loop.runID, makerNodeRunID, makerAttempt, lease, ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted, SelectedOutcome: "done",
	})
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "check" {
		t.Fatalf("finalize second maker = %+v, want an advance to check", finalized)
	}
	secondCheckAttempt, secondFinalized := loop.runCheck(finalized.AdvanceResult.NextNodeRunID, &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}, Stderr: "second failure\n",
	})
	if secondFinalized.AdvanceResult.NextNodeKey != "debug" {
		t.Fatalf("second failing check advanced to %q, want debug", secondFinalized.AdvanceResult.NextNodeKey)
	}
	secondDebugAttempt, _ := loop.start(secondFinalized.AdvanceResult.NextNodeRunID)
	failures := decodeCheckFailures(t, loop.prompt(secondFinalized.AdvanceResult.NextNodeRunID, secondDebugAttempt))
	if len(failures) != 1 || failures[0].EvidenceIDs[0] != secondCheckAttempt+":COMMAND_EXECUTION" || !strings.Contains(failures[0].Why, "second failure") {
		t.Fatalf("checkFailures = %+v, want only the second failure", failures)
	}
}
