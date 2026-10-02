package runtime_test

// V9-02 (ADR-031) — executor-level and finalize-level tests of the three result
// classes of a COMMAND or MACHINE_GATE node, and of the COMMAND_EXECUTION
// evidence a finished command now always leaves.
//
//   - success: exit 0 with output intact / gate PASS -> the success outcome;
//   - functional failure: the process exited on its own with a non-zero code /
//     gate OverallVerdict FAIL -> failureOutcome when declared (Attempt
//     SUCCEEDED, evidence FAILED/FAIL, no retry), the pre-V9-02 FAILED
//     transition when not;
//   - technical error: spawn failure, timeout, kill, exit 0 with cut output,
//     scope violation, gate ERROR/NOT_RUN -> the pre-V9-02 transition, never
//     failureOutcome.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// withCheckFailureOutcome turns the fixtures' single-outcome "implement" check
// into one that declares failureOutcome: a second outcome of that name, routed
// to its own END node ("failed_end") so a test can tell which edge a finalize
// followed.
func withCheckFailureOutcome(document workflow.WorkflowDocument, failureOutcome string) workflow.WorkflowDocument {
	for i := range document.Nodes {
		node := &document.Nodes[i]
		if node.Key != "implement" {
			continue
		}
		node.Outcomes = append(append([]string(nil), node.Outcomes...), failureOutcome)
		switch {
		case node.Command != nil:
			node.Command.FailureOutcome = failureOutcome
		case node.MachineGate != nil:
			node.MachineGate.FailureOutcome = failureOutcome
		}
	}
	document.Nodes = append(document.Nodes, workflow.Node{Key: "failed_end", Type: workflow.NodeEnd})
	document.Edges = append(document.Edges, workflow.Edge{Key: "implement-failure-to-failed-end", From: "implement", Outcome: failureOutcome, To: "failed_end"})
	return document
}

// commandRecord mirrors the JSON the executor stores for COMMAND_EXECUTION
// evidence (commandOutputArtifactContent).
type commandRecord struct {
	ExitCode            int      `json:"exitCode"`
	Argv                []string `json:"argv"`
	CwdRepositoryTarget string   `json:"cwdRepositoryTarget"`
	Cwd                 string   `json:"cwd"`
	DurationMillis      int64    `json:"durationMillis"`
	Truncated           bool     `json:"truncated"`
	Stdout              string   `json:"stdout"`
	Stderr              string   `json:"stderr"`
}

func readCommandRecord(t *testing.T, uow *fake.UnitOfWork, store ports.ArtifactStore, artifactID string) commandRecord {
	t.Helper()
	var record commandRecord
	if err := json.Unmarshal([]byte(mustReadArtifactContent(t, uow, store, artifactID)), &record); err != nil {
		t.Fatalf("decode command execution record: %v", err)
	}
	return record
}

// finalizeResult finalizes whatever a NodeExecutor proposed, the way
// ExecuteNodeHandler does.
func finalizeResult(
	t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string, jobLease ports.JobLease, result ports.NodeExecutionResult,
) runtime.FinalizeExecutionAttemptResult {
	t.Helper()
	failureCode := result.ErrorCode
	if result.State == runtimedomain.ExecutionAttemptSucceeded {
		failureCode = ""
	}
	reason := result.TerminationReason
	if result.State == runtimedomain.ExecutionAttemptSucceeded {
		reason = runtimedomain.TerminationReasonCompleted
	}
	finalized, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: loadAttemptVersion(t, uow, attemptID),
		NextState: result.State, TerminationReason: reason, FailureCode: failureCode, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: result.Evidence,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	return finalized
}

func loadEvidenceRow(t *testing.T, uow *fake.UnitOfWork, id string) runtimedomain.Evidence {
	t.Helper()
	var row runtimedomain.Evidence
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		row, err = tx.Runtime().GetEvidence(context.Background(), id)
		return err
	}); err != nil {
		t.Fatalf("GetEvidence(%s): %v", id, err)
	}
	return row
}

func loadNodeRunState(t *testing.T, uow *fake.UnitOfWork, nodeRunID string) runtimedomain.NodeRun {
	t.Helper()
	var nodeRun runtimedomain.NodeRun
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(context.Background(), nodeRunID)
		return err
	}); err != nil {
		t.Fatalf("GetNodeRun(%s): %v", nodeRunID, err)
	}
	return nodeRun
}

