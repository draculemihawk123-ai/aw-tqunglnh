package runtime_test

// V9-17 — what a maker sent back by a CHECKER reads, and what a CHECKER does
// not read:
//
//	implement (MAKER) -> check (CHECKER AGENT) --approved--> end
//	     ^                   |       \--escalated--> escalated_end   (cyclePolicy)
//	     \------rework-------/
//
// The reviewer's reason is its message, kept as an AGENT_OUTPUT Evidence row
// (agent_output_evidence.go). Driven against the fake store with real
// scheduling, finalization and request assembly.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// recordAgentOutput stores text as the AGENT_OUTPUT evidence of attemptID, the
// way a finished AGENT attempt leaves it: one artifact holding the message and
// one RECORDED row referencing it. It returns the row's id.
func (l *makerCheckLoop) recordAgentOutput(nodeRunID, attemptID, text string) string {
	l.t.Helper()
	ctx := context.Background()
	ref, err := l.store.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain; charset=utf-8", Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader([]byte(text)))
	if err != nil {
		l.t.Fatalf("put agent output artifact: %v", err)
	}
	artifactID := "agent-output-" + attemptID
	err = l.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, l.runID)
		if err != nil {
			return err
		}
		record, err := artifact.NewArtifact(
			artifact.ID(artifactID), run.ProjectID, ref.Locator, ref.SHA256, ref.Size, ref.ContentType,
			ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Attached, false, nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Artifacts().InsertArtifact(ctx, record); err != nil {
			return err
		}
		row, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(attemptID+":"+runtimedomain.EvidenceKindAgentOutput), run.ProjectID, run.WorkItemID, run.ID,
			runtimedomain.NodeRunID(nodeRunID), runtimedomain.ExecutionAttemptID(attemptID),
			runtimedomain.EvidenceKindAgentOutput, runtimedomain.EvidenceVerdictRecorded, []string{artifactID}, workspace.RevisionSet{},
			"policy-version-1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(ctx, row)
		return err
	})
	if err != nil {
		l.t.Fatalf("record agent output of attempt %s: %v", attemptID, err)
	}
	return attemptID + ":" + runtimedomain.EvidenceKindAgentOutput
}

// finalizeReviewer completes the CHECKER's attempt with outcome and returns
// the NodeRun the run advanced to.
func (l *makerCheckLoop) finalizeReviewer(checkNodeRunID, outcome, wantNext string) (reviewerAttemptID, nextNodeRunID string) {
	l.t.Helper()
	attemptID, lease := l.start(checkNodeRunID)
	finalized, err := runtime.FinalizeExecutionAttempt(context.Background(), l.uow, l.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: l.runID, NodeRunID: checkNodeRunID, AttemptID: attemptID, ExpectedVersion: loadAttemptVersion(l.t, l.uow, attemptID),
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: outcome, JobLease: lease, CorrelationID: "corr-1",
	})
	if err != nil || !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != wantNext {
		l.t.Fatalf("finalize reviewer = %+v, %v, want an advance to %s", finalized, err, wantNext)
	}
	return attemptID, finalized.AdvanceResult.NextNodeRunID
}

type promptReviewerFeedback struct {
	ReviewNode  string   `json:"reviewNode"`
	Outcome     string   `json:"outcome"`
	EvidenceIDs []string `json:"evidenceIds"`
	Message     string   `json:"message"`
	Fix         string   `json:"fix"`
}

func decodeReviewerFeedback(t *testing.T, prompt map[string]json.RawMessage) []promptReviewerFeedback {
	t.Helper()
	raw, ok := prompt["reviewerFeedback"]
	if !ok {
		return nil
	}
	var feedback []promptReviewerFeedback
	if err := json.Unmarshal(raw, &feedback); err != nil {
		t.Fatalf("decode reviewerFeedback: %v", err)
	}
	return feedback
}

const reviewerMessage = "Not approved. The delete button has no confirmation step, which acceptance criterion 3 requires.\n\nAdd the confirmation and re-run."

