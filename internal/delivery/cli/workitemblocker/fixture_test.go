package workitemblocker_test

// This file duplicates internal/app/runtime's own commands_test.go/
// cancel_run_test.go/resolve_work_item_blocker_test.go helpers and
// internal/delivery/cli/workitem's own fixture_test.go helpers rather than
// importing either — Go test helpers in a _test.go file are not exported
// across packages. Every fixture helper here drives a REAL (in-memory)
// fake.UnitOfWork through the REAL application commands — never a
// hand-seeded row, except seedBlocker/quarantineExtraRepositoryWorkspace
// below, which are this codebase's own established, user-confirmed test-only
// shortcut for the five WorkItemBlocker types that have no real producer
// yet in this codebase (see internal/app/runtime/resolve_work_item_blocker_test.go's
// own seedBlocker doc comment) and for reaching a QUARANTINED workspace
// fixture without driving the real detection path.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkitemblocker "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitemblocker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// isUsageError reports whether err is a cli.UsageError.
func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

// newSQLiteTestDeps builds a cliworkitemblocker.Dependencies backed by a
// REAL, temp-file sqlite.Store rather than the in-memory fake, plus the
// *sqlite.Store itself (needed to claim the real CANCEL_RUN_COORDINATOR job
// runCancelledBlockerFixtureSQLite below drives) — needed only by a genuine
// concurrent-goroutine race test (see
// TestResolve_ConcurrentResolveRace_ExactlyOneFreshDecision's own doc
// comment for why the fake is unsafe for that specific proof).
// internal/delivery/cli's own archtest guarantee
// (TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters) checks this
// package's own NON-TEST dependency closure only, so importing sqlite here,
// in a _test.go file, does not violate it.
func newSQLiteTestDeps(t *testing.T, name string) (*sqlite.Store, cliworkitemblocker.Dependencies) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, cliworkitemblocker.Dependencies{UoW: sqlite.NewUnitOfWork(store), IDs: idsource.NewSequential("id")}
}

// claimJobOfKind mirrors internal/delivery/httpapi/recovery/fixture_test.go's
// own identical helper: claims jobs off store's own real queue until it
// gets one of the given kind.
func claimJobOfKind(t *testing.T, store *sqlite.Store, kind string, attempts int) ports.DurableJob {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < attempts; i++ {
		job, _, err := store.ClaimJob(ctx, "worker-1", time.Minute)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == kind {
			return job
		}
	}
	t.Fatalf("did not find a %s job to claim within %d attempts", kind, attempts)
	return ports.DurableJob{}
}

// runCancelledBlockerFixtureSQLite mirrors runCancelledBlockerFixture above,
// against a real sqlite-backed store instead of the fake: CancelRun itself
// only moves the Run to CANCELLING and enqueues a real
// CANCEL_RUN_COORDINATOR job — it is that job's own handler that actually
// drives the Run the rest of the way to CANCELLED and opens the
// RUN_CANCELLED blocker, so this fixture must claim and run that job for
// real (mirrors internal/delivery/httpapi/recovery/fixture_test.go's own
// runCancelledBlockerFixture exactly).
func runCancelledBlockerFixtureSQLite(t *testing.T, store *sqlite.Store, deps cliworkitemblocker.Dependencies) (workItemID string, blocker workdomain.WorkItemBlocker) {
	t.Helper()
	root := readyWorkItemFixture(t, deps.UoW, deps.IDs, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UoW, "project-1", "wf-def-1", "wf-v-1")
	run := startTestRun(t, deps.UoW, deps.IDs, "project-1", root.WorkItemID, string(version.ID()), "idem-start-1")
	if _, err := runtime.CancelRun(context.Background(), deps.UoW, deps.IDs, runtime.CancelRunRequest{
		RunID: run.RunID, Actor: "operator-1", Reason: "test setup: force a RUN_CANCELLED blocker",
	}); err != nil {
		t.Fatalf("CancelRun (fixture setup): %v", err)
	}

	job := claimJobOfKind(t, store, runtime.CancelRunCoordinatorJobKind, 5)
	coordinator := runtime.NewCancelRunCoordinatorHandler(deps.UoW, deps.IDs)
	if err := coordinator.Handle(context.Background(), job); err != nil {
		t.Fatalf("CancelRunCoordinatorHandler.Handle (fixture setup): %v", err)
	}

	for _, b := range workItemBlockers(t, deps.UoW, root.WorkItemID) {
		if b.Type == workdomain.BlockerRunCancelled {
			blocker = b
		}
	}
	if blocker.ID == "" {
		t.Fatal("no RUN_CANCELLED blocker found")
	}
	return root.WorkItemID, blocker
}

var fixedNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func newTestDeps(t *testing.T) cliworkitemblocker.Dependencies {
	t.Helper()
	return cliworkitemblocker.Dependencies{UoW: fake.New(), IDs: idsource.NewSequential("id")}
}

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: fixedNow,
		Type: commandType, RequestHash: requestHash,
	}
}

func mustCreateProject(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func mustCreateActiveRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
	if _, err := appcatalog.RegisterRepository(ctx, uow, ids, regCmd, appcatalog.RegisterRepositoryRequest{
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
	return ports.DurableJob{ID: ports.JobID(id), Kind: workapp.WorkspaceProvisionJobKind, Payload: payload}
}

// readyWorkItemFixture creates one ACTIVE repository, a root WorkItem over
// it, drives its sole WorkspaceSet all the way to READY, then forces the
// WorkItem itself BACKLOG->READY — mirrors
// internal/delivery/cli/workitem/fixture_test.go's own identical helper.
func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workapp.CreateRootWorkItemResult {
	t.Helper()
	mustCreateProject(t, uow, projectID)
	mustCreateActiveRepository(t, uow, ids, projectID, repositoryID)
	ctx := context.Background()

	cmd := testCommand("idem-root-"+repositoryID, "hash-root-"+repositoryID, ports.ProjectScope(projectID), "CreateRootWorkItem")
	root, err := workapp.CreateRootWorkItem(ctx, uow, ids, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing",
		InitialScope: []workapp.ScopeGrantRequest{{
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
		PublishedBy: "operator-1", PublishedAt: fixedNow,
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

func startTestRun(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, workItemID, workflowVersionID, idemKey string) runtime.StartWorkflowRunResult {
	t.Helper()
	cmd := testCommand(idemKey, "hash-"+idemKey, ports.ProjectScope(projectID), "StartWorkflowRun")
	result, err := runtime.StartWorkflowRun(context.Background(), uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: workItemID, WorkflowVersionID: workflowVersionID,
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	return result
}

// driveCancelRunCoordinator mirrors internal/app/runtime/cancel_run_test.go's
// own identical helper.
func driveCancelRunCoordinator(t *testing.T, u *fake.UnitOfWork, ids idsource.Source, runID string) {
	t.Helper()
	jobs := u.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var coordinatorJob *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.CancelRunCoordinatorJobKind && jobs[i].AggregateID == runID {
			coordinatorJob = &jobs[i]
		}
	}
	if coordinatorJob == nil {
		t.Fatalf("no %s job found for run %s among %+v", runtime.CancelRunCoordinatorJobKind, runID, jobs)
	}
	handler := runtime.NewCancelRunCoordinatorHandler(u, ids)
	if err := handler.Handle(context.Background(), ports.DurableJob{
		ID: coordinatorJob.ID, AggregateType: coordinatorJob.AggregateType, AggregateID: coordinatorJob.AggregateID,
		Payload: coordinatorJob.Payload,
	}); err != nil {
		t.Fatalf("CancelRunCoordinatorHandler.Handle: %v", err)
	}
}

func workItemState(t *testing.T, uow ports.UnitOfWork, workItemID string) workdomain.WorkItem {
	t.Helper()
	var item workdomain.WorkItem
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(context.Background(), workItemID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkItem(%s): %v", workItemID, err)
	}
	return item
}

func workItemBlockers(t *testing.T, uow ports.UnitOfWork, workItemID string) []workdomain.WorkItemBlocker {
	t.Helper()
	var blockers []workdomain.WorkItemBlocker
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(context.Background(), workItemID)
		return err
	})
	if err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem(%s): %v", workItemID, err)
	}
	return blockers
}

// cancelWorkItemFixture creates one ACTIVE repository, a root WorkItem READY
// to start a run, and publishes workflowDocumentV1 — mirrors
// internal/app/runtime/cancel_work_item_test.go's own identical helper.
func cancelWorkItemFixture(t *testing.T, deps cliworkitemblocker.Dependencies) (workItemID, workflowVersionID string) {
	t.Helper()
	root := readyWorkItemFixture(t, deps.UoW, deps.IDs, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UoW, "project-1", "wf-def-1", "wf-v-1")
	return root.WorkItemID, string(version.ID())
}

// seedBlocker directly inserts a new, OPEN WorkItemBlocker of blockerType —
// this codebase's own accepted test-setup shortcut (confirmed with the user,
// internal/app/runtime/resolve_work_item_blocker_test.go's own seedBlocker
// doc comment) for the five blocker types with no real producer yet.
func seedBlocker(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, workItemID string, blockerType workdomain.BlockerType) workdomain.WorkItemBlocker {
	t.Helper()
	item := workItemState(t, uow, workItemID)
	var created workdomain.WorkItemBlocker
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		blocker, err := workdomain.NewWorkItemBlocker(
			workdomain.BlockerID(ids.NewID()), item.ProjectID, item.ID, blockerType, "", "", "", "seeded for test", fixedNow,
		)
		if err != nil {
			return err
		}
		created, err = tx.Work().CreateWorkItemBlocker(context.Background(), blocker)
		return err
	})
	if err != nil {
		t.Fatalf("seed blocker %s: %v", blockerType, err)
	}
	return created
}

// runCancelledBlockerFixture creates a WorkItem+Run, cancels it standalone
// via CancelRun, drives the coordinator to CANCELLED, and returns the real
// (never seeded) RUN_CANCELLED blocker this produces — mirrors
// internal/app/runtime/resolve_work_item_blocker_test.go's own identical
// fixture.
func runCancelledBlockerFixture(t *testing.T, deps cliworkitemblocker.Dependencies) (workItemID string, blocker workdomain.WorkItemBlocker) {
	t.Helper()
	u := deps.UoW.(*fake.UnitOfWork)
	workItemID, workflowVersionID := cancelWorkItemFixture(t, deps)
	run := startTestRun(t, u, deps.IDs, "project-1", workItemID, workflowVersionID, "idem-start-1")
	if _, err := runtime.CancelRun(context.Background(), u, deps.IDs, runtime.CancelRunRequest{
		RunID: run.RunID, Actor: "operator-1", Reason: "test setup: force a RUN_CANCELLED blocker",
	}); err != nil {
		t.Fatalf("CancelRun (fixture setup): %v", err)
	}
	driveCancelRunCoordinator(t, u, deps.IDs, run.RunID)

	for _, b := range workItemBlockers(t, u, workItemID) {
		if b.Type == workdomain.BlockerRunCancelled {
			blocker = b
		}
	}
	if blocker.ID == "" {
		t.Fatal("no RUN_CANCELLED blocker found")
	}
	return workItemID, blocker
}

// quarantineExtraRepositoryWorkspace registers a second repository under the
// same project, and inserts a QUARANTINED RepositoryWorkspace row for it
// directly under familyWorkspaceSetID — mirrors
// internal/app/runtime/resolve_work_item_blocker_test.go's own identical
// helper.
func quarantineExtraRepositoryWorkspace(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, workspaceSetID string) {
	t.Helper()
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")
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