func executeCommand(
	t *testing.T, opts commandFixtureOptions, supervisor *fake.ProcessSupervisor,
) (result ports.NodeExecutionResult, uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string, jobLease ports.JobLease) {
	t.Helper()
	uow, ids, store, runID, nodeRunID, attemptID, jobLease = commandFixture(t, opts)
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return result, uow, ids, store, runID, nodeRunID, attemptID, jobLease
}

// --- COMMAND: evidence for a non-zero exit, no failureOutcome (G2) ---

// TestCommandNodeExecutor_NonzeroExit_NoFailureOutcome_KeepsTransitionAndRecordsExecution is the
// G2 reproduction at the executor level: before V9-02 a non-zero exit returned
// FAILED with no evidence at all, so nothing recorded the exit code or output.
// The transition is unchanged (FAILED / EXECUTION_FAILED, then the retry
// policy), the record is new.
func TestCommandNodeExecutor_NonzeroExit_NoFailureOutcome_KeepsTransitionAndRecordsExecution(t *testing.T) {
	result, uow, ids, store, runID, nodeRunID, attemptID, jobLease := executeCommand(t,
		commandFixtureOptions{argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}, {Kind: command.ArgvLiteral, Value: "--race"}}},
		&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 7, TreeQuiesced: true}, Stdout: "partial stdout", Stderr: "FAIL: TestThing"},
	)

	if result.State != runtimedomain.ExecutionAttemptFailed || result.TerminationReason != runtimedomain.TerminationReasonExecutionFailed ||
		result.ErrorCode != errorcode.CodeExecutionFailed || result.SelectedOutcome != "" {
		t.Fatalf("result = %+v, want the unchanged FAILED/EXECUTION_FAILED/CodeExecutionFailed with no outcome", result)
	}
	if result.Evidence == nil || len(result.Evidence.EvidenceEntries) != 1 || len(result.Evidence.OutputArtifactRefs) != 1 {
		t.Fatalf("result.Evidence = %+v, want one COMMAND_EXECUTION entry and its artifact", result.Evidence)
	}
	if result.Evidence.ProposedOutcome != nil {
		t.Fatalf("ProposedOutcome = %+v, want nil: a failure that does not route proposes no outcome", result.Evidence.ProposedOutcome)
	}
	entry := result.Evidence.EvidenceEntries[0]
	if entry.Kind != "COMMAND_EXECUTION" || entry.Verdict != "FAILED" {
		t.Fatalf("entry = %+v, want COMMAND_EXECUTION / FAILED", entry)
	}

	record := readCommandRecord(t, uow, store, result.Evidence.OutputArtifactRefs[0])
	if record.ExitCode != 7 || record.Stderr != "FAIL: TestThing" || record.Stdout != "partial stdout" || record.Truncated {
		t.Fatalf("record = %+v, want exit code 7, both streams, not truncated", record)
	}
	if len(record.Argv) != 2 || record.Argv[0] != "run" || record.Argv[1] != "--race" {
		t.Fatalf("record.Argv = %v, want the argv that ran", record.Argv)
	}
	if record.CwdRepositoryTarget != "repo-1" || record.Cwd != "bridge-fixture-working-directory" {
		t.Fatalf("record cwd = %q / %q, want repo-1 and the resolved mount", record.CwdRepositoryTarget, record.Cwd)
	}

	// Finalize: the attempt is FAILED (non-retryable under the fixture policy),
	// the NodeRun fails, and the evidence is attached all the same.
	finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
	if finalized.Advanced || !finalized.NodeRunFailed {
		t.Fatalf("finalized = %+v, want the NodeRun to FAIL exactly as before V9-02", finalized)
	}
	row := loadEvidenceRow(t, uow, attemptID+":COMMAND_EXECUTION")
	if row.Verdict != "FAILED" || len(row.ArtifactReferences) != 1 {
		t.Fatalf("stored evidence = %+v, want a FAILED COMMAND_EXECUTION row with its artifact", row)
	}
	if state := mustGetArtifactAttachState(t, uow, result.Evidence.OutputArtifactRefs[0]); state != "ATTACHED" {
		t.Fatalf("output artifact AttachState after finalize = %s, want ATTACHED", state)
	}
}

