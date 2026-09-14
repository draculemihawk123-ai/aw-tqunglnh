package decision_test

// This file duplicates internal/app/runtime's own test fixture helpers
// (readyFixtureSQLite, approvalDocument, waitSignalDocument,
// publishWorkflowVersionDocument, stubProvider) and
// internal/delivery/httpapi/run's own fixture_test.go helpers rather than
// importing them — Go test helpers in a _test.go file are not exported
// across packages, the same "duplicated locally rather than imported"
// discipline run/fixture_test.go's own doc comment already documents.
// Every fixture helper here drives a REAL *sqlite.Store through the REAL
// application commands (catalog.RegisterRepository, work.CreateRootWorkItem,
// workspaceprovision.Handler, workflow.Compile/PublishWorkflowVersion,
// runtime.StartWorkflowRun, runtime.AdvanceRun) — never a hand-seeded row
// and never a mock — matching this repo's own hard rule: "Never mutate the
// database directly to fabricate a result."

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openDecisionTestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, sqlite.NewUnitOfWork(store)
}

func seedProject(t *testing.T, uow ports.UnitOfWork, projectID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
}

func seedActiveRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

type stubProvider struct {
	handle   ports.WorkspaceHandle
	revision workspace.Revision
}

func (s *stubProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return s.handle, nil
}
func (s *stubProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("stub: Inspect must not be called")
}
func (s *stubProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return s.revision, nil
}
func (s *stubProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("stub: Diff must not be called")
}
func (s *stubProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("stub: Release must not be called")
}
func (s *stubProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("stub: WorkingDirectory must not be called")
}

func mustHandle(t *testing.T, token string) ports.WorkspaceHandle {
	t.Helper()
	h, err := ports.NewWorkspaceHandle(token)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle(%s): %v", token, err)
	}
	return h
}

func provisionJob(id, workItemID, projectID, familyID, workspaceSetID, repositoryID string) ports.DurableJob {
	payload := []byte(`{"workItemId":"` + workItemID + `","projectId":"` + projectID +
		`","familyId":"` + familyID + `","workspaceSetId":"` + workspaceSetID + `","repositoryId":"` + repositoryID + `"}`)
	return ports.DurableJob{ID: ports.JobID(id), Kind: work.WorkspaceProvisionJobKind, Payload: payload}
}

// readyWorkItemFixture creates one Project, one ACTIVE repository, a root
// WorkItem over it, drives its sole WorkspaceSet all the way to READY with a
// real BaseRevisionSet via the real workspaceprovision.Handler, then forces
// the WorkItem itself BACKLOG->READY via a direct
// ports.WorkRepository.TransitionWorkItemStatus call — test setup only,
// mirroring run/fixture_test.go's own readyWorkItemFixture exactly.
func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	seedProject(t, uow, projectID)
	return readyWorkItemFixtureInExistingProject(t, uow, ids, projectID, repositoryID)
}

// readyWorkItemFixtureInExistingProject is readyWorkItemFixture's own twin
// for a project seedProject has already created — used when a test needs
// two (or more) ready WorkItems, each with their own repository, in the
// SAME project. Mirrors run/fixture_test.go's own identical split exactly.
func readyWorkItemFixtureInExistingProject(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	seedActiveRepository(t, uow, ids, projectID, repositoryID)

	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
	}
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	provider := &stubProvider{
		handle:   mustHandle(t, "handle-"+repositoryID),
		revision: workspace.Revision{RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1},
	}
	handler := workspaceprovision.New(uow, ids, provider)
	if err := handler.Handle(ctx, provisionJob(
		root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, projectID, root.FamilyID, root.WorkspaceSetID, repositoryID,
	)); err != nil {
		t.Fatalf("workspaceprovision.Handle: %v", err)
	}

	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: root.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force work item %s READY: %v", root.WorkItemID, err)
	}
	return root
}

// publishWorkflowVersionDocument mirrors internal/app/runtime's own test
// helper of the identical name (advance_test.go): real workflow.Compile +
// PublishWorkflowVersion for an arbitrary caller-supplied document.
func publishWorkflowVersionDocument(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string, document workflow.WorkflowDocument) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID(projectID)
	def := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	var published workflow.WorkflowVersion
	ctx := context.Background()
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		published = p
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
	return published
}

// approvalDocument mirrors internal/app/runtime's own approval_test.go
// fixture of the identical name exactly: start -> gate(APPROVAL) ->
// end_approved|end_rejected|end_escalated, AuthorizedRoles=["reviewer"].
func approvalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "rejected", "escalated"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: timeoutSeconds, EscalationOutcome: "escalated",
				RequestedEvidenceKinds: []string{"test-plan"},
			}},
			{Key: "end_approved", Type: workflow.NodeEnd},
			{Key: "end_rejected", Type: workflow.NodeEnd},
			{Key: "end_escalated", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-gate", From: "start", Outcome: "next", To: "gate"},
			{Key: "gate-to-approved", From: "gate", Outcome: "approved", To: "end_approved"},
			{Key: "gate-to-rejected", From: "gate", Outcome: "rejected", To: "end_rejected"},
			{Key: "gate-to-escalated", From: "gate", Outcome: "escalated", To: "end_escalated"},
		},
	}
}

