package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// TestMigration0016_NewTablesExistAndEnforceCheckConstraints is V4-01's own
// "upgrade/restart/tamper pin tests": every new table from migration 0016
// exists after a fresh migrate, and each widened/new state CHECK constraint
// rejects a value outside its closed enum.
func TestMigration0016_NewTablesExistAndEnforceCheckConstraints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	for _, table := range []string{
		"execution_manifests", "run_manifest_amendments", "branch_tokens",
		"decision_artifacts", "run_cancellation_intents", "work_item_cancellation_intents",
	} {
		var name string
		if err := store.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migration: %v", table, err)
		}
	}

	seedWorkflowRunOwners(t, ctx, store)
	definition := testWorkflowDefinition()
	version := compileWorkflowVersion(t, definition, "wf-tamper", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	now := "2026-08-28T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_runs (
    id, project_id, work_item_id, workflow_version_id, family_id,
    scope_version, state, shared_state_json, version, created_at, updated_at
) VALUES ('tamper-run', 'project-1', 'work-item-1', ?, 'family-1', 1, 'RUNNING', '{}', 1, ?, ?)`,
		version.ID(), now, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE workflow_runs SET state = 'NOT_A_STATE' WHERE id = 'tamper-run'`); err == nil {
		t.Fatal("expected CHECK constraint to reject an invalid workflow_runs.state value")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workflow_runs SET state = 'CANCELLING' WHERE id = 'tamper-run'`); err != nil {
		t.Fatalf("CANCELLING should be accepted by workflow_runs.state CHECK: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workflow_runs SET state = 'VERIFYING' WHERE id = 'tamper-run'`); err != nil {
		t.Fatalf("VERIFYING should be accepted by workflow_runs.state CHECK: %v", err)
	}

	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "tamper-attempt"); err != nil {
		t.Fatalf("seed fixture execution attempt: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE execution_attempts SET state = 'NOT_A_STATE' WHERE id = 'tamper-attempt'`); err == nil {
		t.Fatal("expected CHECK constraint to reject an invalid execution_attempts.state value")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE execution_attempts SET state = 'BLOCKED' WHERE id = 'tamper-attempt'`); err != nil {
		t.Fatalf("BLOCKED should be accepted by execution_attempts.state CHECK: %v", err)
	}
}

// TestExecutionAttemptBlocked_PersistsAndReloadsAcrossRestart is the other
// half of V4-01's "test khẳng định BLOCKED và CANCELLING persist và reload
// đúng qua restart".
func TestExecutionAttemptBlocked_PersistsAndReloadsAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedWorkflowRunOwners(t, ctx, store)
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "blocked-attempt"); err != nil {
		t.Fatalf("seed fixture execution attempt: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE execution_attempts
SET state = 'BLOCKED', termination_reason = ?, version = version + 1
WHERE id = 'blocked-attempt' AND state = 'RUNNING'`,
		string(runtime.TerminationReasonScopeExpansionRequired),
	); err != nil {
		t.Fatalf("transition attempt to BLOCKED: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	restarted, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer restarted.Close()

	state, version, err := restarted.LoadExecutionAttemptState(ctx, "blocked-attempt")
	if err != nil {
		t.Fatalf("load execution attempt state after restart: %v", err)
	}
	if state != runtime.ExecutionAttemptBlocked || version != 2 {
		t.Fatalf("resumed attempt = (state=%s, version=%d), want (BLOCKED, 2)", state, version)
	}

	var reason string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT termination_reason FROM execution_attempts WHERE id = 'blocked-attempt'`,
	).Scan(&reason); err != nil {
		t.Fatalf("read termination reason after restart: %v", err)
	}
	if runtime.TerminationReason(reason) != runtime.TerminationReasonScopeExpansionRequired {
		t.Fatalf("termination reason after restart = %s, want %s", reason, runtime.TerminationReasonScopeExpansionRequired)
	}
}

// TestWorkflowRunCancelling_PersistsAndReloadsAcrossRestart exercises the
// real CompareAndSwapWorkflowRun CAS path (not a raw-SQL fixture) so the
// widened state enum is proven through the actual application contract, not
// just the schema.
func TestWorkflowRunCancelling_PersistsAndReloadsAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	store := openWorkflowTestStore(t, ctx, dbPath)
	seedWorkflowRunOwners(t, ctx, store)

	definition := testWorkflowDefinition()
	version := compileWorkflowVersion(t, definition, "wf-cancelling", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	run, err := runtime.NewWorkflowRun("run-cancelling", "project-1", "work-item-1", version, "family-1", 1, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("new workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	running, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: run.ID, ExpectedState: runtime.WorkflowRunCreated, ExpectedVersion: 1,
		NextState: runtime.WorkflowRunRunning, SharedState: json.RawMessage(`{}`), OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("transition to RUNNING: %v", err)
	}
	cancelling, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: run.ID, ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: running.Version,
		NextState: runtime.WorkflowRunCancelling, SharedState: json.RawMessage(`{}`), OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("transition to CANCELLING: %v", err)
	}
	if cancelling.State != runtime.WorkflowRunCancelling {
		t.Fatalf("state after CAS = %s, want CANCELLING", cancelling.State)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	restarted := openWorkflowTestStore(t, ctx, dbPath)
	defer restarted.Close()
	resumed, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load run after restart: %v", err)
	}
	if resumed.State != runtime.WorkflowRunCancelling || resumed.Version != cancelling.Version {
		t.Fatalf("resumed run = %#v, want state CANCELLING version %d", resumed, cancelling.Version)
	}
}

// runtimeTestFixture opens a fresh, fully migrated Store with one project,
// one task family, one root work item and one published WorkflowVersion —
// enough scaffolding for every RuntimeRepository contract test below,
// mirroring seedWorkflowRunOwners/testWorkflowDefinition's own shape.
type runtimeTestFixture struct {
	store   *Store
	uow     *UnitOfWorkAdapter
	version workflow.WorkflowVersion
}

func newRuntimeTestFixture(t *testing.T, ctx context.Context) runtimeTestFixture {
	t.Helper()
	store := openWorkflowTestStore(t, ctx, filepath.Join(t.TempDir(), "agentkit.db"))
	t.Cleanup(func() { _ = store.Close() })
	seedWorkflowRunOwners(t, ctx, store)
	definition := testWorkflowDefinition()
	version := compileWorkflowVersion(t, definition, "wf-runtime-repo", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	return runtimeTestFixture{store: store, uow: NewUnitOfWork(store), version: version}
}

func (f runtimeTestFixture) createRun(t *testing.T, ctx context.Context, runID string) runtime.WorkflowRun {
	t.Helper()
	run, err := runtime.NewWorkflowRun(runtime.WorkflowRunID(runID), "project-1", "work-item-1", f.version, "family-1", 1, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("new workflow run: %v", err)
	}
	if err := f.store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	return run
}

func exampleRevisionSet(t *testing.T) workspace.RevisionSet {
	t.Helper()
	set, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-1", VCSObjectID: "commit-1", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("new revision set: %v", err)
	}
	return set
}

func TestCreateExecutionManifest_ImmutablePinsAndCrossProjectRejection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)
	run := fixture.createRun(t, ctx, "run-manifest")

	manifest, err := runtime.NewExecutionManifest(
		"manifest-1", run.ID, fixture.version.ID(), fixture.version.ContentHash(),
		fixture.version.Dependencies(), exampleRevisionSet(t), "", "", time.Now(),
	)
	if err != nil {
		t.Fatalf("new execution manifest: %v", err)
	}

	var created runtime.ExecutionManifest
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		created, err = tx.Runtime().CreateExecutionManifest(ctx, manifest)
		return err
	}); err != nil {
		t.Fatalf("create execution manifest: %v", err)
	}
	if created.RunID != run.ID || created.CompiledSnapshotHash != fixture.version.ContentHash() {
		t.Fatalf("created manifest = %#v", created)
	}

	// Idempotent: an identical second call for the same run returns the
	// already-stored manifest rather than erroring or duplicating.
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		again, err := tx.Runtime().CreateExecutionManifest(ctx, manifest)
		if err != nil {
			return err
		}
		if again.RunID != created.RunID {
			t.Fatalf("idempotent create returned a different manifest: %#v", again)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent create execution manifest: %v", err)
	}

	// A second call for the same run with a different pin must never
	// silently overwrite the initial manifest (V4-01's own "Hoàn thành
	// khi").
	drifted := manifest
	drifted.CompiledSnapshotHash = "sha256:different-pin"
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateExecutionManifest(ctx, drifted)
		return err
	}); !errors.Is(err, ports.ErrImmutableVersionConflict) {
		t.Fatalf("drifted pin error = %v, want ErrImmutableVersionConflict", err)
	}
	if err := fixture.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		stored, err := tx.Runtime().GetExecutionManifest(ctx, string(run.ID))
		if err != nil {
			return err
		}
		if stored.CompiledSnapshotHash != fixture.version.ContentHash() {
			t.Fatalf("initial manifest was overwritten: %#v", stored)
		}
		return nil
	}); err != nil {
		t.Fatalf("reload execution manifest: %v", err)
	}

	// Cross-project rejection: a WorkflowVersion belonging to a different
	// project must never be pinned into this run's manifest.
	otherProjectID := project.ProjectID("project-other")
	otherDefinition := workflow.WorkflowDefinition{
		ID: "workflow-definition-other", ProjectID: &otherProjectID, Name: "Other project workflow",
		Status: workflow.DefinitionActive, Version: 1,
	}
	otherVersion := compileWorkflowVersion(t, otherDefinition, "wf-other-project", 1, workflowDocumentV1(), "1")
	now := "2026-08-28T00:00:00Z"
	if _, err := fixture.store.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, status, version, created_at, updated_at) VALUES (?, 'Other', 'ACTIVE', 1, ?, ?)`,
		otherProjectID, now, now); err != nil {
		t.Fatalf("seed other project: %v", err)
	}
	if _, err := fixture.store.PublishWorkflowVersion(ctx, otherDefinition, otherVersion); err != nil {
		t.Fatalf("publish other project version: %v", err)
	}
	crossProject, err := runtime.NewExecutionManifest(
		"manifest-cross", "run-manifest-cross", otherVersion.ID(), otherVersion.ContentHash(),
		otherVersion.Dependencies(), exampleRevisionSet(t), "", "", time.Now(),
	)
	if err != nil {
		t.Fatalf("new cross-project execution manifest: %v", err)
	}
	fixture.createRun(t, ctx, "run-manifest-cross")
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateExecutionManifest(ctx, crossProject)
		return err
	}); !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("cross-project pin error = %v, want ErrCrossProjectReference", err)
	}
}

