package runtime_test

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-09 / B3 at the classify level: the Sink's mid-run checkpoint rejection
// reaches AgentNodeExecutor as the provider adapter's own Go error. It used
// to be answered with PROVIDER_UNAVAILABLE (row 1 of the locked mapping
// table); a scope breach is now SCOPE_VIOLATION, with the quiescence rule
// above it left intact.

func midRunScopeViolation() error {
	// The shape the real chain produces: Sink wraps with %w, the adapter wraps
	// that with %w again ("consume Claude events: ...").
	checkpointErr := fmt.Errorf("agentevents: checkpoint at event 7: %w", scopeguard.NewViolationsError([]scopeguard.Violation{
		{RepositoryID: "repo-1", Path: "leaked.txt", Reason: "path is not covered by a WRITE repository scope"},
	}))
	return fmt.Errorf("consume Claude events: %w", checkpointErr)
}

func agentEventsOf(t *testing.T, uow *fake.UnitOfWork, attemptID string) []ports.AgentEventRecord {
	t.Helper()
	records, err := uow.Snapshot.AgentEvents().ListByAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("ListByAttempt: %v", err)
	}
	return records
}

func TestAgentNodeExecutor_MidRunScopeViolationFromAdapter_IsScopeViolationNotProviderUnavailable(t *testing.T) {
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted},
		agentErr:    midRunScopeViolation(),
	})

	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonScopeViolation ||
		result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION/SCOPE_VIOLATION (it was PROVIDER_UNAVAILABLE before V9-09)", result)
	}

	// The violating path is recorded in the attempt's event stream, after the
	// provider's own events, for the run timeline to read back.
	records := agentEventsOf(t, uow, req.AttemptID)
	if len(records) != 2 || records[1].Sequence != 2 {
		t.Fatalf("agent events = %+v, want the provider's one event plus one orchestrator diagnostic at sequence 2", records)
	}
	detail := agentevents.ScopeViolationDetailFromRecords(records)
	if !strings.Contains(detail, "repo-1:leaked.txt") {
		t.Fatalf("recorded detail = %q, want it to name repo-1:leaked.txt", detail)
	}
}

// The locked quiescence rule outranks the scope verdict: a mutating attempt
// whose process tree never confirmed quiescence is indeterminate, whatever
// the adapter's error said — a descendant could still be writing.
func TestAgentNodeExecutor_MidRunScopeViolationWithoutQuiescence_StaysIndeterminate(t *testing.T) {
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: false},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted},
		agentErr:    midRunScopeViolation(),
	})

	_, err := executor.Execute(context.Background(), req)
	if !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
	if got := len(agentEventsOf(t, uow, req.AttemptID)); got != 1 {
		t.Fatalf("agent events = %d, want 1: an indeterminate attempt records no scope-violation diagnostic", got)
	}
}

// A violation flattened to text (an adapter that formats with %v instead of
// %w) cannot be told from any other provider error — which is exactly why
// the chain must keep %w end to end; this pins the boundary.
func TestAgentNodeExecutor_FlattenedScopeViolationText_IsStillProviderUnavailable(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentErr:    errors.New(midRunScopeViolation().Error()),
	})
	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("result.ErrorCode = %s, want PROVIDER_UNAVAILABLE for an error that no longer wraps ErrScopeViolation", result.ErrorCode)
	}
}

// The end-of-run buildEvidence violation (same classify branch) records the
// same diagnostic, and a long list is capped.
func TestAgentNodeExecutor_EndOfRunScopeViolation_RecordsBoundedPathList(t *testing.T) {
	files := make([]ports.FileStatus, 0, agentevents.MaxReportedViolations+5)
	for i := 0; i < agentevents.MaxReportedViolations+5; i++ {
		files = append(files, ports.FileStatus{Code: "M", Path: fmt.Sprintf("out/of/scope/file-%02d.txt", i)})
	}
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        ports.WorkspaceDiff{RepositoryID: "repo-2", Files: files}, // repo-2 is not in EffectiveScope at all
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})

	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorCode != errorcode.CodeScopeViolation || result.TerminationReason != domainruntime.TerminationReasonScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION", result)
	}
	detail := agentevents.ScopeViolationDetailFromRecords(agentEventsOf(t, uow, req.AttemptID))
	if !strings.Contains(detail, "25 path(s) outside the granted scope") ||
		!strings.Contains(detail, "repo-2:out/of/scope/file-00.txt") ||
		!strings.Contains(detail, "repo-2:out/of/scope/file-19.txt") ||
		strings.Contains(detail, "file-20.txt") || !strings.HasSuffix(detail, "and 5 more") {
		t.Fatalf("detail = %q, want 25 paths summarized: the first 20 named, then 'and 5 more'", detail)
	}
}

