package runtime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// auditingFakeWorkspaceProvider is a provider that, like gitworktree.Provider,
// also satisfies ports.WorkspaceHeadAuditor.
type auditingFakeWorkspaceProvider struct {
	bridgeFakeWorkspaceProvider
	audit    ports.HeadAudit
	auditErr error
}

func (f *auditingFakeWorkspaceProvider) AuditHead(context.Context, ports.AuditHeadRequest) (ports.HeadAudit, error) {
	return f.audit, f.auditErr
}

var _ ports.WorkspaceHeadAuditor = (*auditingFakeWorkspaceProvider)(nil)

const driftHead = "0000000000000000000000000000000000dead"

type staleGateRun struct {
	uow     *fake.UnitOfWork
	req     ports.NodeExecutionRequest
	result  ports.NodeExecutionResult
	records []ports.AgentEventRecord
}

func runStaleGate(t *testing.T, workspaces ports.WorkspaceProvider) staleGateRun {
	t.Helper()
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	executor := runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
	)
	req := ports.NodeExecutionRequest{AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease}
	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var records []ports.AgentEventRecord
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		records, err = tx.AgentEvents().ListByAttempt(context.Background(), attemptID)
		return err
	}); err != nil {
		t.Fatalf("list agent events: %v", err)
	}
	return staleGateRun{uow: uow, req: req, result: result, records: records}
}

// V11-01: the gate still fails closed with VALIDATION_FAILED, but the attempt now
// carries a typed diagnostic that names the repository, both revisions and the
// commits made outside aw.
func TestGateNodeExecutor_HeadDrift_RecordsWhichCommitsWereMadeOutsideAw(t *testing.T) {
	workspaces := &auditingFakeWorkspaceProvider{
		bridgeFakeWorkspaceProvider: bridgeFakeWorkspaceProvider{
			diff:            defaultInScopeDiff(),
			captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: driftHead, WorkspaceGeneration: 1},
		},
		audit: ports.HeadAudit{
			Head: driftHead, Pinned: fixtureRepo1PinnedRevision, FastForward: true, TotalCommits: 1,
			Commits: []ports.HeadAuditCommit{{
				CommitID: "1111111111111111111111111111111111111111", AuthorName: "Ide User", AuthorEmail: "ide@example.invalid",
				AuthoredAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Subject: "commit tay",
			}},
		},
	}

	run := runStaleGate(t, workspaces)
	if run.result.State != runtimedomain.ExecutionAttemptFailed || run.result.ErrorCode != errorcode.CodeValidationFailed {
		t.Fatalf("result = %+v, want FAILED/VALIDATION_FAILED (fail-closed classification is unchanged)", run.result)
	}
	detail := agentevents.WorkspaceHeadDriftDetailFromRecords(run.records)
	for _, want := range []string{"WORKSPACE_HEAD_DRIFT", "repo-1", "11111111", "Ide User", "commit tay", "adopt-head"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not mention %q", detail, want)
		}
	}

	// The operator reads it from the run timeline once the attempt is finalized.
	version := loadAttemptVersion(t, run.uow, run.req.AttemptID)
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), run.uow, idsource.NewSequential("fin"), clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: run.req.RunID, NodeRunID: run.req.NodeRunID, AttemptID: run.req.AttemptID, ExpectedVersion: version,
		NextState: run.result.State, TerminationReason: run.result.TerminationReason, FailureCode: run.result.ErrorCode,
		JobLease: run.req.JobLease, CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	timeline, err := runtime.GetRunTimeline(context.Background(), run.uow, redact.NewMatcher(), run.req.RunID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	var fromTimeline string
	for _, entry := range timeline.Entries {
		if entry.Kind == runtime.TimelineEntryExecutionAttempt && entry.AttemptID == run.req.AttemptID {
			fromTimeline = entry.FailureDetail
		}
	}
	if fromTimeline != detail {
		t.Fatalf("timeline failureDetail = %q, want the recorded detail %q", fromTimeline, detail)
	}
}

// A provider that cannot audit still produces the typed diagnostic, saying only
// that HEAD differs.
func TestGateNodeExecutor_HeadDrift_WithoutAuditStillExplainsTheCause(t *testing.T) {
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: driftHead, WorkspaceGeneration: 1},
	}

	run := runStaleGate(t, workspaces)
	if run.result.ErrorCode != errorcode.CodeValidationFailed {
		t.Fatalf("result = %+v, want VALIDATION_FAILED", run.result)
	}
	detail := agentevents.WorkspaceHeadDriftDetailFromRecords(run.records)
	if !strings.Contains(detail, "WORKSPACE_HEAD_DRIFT") || !strings.Contains(detail, "repo-1") || !strings.Contains(detail, "could not list") {
		t.Fatalf("detail = %q, want a typed diagnostic that says the commits could not be listed", detail)
	}
}

func TestGateNodeExecutor_HeadDrift_NonFastForwardDoesNotOfferAdoption(t *testing.T) {
	workspaces := &auditingFakeWorkspaceProvider{
		bridgeFakeWorkspaceProvider: bridgeFakeWorkspaceProvider{
			diff:            defaultInScopeDiff(),
			captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: driftHead, WorkspaceGeneration: 1},
		},
		audit: ports.HeadAudit{Head: driftHead, Pinned: fixtureRepo1PinnedRevision, FastForward: false},
	}

	run := runStaleGate(t, workspaces)
	detail := agentevents.WorkspaceHeadDriftDetailFromRecords(run.records)
	if strings.Contains(detail, "run `aw repository-workspace adopt-head`") || !strings.Contains(detail, "cannot adopt") {
		t.Fatalf("detail = %q, want an explanation that HEAD cannot be adopted", detail)
	}
}
