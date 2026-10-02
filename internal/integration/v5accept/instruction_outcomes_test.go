// V9-03 — "Instruction artifact v2: ưu tiên, outcome hợp lệ, thứ tự"
// (docs/design/12-v9-harness-alignment.md V9-03; ADR-032 in
// docs/architecture/02-architecture-decisions.md; gap G3 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-03 a node with more than one outcome REQUIRED the agent to end its
// last message with an outcome marker, but nothing in the prompt said which
// outcomes were valid; the operator had to copy the list into the Skill. The
// scenarios here run a real fake provider CLI process (the "outcome-from-prompt"
// mode of providers/fixtures.go) that does what a real agent following the v2
// instruction does: it reads taskContract.allowedOutcomes from the prompt it
// receives on stdin and reports one of them. Nothing tells the process the
// outcome out of band (no AGENTKIT_HELPER_OUTCOME), so these fail on a build
// whose prompt does not list the outcomes.
//
//   - the agent picks the first listed outcome -> the run follows that edge;
//   - the agent picks the last listed outcome  -> the run follows the other edge;
//   - the agent reports an outcome that is not in the list -> OUTCOME_REJECTED
//     and the run never follows any edge (the provider adapter reports it as a
//     rejected outcome marker, ports.ErrOutcomeMarkerRejected, which the bridge
//     answers exactly like a missing marker — not as an unavailable provider,
//     which is what it ended as before the V9-03 follow-up);
//   - the agent reports nothing on a node with a choice -> OUTCOME_REJECTED,
//     the same termination reason and failure code.
package v5accept

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	v9oApproved = "approved"
	v9oRework   = "rework"
)

// v9OutcomeDocument is start -> decide (AGENT, two selectable outcomes) ->
// end (approved) | rework_end (rework).
func v9OutcomeDocument(agentBuildID string) workflow.WorkflowDocument {
	decide := v9AgentNode("decide", agentBuildID, workflow.AgentRoleMaker, v9oApproved)
	decide.Outcomes = []string{v9oApproved, v9oRework}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			decide,
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "rework_end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-decide", From: "start", Outcome: "next", To: "decide"},
			{Key: "decide-end", From: "decide", Outcome: v9oApproved, To: "end"},
			{Key: "decide-rework", From: "decide", Outcome: v9oRework, To: "rework_end"},
		},
	}
}

// runV9OutcomeScenario runs the document with the fake agent steered by pick
// (AGENTKIT_HELPER_OUTCOME_PICK) until the run reaches want, and returns the
// run and the prompt the fake process received on stdin.
func runV9OutcomeScenario(t *testing.T, pick string, want runtimedomain.WorkflowRunState) (v9Run, providers.FakeCLIInvocation, *v5AcceptFixture) {
	t.Helper()
	f := newV5AcceptFixture(t)
	capturePath := filepath.Join(f.fixtureRoot, "fake-cli-capture.json")
	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-from-prompt")
	t.Setenv("AGENTKIT_HELPER_OUTCOME_PICK", pick)
	t.Setenv("AGENTKIT_CAPTURE_PATH", capturePath)

	agents := newV9AgentSetup(t, f)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9o-workflow-def", "v9o-workflow-v1", v9OutcomeDocument(agents.buildID), workflow.DependencyManifest{})
	router := &runtime.NodeExecutorRouter{Agent: agents.executor}
	run := startV9Run(t, f, router, agents.registry, version, "v9o", want, nil)

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read the fake CLI capture: %v", err)
	}
	var invocation providers.FakeCLIInvocation
	if err := json.Unmarshal(raw, &invocation); err != nil {
		t.Fatalf("decode the fake CLI capture: %v", err)
	}
	return run, invocation, f
}

// requireV2PromptOffersTheChoice checks the prompt the process really received
// is a schema v2 instruction listing both outcomes and describing the marker.
func requireV2PromptOffersTheChoice(t *testing.T, invocation providers.FakeCLIInvocation) {
	t.Helper()
	var prompt struct {
		SchemaVersion int `json:"schemaVersion"`
		TaskContract  struct {
			AllowedOutcomes []string `json:"allowedOutcomes"`
			OutcomeProtocol string   `json:"outcomeProtocol"`
		} `json:"taskContract"`
		Closing struct {
			AllowedOutcomes []string `json:"allowedOutcomes"`
		} `json:"closingChecklist"`
	}
	if err := json.Unmarshal([]byte(invocation.Stdin), &prompt); err != nil {
		t.Fatalf("the prompt on stdin is not JSON: %v\n%s", err, invocation.Stdin)
	}
	want := []string{v9oApproved, v9oRework}
	if prompt.SchemaVersion != 2 || !equalOutcomes(prompt.TaskContract.AllowedOutcomes, want) || !equalOutcomes(prompt.Closing.AllowedOutcomes, want) {
		t.Fatalf("prompt = %+v, want schema 2 listing %v in the contract and the closing checklist", prompt, want)
	}
	if prompt.TaskContract.OutcomeProtocol == "" {
		t.Fatal("the contract of a node with a choice carries no outcomeProtocol")
	}
}