func TestAppendRunManifestAmendment_AppendOnlyAndDoesNotOverwriteInitialManifest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)
	run := fixture.createRun(t, ctx, "run-amendment")
	manifest, err := runtime.NewExecutionManifest(
		"manifest-amend", run.ID, fixture.version.ID(), fixture.version.ContentHash(),
		fixture.version.Dependencies(), exampleRevisionSet(t), "", "", time.Now(),
	)
	if err != nil {
		t.Fatalf("new execution manifest: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateExecutionManifest(ctx, manifest)
		return err
	}); err != nil {
		t.Fatalf("create execution manifest: %v", err)
	}

	first, err := runtime.NewRunManifestAmendment("amend-1", run.ID, 1, 0, 2, "scope expansion 1", "operator-1", time.Now(), "sha256:amend-1")
	if err != nil {
		t.Fatalf("new amendment 1: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().AppendRunManifestAmendment(ctx, first)
		return err
	}); err != nil {
		t.Fatalf("append amendment 1: %v", err)
	}

	second, err := runtime.NewRunManifestAmendment("amend-2", run.ID, 2, 1, 3, "scope expansion 2", "operator-1", time.Now(), "sha256:amend-2")
	if err != nil {
		t.Fatalf("new amendment 2: %v", err)
	}

	// A second, independently-built candidate racing for the exact same
	// next revision (previous=1, revision=2 — both domain-valid on their
	// own) loses to whichever commits first: the loser's own
	// PreviousRevision no longer matches the run's real high-water mark.
	stale, err := runtime.NewRunManifestAmendment("amend-2-stale", run.ID, 2, 1, 3, "racing candidate", "operator-2", time.Now(), "sha256:amend-2-stale")
	if err != nil {
		t.Fatalf("new stale amendment: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().AppendRunManifestAmendment(ctx, second)
		return err
	}); err != nil {
		t.Fatalf("append amendment 2: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().AppendRunManifestAmendment(ctx, stale)
		return err
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale racing amendment error = %v, want ErrOptimisticConflict", err)
	}

	if err := fixture.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		amendments, err := tx.Runtime().ListRunManifestAmendments(ctx, string(run.ID))
		if err != nil {
			return err
		}
		if len(amendments) != 2 || amendments[0].Revision != 1 || amendments[1].Revision != 2 {
			t.Fatalf("amendments = %#v, want revisions [1, 2]", amendments)
		}
		stillInitial, err := tx.Runtime().GetExecutionManifest(ctx, string(run.ID))
		if err != nil {
			return err
		}
		if stillInitial.CompiledSnapshotHash != fixture.version.ContentHash() {
			t.Fatalf("initial manifest changed after amendments: %#v", stillInitial)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify amendments: %v", err)
	}
}

