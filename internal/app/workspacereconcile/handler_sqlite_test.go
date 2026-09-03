package workspacereconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises the real stack end to end — sqlite persistence, the
// real internal/adapters/gitworktree.Provider (real git subprocess),
// internal/app/workerpool.Pool claiming real durable jobs — for this
// task's own explicit Verify line: "kill writer, stale token, dirty
// unknown, idempotent reconcile tests". No mocking of Inspect/Diff here,
// mirroring internal/adapters/gitworktree/provider_test.go's own pattern
// and internal/app/workspaceprovision/handler_sqlite_test.go's own
// identical real-stack discipline for the sibling V3-06 concern.

func createReconcileFixtureGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for this end-to-end test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runReconcileFixtureGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runReconcileFixtureGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runReconcileFixtureGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runReconcileFixtureGit(t, repositoryPath, "add", "--", "service.txt")
	runReconcileFixtureGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runReconcileFixtureGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", commandArguments, err, output)
	}
	return string(output)
}

func openReconcileTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func mustSeedActiveRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, localPath string) {
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
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: localPath, DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		_, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", repositoryID, err)
	}
}

func mustCreateRootWorkItemSQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) appwork.CreateRootWorkItemResult {
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

// provisionJobSQLite mirrors workspaceprovision_test's own identical
// helper: the exact JSON shape internal/app/work.CreateRootWorkItem
// marshals for a WORKSPACE_PROVISION job.
func provisionJobSQLite(id, workItemID, projectID, familyID, workspaceSetID, repositoryID string) ports.DurableJob {
	payload, _ := json.Marshal(struct {
		WorkItemID     string `json:"workItemId"`
		ProjectID      string `json:"projectId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
		RepositoryID   string `json:"repositoryId"`
	}{
		WorkItemID: workItemID, ProjectID: projectID, FamilyID: familyID,
		WorkspaceSetID: workspaceSetID, RepositoryID: repositoryID,
	})
	return ports.DurableJob{ID: ports.JobID(id), Kind: appwork.WorkspaceProvisionJobKind, Payload: payload}
}

// realFixture bundles one real, on-disk-git-backed RepositoryWorkspace at
// generation 1, READY, ready for a reconciliation scenario to act on.
type realFixture struct {
	store          *sqlite.Store
	uow            ports.UnitOfWork
	ids            idsource.Source
	provider       *gitworktree.Provider
	projectID      string
	familyID       string
	workspaceSetID string
	repositoryID   string
	rw             workspace.RepositoryWorkspace
	repoPath       string
}

// newRealFixture provisions one real RepositoryWorkspace end to end: real
// sqlite store, real git fixture repository, real gitworktree.Provider,
// driven all the way to READY via the real
// internal/app/workspaceprovision.Handler — exactly the same real path
// production uses, so every reconciliation scenario below acts on a
// genuinely real on-disk worktree, never a stub.
func newRealFixture(t *testing.T, dbName string) *realFixture {
	t.Helper()
	fixtureRoot := t.TempDir()
	repoPath := createReconcileFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))

	store := openReconcileTestStore(t, dbName)
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}

	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a", repoPath)
	root := mustCreateRootWorkItemSQLite(t, uow, ids, "project-1", "repo-a")

	provisionHandler := workspaceprovision.New(uow, ids, provider)
	job := provisionJobSQLite(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-a")
	if err := provisionHandler.Handle(context.Background(), job); err != nil {
		t.Fatalf("provision handler.Handle: %v", err)
	}

	var rw workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-a", 1)
		return err
	}); err != nil {
		t.Fatalf("read provisioned repository workspace: %v", err)
	}
	if rw.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("provisioned repository workspace state = %s, want READY", rw.State)
	}

	return &realFixture{
		store: store, uow: uow, ids: ids, provider: provider,
		projectID: "project-1", familyID: root.FamilyID, workspaceSetID: root.WorkspaceSetID,
		repositoryID: "repo-a", rw: rw, repoPath: repoPath,
	}
}

// workingDirectory resolves f's own real on-disk worktree path via the
// real provider — the one sanctioned bridge from an opaque handle to a
// filesystem path (Provider.WorkingDirectory's own doc comment).
func (f *realFixture) workingDirectory(t *testing.T) string {
	t.Helper()
	handle, err := ports.NewWorkspaceHandle(f.rw.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	dir, err := f.provider.WorkingDirectory(context.Background(), handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	return dir
}

// requestAndRunReconciliation runs the full public/internal pipeline for
// f's own current RepositoryWorkspace: RequestWorkspaceReconciliation
// (public command, enqueues the job) followed by Handler.Handle (internal
// job handler, real Inspect/Diff and real Lifecycle mutation) against the
// exact job RequestWorkspaceReconciliation enqueued — proving the whole
// chain a real operator/API call would drive, not just Handle() in
// isolation.
func (f *realFixture) requestAndRunReconciliation(t *testing.T, idempotencyKey string, expectedVersion uint64) error {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "operator-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(f.projectID), ExpectedVersion: expectedVersion, RequestedAt: time.Now().UTC(),
		Type: "RequestWorkspaceReconciliation", RequestHash: "hash-" + idempotencyKey,
	}
	result, err := workspacereconcile.RequestWorkspaceReconciliation(ctx, f.uow, f.ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: string(f.rw.ID), ProjectID: f.projectID,
	})
	if err != nil {
		return err
	}

	reconcileHandler := workspacereconcile.New(f.uow, f.ids, f.provider, f.store)
	registry := workerpool.NewRegistry()
	registry.Register(workspacereconcile.WorkspaceReconciliationJobKind, reconcileHandler)
	pool, err := workerpool.New(f.store, registry, workerpool.Config{
		Concurrency: 1, Owner: "w", LeaseTTL: 5 * time.Second, HeartbeatEvery: time.Second,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: 2 * time.Second, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}
	poolCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(poolCtx) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state, err := f.store.LoadDurableJobState(ctx, ports.JobID(result.ReconciliationJobID))
		if err == nil && (state == ports.JobSucceeded || state == ports.JobFailed || state == ports.JobDead) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-runErr

	state, err := f.store.LoadDurableJobState(ctx, ports.JobID(result.ReconciliationJobID))
	if err != nil {
		t.Fatalf("LoadDurableJobState: %v", err)
	}
	if state != ports.JobSucceeded {
		t.Fatalf("WORKSPACE_RECONCILIATION job state = %s, want SUCCEEDED", state)
	}
	return nil
}

func (f *realFixture) reload(t *testing.T) workspace.RepositoryWorkspace {
	t.Helper()
	var rw workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), f.workspaceSetID, f.repositoryID, f.rw.Generation)
		return err
	}); err != nil {
		t.Fatalf("reload repository workspace: %v", err)
	}
	return rw
}

// TestEndToEnd_Accept_CleanReadyWorkspace_StaysReady is the ACCEPT branch:
// a clean, genuinely healthy READY workspace is confirmed usable with no
// state change at all.
func TestEndToEnd_Accept_CleanReadyWorkspace_StaysReady(t *testing.T) {
	f := newRealFixture(t, "e2e-accept.db")
	if err := f.requestAndRunReconciliation(t, "idem-accept-1", f.rw.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}
	got := f.reload(t)
	if got.State != workspace.RepositoryWorkspaceReady || got.Version != f.rw.Version {
		t.Fatalf("workspace after ACCEPT = %+v, want unchanged READY@%d", got, f.rw.Version)
	}
}

// TestEndToEnd_Block_DirtyReadyWorkspace_QuarantinesWithEvidenceReason is
// this task's own "dirty unknown" Verify-line scenario: a real, genuinely
// uncommitted file is left in the real on-disk worktree with no attempt
// context to attribute it to — ExecuteWorkspaceReconciliation's real
// Inspect call finds it Dirty and must quarantine, never silently accept
// or reset it ("không destructive reset mù").
func TestEndToEnd_Block_DirtyReadyWorkspace_QuarantinesWithEvidenceReason(t *testing.T) {
	f := newRealFixture(t, "e2e-dirty-unknown.db")
	dir := f.workingDirectory(t)
	if err := os.WriteFile(filepath.Join(dir, "unexplained.txt"), []byte("nobody knows how this got here\n"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	if err := f.requestAndRunReconciliation(t, "idem-dirty-1", f.rw.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}
	got := f.reload(t)
	if got.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("workspace after BLOCK = %+v, want QUARANTINED", got)
	}
	if got.Version != f.rw.Version+1 {
		t.Fatalf("workspace version after quarantine = %d, want %d", got.Version, f.rw.Version+1)
	}

	assertWriteLeaseAndReleaseBlocked(t, f.store, f.repositoryID, string(got.ID), got.Generation)
}

// TestEndToEnd_KillWriter_AlreadyQuarantinedDirty_StaysQuarantined_WriterAndReleaseBlocked
// is this task's own "kill writer" Verify-line scenario combined with its
// own closing bar ("Hoàn thành khi: quarantined workspace không cấp writer
// hoặc release"). It reuses the real primitives GC-ACC-05's own automatic
// crash-recovery path is built on (worker.ReconcileMutatingAttempt,
// ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace) — the same real
// functions internal/adapters/sqlite/spk09_workspace_quarantine_test.go's
// own SPK-09 boundary test already exercises directly, without
// reconstructing the full crash-worker subprocess machinery
// internal/adapters/sqlite/crashworker.go owns proving elsewhere — to
// simulate "kill worker giữa mutating attempt" landing exactly where this
// task's own operator-triggered flow picks up: a workspace already
// QUARANTINED by the automatic path, with a real, genuinely uncommitted
// leftover mutation still on disk from the killed writer. It then proves
// this task's own operator reconciliation (a) correctly classifies that
// leftover as unattributable dirt and leaves it QUARANTINED, and (b) the
// resulting QUARANTINED workspace genuinely cannot acquire a new
// ports.WriteLeaseManager grant nor be released — exercised against
// V3-09's own real, already-merged AcquireWriteLeases/ReleaseRepositoryWorkspace
// code paths, not just this task's own internal state.
func TestEndToEnd_KillWriter_AlreadyQuarantinedDirty_StaysQuarantined_WriterAndReleaseBlocked(t *testing.T) {
	f := newRealFixture(t, "e2e-kill-writer.db")
	ctx := context.Background()

	// The killed writer's own real, uncommitted leftover mutation.
	dir := f.workingDirectory(t)
	if err := os.WriteFile(filepath.Join(dir, "partial-write.txt"), []byte("left behind by the killed writer\n"), 0o600); err != nil {
		t.Fatalf("write killed-writer leftover: %v", err)
	}

	// A fresh worker cannot prove the killed writer's mutation never
	// landed: pinned (what the attempt was given) vs current (the
	// workspace's own durable current_revision column) disagree.
	verdict, err := worker.ReconcileMutatingAttempt(f.rw.BaseRevision, f.rw.BaseRevision+"-mutated-by-killed-writer")
	if err != nil {
		t.Fatalf("ReconcileMutatingAttempt: %v", err)
	}
	if verdict != worker.ReconciliationMutationObserved {
		t.Fatalf("verdict = %s, want MUTATION_OBSERVED_REQUIRES_QUARANTINE", verdict)
	}
	if err := f.store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Reason: string(verdict),
		EventID: "event-kill-writer-quarantine", CorrelationID: "corr-kill-writer", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace (simulated automatic path): %v", err)
	}

	quarantined := f.reload(t)
	if quarantined.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("workspace after simulated automatic quarantine = %+v, want QUARANTINED", quarantined)
	}

	// The operator's own follow-up: this task's own real entry point.
	f.rw = quarantined
	if err := f.requestAndRunReconciliation(t, "idem-kill-writer-1", quarantined.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}

	got := f.reload(t)
	if got.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("workspace after operator reconciliation = %+v, want still QUARANTINED (dirty, unattributable)", got)
	}
	if got.Version != quarantined.Version {
		t.Fatalf("workspace version changed from %d to %d — a no-op BLOCK on an already-quarantined workspace must never bump version",
			quarantined.Version, got.Version)
	}

	assertWriteLeaseAndReleaseBlocked(t, f.store, f.repositoryID, string(got.ID), got.Generation)
}

// TestEndToEnd_Recreate_QuarantinedCleanWorkspace_ProvisionsFreshGeneration
// is the RECREATE branch: a workspace already QUARANTINED (simulated the
// same real-primitive way as the kill-writer test above) but whose real
// on-disk worktree is genuinely clean — the only "back into service" path
// once already quarantined, since no primitive un-quarantines a generation
// in place.
func TestEndToEnd_Recreate_QuarantinedCleanWorkspace_ProvisionsFreshGeneration(t *testing.T) {
	f := newRealFixture(t, "e2e-recreate.db")
	ctx := context.Background()

	if err := f.store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Reason: "SIMULATED_OPERATOR_SUSPICION",
		EventID: "event-recreate-quarantine", CorrelationID: "corr-recreate", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace (simulated precondition): %v", err)
	}
	quarantined := f.reload(t)
	f.rw = quarantined

	if err := f.requestAndRunReconciliation(t, "idem-recreate-1", quarantined.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}

	// The previous generation's row is never mutated further — it remains
	// QUARANTINED permanently as evidence.
	previous := f.reload(t)
	if previous.State != workspace.RepositoryWorkspaceQuarantined || previous.Version != quarantined.Version {
		t.Fatalf("previous generation after RECREATE = %+v, want unchanged QUARANTINED@%d", previous, quarantined.Version)
	}

	var next workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		next, err = tx.Work().GetRepositoryWorkspace(ctx, f.workspaceSetID, f.repositoryID, f.rw.Generation+1)
		return err
	}); err != nil {
		t.Fatalf("read recreated generation: %v", err)
	}
	if next.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("recreated generation state = %s, want READY", next.State)
	}
	if next.Locator == "" || next.BaseRevision == "" {
		t.Fatal("recreated generation is missing a real locator/base revision")
	}
	if next.ID == previous.ID {
		t.Fatal("recreated generation reused the previous generation's own RepositoryWorkspace ID")
	}

	// Only the previous generation is fenced out — proven end to end by
	// the kill-writer test above for the identical
	// AcquireWriteLeases/ReleaseRepositoryWorkspace primitives. (Proving the
	// new generation itself CAN receive a writer would need a real
	// WorkflowRun/NodeRun/ExecutionAttempt chain seeded purely to satisfy
	// write_leases' own FK on holder_attempt_id — genuine ceremony this
	// task's own scope does not require: "Hoàn thành khi" only asks that
	// the quarantined generation grants neither a writer nor a release.)
}

// TestEndToEnd_IdempotentReconcile_JobLayer_RerunAfterRecreateIsNoOp is
// this task's own "idempotent reconcile" Verify-line requirement at the
// JOB layer (mirroring a crash-recovery reclaim of the exact same job
// after it already committed its decision): running
// ExecuteWorkspaceReconciliation a second time with the identical request
// after a RECREATE already committed must be a safe no-op — never a
// second generation, never an error.
func TestEndToEnd_IdempotentReconcile_JobLayer_RerunAfterRecreateIsNoOp(t *testing.T) {
	f := newRealFixture(t, "e2e-idempotent-recreate.db")
	ctx := context.Background()

	if err := f.store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Reason: "SIMULATED_OPERATOR_SUSPICION",
		EventID: "event-idempotent-quarantine", CorrelationID: "corr-idempotent", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace (simulated precondition): %v", err)
	}
	quarantined := f.reload(t)

	deps := workspacereconcile.ExecuteWorkspaceReconciliationDeps{UnitOfWork: f.uow, IDs: f.ids, Provider: f.provider, Lifecycle: f.store}
	request := workspacereconcile.ExecuteWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: string(quarantined.ID), ProjectID: f.projectID, FamilyID: f.familyID,
		WorkspaceSetID: f.workspaceSetID, RepositoryID: f.repositoryID,
		Generation: quarantined.Generation, ExpectedVersion: quarantined.Version, CorrelationID: "corr-idempotent",
	}

	if err := workspacereconcile.ExecuteWorkspaceReconciliation(ctx, deps, request); err != nil {
		t.Fatalf("first ExecuteWorkspaceReconciliation: %v", err)
	}

	var afterFirst workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		afterFirst, err = tx.Work().GetRepositoryWorkspace(ctx, f.workspaceSetID, f.repositoryID, quarantined.Generation+1)
		return err
	}); err != nil {
		t.Fatalf("read recreated generation after first run: %v", err)
	}

	// Second run: same request, exactly as a crash-recovery reclaim of the
	// identical job would replay it.
	if err := workspacereconcile.ExecuteWorkspaceReconciliation(ctx, deps, request); err != nil {
		t.Fatalf("second (idempotent replay) ExecuteWorkspaceReconciliation: %v", err)
	}

	var afterSecond workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		afterSecond, err = tx.Work().GetRepositoryWorkspace(ctx, f.workspaceSetID, f.repositoryID, quarantined.Generation+1)
		return err
	}); err != nil {
		t.Fatalf("read recreated generation after second run: %v", err)
	}
	if afterSecond != afterFirst {
		t.Fatalf("recreated generation changed across the idempotent replay: first=%+v second=%+v", afterFirst, afterSecond)
	}

	var thirdGenerationExists bool
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().GetRepositoryWorkspace(ctx, f.workspaceSetID, f.repositoryID, quarantined.Generation+2)
		thirdGenerationExists = err == nil
		return nil
	}); err != nil {
		t.Fatalf("check for a spurious third generation: %v", err)
	}
	if thirdGenerationExists {
		t.Fatal("a second run created a THIRD generation — idempotent replay must never re-run RECREATE")
	}
}

// mustClaimAnyJob enqueues and claims one throwaway durable job so a test
// has a real, currently-LEASED ports.JobLease to present to
// AcquireWriteLeases — mirroring
// internal/adapters/sqlite/spk09_workspace_quarantine_test.go's own
// identical "a run-scoped job stands in for the durable job a real worker
// holds" technique.
func mustClaimAnyJob(t *testing.T, store *sqlite.Store, jobID string) ports.JobLease {
	t.Helper()
	ctx := context.Background()
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: "project-1", Kind: "PROBE_WRITER",
		AggregateType: "Test", AggregateID: jobID, MaxClaims: 3, IdempotencyKey: jobID + "-key",
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	_, lease, err := store.ClaimJob(ctx, "writer-probe", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	return lease
}

// assertWriteLeaseAndReleaseBlocked is this task's own closing bar,
// Judgment call 4: a QUARANTINED repositoryWorkspaceID must genuinely be
// unable to acquire a new ports.WriteLeaseManager grant (V3-09's own real
// AcquireWriteLeases, gated on repository_workspaces.state='READY') and
// must genuinely be unable to be released (ports.ErrWorkspaceQuarantined,
// ReleaseRepositoryWorkspace's own real refusal) — exercised against the
// real store end to end, not asserted from this task's own internal state
// alone.
func assertWriteLeaseAndReleaseBlocked(t *testing.T, store *sqlite.Store, repositoryID, repositoryWorkspaceID string, generation uint64) {
	t.Helper()
	ctx := context.Background()
	lease := mustClaimAnyJob(t, store, "writer-probe-"+repositoryWorkspaceID)
	_, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: lease, AttemptID: "attempt-writer-probe",
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: project.RepositoryID(repositoryID), RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID), Generation: generation,
		}},
		TTL: 5 * time.Second,
	})
	if !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("AcquireWriteLeases() on a QUARANTINED repository workspace error = %v, want ErrWriteLeaseConflict", err)
	}

	// ExpectedVersion is deliberately an arbitrary value that cannot match
	// the row's real current version: ReleaseRepositoryWorkspace's own real
	// implementation still correctly reports ErrWorkspaceQuarantined rather
	// than ErrOptimisticConflict for a QUARANTINED row, regardless of what
	// version is presented (it checks state before it would ever get to
	// compare versions) — see workspace_lifecycle.go's own
	// ReleaseRepositoryWorkspace doc comment.
	if err := store.ReleaseRepositoryWorkspace(ctx, ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID), ExpectedVersion: 999999,
		EventID: "event-release-probe-" + repositoryWorkspaceID, CorrelationID: "corr-release-probe", OccurredAt: time.Now().UTC(),
	}); !errors.Is(err, ports.ErrWorkspaceQuarantined) {
		t.Fatalf("ReleaseRepositoryWorkspace() on a QUARANTINED repository workspace error = %v, want ErrWorkspaceQuarantined", err)
	}
}