// TestCommandNodeExecutor_EvidenceIsWrittenEvenWhenNoStreamIsCaptured: the
// record is not conditional on the output contract — argv, cwd, exit code and
// duration are worth having even when the author asked for no stream (before
// V9-02 such a run left no evidence row at all).
func TestCommandNodeExecutor_EvidenceIsWrittenEvenWhenNoStreamIsCaptured(t *testing.T) {
	for name, exit := range map[string]int{"exit 0": 0, "exit 4": 4} {
		t.Run(name, func(t *testing.T) {
			result, uow, _, store, _, _, _, _ := executeCommand(t,
				commandFixtureOptions{output: &command.OutputContract{MaxOutputBytes: 1024}},
				&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: exit, TreeQuiesced: true}, Stdout: "not captured", Stderr: "not captured"},
			)
			if result.Evidence == nil || len(result.Evidence.EvidenceEntries) != 1 || len(result.Evidence.OutputArtifactRefs) != 1 {
				t.Fatalf("result.Evidence = %+v, want one COMMAND_EXECUTION entry even with no stream captured", result.Evidence)
			}
			record := readCommandRecord(t, uow, store, result.Evidence.OutputArtifactRefs[0])
			if record.ExitCode != exit || len(record.Argv) == 0 || record.Stdout != "" || record.Stderr != "" {
				t.Fatalf("record = %+v, want exit %d with argv and no stream (the contract captures none)", record, exit)
			}
		})
	}
}

// --- COMMAND: failureOutcome ---

func TestCommandNodeExecutor_NonzeroExit_WithFailureOutcome_RoutesToIt(t *testing.T) {
	result, uow, ids, store, runID, nodeRunID, attemptID, jobLease := executeCommand(t,
		commandFixtureOptions{failureOutcome: "failed"},
		&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}, Stderr: "assertion failed: want 2 got 3"},
	)
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "failed" ||
		result.TerminationReason != runtimedomain.TerminationReasonCompleted || result.ErrorCode != "" {
		t.Fatalf("result = %+v, want SUCCEEDED with outcome failed and no error code", result)
	}
	if result.Evidence == nil || result.Evidence.ProposedOutcome == nil || result.Evidence.ProposedOutcome.Value != "failed" {
		t.Fatalf("result.Evidence = %+v, want a proposed outcome of failed (finalize requires it to match)", result.Evidence)
	}
	if entries := result.Evidence.EvidenceEntries; len(entries) != 1 || entries[0].Kind != "COMMAND_EXECUTION" || entries[0].Verdict != runtimedomain.EvidenceVerdictFailed {
		t.Fatalf("EvidenceEntries = %+v, want one COMMAND_EXECUTION / FAILED", entries)
	}
	if record := readCommandRecord(t, uow, store, result.Evidence.OutputArtifactRefs[0]); record.ExitCode != 1 || record.Stderr != "assertion failed: want 2 got 3" {
		t.Fatalf("record = %+v, want exit code 1 and the stderr", record)
	}

	// Finalize accepts a SUCCEEDED attempt whose evidence verdict is FAILED,
	// and routing follows the failureOutcome edge.
	finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
	if !finalized.Advanced || finalized.Retried || finalized.NodeRunFailed {
		t.Fatalf("finalized = %+v, want Advanced and neither Retried nor NodeRunFailed (a deterministic result is not retried)", finalized)
	}
	if finalized.AdvanceResult.NextNodeKey != "failed_end" || finalized.AdvanceResult.SelectedOutcome != "failed" {
		t.Fatalf("advance = %+v, want the failureOutcome edge to failed_end", finalized.AdvanceResult)
	}
	nodeRun := loadNodeRunState(t, uow, nodeRunID)
	if nodeRun.State != runtimedomain.NodeRunSucceeded || nodeRun.SelectedOutcome != "failed" {
		t.Fatalf("node run = %s / %q, want SUCCEEDED / failed", nodeRun.State, nodeRun.SelectedOutcome)
	}
	if row := loadEvidenceRow(t, uow, attemptID+":COMMAND_EXECUTION"); row.Verdict != runtimedomain.EvidenceVerdictFailed {
		t.Fatalf("stored evidence verdict = %s, want FAILED", row.Verdict)
	}
}

func TestCommandNodeExecutor_ExitZero_WithFailureOutcome_SelectsTheSuccessOutcome(t *testing.T) {
	result, uow, ids, _, runID, nodeRunID, attemptID, jobLease := executeCommand(t,
		commandFixtureOptions{failureOutcome: "failed"},
		&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}},
	)
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "done" {
		t.Fatalf("result = %+v, want SUCCEEDED with the success outcome done, not the failureOutcome", result)
	}
	if got := result.Evidence.EvidenceEntries[0].Verdict; got != runtimedomain.EvidenceVerdictSucceeded {
		t.Fatalf("verdict = %s, want SUCCEEDED", got)
	}
	if got := result.Evidence.ProposedOutcome.Source; got != ports.AgentOutcomeDerivedCheckVerdict {
		t.Fatalf("proposal source = %s, want DERIVED_CHECK_VERDICT", got)
	}
	finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "end" {
		t.Fatalf("finalized = %+v, want the success edge to end", finalized)
	}
}

