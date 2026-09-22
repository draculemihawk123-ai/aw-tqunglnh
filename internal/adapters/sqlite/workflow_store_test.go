package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func TestWorkflowVersionAndRunSurviveRestartWithPinnedSnapshot(t *testing.T) {
	ctx := context.Background()
	databasePath := migratedDatabasePath(t, "agentkit.db")
	store := openWorkflowTestStore(t, ctx, databasePath)
	seedWorkflowRunOwners(t, ctx, store)

	definition := testWorkflowDefinition()
	v1 := compileWorkflowVersion(t, definition, "workflow-v1", 1, workflowDocumentV1(), "1")
	persistedV1, err := store.PublishWorkflowVersion(ctx, definition, v1)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	assertSameWorkflowVersion(t, persistedV1, v1)

	loadedV1, err := store.LoadWorkflowVersion(ctx, v1.ID())
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	assertSameWorkflowVersion(t, loadedV1, v1)

	// A caller may retry a semantic publish with a fresh candidate identity.
	// The store returns the existing immutable snapshot rather than adding a
	// duplicate version.
	idempotentCandidate := compileWorkflowVersion(
		t,
		definition,
		"workflow-v1-retry",
		2,
		workflowDocumentV1(),
		"1",
	)
	idempotentResult, err := store.PublishWorkflowVersion(ctx, definition, idempotentCandidate)
	if err != nil {
		t.Fatalf("idempotent publish: %v", err)
	}
	if idempotentResult.ID() != v1.ID() {
		t.Fatalf("idempotent publish returned %s, want existing %s", idempotentResult.ID(), v1.ID())
	}

	// Reusing an immutable ID for different canonical content must fail.
	conflictingV1 := compileWorkflowVersion(t, definition, v1.ID(), 2, workflowDocumentV2(), "2")
	if _, err := store.PublishWorkflowVersion(ctx, definition, conflictingV1); !errors.Is(err, ports.ErrImmutableVersionConflict) {
		t.Fatalf("republish changed v1 error = %v, want ErrImmutableVersionConflict", err)
	}

	run, err := runtime.NewWorkflowRun(
		"run-1",
		"project-1",
		"work-item-1",
		v1,
		"family-1",
		1,
		json.RawMessage(`{"checkpoint":"created"}`),
	)
	if err != nil {
		t.Fatalf("new workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}

	running, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID:           run.ID,
		ExpectedState:   runtime.WorkflowRunCreated,
		ExpectedVersion: 1,
		NextState:       runtime.WorkflowRunRunning,
		SharedState:     json.RawMessage(`{"checkpoint":"node-a"}`),
		OccurredAt:      time.Date(2026, 8, 28, 1, 2, 3, 4, time.UTC),
	})
	if err != nil {
		t.Fatalf("transition run to running: %v", err)
	}
	if running.Version != 2 || running.State != runtime.WorkflowRunRunning || running.StartedAt == nil {
		t.Fatalf("running state = %#v", running)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close before simulated restart: %v", err)
	}
	restarted := openWorkflowTestStore(t, ctx, databasePath)
	defer func() { _ = restarted.Close() }()

	resumed, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load run after restart: %v", err)
	}
	if resumed.State != runtime.WorkflowRunRunning || resumed.Version != 2 || resumed.StartedAt == nil {
		t.Fatalf("resumed state = %#v", resumed)
	}
	if resumed.WorkflowVersionID != v1.ID() || resumed.WorkflowVersionHash != v1.ContentHash() {
		t.Fatalf(
			"resumed run pin = %s/%s, want %s/%s",
			resumed.WorkflowVersionID,
			resumed.WorkflowVersionHash,
			v1.ID(),
			v1.ContentHash(),
		)
	}
	if !reflect.DeepEqual(resumed.PinnedDependencies, v1.Dependencies()) {
		t.Fatalf("resumed dependencies = %#v, want %#v", resumed.PinnedDependencies, v1.Dependencies())
	}
	if string(resumed.SharedState) != `{"checkpoint":"node-a"}` {
		t.Fatalf("resumed shared state = %s", resumed.SharedState)
	}

	// Publish v2 only after R1 already exists. Loading R1 must still resolve the
	// exact v1 graph/hash/dependencies that were pinned when R1 started.
	v2 := compileWorkflowVersion(t, definition, "workflow-v2", 2, workflowDocumentV2(), "2")
	if _, err := restarted.PublishWorkflowVersion(ctx, definition, v2); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	pinnedAfterV2, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load R1 after publishing v2: %v", err)
	}
	if pinnedAfterV2.WorkflowVersionID != v1.ID() || pinnedAfterV2.WorkflowVersionHash != v1.ContentHash() {
		t.Fatalf("R1 moved after v2 publish: %#v", pinnedAfterV2)
	}

	waiting, err := restarted.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID:           run.ID,
		ExpectedState:   runtime.WorkflowRunRunning,
		ExpectedVersion: 2,
		NextState:       runtime.WorkflowRunWaiting,
		SharedState:     json.RawMessage(`{"checkpoint":"awaiting-worker-finalization"}`),
		OccurredAt:      time.Date(2026, 8, 28, 1, 3, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("resume and pause run: %v", err)
	}
	if waiting.State != runtime.WorkflowRunWaiting || waiting.Version != 3 || waiting.FinishedAt != nil {
		t.Fatalf("waiting state = %#v", waiting)
	}
	if waiting.WorkflowVersionID != v1.ID() || waiting.WorkflowVersionHash != v1.ContentHash() {
		t.Fatalf("waiting R1 lost its pin: %#v", waiting)
	}
}

