package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: commandType, RequestHash: requestHash,
	}
}

func mustCreateProject(t *testing.T, uow *fake.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// mustCreateActiveRepository mirrors internal/app/work's own test helper of
// the identical name — duplicated locally rather than imported, since Go
// test helpers are not exported across _test.go files in different
// packages.
func mustCreateActiveRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
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

// stubProvider mirrors internal/app/workspaceprovision's own unexported test
// double of the same name: no real git/filesystem I/O, a scripted
// Provision/CaptureRevision outcome, so this file can drive a WorkspaceSet
// all the way to READY with a real, computed BaseRevisionSet through the
// REAL workspaceprovision.Handler (V3-06) — never a shortcut/mock of
// StartWorkflowRun's own preconditions.
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

// readyFixture creates one ACTIVE repository, a root WorkItem over it
// (internal/app/work.CreateRootWorkItem), drives its sole WorkspaceSet all
// the way to READY with a real BaseRevisionSet via the real
// workspaceprovision.Handler (V3-06), then forces the WorkItem itself
// BACKLOG->READY via a direct ports.WorkRepository.TransitionWorkItemStatus
// call — test setup, not the behavior under test: no real command in this
// codebase can reach WorkItem READY yet (StartWorkflowRun's own doc comment
// notes it is TransitionWorkItemStatus's first real caller, for the
// READY->ACTIVE half only), the same "poke the CAS directly to reach a
// precondition state no business command produces yet" discipline
// mustCreateActiveRepository already uses for Repository ACTIVE.
func readyFixture(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	mustCreateProject(t, uow, projectID)
	mustCreateActiveRepository(t, uow, ids, projectID, repositoryID)

	cmd := testCommand("idem-root-"+repositoryID, "hash-root-"+repositoryID, ports.ProjectScope(projectID), "CreateRootWorkItem")
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

// workflowDocumentV1 is a minimal, always-compilable two-node graph — a
// START and an END joined by one outcome edge — enough for
// resolveStartNodeKey to find its one START node, mirroring
// internal/adapters/sqlite's own workflowDocumentV1 test fixture of the
// identical shape.
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

// publishTestWorkflowVersion compiles and publishes a real, immutable
// workflow.WorkflowVersion the same way internal/adapters/sqlite's own
// compileWorkflowVersion/store.PublishWorkflowVersion test helpers do
// (workflow.Compile is a pure domain function; PublishWorkflowVersion is the
// one ports.DefinitionsRepository method every WorkflowVersion in this
// codebase goes through) — never StartWorkflowRun's own shortcut.
func publishTestWorkflowVersion(t *testing.T, uow *fake.UnitOfWork, projectID, definitionID, versionID string) workflow.WorkflowVersion {
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

// --- StartWorkflowRun: happy path ---

func TestStartWorkflowRun_CreatesRunManifestNodeRunEventAndJobAtomically_TransitionsWorkItemActive(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	result, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if result.RunID == "" || result.NodeRunID == "" || result.JobID == "" {
		t.Fatalf("result = %+v, want non-empty RunID/NodeRunID/JobID", result)
	}
	if result.FamilyID != root.FamilyID {
		t.Fatalf("result.FamilyID = %s, want %s", result.FamilyID, root.FamilyID)
	}
	if result.State != string(runtimedomain.WorkflowRunRunning) {
		t.Fatalf("result.State = %s, want RUNNING", result.State)
	}

	item, err := uow.Snapshot.Work().GetWorkItem(ctx, root.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want ACTIVE", item.Status)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var advanceJob *ports.EnqueueJobRequest
	for i := range jobs {
		if string(jobs[i].ID) == result.JobID {
			advanceJob = &jobs[i]
		}
	}
	if advanceJob == nil {
		t.Fatalf("no enqueued job with id %s, jobs = %+v", result.JobID, jobs)
	}
	if advanceJob.Kind != runtime.AdvanceRunJobKind || advanceJob.AggregateType != "WorkflowRun" || advanceJob.AggregateID != result.RunID {
		t.Fatalf("advance job = %+v, want Kind=%s AggregateType=WorkflowRun AggregateID=%s", advanceJob, runtime.AdvanceRunJobKind, result.RunID)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.AggregateType == "WorkflowRun" && e.AggregateID == result.RunID && e.EventType == "WorkflowRunStarted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no WorkflowRunStarted event for run %s, events = %+v", result.RunID, events)
	}
}

// --- StartWorkflowRun: precondition failures ---

func TestStartWorkflowRun_WorkItemNotReady_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")
	cmd := testCommand("idem-root-1", "hash-root-1", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	_, err = runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if !errors.Is(err, runtime.ErrWorkItemNotReady) {
		t.Fatalf("err = %v, want ErrWorkItemNotReady (work item is still BACKLOG)", err)
	}
}

func TestStartWorkflowRun_WorkspaceNotReady_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")
	cmd := testCommand("idem-root-1", "hash-root-1", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	// Force the work item READY without ever provisioning its WorkspaceSet
	// (still REQUESTED) — StartWorkflowRun must still refuse it.
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: root.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force work item READY: %v", err)
	}
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	_, err = runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if !errors.Is(err, runtime.ErrWorkspaceNotReady) {
		t.Fatalf("err = %v, want ErrWorkspaceNotReady (workspace set is still REQUESTED)", err)
	}
}

// --- StartWorkflowRun: GC-INV-39 cancellation fence ---

// TestStartWorkflowRun_WorkItemCancellationPending_RejectedByCAS is this
// task's own GC-INV-39 test: a pending work_item_cancellation_intent must
// fence StartWorkflowRun even though no real CancelWorkItem command exists
// yet (V4-12C) — recording the intent directly via
// ports.RuntimeRepository.RecordWorkItemCancellationIntent stands in for
// that future command's own first write, the same "poke the primitive
// directly, the future command's handler is out of this task's scope"
// discipline readyFixture already uses for WorkItem READY.
func TestStartWorkflowRun_WorkItemCancellationPending_RejectedByCAS(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	intent, err := runtimedomain.NewWorkItemCancellationIntent(
		"intent-1", "project-1", workdomain.WorkItemID(root.WorkItemID), "actor-1", "operator requested cancel", time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWorkItemCancellationIntent: %v", err)
	}
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordWorkItemCancellationIntent(ctx, intent)
		return err
	})
	if err != nil {
		t.Fatalf("RecordWorkItemCancellationIntent: %v", err)
	}

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	_, err = runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if !errors.Is(err, runtime.ErrWorkItemCancellationPending) {
		t.Fatalf("err = %v, want ErrWorkItemCancellationPending", err)
	}

	item, err := uow.Snapshot.Work().GetWorkItem(ctx, root.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemReady {
		t.Fatalf("work item status = %s, want still READY (rejected start must not touch it)", item.Status)
	}
}

// TestStartWorkflowRun_ThenWorkItemCancellationIntentRecorded_IntentStillSucceeds
// documents the OTHER commit order GC-INV-39/V4-02's own Verify line names
// ("start trước → Run mới được quiesce cùng các Run khác"): once a Run has
// already started, recording a work-item cancellation intent afterward must
// still succeed — StartWorkflowRun does not, and must not, hold any lock
// that blocks it. Actually quiescing the already-started Run alongside
// siblings is V4-12B's own coordinator, out of this task's scope entirely
// (RunID cancellation intents/CANCELLING wiring); this test only proves
// V4-02's own command never becomes an accidental obstacle to that future
// intent being durably recorded.
func TestStartWorkflowRun_ThenWorkItemCancellationIntentRecorded_IntentStillSucceeds(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	if _, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	}); err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	intent, err := runtimedomain.NewWorkItemCancellationIntent(
		"intent-1", "project-1", workdomain.WorkItemID(root.WorkItemID), "actor-1", "operator requested cancel", time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWorkItemCancellationIntent: %v", err)
	}
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordWorkItemCancellationIntent(ctx, intent)
		return err
	})
	if err != nil {
		t.Fatalf("RecordWorkItemCancellationIntent after start: %v", err)
	}
}