// A non-zero exit whose output was ALSO cut is still a functional failure
// (the process ended on its own with a code); the evidence carries the flag.
func TestCommandNodeExecutor_NonzeroExitWithTruncatedOutput_IsFunctionalFailureWithFlag(t *testing.T) {
	result, uow, _, store, _, _, _, _ := executeCommand(t,
		commandFixtureOptions{failureOutcome: "failed"},
		&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 2, TreeQuiesced: true, OutputTruncated: true}, Stderr: "lots of output"},
	)
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "failed" {
		t.Fatalf("result = %+v, want the failureOutcome: truncation does not turn a non-zero exit into a technical error", result)
	}
	if record := readCommandRecord(t, uow, store, result.Evidence.OutputArtifactRefs[0]); !record.Truncated || record.ExitCode != 2 {
		t.Fatalf("record = %+v, want the truncated flag set", record)
	}
}

// TestCommandNodeExecutor_TechnicalErrors_NeverTakeTheFailureOutcome: each of
// these is today's FAILED transition whether or not the node declares a
// failureOutcome.
func TestCommandNodeExecutor_TechnicalErrors_NeverTakeTheFailureOutcome(t *testing.T) {
	tests := []struct {
		name       string
		supervisor *fake.ProcessSupervisor
		wantCode   errorcode.Code
	}{
		{"timeout", &fake.ProcessSupervisor{Result: ports.ProcessResult{TimedOut: true, ExitCode: -1, TreeQuiesced: true}}, errorcode.CodeTimeout},
		{"spawn failure", &fake.ProcessSupervisor{Err: context.DeadlineExceeded, Result: ports.ProcessResult{ExitCode: -1, TreeQuiesced: true}}, errorcode.CodeProviderUnavailable},
		{"exit 0 with truncated output", &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true, OutputTruncated: true}}, errorcode.CodeExecutionFailed},
		{"killed by a signal", &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: -1, TreeQuiesced: true}}, errorcode.CodeExecutionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, uow, ids, _, runID, nodeRunID, attemptID, jobLease := executeCommand(t, commandFixtureOptions{failureOutcome: "failed"}, test.supervisor)
			if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != test.wantCode || result.SelectedOutcome != "" {
				t.Fatalf("result = %+v, want FAILED/%s with no outcome", result, test.wantCode)
			}
			if result.Evidence != nil {
				if result.Evidence.ProposedOutcome != nil {
					t.Fatalf("a technical error proposed outcome %+v", result.Evidence.ProposedOutcome)
				}
				for _, entry := range result.Evidence.EvidenceEntries {
					if entry.Verdict == runtimedomain.EvidenceVerdictSucceeded {
						t.Fatalf("a technical error left a SUCCEEDED evidence entry %+v", entry)
					}
				}
			}
			// And finalize treats it as the failure it is: no routing.
			finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
			if finalized.Advanced {
				t.Fatalf("finalized = %+v, a technical error must never advance the run", finalized)
			}
		})
	}
}

// TestCommandNodeExecutor_ScopeViolationOnFailure: a failing command that
// also wrote outside what it may write is a technical error when the node
// would route its failure (SCOPE_VIOLATION, never the failureOutcome), and
// stays the plain EXECUTION_FAILED it always was when the node does not.
func TestCommandNodeExecutor_ScopeViolationOnFailure(t *testing.T) {
	outOfScope := defaultInScopeDiff()
	outOfScope.Files = []ports.FileStatus{{Code: "M", Path: "docs/not-in-scope.md"}}

	run := func(t *testing.T, failureOutcome string) ports.NodeExecutionResult {
		t.Helper()
		uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{failureOutcome: failureOutcome})
		registry := eventschema.NewRegistry()
		agentevents.RegisterEventSchemas(registry)
		workspaces := &bridgeFakeWorkspaceProvider{
			diff:            outOfScope,
			captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1},
		}
		executor := runtime.NewCommandNodeExecutor(
			uow, ids, store, workspaces, &bridgeFakeWriteLeaseManager{}, &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}},
			fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
			&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
		)
		result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
			AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return result
	}

	routed := run(t, "failed")
	if routed.State != runtimedomain.ExecutionAttemptFailed || routed.ErrorCode != errorcode.CodeScopeViolation || routed.SelectedOutcome != "" {
		t.Fatalf("routed result = %+v, want FAILED/SCOPE_VIOLATION and no outcome", routed)
	}
	plain := run(t, "")
	if plain.State != runtimedomain.ExecutionAttemptFailed || plain.ErrorCode != errorcode.CodeExecutionFailed {
		t.Fatalf("plain result = %+v, want the unchanged FAILED/EXECUTION_FAILED for a node with no failureOutcome", plain)
	}
}