func TestWorkflowRunCompareAndSwapAllowsExactlyOneConcurrentWinner(t *testing.T) {
	ctx := context.Background()
	store := openWorkflowTestStore(t, ctx, migratedDatabasePath(t, "agentkit.db"))
	defer func() { _ = store.Close() }()
	seedWorkflowRunOwners(t, ctx, store)

	definition := testWorkflowDefinition()
	version := compileWorkflowVersion(t, definition, "workflow-v1", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		"run-cas",
		"project-1",
		"work-item-1",
		version,
		"family-1",
		1,
		json.RawMessage(`{"winner":null}`),
	)
	if err != nil {
		t.Fatalf("new workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	transitions := []ports.WorkflowRunTransition{
		{
			RunID:           run.ID,
			ExpectedState:   runtime.WorkflowRunCreated,
			ExpectedVersion: 1,
			NextState:       runtime.WorkflowRunRunning,
			SharedState:     json.RawMessage(`{"winner":"worker-a"}`),
			OccurredAt:      time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC),
		},
		{
			RunID:           run.ID,
			ExpectedState:   runtime.WorkflowRunCreated,
			ExpectedVersion: 1,
			NextState:       runtime.WorkflowRunCancelled,
			SharedState:     json.RawMessage(`{"winner":"worker-b"}`),
			OccurredAt:      time.Date(2026, 8, 28, 2, 0, 0, 1, time.UTC),
		},
	}
	for _, transition := range transitions {
		transition := transition
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.CompareAndSwapWorkflowRun(ctx, transition)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	winners := 0
	stale := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ports.ErrOptimisticConflict):
			stale++
		default:
			t.Fatalf("concurrent CAS returned unexpected error: %v", err)
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("CAS results: winners=%d stale=%d, want 1/1", winners, stale)
	}

	stored, err := store.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load CAS result: %v", err)
	}
	if stored.Version != 2 {
		t.Fatalf("CAS run version = %d, want 2", stored.Version)
	}
	if stored.State != runtime.WorkflowRunRunning && stored.State != runtime.WorkflowRunCancelled {
		t.Fatalf("CAS run state = %s", stored.State)
	}
}

func openWorkflowTestStore(t *testing.T, ctx context.Context, path string) *Store {
	t.Helper()
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	return store
}

func seedWorkflowRunOwners(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	now := "2026-08-28T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO projects (id, name, status, version, created_at, updated_at)
VALUES ('project-1', 'Spike', 'ACTIVE', 1, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO task_families (
    id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at
) VALUES ('family-1', 'project-1', 'work-item-1', 1, 'ACTIVE', 1, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed task family: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO work_items (
    id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at
) VALUES ('work-item-1', 'project-1', 'ROOT', NULL, 'family-1', 'Spike run', 'BACKLOG', 1, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed work item: %v", err)
	}
}

func testWorkflowDefinition() workflow.WorkflowDefinition {
	projectID := project.ProjectID("project-1")
	return workflow.WorkflowDefinition{
		ID:        "workflow-definition-1",
		ProjectID: &projectID,
		Name:      "Spike workflow",
		Status:    workflow.DefinitionActive,
		Version:   1,
	}
}

func compileWorkflowVersion(
	t *testing.T,
	definition workflow.WorkflowDefinition,
	id workflow.WorkflowVersionID,
	versionNumber uint64,
	document workflow.WorkflowDocument,
	dependencyVersion string,
) workflow.WorkflowVersion {
	t.Helper()
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID:     id,
		VersionNumber: versionNumber,
		Document:      document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: dependencyVersion, Hash: "sha256:dependency-" + dependencyVersion},
		}},
		PublishedBy: "spike-test",
		PublishedAt: time.Date(2026, 8, 28, int(versionNumber), 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", id, err)
	}
	return version
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

func workflowDocumentV2() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{
					Kind:         definition.KindAgentProfile,
					DefinitionID: "agent-default",
					VersionID:    "agent-default-v1",
				},
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

func assertSameWorkflowVersion(t *testing.T, actual, expected workflow.WorkflowVersion) {
	t.Helper()
	if actual.ID() != expected.ID() ||
		actual.DefinitionID() != expected.DefinitionID() ||
		actual.VersionNumber() != expected.VersionNumber() ||
		actual.SchemaVersion() != expected.SchemaVersion() ||
		actual.ContentHash() != expected.ContentHash() ||
		actual.PublishedBy() != expected.PublishedBy() ||
		!actual.PublishedAt().Equal(expected.PublishedAt()) ||
		!reflect.DeepEqual(actual.Document(), expected.Document()) ||
		!reflect.DeepEqual(actual.Dependencies(), expected.Dependencies()) {
		t.Fatalf("workflow versions differ:\nactual: %#v\nexpected: %#v", actual, expected)
	}
}
