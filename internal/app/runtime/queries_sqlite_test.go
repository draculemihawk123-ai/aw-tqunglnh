package runtime_test

// Real-sqlite coverage for queries.go (V6-07B): every query in that file is
// exercised here against a real *sqlite.Store/ports.UnitOfWork, never a
// mock or a direct row fabrication (this repo's own hard rule — mirrors
// internal/app/work/queries_sqlite_test.go's own identical pattern for
// V6-04's queries.go). openSQLiteStore/seedActiveRepositorySQLite
// (commands_sqlite_test.go, same package) are reused directly rather than
// duplicated. HTTP-level coverage for these same queries, driven through a
// real internal/delivery/httpapi/evidence server, lives in that package's
// own test files; this file additionally covers scope-check/defense-in-
// depth edge cases that are awkward to construct end to end through HTTP
// (e.g. a data-integrity edge case where an Evidence row's own
// ArtifactReferences names an artifact from a DIFFERENT project than the
// Evidence row itself).

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// seedQueriesRootWorkItem creates a root WorkItem via the real
// CreateRootWorkItem command against an ALREADY-active repository (the
// caller must call seedActiveRepositorySQLite itself, once per project —
// calling it again for the same projectID would try to re-create a
// project with a duplicate ID). BACKLOG is fine here (unlike
// readyFixtureSQLite, no test in this file needs a READY WorkItem, since
// none of queries.go's own scope checks care about Status).
func seedQueriesRootWorkItem(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, suffix string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-root-q-" + suffix, IdempotencyKey: "idem-root-q-" + suffix, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-q-" + suffix,
	}
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Task " + suffix,
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/" + suffix}, Reason: "seed " + suffix,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", suffix, err)
	}
	return root
}

// seedQueriesAttempt builds a minimal (non-dispatchable) WorkflowRun->
// NodeRun->ExecutionAttempt chain via the low-level CreateWorkflowRun/
// CreateNodeRun/CreateExecutionAttempt Tx methods — mirrors
// internal/delivery/httpapi/message/context_snapshot_test.go's own
// seedFakeExecutionAttempt, extended to return every one of the three IDs
// this file's own Evidence fixtures need.
func seedQueriesAttempt(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, familyID, suffix string) (runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID("wf-def-" + suffix), ProjectID: &pid,
		Name: "workflow " + suffix, Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID("wf-ver-" + suffix), VersionNumber: 1,
		Document: workflow.WorkflowDocument{
			SchemaVersion: "1",
			Nodes: []workflow.Node{
				{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
				{Key: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
		},
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("workflow.Compile: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, version)
		return err
	}); err != nil {
		t.Fatalf("PublishWorkflowVersion: %v", err)
	}

	run, err := runtimedomain.NewWorkflowRun(
		runtimedomain.WorkflowRunID("wf-run-"+suffix), project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		version, workdomain.TaskFamilyID(familyID), 1, []byte(`{}`),
	)
	if err != nil {
		t.Fatalf("NewWorkflowRun: %v", err)
	}
	nodeRun, err := runtimedomain.NewNodeRun(runtimedomain.NodeRunID("node-run-"+suffix), run.ID, "agent", 1, 0, nil, "sha256:input-fixture", "sha256:profile-fixture")
	if err != nil {
		t.Fatalf("NewNodeRun: %v", err)
	}
	attempt, err := runtimedomain.NewExecutionAttempt(
		runtimedomain.ExecutionAttemptID("attempt-"+suffix), nodeRun.ID, 1, "sha256:profile-fixture", "fake-provider", nil,
	)
	if err != nil {
		t.Fatalf("NewExecutionAttempt: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().CreateWorkflowRun(ctx, run); err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}
		_, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt)
		return err
	}); err != nil {
		t.Fatalf("seed queries attempt: %v", err)
	}
	return string(run.ID), string(nodeRun.ID), string(attempt.ID)
}

func seedQueriesEvidence(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, runID, nodeRunID, attemptID, kind, verdict string, artifactRefs []string) runtimedomain.Evidence {
	t.Helper()
	ctx := context.Background()
	revisions, err := workspace.NewRevisionSet(nil)
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	evidence, err := runtimedomain.NewEvidence(
		runtimedomain.EvidenceID("evidence-"+attemptID+"-"+kind), project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		runtimedomain.WorkflowRunID(runID), runtimedomain.NodeRunID(nodeRunID), runtimedomain.ExecutionAttemptID(attemptID),
		kind, verdict, artifactRefs, revisions, "policy-v1", time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewEvidence: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateEvidence(ctx, evidence)
		return err
	}); err != nil {
		t.Fatalf("CreateEvidence: %v", err)
	}
	return evidence
}