func TestCommandNodeExecutor_FailureRecordRedactsSecrets(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:           []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "--token=sh-secret-value"}},
		secretRefs:     []string{"MY_SECRET"},
		failureOutcome: "failed",
	})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true},
		Stdout: "using sh-secret-value", Stderr: "denied for sh-secret-value",
	}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{Values: map[string]string{"MY_SECRET": "sh-secret-value"}})
	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.SelectedOutcome != "failed" {
		t.Fatalf("result = %+v, want the failureOutcome", result)
	}
	content := mustReadArtifactContent(t, uow, store, result.Evidence.OutputArtifactRefs[0])
	if strings.Contains(content, "sh-secret-value") {
		t.Fatalf("the failure record still holds the raw secret (argv, stdout or stderr): %s", content)
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("the failure record was never redacted at all: %s", content)
	}
}

// --- MACHINE_GATE ---

func executeGate(
	t *testing.T, opts gateFixtureOptions, supervisor *fake.ProcessSupervisor,
) (result ports.NodeExecutionResult, uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string, jobLease ports.JobLease) {
	t.Helper()
	uow, ids, store, runID, nodeRunID, attemptID, jobLease = gateFixture(t, opts)
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return result, uow, ids, store, runID, nodeRunID, attemptID, jobLease
}

func TestGateNodeExecutor_Fail_NoFailureOutcome_KeepsTransition(t *testing.T) {
	result, _, _, _, _, _, _, _ := executeGate(t, gateFixtureOptions{},
		&fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "FAIL", "detail": "3 findings"}})),
		})
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeExecutionFailed || result.SelectedOutcome != "" {
		t.Fatalf("result = %+v, want the unchanged FAILED/CodeExecutionFailed with no outcome", result)
	}
	if result.Evidence == nil || result.Evidence.ProposedOutcome != nil || len(result.Evidence.EvidenceEntries) != 1 || result.Evidence.EvidenceEntries[0].Verdict != "FAIL" {
		t.Fatalf("result.Evidence = %+v, want the FAIL criterion row and no proposed outcome", result.Evidence)
	}
}

func TestGateNodeExecutor_Fail_WithFailureOutcome_RoutesToIt(t *testing.T) {
	result, uow, ids, _, runID, nodeRunID, attemptID, jobLease := executeGate(t,
		gateFixtureOptions{
			criteria:       []gate.Criterion{{Name: "lint", EvidenceKey: "lint"}, {Name: "tests", EvidenceKey: "tests"}},
			failureOutcome: "failed",
		},
		&fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{
				"lint":  {"verdict": "PASS"},
				"tests": {"verdict": "FAIL", "detail": "3 tests failed"},
			})),
		})
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "failed" ||
		result.TerminationReason != runtimedomain.TerminationReasonCompleted || result.ErrorCode != "" {
		t.Fatalf("result = %+v, want SUCCEEDED with outcome failed", result)
	}
	if result.Evidence == nil || result.Evidence.ProposedOutcome == nil || result.Evidence.ProposedOutcome.Value != "failed" {
		t.Fatalf("result.Evidence = %+v, want a proposed outcome of failed", result.Evidence)
	}

	finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
	if !finalized.Advanced || finalized.Retried || finalized.NodeRunFailed || finalized.AdvanceResult.NextNodeKey != "failed_end" {
		t.Fatalf("finalized = %+v, want the failureOutcome edge to failed_end, no retry", finalized)
	}
	if row := loadEvidenceRow(t, uow, attemptID+":tests"); row.Verdict != string(gate.VerdictFail) {
		t.Fatalf("tests evidence verdict = %s, want FAIL", row.Verdict)
	}
	if row := loadEvidenceRow(t, uow, attemptID+":lint"); row.Verdict != string(gate.VerdictPass) {
		t.Fatalf("lint evidence verdict = %s, want PASS", row.Verdict)
	}
}