// The first maker has nobody to hear from. After a reviewer says `rework`, the
// next maker's prompt carries that reviewer's message and the row it came from.
func TestMakerAfterReworkingChecker_ReceivesTheReviewersMessage(t *testing.T) {
	l := newMakerCheckLoop(t, workflow.NodeAgent)

	firstMakerAttempt, checkNodeRunID := l.runMaker()
	if refs := l.snapshotEvidenceIDs(firstMakerAttempt); len(refs) != 0 {
		t.Fatalf("first maker evidence refs = %v, want none", refs)
	}
	if feedback := decodeReviewerFeedback(t, l.prompt(l.makerNodeRunID, firstMakerAttempt)); len(feedback) != 0 {
		t.Fatalf("first maker reviewerFeedback = %+v, want absent", feedback)
	}

	reviewerAttempt, secondMakerNodeRunID := l.finalizeReviewer(checkNodeRunID, "rework", "implement")
	wantEvidenceID := l.recordAgentOutput(checkNodeRunID, reviewerAttempt, reviewerMessage)

	secondMakerAttempt, _ := l.start(secondMakerNodeRunID)
	refs := l.snapshotEvidenceIDs(secondMakerAttempt)
	if len(refs) != 1 || refs[0] != wantEvidenceID {
		t.Fatalf("second maker evidence refs = %v, want exactly [%s]", refs, wantEvidenceID)
	}
	prompt := l.prompt(secondMakerNodeRunID, secondMakerAttempt)
	feedback := decodeReviewerFeedback(t, prompt)
	if len(feedback) != 1 {
		t.Fatalf("reviewerFeedback = %+v, want one entry", feedback)
	}
	got := feedback[0]
	if got.ReviewNode != "check" || got.Outcome != "rework" || got.Message != reviewerMessage ||
		len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != wantEvidenceID {
		t.Fatalf("reviewerFeedback[0] = %+v, want the reviewer's own message from %s", got, wantEvidenceID)
	}
	if !strings.Contains(got.Fix, "authoritative") || !strings.Contains(got.Fix, "runs again") {
		t.Fatalf("fix = %q, want the engine's instruction to act on it and that the review repeats", got.Fix)
	}
	// An approving or failing check's section is not invented for a reviewer.
	if failures := decodeCheckFailures(t, prompt); len(failures) != 0 {
		t.Fatalf("checkFailures = %+v, want none for a reviewer", failures)
	}
	// The section sits right after the task contract and before the resources,
	// where a maker sent back must not have it buried.
	raw := l.promptText(secondMakerNodeRunID, secondMakerAttempt)
	contract, feedbackAt, resources := strings.Index(raw, `"taskContract"`), strings.Index(raw, `"reviewerFeedback"`), strings.Index(raw, `"resources"`)
	if !(contract >= 0 && contract < feedbackAt && feedbackAt < resources) {
		t.Fatalf("key order taskContract=%d reviewerFeedback=%d resources=%d, want taskContract < reviewerFeedback < resources", contract, feedbackAt, resources)
	}
}

// promptText is the rendered prompt exactly as the provider receives it.
func (l *makerCheckLoop) promptText(nodeRunID, attemptID string) string {
	l.t.Helper()
	req, err := runtime.AssembleAgentExecutionRequest(context.Background(), l.uow, l.store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: l.runID, NodeRunID: nodeRunID, AttemptID: attemptID,
	})
	if err != nil {
		l.t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	return req.Prompt
}