func seedQueriesArtifact(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, locator, contentHash string, size int64) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	a, err := artifact.NewArtifact(
		artifact.ID(ids.NewID()), project.ProjectID(projectID), locator, contentHash, size, "text/plain",
		redact.Public, false, artifact.RetentionRawOutputTemp, artifact.Attached, false, artifact.ComputeExpiresAt(artifact.RetentionRawOutputTemp, now), now, 1,
	)
	if err != nil {
		t.Fatalf("NewArtifact: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}
	return a
}

func TestListEvidenceForWorkItem_InstallationScope_RejectsAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "list-evidence-installation-scope.db")
	uow := sqlite.NewUnitOfWork(store)

	_, err := runtime.ListEvidenceForWorkItem(context.Background(), uow, ports.InstallationScope(), "wi-1", runtime.EvidenceFilter{})
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestListEvidenceForWorkItem_UnknownWorkItem_ReturnsPersistenceNotFound(t *testing.T) {
	store := openSQLiteStore(t, "list-evidence-unknown-workitem.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")

	_, err := runtime.ListEvidenceForWorkItem(context.Background(), uow, ports.ProjectScope("project-1"), "does-not-exist", runtime.EvidenceFilter{})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ErrPersistenceNotFound", err)
	}
}

func TestListEvidenceForWorkItem_OrdersByCreatedAtThenKind(t *testing.T) {
	store := openSQLiteStore(t, "list-evidence-order.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	root := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "1")
	runID, nodeRunID, attemptID := seedQueriesAttempt(t, uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := seedQueriesArtifact(t, uow, ids, "project-1", "sha256:"+seq64(1), "sha256:"+seq64(1), 10)

	seedQueriesEvidence(t, uow, "project-1", root.WorkItemID, runID, nodeRunID, attemptID, "zzz-last", "PASS", []string{string(a.ID)})
	seedQueriesEvidence(t, uow, "project-1", root.WorkItemID, runID, nodeRunID, attemptID, "aaa-first", "PASS", []string{string(a.ID)})

	items, err := runtime.ListEvidenceForWorkItem(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID, runtime.EvidenceFilter{})
	if err != nil {
		t.Fatalf("ListEvidenceForWorkItem: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	// Assert the result is actually sorted by (CreatedAt, Kind) rather than
	// asserting one hardcoded expected order: real wall-clock CreatedAt
	// values may or may not land in the same second depending on machine
	// speed, so either a strict CreatedAt-ascending order OR (when both
	// timestamps truly tie) the Kind tiebreaker is correct — what must
	// never happen is CreatedAt going backwards, or (on a real tie) Kind
	// going backwards.
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		if cur.CreatedAt.Before(prev.CreatedAt) {
			t.Fatalf("items[%d].CreatedAt (%s) is before items[%d].CreatedAt (%s): not ordered", i, cur.CreatedAt, i-1, prev.CreatedAt)
		}
		if cur.CreatedAt.Equal(prev.CreatedAt) && cur.Kind < prev.Kind {
			t.Fatalf("items[%d].Kind (%s) < items[%d].Kind (%s) despite equal CreatedAt: tiebreaker not applied", i, cur.Kind, i-1, prev.Kind)
		}
	}
	gotKinds := map[string]bool{}
	for _, item := range items {
		gotKinds[item.Kind] = true
	}
	if !gotKinds["aaa-first"] || !gotKinds["zzz-last"] {
		t.Fatalf("items = %+v, want both aaa-first and zzz-last present", items)
	}
}

func TestGetEvidence_WrongWorkItem_ReturnsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "get-evidence-wrong-workitem.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	rootA := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "a")
	rootB := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "b")
	runID, nodeRunID, attemptID := seedQueriesAttempt(t, uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "a")
	a := seedQueriesArtifact(t, uow, ids, "project-1", "sha256:"+seq64(1), "sha256:"+seq64(1), 10)
	evidence := seedQueriesEvidence(t, uow, "project-1", rootA.WorkItemID, runID, nodeRunID, attemptID, "kind-a", "PASS", []string{string(a.ID)})

	_, err := runtime.GetEvidence(context.Background(), uow, ports.ProjectScope("project-1"), rootB.WorkItemID, string(evidence.ID))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

// TestListArtifactsForEvidence_ReferencesForeignProjectArtifact_RejectsAsScopeMismatch
// is the defense-in-depth check queries.go's own doc comment names: even
// though this shape should never arise from any real production write path
// (an Evidence row's own ArtifactReferences should always name artifacts
// from its own project), ListArtifactsForEvidence must never silently
// return or crash on a foreign-project artifact if the data ever drifted —
// it must reject exactly like any other scope violation.
func TestListArtifactsForEvidence_ReferencesForeignProjectArtifact_RejectsAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "list-artifacts-foreign-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	root := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "1")
	seedActiveRepositorySQLite(t, uow, ids, "project-2", "repo-b")
	runID, nodeRunID, attemptID := seedQueriesAttempt(t, uow, "project-1", root.WorkItemID, root.FamilyID, "1")

	foreignArtifact := seedQueriesArtifact(t, uow, ids, "project-2", "sha256:"+seq64(1), "sha256:"+seq64(1), 10)
	evidence := seedQueriesEvidence(t, uow, "project-1", root.WorkItemID, runID, nodeRunID, attemptID, "kind-a", "PASS", []string{string(foreignArtifact.ID)})

	_, err := runtime.ListArtifactsForEvidence(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID, string(evidence.ID))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestResolveEvidenceArtifactContent_ArtifactNotInEvidenceReferences_ReturnsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "resolve-artifact-content-not-referenced.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	root := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "1")
	runID, nodeRunID, attemptID := seedQueriesAttempt(t, uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	referenced := seedQueriesArtifact(t, uow, ids, "project-1", "sha256:"+seq64(1), "sha256:"+seq64(1), 10)
	unreferenced := seedQueriesArtifact(t, uow, ids, "project-1", "sha256:"+seq64(2), "sha256:"+seq64(2), 10)
	evidence := seedQueriesEvidence(t, uow, "project-1", root.WorkItemID, runID, nodeRunID, attemptID, "kind-a", "PASS", []string{string(referenced.ID)})

	_, _, err := runtime.ResolveEvidenceArtifactContent(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID, string(evidence.ID), string(unreferenced.ID))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestResolveEvidenceArtifactContent_HappyPath_ReturnsRealRef(t *testing.T) {
	store := openSQLiteStore(t, "resolve-artifact-content-happy.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	root := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "1")
	runID, nodeRunID, attemptID := seedQueriesAttempt(t, uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := seedQueriesArtifact(t, uow, ids, "project-1", "sha256:"+seq64(1), "sha256:"+seq64(1), 42)
	evidence := seedQueriesEvidence(t, uow, "project-1", root.WorkItemID, runID, nodeRunID, attemptID, "kind-a", "PASS", []string{string(a.ID)})

	ref, summary, err := runtime.ResolveEvidenceArtifactContent(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID, string(evidence.ID), string(a.ID))
	if err != nil {
		t.Fatalf("ResolveEvidenceArtifactContent: %v", err)
	}
	if ref.Locator != a.Locator || ref.SHA256 != a.ContentHash || ref.Size != a.Size {
		t.Fatalf("ref = %+v, want Locator=%s SHA256=%s Size=%d", ref, a.Locator, a.ContentHash, a.Size)
	}
	if summary.ArtifactID != string(a.ID) {
		t.Fatalf("summary.ArtifactID = %q, want %q", summary.ArtifactID, a.ID)
	}
}

func TestGetContextSnapshot_WrongWorkItem_ReturnsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "get-context-snapshot-wrong-workitem.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	rootA := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "a")
	rootB := seedQueriesRootWorkItem(t, uow, ids, "project-1", "repo-a", "b")
	_, _, attemptID := seedQueriesAttempt(t, uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "a")

	revisions, err := workspace.NewRevisionSet(nil)
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	snapshot, err := contextsnapshot.NewSnapshot(
		contextsnapshot.ID("snapshot-1"), project.ProjectID("project-1"), workdomain.WorkItemID(rootA.WorkItemID),
		contextsnapshot.AttemptID(attemptID), nil, nil, nil, revisions, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.ContextSnapshots().CreateSnapshot(context.Background(), snapshot)
		return err
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	_, err = runtime.GetContextSnapshot(context.Background(), uow, ports.ProjectScope("project-1"), rootB.WorkItemID, string(snapshot.ID))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

// seq64 returns a deterministic, distinct 64-character lowercase-hex-shaped
// string for fixture Locators/ContentHashes — these tests never touch a
// real ports.ArtifactStore (InsertArtifact only needs a well-formed row, no
// real bytes), so a real sha256 digest is unnecessary; only well-formed and
// distinct per call matters here.
func seq64(n int) string {
	suffix := strconv.Itoa(n)
	return strings.Repeat("0", 64-len(suffix)) + suffix
}