// waitSignalDocument mirrors internal/app/runtime's own wait_test.go
// fixture of the identical name exactly: start -> pause(WAIT, SIGNAL) ->
// end_resumed|end_expired. timeoutSeconds == 0 means no timeout ceiling at
// all (TimeoutOutcome stays empty).
func waitSignalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	waitCfg := &workflow.WaitNodeConfig{
		Mode: workflow.WaitModeSignal, SignalName: "ci-passed", TimeoutSeconds: timeoutSeconds,
		CompletionOutcome: "resumed",
	}
	outcomes := []string{"resumed", "expired"}
	if timeoutSeconds > 0 {
		waitCfg.TimeoutOutcome = "expired"
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "pause", Type: workflow.NodeWait, Outcomes: outcomes, Wait: waitCfg},
			{Key: "end_resumed", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-pause", From: "start", Outcome: "next", To: "pause"},
			{Key: "pause-to-end-resumed", From: "pause", Outcome: "resumed", To: "end_resumed"},
			{Key: "pause-to-end-expired", From: "pause", Outcome: "expired", To: "end_expired"},
		},
	}
}

// approvalRequestFixture drives a REAL Run, through REAL
// runtime.StartWorkflowRun + runtime.AdvanceRun, from START all the way to
// its own APPROVAL node — returning the RunID and the freshly minted
// ApprovalRequestID (always at Version 1, runtimedomain.NewApprovalRequest's
// own hardcoded initial version) this package's own HTTP tests dispatch
// against.
func approvalRequestFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, definitionID, versionID string, timeoutSeconds uint32) (runID, approvalRequestID string) {
	t.Helper()
	seedProject(t, uow, projectID)
	return approvalRequestFixtureInProject(t, uow, ids, projectID, repositoryID, definitionID, versionID, timeoutSeconds)
}

// approvalRequestFixtureInProject is approvalRequestFixture's own twin for
// a project seedProject has already created — used when a test needs two
// (or more) ApprovalRequests, each under its own Run/repository, in the
// SAME project (e.g. approval_test.go's own
// TestResolveApproval_HTTP_StaleCrossRunTarget_404).
func approvalRequestFixtureInProject(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, definitionID, versionID string, timeoutSeconds uint32) (runID, approvalRequestID string) {
	t.Helper()
	ctx := context.Background()
	root := readyWorkItemFixtureInExistingProject(t, uow, ids, projectID, repositoryID)
	version := publishWorkflowVersionDocument(t, uow, projectID, definitionID, versionID, approvalDocument(timeoutSeconds))

	startCmd := ports.Command{
		ID: "cmd-start-" + versionID, IdempotencyKey: "idem-start-" + versionID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-" + versionID,
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->gate): %v", err)
	}
	if hop.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want a minted NextApprovalRequestID", hop)
	}
	return startResult.RunID, hop.NextApprovalRequestID
}

// waitRegistrationFixture is approvalRequestFixture's own SIGNAL-mode WAIT
// twin: drives a REAL Run from START to its own WAIT node, returning the
// RunID and the freshly minted WaitRegistrationID (always at Version 1).
func waitRegistrationFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, definitionID, versionID string, timeoutSeconds uint32) (runID, waitRegistrationID string) {
	t.Helper()
	seedProject(t, uow, projectID)
	return waitRegistrationFixtureInProject(t, uow, ids, projectID, repositoryID, definitionID, versionID, timeoutSeconds)
}

// waitRegistrationFixtureInProject is waitRegistrationFixture's own twin
// for a project seedProject has already created — used when a test needs
// two (or more) WaitRegistrations, each under its own Run/repository, in
// the SAME project (e.g. wait_test.go's own
// TestSubmitWaitSignal_HTTP_StaleCrossRunTarget_404).
func waitRegistrationFixtureInProject(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, definitionID, versionID string, timeoutSeconds uint32) (runID, waitRegistrationID string) {
	t.Helper()
	ctx := context.Background()
	root := readyWorkItemFixtureInExistingProject(t, uow, ids, projectID, repositoryID)
	version := publishWorkflowVersionDocument(t, uow, projectID, definitionID, versionID, waitSignalDocument(timeoutSeconds))

	startCmd := ports.Command{
		ID: "cmd-start-" + versionID, IdempotencyKey: "idem-start-" + versionID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-" + versionID,
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->pause): %v", err)
	}
	if hop.NextWaitRegistrationID == "" {
		t.Fatalf("hop = %+v, want a minted NextWaitRegistrationID", hop)
	}
	return startResult.RunID, hop.NextWaitRegistrationID
}