// --- StartWorkflowRun: idempotency ---

func TestStartWorkflowRun_DuplicateIdempotencyKey_ReplaysSameResultWithoutNewRun(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	req := runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())}
	first, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first StartWorkflowRun: %v", err)
	}
	second, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("replayed StartWorkflowRun: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	count := 0
	for _, j := range jobs {
		if j.Kind == runtime.AdvanceRunJobKind {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("advance run jobs = %d, want exactly 1 (replay must not enqueue a second one)", count)
	}
}

func TestStartWorkflowRun_SameIdempotencyKeyDifferentRequestHash_ReturnsReceiptConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	req := runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())}
	first := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	if _, err := runtime.StartWorkflowRun(ctx, uow, ids, first, req); err != nil {
		t.Fatalf("first StartWorkflowRun: %v", err)
	}
	second := testCommand("idem-start-1", "hash-b", ports.ProjectScope("project-1"), "StartWorkflowRun")
	_, err := runtime.StartWorkflowRun(ctx, uow, ids, second, req)
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("err = %v, want ErrReceiptConflict", err)
	}
}

// Concurrent-start races (this task's own "Hoàn thành khi: một WorkItem
// policy chỉ có số active run cho phép") are covered in
// commands_sqlite_test.go, not here: fake.UnitOfWork.run
// (internal/app/ports/fake/unitofwork.go) deliberately does NOT serialize
// genuinely concurrent callers by blocking — it takes its mutex only long
// enough to check/set an inTx guard and fails a truly concurrent second
// caller closed with ErrNestedTransaction (a single-threaded-test-misuse
// guard, not a queuing primitive), so a goroutine race against the fake
// mostly proves that guard fires, not this command's own CAS. The real
// *sqlite.Store.RunSerializedWrite (internal/adapters/sqlite/txrunner.go)
// genuinely queues concurrent writers (SQLite's own writer serialization
// plus busy-retry), so that is where a real "two callers race
// StartWorkflowRun for the same WorkItem, only one wins" proof belongs.
