package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
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
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openSQLiteStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedActiveRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
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
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

// readyFixtureSQLite is readyFixture's sqlite-backed twin: same real
// CreateRootWorkItem -> workspaceprovision.Handler -> forced READY sequence,
// composed against a real *sqlite.Store instead of the fake, so this file's
// own tests exercise the real UnitOfWork/Tx wiring V4-02's own sqlite side
// (internal/adapters/sqlite/start_workflow_run.go) actually runs under.
func readyFixtureSQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	seedActiveRepositorySQLite(t, uow, ids, projectID, repositoryID)

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

func publishTestWorkflowVersionSQLite(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string) workflow.WorkflowVersion {
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

// TestStartWorkflowRun_SQLite_PersistsAcrossRestart proves every row
// StartWorkflowRun writes (WorkflowRun, its START NodeRun, the WorkItem's
// own READY->ACTIVE transition) survives a real process restart — reopening
// the same sqlite file — using the SAME real accessors
// (Store.LoadWorkflowRun/LoadNodeRunState) the spike-era recovery path
// already relies on, not a re-derivation of this command's own logic.
func TestStartWorkflowRun_SQLite_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-start-workflow-run.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersionSQLite(t, uow, "project-1", "wf-def-1", "wf-v-1")

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	result, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	restarted, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer restarted.Close()

	run, err := restarted.LoadWorkflowRun(ctx, runtimedomain.WorkflowRunID(result.RunID))
	if err != nil {
		t.Fatalf("LoadWorkflowRun after restart: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunRunning || run.WorkItemID != workdomain.WorkItemID(root.WorkItemID) {
		t.Fatalf("resumed run = %+v, want RUNNING for work item %s", run, root.WorkItemID)
	}
	if run.WorkflowVersionID != version.ID() || run.WorkflowVersionHash != version.ContentHash() {
		t.Fatalf("resumed run version pin = (%s,%s), want (%s,%s)", run.WorkflowVersionID, run.WorkflowVersionHash, version.ID(), version.ContentHash())
	}

	nodeState, nodeVersion, err := restarted.LoadNodeRunState(ctx, result.NodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState after restart: %v", err)
	}
	if nodeState != runtimedomain.NodeRunRunning || nodeVersion != 1 {
		t.Fatalf("resumed node run = (state=%s, version=%d), want (RUNNING, 1)", nodeState, nodeVersion)
	}

	restartedUow := sqlite.NewUnitOfWork(restarted)
	var item workdomain.WorkItem
	err = restartedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, root.WorkItemID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkItem after restart: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("resumed work item status = %s, want ACTIVE", item.Status)
	}

	manifestCount, err := restarted.CountExecutionManifests(ctx)
	if err != nil {
		t.Fatalf("CountExecutionManifests: %v", err)
	}
	if manifestCount != 1 {
		t.Fatalf("execution manifest count = %d, want 1", manifestCount)
	}
}

// TestStartWorkflowRun_SQLite_RollbackOnMidTransactionFailure_NoOrphanRows is
// this task's own most important rollback proof, the exact same shape as
// CreateRootWorkItem's own TestCreateRootWorkItem_RollbackOnMidTransactionFailure_NoOrphanRows
// (internal/app/work/commands_sqlite_test.go): force a REAL failure at the
// LAST write StartWorkflowRun's own transaction body makes — the
// ADVANCE_RUN job's own idempotency_key uniquely colliding with a job
// pre-seeded outside the transaction — after the WorkItem transition,
// WorkflowRun, ExecutionManifest, NodeRun and domain event have all already
// been written to the live *sql.Tx, and require every one of those to roll
// back to zero rows, and the WorkItem to still read READY at its original
// version.
func TestStartWorkflowRun_SQLite_RollbackOnMidTransactionFailure_NoOrphanRows(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-start-workflow-run-rollback.db")
	uow := sqlite.NewUnitOfWork(store)
	setupIDs := idsource.NewSequential("setup")

	root := readyFixtureSQLite(t, uow, setupIDs, "project-1", "repo-1")
	version := publishTestWorkflowVersionSQLite(t, uow, "project-1", "wf-def-1", "wf-v-1")

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	// StartWorkflowRun mints RunID/ManifestID/NodeRunID/JobID as calls 1-4
	// of its own idsource.Source, in that order — a dedicated Sequential
	// source starting fresh here makes the eventual JobID ("run-4")
	// deterministic without depending on how many IDs setup above already
	// consumed.
	runIDs := idsource.NewSequential("run")
	conflictingIdempotencyKey := cmd.IdempotencyKey + "-advance-run-1"
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "pre-existing-job", ProjectID: "project-1", Kind: "PRE_EXISTING",
		AggregateType: "Probe", AggregateID: "probe-1", MaxClaims: 1,
		IdempotencyKey: conflictingIdempotencyKey,
	}); err != nil {
		t.Fatalf("seed conflicting job: %v", err)
	}

	_, err := runtime.StartWorkflowRun(ctx, uow, runIDs, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err == nil {
		t.Fatal("StartWorkflowRun succeeded, want a failure from the seeded idempotency-key collision")
	}

	assertCountRuntime(t, "workflow_runs", 0, func() (int, error) { return store.CountWorkflowRuns(ctx) })
	assertCountRuntime(t, "execution_manifests", 0, func() (int, error) { return store.CountExecutionManifests(ctx) })
	assertCountRuntime(t, "node_runs", 0, func() (int, error) { return store.CountNodeRuns(ctx) })

	eventCount, err := store.CountDomainEventsByType(ctx, "WorkflowRunStarted")
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("WorkflowRunStarted event count = %d, want 0 (rollback must remove it)", eventCount)
	}

	jobCount, err := store.CountDurableJobsByIdempotencyKey(ctx, conflictingIdempotencyKey)
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("jobs with the conflicting idempotency key = %d, want exactly 1 (only the pre-seeded one)", jobCount)
	}

	var item workdomain.WorkItem
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, root.WorkItemID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemReady || item.Version != 2 {
		t.Fatalf("work item after rollback = (status=%s, version=%d), want (READY, 2) — the failed attempt's own READY->ACTIVE write must not survive", item.Status, item.Version)
	}
}

