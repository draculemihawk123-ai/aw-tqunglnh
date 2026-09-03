package workspacereconcile_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// mustSeedActiveRepository registers repositoryID under projectID and
// drives it all the way to ACTIVE (bypassing the real V3-02 probe worker,
// out of this package's own scope) so
// internal/app/work.CreateRootWorkItem's own same-project/ACTIVE-repository
// validation passes — mirrors workspaceprovision_test's own identical
// helper exactly.
func mustSeedActiveRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) project.Repository {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), Type: "RegisterRepository",
		RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: "/fixture/" + repositoryID, DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	var active project.Repository
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		active, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", repositoryID, err)
	}
	return active
}

// mustCreateRootWorkItem grants initial scope over repositoryID via the
// real internal/app/work.CreateRootWorkItem command — the only sanctioned
// way to get a real TaskFamily+WorkspaceSet into either the fake or real
// store, mirroring workspaceprovision_test's own identical helper.
func mustCreateRootWorkItem(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(projectID), Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, cmd, appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Root task", InitialScope: []appwork.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

// seededWorkspace bundles everything a test needs to address one real
// RepositoryWorkspace row seeded directly (bypassing the whole
// workspaceprovision job machinery, out of this package's own scope): the
// owning project/repository/family/workspace-set identity plus the
// RepositoryWorkspace's own ID and current Version.
type seededWorkspace struct {
	ProjectID             string
	FamilyID              string
	WorkspaceSetID        string
	RepositoryID          string
	RepositoryWorkspaceID string
	Version               uint64
}

// mustSeedRepositoryWorkspace seeds one real RepositoryWorkspace row for
// repositoryID at the given generation/state directly via
// workspace.NewRepositoryWorkspace + tx.Work().CreateRepositoryWorkspace —
// exactly the domain constructor workspaceprovision.Handler's own
// finishReady already uses — so a command/handler test never has to run
// the full provisioning job machinery just to get one real row to act on.
func mustSeedRepositoryWorkspace(
	t *testing.T, uow *fake.UnitOfWork, ids idsource.Source,
	projectID, repositoryID string, generation uint64, locator, baseRevision string, state workspace.RepositoryWorkspaceState,
) seededWorkspace {
	t.Helper()
	ctx := context.Background()
	repo := mustSeedActiveRepository(t, uow, ids, projectID, repositoryID)
	root := mustCreateRootWorkItem(t, uow, ids, projectID, repositoryID)

	var result seededWorkspace
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID("rw-"+repositoryID), set, repo, generation, locator, "", baseRevision,
		)
		if err != nil {
			return err
		}
		rw.State = state
		rw.CurrentRevision = baseRevision
		created, err := tx.Work().CreateRepositoryWorkspace(ctx, rw)
		if err != nil {
			return err
		}
		result = seededWorkspace{
			ProjectID: projectID, FamilyID: root.FamilyID, WorkspaceSetID: root.WorkspaceSetID,
			RepositoryID: repositoryID, RepositoryWorkspaceID: string(created.ID), Version: created.Version,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed repository workspace: %v", err)
	}
	return result
}

// reconciliationJobs filters uow's own enqueued jobs down to
// WorkspaceReconciliationJobKind alone — mustSeedRepositoryWorkspace's own
// setup (mustSeedActiveRepository's RegisterRepository, mustCreateRootWorkItem's
// CreateRootWorkItem) already enqueues its own REPOSITORY_PROBE/
// WORKSPACE_PROVISION jobs as an unrelated side effect of getting a real
// RepositoryWorkspace row seeded, so a bare job-count assertion must not
// count those.
func reconciliationJobs(uow *fake.UnitOfWork) []ports.EnqueueJobRequest {
	var result []ports.EnqueueJobRequest
	for _, job := range uow.Snapshot.Jobs().(*fake.JobsRepository).Items() {
		if job.Kind == workspacereconcile.WorkspaceReconciliationJobKind {
			result = append(result, job)
		}
	}
	return result
}

func reconcileCommand(idempotencyKey, requestHash string, projectID string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), ExpectedVersion: expectedVersion,
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        "RequestWorkspaceReconciliation", RequestHash: requestHash,
	}
}

func TestRequestWorkspaceReconciliation_FromReady_EnqueuesJobAndWritesEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)

	cmd := reconcileCommand("idem-1", "hash-a", "project-1", seeded.Version)
	result, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("RequestWorkspaceReconciliation: %v", err)
	}
	if result.RepositoryWorkspaceID != seeded.RepositoryWorkspaceID || result.State != string(workspace.RepositoryWorkspaceReady) {
		t.Fatalf("result = %+v, want RepositoryWorkspaceID=%s State=READY", result, seeded.RepositoryWorkspaceID)
	}
	if result.ReconciliationJobID == "" {
		t.Fatal("result.ReconciliationJobID is empty, want a minted job id")
	}

	jobs := reconciliationJobs(uow)
	if len(jobs) != 1 {
		t.Fatalf("enqueued reconciliation jobs = %d, want exactly 1", len(jobs))
	}
	if jobs[0].AggregateType != "RepositoryWorkspace" || jobs[0].AggregateID != seeded.RepositoryWorkspaceID {
		t.Fatalf("job = %+v, want AggregateType=RepositoryWorkspace AggregateID=%s", jobs[0], seeded.RepositoryWorkspaceID)
	}

	var reconciliationEvents int
	for _, event := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if event.EventType == "WorkspaceReconciliationRequested" {
			reconciliationEvents++
		}
	}
	if reconciliationEvents != 1 {
		t.Fatalf("WorkspaceReconciliationRequested events = %d, want exactly 1", reconciliationEvents)
	}
}