func TestGateNodeExecutor_Pass_WithFailureOutcome_SelectsTheSuccessOutcome(t *testing.T) {
	result, _, _, _, _, _, _, _ := executeGate(t, gateFixtureOptions{failureOutcome: "failed"},
		&fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
		})
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "done" {
		t.Fatalf("result = %+v, want SUCCEEDED with the success outcome done", result)
	}
}

// TestGateNodeExecutor_TechnicalVerdicts_NeverTakeTheFailureOutcome: ERROR and
// NOT_RUN (and a scope violation) are the evaluator not being trustworthy or
// not finishing, not a trusted FAIL.
func TestGateNodeExecutor_TechnicalVerdicts_NeverTakeTheFailureOutcome(t *testing.T) {
	tests := []struct {
		name        string
		supervisor  *fake.ProcessSupervisor
		wantVerdict string
	}{
		{"evaluator exits non-zero", &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}}, "ERROR"},
		{"evaluator timed out", &fake.ProcessSupervisor{Result: ports.ProcessResult{TimedOut: true, TreeQuiesced: true}}, "ERROR"},
		{"evaluator cannot be spawned", &fake.ProcessSupervisor{Err: context.DeadlineExceeded, Result: ports.ProcessResult{ExitCode: -1, TreeQuiesced: true}}, "ERROR"},
		{"output cut, even if it parses and says FAIL", &fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true, OutputTruncated: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "FAIL"}})),
		}, "ERROR"},
		{"no parseable output", &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}, Stdout: "not json"}, "ERROR"},
		{"criterion not reported (NOT_RUN)", &fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{})),
		}, "NOT_RUN"},
		{"explicit ERROR verdict", &fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "ERROR", "detail": "linter crashed"}})),
		}, "ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, uow, ids, _, runID, nodeRunID, attemptID, jobLease := executeGate(t, gateFixtureOptions{failureOutcome: "failed"}, test.supervisor)
			if result.State != runtimedomain.ExecutionAttemptFailed || result.SelectedOutcome != "" {
				t.Fatalf("result = %+v, want FAILED with no outcome — never the failureOutcome", result)
			}
			if result.Evidence == nil || result.Evidence.ProposedOutcome != nil || len(result.Evidence.EvidenceEntries) != 1 ||
				result.Evidence.EvidenceEntries[0].Verdict != test.wantVerdict {
				t.Fatalf("result.Evidence = %+v, want one %s row and no proposed outcome", result.Evidence, test.wantVerdict)
			}
			finalized := finalizeResult(t, uow, ids, runID, nodeRunID, attemptID, jobLease, result)
			if finalized.Advanced {
				t.Fatalf("finalized = %+v, a technical verdict must never advance the run", finalized)
			}
		})
	}
}

func TestGateNodeExecutor_FailWithFailureOutcome_ScopeViolationIsTechnical(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{failureOutcome: "failed"})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "FAIL"}})),
	}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(), // a read-only gate must change nothing
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1},
	}
	executor := runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
	)
	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeScopeViolation || result.SelectedOutcome != "" {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION and no outcome", result)
	}
}

// --- finalize ---

// TestFinalizeExecutionAttempt_FailureOutcomeMustMatchProposedOutcome: finalize
// keeps the existing rule that the proposal and the selected outcome agree, so
// a result cannot claim the failureOutcome while its evidence proposes another.
func TestFinalizeExecutionAttempt_FailureOutcomeMustMatchProposedOutcome(t *testing.T) {
	result, uow, ids, _, runID, nodeRunID, attemptID, jobLease := executeCommand(t,
		commandFixtureOptions{failureOutcome: "failed"},
		&fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}},
	)
	tampered := *result.Evidence
	tampered.ProposedOutcome = &ports.AgentProposedOutcome{Value: "done", Source: ports.AgentOutcomeDerivedCheckVerdict, SchemaVersion: 1}
	version := loadAttemptVersion(t, uow, attemptID)
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: &tampered,
	}); err == nil {
		t.Fatal("FinalizeExecutionAttempt accepted failed with a proposal of done, want a fail-closed error")
	}
	if after := loadAttemptVersion(t, uow, attemptID); after != version {
		t.Fatalf("attempt version after rejected finalize = %d, want unchanged %d", after, version)
	}
}