// The checker input allowlist is requirement, diff and evidence. The maker's
// diff-manifest row is still pinned; its AGENT_OUTPUT row is not.
func TestCheckerSnapshot_PinsTheMakersDiffEvidenceButNotItsOutput(t *testing.T) {
	l := newMakerCheckLoop(t, workflow.NodeAgent)
	makerAttempt, checkNodeRunID := l.runMaker()
	diffEvidenceID := l.recordMakerDiffEvidence(l.makerNodeRunID, makerAttempt)
	l.recordAgentOutput(l.makerNodeRunID, makerAttempt, "I implemented the delete button.")

	reviewerAttempt, _ := l.start(checkNodeRunID)
	refs := l.snapshotEvidenceIDs(reviewerAttempt)
	if len(refs) != 1 || refs[0] != diffEvidenceID {
		t.Fatalf("checker evidence refs = %v, want only the diff evidence [%s]", refs, diffEvidenceID)
	}
}

// recordMakerDiffEvidence stores the maker's AGENT_EXECUTION row (its diff
// manifests), the way V9-01's finalize leaves it.
func (l *makerCheckLoop) recordMakerDiffEvidence(nodeRunID, attemptID string) string {
	l.t.Helper()
	ctx := context.Background()
	err := l.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, l.runID)
		if err != nil {
			return err
		}
		row, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(attemptID+":"+runtimedomain.EvidenceKindAgentExecution), run.ProjectID, run.WorkItemID, run.ID,
			runtimedomain.NodeRunID(nodeRunID), runtimedomain.ExecutionAttemptID(attemptID),
			runtimedomain.EvidenceKindAgentExecution, runtimedomain.EvidenceVerdictRecorded, []string{"diff-manifest-" + attemptID}, workspace.RevisionSet{},
			"policy-version-1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(ctx, row)
		return err
	})
	if err != nil {
		l.t.Fatalf("record diff evidence of attempt %s: %v", attemptID, err)
	}
	return attemptID + ":" + runtimedomain.EvidenceKindAgentExecution
}

// The review is bounded: a very long message is cut, and the prompt says so.
func TestMakerAfterReworkingChecker_LongReviewIsCut(t *testing.T) {
	l := newMakerCheckLoop(t, workflow.NodeAgent)
	_, checkNodeRunID := l.runMaker()
	reviewerAttempt, secondMakerNodeRunID := l.finalizeReviewer(checkNodeRunID, "rework", "implement")
	l.recordAgentOutput(checkNodeRunID, reviewerAttempt, "Finding: "+strings.Repeat("x", 20000)+" END")

	secondMakerAttempt, _ := l.start(secondMakerNodeRunID)
	feedback := decodeReviewerFeedback(t, l.prompt(secondMakerNodeRunID, secondMakerAttempt))
	if len(feedback) != 1 {
		t.Fatalf("reviewerFeedback = %+v, want one entry", feedback)
	}
	message := feedback[0].Message
	if !strings.HasPrefix(message, "Finding: xxx") || strings.Contains(message, "END") || !strings.Contains(message, "omitted") {
		t.Fatalf("message = %q..., want the start kept, the end cut and a note saying so", message[:60])
	}
	if len(message) > 8192+200 {
		t.Fatalf("message is %d bytes, want it bounded near 8192", len(message))
	}
}

// Rendering is a pure function of the snapshot: assembling twice gives the same
// bytes, so the same snapshot keeps the same instruction hash.
func TestMakerAfterReworkingChecker_PromptIsStableForTheSameSnapshot(t *testing.T) {
	l := newMakerCheckLoop(t, workflow.NodeAgent)
	_, checkNodeRunID := l.runMaker()
	reviewerAttempt, secondMakerNodeRunID := l.finalizeReviewer(checkNodeRunID, "rework", "implement")
	l.recordAgentOutput(checkNodeRunID, reviewerAttempt, reviewerMessage)
	secondMakerAttempt, _ := l.start(secondMakerNodeRunID)

	first := sha256.Sum256(mustMarshal(t, l.prompt(secondMakerNodeRunID, secondMakerAttempt)))
	second := sha256.Sum256(mustMarshal(t, l.prompt(secondMakerNodeRunID, secondMakerAttempt)))
	if hex.EncodeToString(first[:]) != hex.EncodeToString(second[:]) {
		t.Fatal("the prompt differs between two assemblies of the same snapshot")
	}
}