// snapshotOfAttempt reads the ContextSnapshot bound to attemptID.
func (f *v5AcceptFixture) snapshotOfAttempt(t *testing.T, attemptID string) contextsnapshot.Snapshot {
	t.Helper()
	ctx := context.Background()
	var snapshot contextsnapshot.Snapshot
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		snapshot, err = tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("read context snapshot of attempt %s: %v", attemptID, err)
	}
	return snapshot
}

func equalOutcomes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestV9AcceptInstructionV2_AgentSelectsAnOutcomeListedInThePrompt: whichever
// listed outcome the agent reads out of its prompt and reports, the run takes
// the matching edge.
func TestV9AcceptInstructionV2_AgentSelectsAnOutcomeListedInThePrompt(t *testing.T) {
	for _, tc := range []struct {
		pick, outcome, reached, notReached string
	}{
		{"first", v9oApproved, "end", "rework_end"},
		{"last", v9oRework, "rework_end", "end"},
	} {
		t.Run(tc.pick, func(t *testing.T) {
			run, invocation, f := runV9OutcomeScenario(t, tc.pick, runtimedomain.WorkflowRunVerifying)
			requireV2PromptOffersTheChoice(t, invocation)

			decide := run.nodeRuns["decide"]
			if decide.State != runtimedomain.NodeRunSucceeded || decide.SelectedOutcome != tc.outcome {
				t.Fatalf("decide = %s / %q, want SUCCEEDED / %q", decide.State, decide.SelectedOutcome, tc.outcome)
			}
			if _, ok := run.nodeRuns[tc.reached]; !ok {
				t.Fatalf("node %q was not reached; node runs = %v", tc.reached, run.nodeRuns)
			}
			if _, ok := run.nodeRuns[tc.notReached]; ok {
				t.Fatalf("node %q was reached although the agent chose %q", tc.notReached, tc.outcome)
			}
			attempt := requireAttemptState(t, run, "decide", runtimedomain.ExecutionAttemptSucceeded)
			if attempt.TerminationReason != runtimedomain.TerminationReasonCompleted {
				t.Fatalf("attempt termination reason = %s, want COMPLETED", attempt.TerminationReason)
			}
			if snapshot := f.snapshotOfAttempt(t, string(attempt.ID)); snapshot.InstructionSchemaVersion != contextsnapshot.InstructionSchemaV2 {
				t.Fatalf("the snapshot of the attempt records instruction schema %d, want 2", snapshot.InstructionSchemaVersion)
			}
		})
	}
}

// TestV9AcceptInstructionV2_OutcomeNotInTheListIsStillRejected: the list in
// the prompt is advice to the agent, not the check — an outcome outside it is
// refused with OUTCOME_REJECTED (design doc V9-03 Verify), the same verdict a
// missing marker gets, and the run follows no edge.
func TestV9AcceptInstructionV2_OutcomeNotInTheListIsStillRejected(t *testing.T) {
	run, invocation, _ := runV9OutcomeScenario(t, "not-listed", runtimedomain.WorkflowRunFailed)
	requireV2PromptOffersTheChoice(t, invocation)

	attempt := requireAttemptState(t, run, "decide", runtimedomain.ExecutionAttemptFailed)
	if attempt.TerminationReason != runtimedomain.TerminationReasonOutcomeRejected || attempt.FailureCode != errorcode.CodeValidationFailed {
		t.Fatalf("attempt = %s / %s, want OUTCOME_REJECTED / VALIDATION_FAILED (it ended EXECUTION_FAILED / PROVIDER_UNAVAILABLE while the adapter's marker error was classified as a provider failure)", attempt.TerminationReason, attempt.FailureCode)
	}
	if decide := run.nodeRuns["decide"]; decide.SelectedOutcome != "" || decide.State == runtimedomain.NodeRunSucceeded {
		t.Fatalf("decide = %s / %q, want no outcome selected and not SUCCEEDED", decide.State, decide.SelectedOutcome)
	}
	for _, key := range []string{"end", "rework_end"} {
		if _, ok := run.nodeRuns[key]; ok {
			t.Fatalf("node %q was reached although the reported outcome %q is not in the list", key, providers.OutcomeNotListed)
		}
	}
}

// TestV9AcceptInstructionV2_MissingMarkerOnANodeWithAChoiceIsOutcomeRejected:
// listing the outcomes does not make the marker optional.
func TestV9AcceptInstructionV2_MissingMarkerOnANodeWithAChoiceIsOutcomeRejected(t *testing.T) {
	run, invocation, _ := runV9OutcomeScenario(t, "none", runtimedomain.WorkflowRunFailed)
	requireV2PromptOffersTheChoice(t, invocation)

	attempt := requireAttemptState(t, run, "decide", runtimedomain.ExecutionAttemptFailed)
	if attempt.TerminationReason != runtimedomain.TerminationReasonOutcomeRejected || attempt.FailureCode != errorcode.CodeValidationFailed {
		t.Fatalf("attempt = %s / %s, want OUTCOME_REJECTED / VALIDATION_FAILED", attempt.TerminationReason, attempt.FailureCode)
	}
	for _, key := range []string{"end", "rework_end"} {
		if _, ok := run.nodeRuns[key]; ok {
			t.Fatalf("node %q was reached without any reported outcome", key)
		}
	}
}