func assertCountRuntime(t *testing.T, table string, want int, count func() (int, error)) {
	t.Helper()
	got, err := count()
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", table, got, want)
	}
}

// TestStartWorkflowRun_SQLite_ConcurrentDistinctCommandsSameWorkItem_OnlyOneCreatesRun
// is V4-02's own "concurrent start" test against the REAL backend: several
// goroutines race genuinely distinct StartWorkflowRun commands (their own
// IdempotencyKey/RequestHash) for the SAME WorkItem through
// *sqlite.Store.RunSerializedWrite, which really does queue concurrent
// writers (SQLite's own writer serialization plus this codebase's own
// busy-retry, internal/adapters/sqlite/txrunner.go) rather than fail one
// closed the way the fake's single-threaded-misuse guard would — exactly
// one caller's TransitionWorkItemStatus CAS wins the WorkItem READY->ACTIVE
// transition; every other observes it already ACTIVE and fails closed with
// ErrWorkItemNotReady, never a second RunID for the one WorkItem.
func TestStartWorkflowRun_SQLite_ConcurrentDistinctCommandsSameWorkItem_OnlyOneCreatesRun(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-start-workflow-run-race.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersionSQLite(t, uow, "project-1", "wf-def-1", "wf-v-1")

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var succeeded int
	var runIDs []string
	for i := 0; i < attempts; i++ {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			suffix := strconv.Itoa(idx)
			raceIDs := idsource.NewSequential("race" + suffix)
			cmd := ports.Command{
				ID: "cmd-start-race-" + suffix, IdempotencyKey: "idem-start-race-" + suffix, Actor: "actor-1",
				CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
				Type: "StartWorkflowRun", RequestHash: "hash-start-race-" + suffix,
			}
			result, err := runtime.StartWorkflowRun(ctx, uow, raceIDs, cmd, runtime.StartWorkflowRunRequest{
				ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, runtime.ErrWorkItemNotReady) {
					t.Errorf("goroutine %d: unexpected error: %v", idx, err)
				}
				return
			}
			succeeded++
			runIDs = append(runIDs, result.RunID)
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("succeeded = %d (RunIDs %v), want exactly 1 — every other distinct concurrent command must fail closed", succeeded, runIDs)
	}

	runCount, err := store.CountWorkflowRuns(ctx)
	if err != nil {
		t.Fatalf("CountWorkflowRuns: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("workflow_runs row count = %d, want exactly 1", runCount)
	}

	var item workdomain.WorkItem
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, root.WorkItemID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want ACTIVE", item.Status)
	}
}

// TestStartWorkflowRun_SQLite_WorkflowVersionMismatch_RejectsPinnedWorkItem
// exercises ErrWorkflowVersionMismatch. No real command in this codebase can
// pin WorkItem.WorkflowVersionID yet — V3-03's own scope note is explicit
// that a WorkItem's contract fields (WorkflowVersionID included) are set
// directly on the exported domain struct by a future caller, not through any
// command this task's own dependency set builds — so this test reaches that
// precondition the same way runtime_manifest_test.go's own tamper tests
// reach CHECK-constraint-only states no application command produces yet: a
// direct SQL UPDATE against the column migration 0007 already added.
func TestStartWorkflowRun_SQLite_WorkflowVersionMismatch_RejectsPinnedWorkItem(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-start-workflow-run-version-mismatch.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	pinned := publishTestWorkflowVersionSQLite(t, uow, "project-1", "wf-def-pinned", "wf-v-pinned")
	requested := publishTestWorkflowVersionSQLite(t, uow, "project-1", "wf-def-requested", "wf-v-requested")

	if err := store.SetWorkItemWorkflowVersionForTest(ctx, root.WorkItemID, string(pinned.ID())); err != nil {
		t.Fatalf("pin work item to a workflow version: %v", err)
	}

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	_, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(requested.ID()),
	})
	if !errors.Is(err, runtime.ErrWorkflowVersionMismatch) {
		t.Fatalf("err = %v, want ErrWorkflowVersionMismatch (work item is pinned to %s, requested %s)", err, pinned.ID(), requested.ID())
	}
}