// A CHECKER's strict read-only violation (the same classify branch) names the
// path it touched too — the list is reusable by every scope-violation
// producer, not only the write-scope check.
func TestAgentNodeExecutor_CheckerReadOnlyViolation_NamesTheTouchedPath(t *testing.T) {
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		role:        workflow.AgentRoleChecker,
		diff:        defaultInScopeDiff(), // one changed file, inside the write scope but not allowed for a read-only attempt
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want SCOPE_VIOLATION", result)
	}
	detail := agentevents.ScopeViolationDetailFromRecords(agentEventsOf(t, uow, req.AttemptID))
	if !strings.Contains(detail, "repo-1:**/src/main.go") {
		t.Fatalf("detail = %q, want the touched path repo-1:**/src/main.go", detail)
	}
}

// V9-01 integration: a CHECKER that changes the worktree after its InputTree
// was taken is rejected by validateStrictlyReadOnlyTrees. That error is the
// typed *scopeguard.ViolationsError, so the changed paths reach the run
// timeline's failureDetail — the same operator-visible channel as every other
// scope violation — once the attempt is finalized.
func TestAgentNodeExecutor_CheckerTreeViolation_SurfacesThePathInTheTimelineFailureDetail(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, func(w *treeFakeWorkspaces) {
		w.setFile("src/main.go", "tampered by the checker")
		w.setFile("notes/untracked.txt", "new")
	})

	result := c.execute(t)
	requireScopeViolation(t, result, "the checker changed the worktree after its InputTree was taken")

	// The error the executor classified is the typed one: it names the paths,
	// and the recorded diagnostic carries them.
	detail := agentevents.ScopeViolationDetailFromRecords(agentEventsOf(t, c.uow, c.req.AttemptID))
	for _, want := range []string{"2 path(s) outside the granted scope", "repo-1:notes/untracked.txt", "repo-1:src/main.go"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("recorded detail = %q, want it to contain %q", detail, want)
		}
	}

	// ...and the operator reads it from the run timeline once the attempt is
	// finalized FAILED/SCOPE_VIOLATION.
	version := loadAttemptVersion(t, c.uow, c.req.AttemptID)
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), c.uow, idsource.NewSequential("fin"), clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: c.req.RunID, NodeRunID: c.req.NodeRunID, AttemptID: c.req.AttemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, FailureCode: result.ErrorCode,
		JobLease: c.req.JobLease, CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	timeline, err := runtime.GetRunTimeline(context.Background(), c.uow, redact.NewMatcher(), c.req.RunID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	var fromTimeline string
	for _, entry := range timeline.Entries {
		if entry.Kind == runtime.TimelineEntryExecutionAttempt && entry.AttemptID == c.req.AttemptID {
			fromTimeline = entry.FailureDetail
		}
	}
	if fromTimeline != detail || !strings.Contains(fromTimeline, "repo-1:src/main.go") {
		t.Fatalf("timeline failureDetail = %q, want the recorded detail %q", fromTimeline, detail)
	}
}

// A MACHINE_GATE whose evaluator changed a read-only mount ends in the same
// FAILED/SCOPE_VIOLATION and records the same operator-visible diagnostic.
func TestGateNodeExecutor_ReadOnlyViolation_RecordsTheTouchedPath(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
	}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(),
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
	if result.State != domainruntime.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION", result)
	}
	detail := agentevents.ScopeViolationDetailFromRecords(agentEventsOf(t, uow, attemptID))
	if !strings.Contains(detail, "repo-1:**/src/main.go") {
		t.Fatalf("recorded detail = %q, want the path the gate's evaluator touched", detail)
	}
}

// The recorded detail passes through the same redaction as every agent event
// and the timeline read.
func TestAgentNodeExecutor_ScopeViolationDetail_IsRedacted(t *testing.T) {
	secret := "hunter2-super-secret-value"
	matcher := redact.NewMatcher(secret)
	detail := agentevents.RedactDetail(matcher, agentevents.ScopeViolationDetail(scopeguard.NewViolationsError([]scopeguard.Violation{
		{RepositoryID: "repo-1", Path: "dir/" + secret + ".txt", Reason: "x"},
	})))
	if strings.Contains(detail, secret) || !strings.Contains(detail, "[REDACTED]") {
		t.Fatalf("detail %q must mask the known secret embedded in the path", detail)
	}
}