func TestRequestWorkspaceReconciliation_FromQuarantined_Eligible(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceQuarantined)

	cmd := reconcileCommand("idem-1", "hash-a", "project-1", seeded.Version)
	result, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("RequestWorkspaceReconciliation: %v", err)
	}
	if result.State != string(workspace.RepositoryWorkspaceQuarantined) {
		t.Fatalf("result.State = %q, want QUARANTINED", result.State)
	}
}

// TestRequestWorkspaceReconciliation_StaleExpectedVersion_Rejected is
// Judgment call 3(b)'s own "stale token" scenario: a caller presenting an
// ExpectedVersion that no longer matches the workspace's current version
// must be rejected outright, never silently reconciled against a
// generation it no longer has an accurate picture of.
func TestRequestWorkspaceReconciliation_StaleExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)

	staleVersion := seeded.Version + 41 // any value that does not match the real stored version
	cmd := reconcileCommand("idem-1", "hash-a", "project-1", staleVersion)
	_, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1",
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RequestWorkspaceReconciliation() error = %v, want ErrOptimisticConflict", err)
	}
	if jobs := reconciliationJobs(uow); len(jobs) != 0 {
		t.Fatalf("enqueued reconciliation jobs after stale-token rejection = %d, want 0", len(jobs))
	}
}

// TestRequestWorkspaceReconciliation_IneligibleState_Rejected proves a
// RepositoryWorkspace outside {READY, QUARANTINED} — here, still
// PROVISIONING — is never accepted for reconciliation.
func TestRequestWorkspaceReconciliation_IneligibleState_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceProvisioning)

	cmd := reconcileCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1",
	})
	if !errors.Is(err, workspacereconcile.ErrWorkspaceNotReconcilable) {
		t.Fatalf("RequestWorkspaceReconciliation() error = %v, want ErrWorkspaceNotReconcilable", err)
	}
}

func TestRequestWorkspaceReconciliation_CrossProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)

	cmd := reconcileCommand("idem-1", "hash-a", "project-other", seeded.Version)
	_, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-other",
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("RequestWorkspaceReconciliation() error = %v, want ErrCrossProjectReference", err)
	}
}

func TestRequestWorkspaceReconciliation_DuplicateSameCommand_ReplaysWithoutNewJobOrEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)

	cmd := reconcileCommand("idem-1", "hash-a", "project-1", seeded.Version)
	req := workspacereconcile.RequestWorkspaceReconciliationRequest{RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1"}
	first, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first RequestWorkspaceReconciliation: %v", err)
	}
	second, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) RequestWorkspaceReconciliation: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}
	if jobs := reconciliationJobs(uow); len(jobs) != 1 {
		t.Fatalf("enqueued reconciliation jobs after replay = %d, want 1 (a replay must never redo the mutation)", len(jobs))
	}
}

// TestRequestWorkspaceReconciliation_SecondDistinctRequest_AlreadyOpen_Rejected
// is this task's own "idempotent reconcile" Verify-line requirement at the
// REQUEST layer: two DIFFERENT commands (different IdempotencyKey/RequestHash
// — e.g. two separate operator clicks, not a retry of the same one) for
// the same RepositoryWorkspace at the same still-current version must not
// both succeed in enqueuing a job — the second is rejected, and only one
// job ever exists.
func TestRequestWorkspaceReconciliation_SecondDistinctRequest_AlreadyOpen_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)
	req := workspacereconcile.RequestWorkspaceReconciliationRequest{RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1"}

	first := reconcileCommand("idem-1", "hash-a", "project-1", seeded.Version)
	if _, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, first, req); err != nil {
		t.Fatalf("first RequestWorkspaceReconciliation: %v", err)
	}

	second := reconcileCommand("idem-2-different-caller", "hash-b", "project-1", seeded.Version)
	_, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, second, req)
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("second distinct RequestWorkspaceReconciliation() error = %v, want ErrPersistenceAlreadyExists (already has an open reconciliation)", err)
	}
	if jobs := reconciliationJobs(uow); len(jobs) != 1 {
		t.Fatalf("enqueued reconciliation jobs after a second, distinct in-flight request = %d, want exactly 1", len(jobs))
	}
}

func TestRequestWorkspaceReconciliation_MissingExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedRepositoryWorkspace(t, uow, ids, "project-1", "repo-1", 1, "handle-1", "base-sha", workspace.RepositoryWorkspaceReady)

	cmd := reconcileCommand("idem-1", "hash-a", "project-1", 0)
	_, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: seeded.RepositoryWorkspaceID, ProjectID: "project-1",
	})
	if err == nil {
		t.Fatal("RequestWorkspaceReconciliation() with cmd.ExpectedVersion=0 error = nil, want an error")
	}
}
