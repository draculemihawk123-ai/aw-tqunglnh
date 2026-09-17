package workitem_test

// This file duplicates internal/app/runtime's own commands_test.go/
// cancel_run_test.go and internal/delivery/cli/run's own fixture_test.go
// helpers (mustCreateProject, mustCreateActiveRepository, stubProvider,
// readyWorkItemFixture, ...) rather than importing them — Go test helpers
// in a _test.go file are not exported across packages (that file's own doc
// comment). Every fixture helper here drives a REAL (in-memory)
// fake.UnitOfWork through the REAL application commands
// (catalog.RegisterRepository, work.CreateRootWorkItem,
// workspaceprovision.Handler, runtime.StartWorkflowRun/CancelRun/
// CancelRunCoordinatorHandler) — never a hand-seeded row and never a mock,
// for the same reason those sibling packages' own fixtures do: this
// package's own commands reload real rows through real ports.Tx accessors,
// so a fixture must produce real rows for their own preconditions to
// genuinely exercise. fake.UnitOfWork (not sqlite) is used throughout,
// mirroring internal/delivery/cli/catalog's own test-layering choice and
// internal/app/runtime's own cancel_work_item_test.go/
// resolve_work_item_blocker_test.go precedent for these exact two
// application packages (work/runtime) — no test in this package needs a
// real durable-job WORKER sweep the way internal/delivery/cli/run's own
// `--wait` tests do, so the lighter, faster in-memory fake is sufficient
// here.

import (
	"bytes"
	"context"
	"encoding/json"
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
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

var fixedNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

// newTestDeps builds a fresh in-memory cliworkitem.Dependencies for one
// test — a fake.UnitOfWork, a deterministic idsource.Sequential and a fixed
// Now, mirroring internal/delivery/cli/catalog/catalog_test.go's own
// newTestDeps. Every test gets its own isolated UnitOfWork.
func newTestDeps(t *testing.T) cliworkitem.Dependencies {
	t.Helper()
	return cliworkitem.Dependencies{
		UoW: fake.New(),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

// newSQLiteTestDeps builds a cliworkitem.Dependencies backed by a REAL,
// temp-file sqlite.Store rather than the in-memory fake — needed only by a
// genuine concurrent-goroutine race test (see
// TestRunWorkItemMarkReady_ConcurrentDoubleMarkReady_ExactlyOneWinner's own
// doc comment for why the fake is unsafe for that specific proof).
// internal/delivery/cli's own archtest guarantee
// (TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters) checks this
// package's own NON-TEST dependency closure only, so importing sqlite here,
// in a _test.go file, does not violate it.
func newSQLiteTestDeps(t *testing.T, name string) cliworkitem.Dependencies {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return cliworkitem.Dependencies{
		UoW: sqlite.NewUnitOfWork(store),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
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

// rootWorkItemFixture creates one ACTIVE repository and a root WorkItem over
// it (direct app-layer call, not through the CLI leaf — this is shared setup
// several test files build on, distinct from create_test.go's own tests of
// the CLI leaf itself), BACKLOG, no workspace provisioning. Cheaper than
// readyWorkItemFixture below for tests that only need a valid
// (ProjectID, FamilyID) pair and a real root WorkItem to reference/extend.
func rootWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workapp.CreateRootWorkItemResult {
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
	return root
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
// internal/delivery/cli/run/fixture_test.go's own identical helper. Used by
// mark-ready/cancel tests that need a WorkItem that can genuinely start a
// real WorkflowRun.
func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workapp.CreateRootWorkItemResult {
	t.Helper()
	root := rootWorkItemFixture(t, uow, ids, projectID, repositoryID)
	ctx := context.Background()

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

	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

// startTestRun starts a new WorkflowRun for workItemID via the real
// runtime.StartWorkflowRun — the WorkItem must currently be READY.
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

// fullyContractedWorkItem builds (but does not persist) a WorkItem whose
// contract genuinely satisfies workdomain.ValidateReadinessGate — the
// "caller builds a work.WorkItem value directly ... sets the exported
// fields directly" escape hatch internal/app/work/queries.go's own top-of-
// file doc comment describes: no public command in this codebase populates
// a WorkItem's own contract fields yet, so a test that needs one that
// "genuinely round-trips and can genuinely pass" builds it directly. status
// is caller-supplied deliberately (not always BACKLOG) so a test can prove
// ExplainWorkItemReadiness's own answer depends only on contract
// completeness, never on Status.
func fullyContractedWorkItem(id workdomain.WorkItemID, projectID project.ProjectID, familyID workdomain.TaskFamilyID, status workdomain.WorkItemStatus) workdomain.WorkItem {
	return workdomain.WorkItem{
		ID: id, ProjectID: projectID, Kind: workdomain.WorkItemRoot, FamilyID: familyID,
		Title: "A fully contracted work item", SchemaVersion: 1, Behavior: "does the thing",
		AcceptanceCriteria: []workdomain.AcceptanceCriterion{{Description: "thing works", VerificationRef: "cmd:verify"}},
		VerificationSpec:   "run verify",
		RiskLevel:          "low",
		Status:             status,
		Version:            1,
	}
}

func persistWorkItem(t *testing.T, uow ports.UnitOfWork, item workdomain.WorkItem) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().CreateWorkItem(context.Background(), item)
		return err
	})
	if err != nil {
		t.Fatalf("persist work item %s: %v", item.ID, err)
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

func runState(t *testing.T, uow ports.UnitOfWork, runID string) runtimedomain.WorkflowRun {
	t.Helper()
	var run runtimedomain.WorkflowRun
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(context.Background(), runID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkflowRun(%s): %v", runID, err)
	}
	return run
}

// driveCancelRunCoordinator mirrors internal/app/runtime/cancel_run_test.go's
// own identical helper: claims and runs the real CANCEL_RUN_COORDINATOR job
// CancelRun's own transaction enqueues, driving runID the rest of the way to
// CANCELLED.
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

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document — mirrors
// internal/delivery/cli/catalog/catalog_test.go's own identical helper.
func resultField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return string(envelope.Result)
}

func replayedField(t *testing.T, body string) bool {
	t.Helper()
	var envelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return envelope.Replayed
}

func idempotencyKeyField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return envelope.IdempotencyKey
}

// isUsageError reports whether err is a cli.UsageError — mirrors
// internal/delivery/cli/catalog/catalog_test.go's own identical helper.
func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

func decodeCreateRootResult(t *testing.T, stdout *bytes.Buffer) workapp.CreateRootWorkItemResult {
	t.Helper()
	var result workapp.CreateRootWorkItemResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode CreateRootWorkItemResult: %v", err)
	}
	return result
}