func TestCreateBranchToken_IdempotentByRunForkBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)
	run := fixture.createRun(t, ctx, "run-branch")

	token, err := runtime.NewBranchToken("branch-1", run.ID, "fork-node", "branch-a", "child-node")
	if err != nil {
		t.Fatalf("new branch token: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateBranchToken(ctx, token)
		return err
	}); err != nil {
		t.Fatalf("create branch token: %v", err)
	}

	// Idempotent replay with identical CurrentNodeKey.
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		again, err := tx.Runtime().CreateBranchToken(ctx, token)
		if err != nil {
			return err
		}
		if again.ID != token.ID {
			t.Fatalf("idempotent branch token = %#v", again)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent create branch token: %v", err)
	}

	// A different CurrentNodeKey for the same (run, fork, branch) is a
	// conflict, not a silent overwrite.
	drifted := token
	drifted.CurrentNodeKey = "other-node"
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateBranchToken(ctx, drifted)
		return err
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("drifted branch token error = %v, want ErrOptimisticConflict", err)
	}

	second, err := runtime.NewBranchToken("branch-2", run.ID, "fork-node", "branch-b", "child-node-2")
	if err != nil {
		t.Fatalf("new branch token 2: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateBranchToken(ctx, second)
		return err
	}); err != nil {
		t.Fatalf("create branch token 2: %v", err)
	}
	if err := fixture.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		tokens, err := tx.Runtime().ListBranchTokensForRun(ctx, string(run.ID))
		if err != nil {
			return err
		}
		if len(tokens) != 2 {
			t.Fatalf("branch tokens = %#v, want 2", tokens)
		}
		return nil
	}); err != nil {
		t.Fatalf("list branch tokens: %v", err)
	}
}

