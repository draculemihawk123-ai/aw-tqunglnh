package diagnostics_test

// This file duplicates internal/delivery/httpapi/run's own fixture_test.go
// and internal/delivery/httpapi/recovery's own identical duplicate
// (readyWorkItemFixture/seedProject/seedActiveRepository/stubProvider/
// workflowDocumentV1/publishTestWorkflowVersion/startedRunFixture/
// runCancelledBlockerFixture/claimJobOfKind) rather than importing them — Go
// test helpers in a _test.go file are not exported across packages, the
// same "duplicated locally rather than imported" discipline recovery's own
// fixture_test.go doc comment already documents (itself duplicated from
// internal/app/runtime's own commands_sqlite_test.go). Every fixture helper
// here drives a REAL *sqlite.Store through the REAL application commands
// (catalog.RegisterRepository, work.CreateRootWorkItem,
// workspaceprovision.Handler, workflow.Compile/PublishWorkflowVersion,
// runtime.StartWorkflowRun/CancelRun) — never a hand-seeded row and never a
// mock — matching this repo's own hard rule: "Never mutate the database
// directly to fabricate a result."
//
// quarantineExtraRepositoryWorkspace is this file's own addition, mirroring
// internal/app/runtime's own resolve_work_item_blocker_test.go identical
// helper of the same name: a direct tx.Work().CreateRepositoryWorkspace
// insert is the SAME already-accepted construction that file's own
// ErrWorkspaceQuarantined test relies on — no command in this codebase
// today ever transitions a RepositoryWorkspace INTO QUARANTINED from a
// healthy state (V3-10's own real producer is a failed reconcile probe,
// out of this package's own cheap-fixture budget), so this constructs the
// PRECONDITION state directly rather than a fabricated diagnostic RESULT.

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

// claimJobOfKind claims jobs off store's own real queue (store.ClaimJob)
// until it gets one of the given kind. The lease is deliberately SHORT
// (claimJobLeaseTTL), not the usual production minute-plus: this package's
// own "lost/orphaned attempt" tests need this exact same EXECUTE_NODE job's
// own lease to genuinely expire — real wall-clock time, never a DB write —
// shortly after a synchronous handler.Handle call returns (mirrors
// internal/integration/v5accept/crash_recovery_test.go's own identical
// "real lease_until, never a mock" discipline, at a small fraction of that
// test's own ~15s real-crash-recovery cost, since this package's own tests
// only need the ORPHANED classification itself, not a full recovery-reaper
// drive).
const claimJobLeaseTTL = 3 * time.Second

func claimJobOfKind(t *testing.T, store *sqlite.Store, kind string, attempts int) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < attempts; i++ {
		job, lease, err := store.ClaimJob(ctx, "worker-1", claimJobLeaseTTL)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == kind {
			return job, lease
		}
	}
	t.Fatalf("did not find a %s job to claim within %d attempts", kind, attempts)
	return ports.DurableJob{}, ports.JobLease{}
}

func openDiagnosticsTestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
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

func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	seedProject(t, uow, projectID)
	return readyWorkItemFixtureInExistingProject(t, uow, ids, projectID, repositoryID)
}

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

func workflowDocumentV1() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-end", From: "start", Outcome: "next", To: "end"},
		},
	}
}

func publishTestWorkflowVersion(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: workflowDocumentV1(),
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
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, candidate)
		published = p
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
	return published
}

// startedRunFixture starts one real Run (through the real
// runtime.StartWorkflowRun application command, not the HTTP layer) over a
// fresh readyWorkItemFixture, and returns its RunID/NodeRunID.
func startedRunFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) runtime.StartWorkflowRunResult {
	t.Helper()
	root := readyWorkItemFixture(t, uow, ids, projectID, repositoryID)
	version := publishTestWorkflowVersion(t, uow, projectID, "wf-def-"+repositoryID, "wf-v-"+repositoryID)
	cmd := ports.Command{
		ID: "cmd-start-" + repositoryID, IdempotencyKey: "idem-start-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-" + repositoryID,
	}
	result, err := runtime.StartWorkflowRun(context.Background(), uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	return result
}

// runCancelledBlockerFixture drives startedRunFixture's own Run through a
// real runtime.CancelRun call plus its own real CANCEL_RUN_COORDINATOR job
// handler — the one real producer of a RUN_CANCELLED WorkItemBlocker in
// this codebase.
func runCancelledBlockerFixture(t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) (workItemID, blockerID string) {
	t.Helper()
	started := startedRunFixture(t, uow, ids, projectID, repositoryID)
	if _, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: started.RunID, Actor: "actor-1", Reason: "test setup: force a RUN_CANCELLED blocker", CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("CancelRun (fixture setup): %v", err)
	}

	job, _ := claimJobOfKind(t, store, runtime.CancelRunCoordinatorJobKind, 5)
	coordinator := runtime.NewCancelRunCoordinatorHandler(uow, ids)
	if err := coordinator.Handle(context.Background(), job); err != nil {
		t.Fatalf("CancelRunCoordinatorHandler.Handle (fixture setup): %v", err)
	}

	return started.WorkItemID, started.RunID + "-run-cancelled-blocker"
}

// quarantineExtraRepositoryWorkspace mirrors internal/app/runtime's own
// resolve_work_item_blocker_test.go identical helper — see this file's own
// doc comment for why a direct insert is the accepted construction here.
func quarantineExtraRepositoryWorkspace(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, workspaceSetID string) {
	t.Helper()
	seedActiveRepository(t, uow, ids, projectID, "repo-2")
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(context.Background(), workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(ids.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(workspaceSetID),
			RepositoryID: project.RepositoryID("repo-2"), Generation: 1, Locator: "handle-repo-2",
			BaseRevision: "cafebabecafebabecafebabecafebabecafebabe", State: workspace.RepositoryWorkspaceQuarantined, Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed quarantined repository workspace: %v", err)
	}
}