func TestRecordDecisionArtifact_ImmutableAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)

	artifact, err := runtime.NewDecisionArtifact(
		"decision-1", "project-1", "SCOPE_WAIVE", "policy-v1",
		json.RawMessage(`{"reason":"test"}`), json.RawMessage(`{"outcome":"WAIVED"}`), time.Now(),
	)
	if err != nil {
		t.Fatalf("new decision artifact: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordDecisionArtifact(ctx, artifact)
		return err
	}); err != nil {
		t.Fatalf("record decision artifact: %v", err)
	}

	// A second call with the same ID is not idempotent replace — it is a
	// programming/race error: decision_artifacts is immutable and
	// append-only, never updated.
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordDecisionArtifact(ctx, artifact)
		return err
	}); !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("duplicate decision artifact error = %v, want ErrPersistenceAlreadyExists", err)
	}

	if err := fixture.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Runtime().GetDecisionArtifact(ctx, "decision-1")
		if err != nil {
			return err
		}
		if loaded.Kind != "SCOPE_WAIVE" || loaded.PolicyVersion != "policy-v1" {
			t.Fatalf("loaded decision artifact = %#v", loaded)
		}
		return nil
	}); err != nil {
		t.Fatalf("get decision artifact: %v", err)
	}
}

func TestRunCancellationIntent_IdempotentPerRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)
	run := fixture.createRun(t, ctx, "run-cancel-intent")

	first, err := runtime.NewRunCancellationIntent("intent-1", "project-1", run.ID, "operator-1", "user requested", time.Now())
	if err != nil {
		t.Fatalf("new run cancellation intent: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordRunCancellationIntent(ctx, first)
		return err
	}); err != nil {
		t.Fatalf("record run cancellation intent: %v", err)
	}

	// A second, differently-reasoned intent for the same run is idempotent:
	// it returns the first intent, never a second row (ADR-020's "CancelRun
	// idempotent theo run").
	second, err := runtime.NewRunCancellationIntent("intent-2", "project-1", run.ID, "operator-2", "different reason", time.Now())
	if err != nil {
		t.Fatalf("new second run cancellation intent: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		result, err := tx.Runtime().RecordRunCancellationIntent(ctx, second)
		if err != nil {
			return err
		}
		if result.ID != first.ID {
			t.Fatalf("second RecordRunCancellationIntent = %#v, want the first intent returned unchanged", result)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent record run cancellation intent: %v", err)
	}

	if err := fixture.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Runtime().GetRunCancellationIntent(ctx, string(run.ID))
		if err != nil {
			return err
		}
		if loaded.Actor != "operator-1" || loaded.State != runtime.CancellationIntentRequested {
			t.Fatalf("loaded run cancellation intent = %#v", loaded)
		}
		return nil
	}); err != nil {
		t.Fatalf("get run cancellation intent: %v", err)
	}
}

func TestWorkItemCancellationIntent_IdempotentPerWorkItem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRuntimeTestFixture(t, ctx)

	first, err := runtime.NewWorkItemCancellationIntent("wi-intent-1", "project-1", "work-item-1", "operator-1", "user requested", time.Now())
	if err != nil {
		t.Fatalf("new work item cancellation intent: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().RecordWorkItemCancellationIntent(ctx, first)
		return err
	}); err != nil {
		t.Fatalf("record work item cancellation intent: %v", err)
	}

	second, err := runtime.NewWorkItemCancellationIntent("wi-intent-2", "project-1", "work-item-1", "operator-2", "different reason", time.Now())
	if err != nil {
		t.Fatalf("new second work item cancellation intent: %v", err)
	}
	if err := fixture.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		result, err := tx.Runtime().RecordWorkItemCancellationIntent(ctx, second)
		if err != nil {
			return err
		}
		if result.ID != first.ID {
			t.Fatalf("second RecordWorkItemCancellationIntent = %#v, want the first intent returned unchanged", result)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent record work item cancellation intent: %v", err)
	}
}
