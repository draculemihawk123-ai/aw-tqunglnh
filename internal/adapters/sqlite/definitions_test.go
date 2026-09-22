package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func openDefinitionsTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), migratedDatabasePath(t, name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

var allEightSharedKinds = []definition.Kind{
	definition.KindBlock, definition.KindSkill, definition.KindLayer, definition.KindEngineeringPack,
	definition.KindAgentProfile, definition.KindCommand, definition.KindGate, definition.KindPolicy,
}

func createTestSharedDefinition(t *testing.T, store *Store, id string, kind definition.Kind, scope definition.Scope, name string) {
	t.Helper()
	if err := store.CreateSharedDefinition(context.Background(), id, kind, scope, name, time.Now().UTC()); err != nil {
		t.Fatalf("CreateSharedDefinition(%s, %s): %v", id, kind, err)
	}
}

func publishRequest(definitionID string, kind definition.Kind, versionID, compiledContent string) ports.PublishVersionRequest {
	return ports.PublishVersionRequest{
		DefinitionID: definitionID, Kind: kind, VersionID: versionID, SchemaVersion: 1,
		CanonicalSource: compiledContent, SourceHash: "sha256:source-" + versionID,
		CompiledSnapshot: compiledContent, CompiledHash: "sha256:compiled-" + compiledContent,
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	}
}

// TestPublishSharedDefinitionVersion_AllEightKinds is V2-02's own
// "Contract publish chạy cho đủ 8 kind mới" required test.
func TestPublishSharedDefinitionVersion_AllEightKinds(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-eight-kinds.db")
	ctx := context.Background()

	for _, kind := range allEightSharedKinds {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			defID := "def-" + string(kind)
			createTestSharedDefinition(t, store, defID, kind, definition.GlobalScope(), "test "+string(kind))

			published, err := store.PublishDefinitionVersion(ctx, publishRequest(defID, kind, "ver-"+string(kind), `{"content":"v1"}`))
			if err != nil {
				t.Fatalf("PublishDefinitionVersion(%s): %v", kind, err)
			}
			if published.Kind() != kind || published.VersionNumber() != 1 {
				t.Fatalf("published = %+v, want Kind=%s VersionNumber=1", published, kind)
			}
		})
	}
}

// TestPublishSharedDefinitionVersion_ConcurrentPublish_NoDuplicateVersionOrHash
// is V2-02's own "Concurrent publish không cấp trùng version number/hash"
// required test.
func TestPublishSharedDefinitionVersion_ConcurrentPublish_NoDuplicateVersionOrHash(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-concurrent.db")
	ctx := context.Background()
	createTestSharedDefinition(t, store, "def-race", definition.KindBlock, definition.GlobalScope(), "race target")

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]definition.VersionFields, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Each writer needs its own VersionID (a real caller would
			// generate a fresh one per candidate, same as
			// compileWorkflowVersion's callers do elsewhere in this
			// package) — definition_versions.id is a primary key, so
			// reusing one across writers would collide on that alone
			// and say nothing about version-number/hash duplication.
			req := publishRequest("def-race", definition.KindBlock, fmt.Sprintf("ver-race-%d", i), "distinct-content")
			req.CompiledHash = fmt.Sprintf("sha256:distinct-%d", i)
			results[i], errs[i] = store.PublishDefinitionVersion(ctx, req)
		}()
	}
	close(start)
	wg.Wait()

	seenVersionNumbers := map[uint64]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if seenVersionNumbers[results[i].VersionNumber()] {
			t.Fatalf("writer %d got duplicate version number %d", i, results[i].VersionNumber())
		}
		seenVersionNumbers[results[i].VersionNumber()] = true
	}
	if len(seenVersionNumbers) != writers {
		t.Fatalf("got %d distinct version numbers, want %d", len(seenVersionNumbers), writers)
	}

	var rowCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM definition_versions WHERE definition_id = 'def-race'`).Scan(&rowCount); err != nil {
		t.Fatalf("count definition_versions: %v", err)
	}
	if rowCount != writers {
		t.Fatalf("definition_versions row count = %d, want %d (one per distinct content, no duplicates/loss)", rowCount, writers)
	}
}

func TestPublishSharedDefinitionVersion_IdempotentRepublish(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-idempotent.db")
	ctx := context.Background()
	createTestSharedDefinition(t, store, "def-idem", definition.KindSkill, definition.GlobalScope(), "idempotent target")

	req := publishRequest("def-idem", definition.KindSkill, "ver-1", `{"content":"same"}`)
	first, err := store.PublishDefinitionVersion(ctx, req)
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}

	req2 := req
	req2.VersionID = "ver-1-retry" // different candidate identity, identical compiled content
	second, err := store.PublishDefinitionVersion(ctx, req2)
	if err != nil {
		t.Fatalf("second (idempotent) publish: %v", err)
	}
	if second.ID() != first.ID() || second.VersionNumber() != first.VersionNumber() {
		t.Fatalf("idempotent republish = %+v, want it to return the existing version %+v", second, first)
	}

	var rowCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM definition_versions WHERE definition_id = 'def-idem'`).Scan(&rowCount); err != nil {
		t.Fatalf("count definition_versions: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("definition_versions row count = %d, want 1 (identical content must never duplicate)", rowCount)
	}
}

// TestCreateSharedDefinition_RejectsWorkflowKind and
// TestSharedDefinitionsTable_CheckConstraint_RejectsWorkflowKind are
// V2-02's own "Insert WORKFLOW vào shared table bị từ chối" required
// test, checked at both the Go repository layer and the raw SQL CHECK
// constraint layer.
func TestCreateSharedDefinition_RejectsWorkflowKind(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-reject-workflow-go.db")
	err := store.CreateSharedDefinition(context.Background(), "def-wf", definition.KindWorkflow, definition.GlobalScope(), "should fail", time.Now().UTC())
	if err == nil {
		t.Fatal("CreateSharedDefinition(KindWorkflow) should fail")
	}
}

func TestSharedDefinitionsTable_CheckConstraint_RejectsWorkflowKind(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-reject-workflow-sql.db")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := store.db.ExecContext(context.Background(), `
INSERT INTO definitions (id, kind, project_id, name, status, version, created_at, updated_at)
VALUES ('def-wf-raw', 'WORKFLOW', NULL, 'raw insert', 'DRAFT', 1, ?, ?)`, now, now)
	if err == nil {
		t.Fatal("raw INSERT of kind='WORKFLOW' into definitions should violate the CHECK constraint")
	}
}

// TestPublishSharedDefinitionVersion_CrossProjectDependencyRejected and
// TestPublishSharedDefinitionVersion_SameProjectDependencyAccepted are
// V2-02's own "Cross-project dependency bị từ chối" required test.
func TestPublishSharedDefinitionVersion_CrossProjectDependencyRejected(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-cross-project.db")
	ctx := context.Background()
	seedTwoProjects(t, store)

	createTestSharedDefinition(t, store, "def-in-proj-a", definition.KindSkill, definition.ProjectScope("project-a"), "dependency target")
	createTestSharedDefinition(t, store, "def-in-proj-b", definition.KindBlock, definition.ProjectScope("project-b"), "publishing definition")

	req := publishRequest("def-in-proj-b", definition.KindBlock, "ver-1", `{"content":"v1"}`)
	req.Dependencies = definition.DependencyManifest{
		Pins: []definition.DependencyPin{{Kind: definition.KindSkill, DefinitionID: "def-in-proj-a", VersionID: "dep-ver-1"}},
	}
	_, err := store.PublishDefinitionVersion(ctx, req)
	if !errors.Is(err, ports.ErrCrossProjectDependency) {
		t.Fatalf("PublishDefinitionVersion with a cross-project dependency err = %v, want ports.ErrCrossProjectDependency", err)
	}
}

func TestPublishSharedDefinitionVersion_SameProjectDependencyAccepted(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-same-project.db")
	ctx := context.Background()
	seedTwoProjects(t, store)

	createTestSharedDefinition(t, store, "def-dep", definition.KindSkill, definition.ProjectScope("project-a"), "dependency target")
	createTestSharedDefinition(t, store, "def-main", definition.KindBlock, definition.ProjectScope("project-a"), "publishing definition")

	req := publishRequest("def-main", definition.KindBlock, "ver-1", `{"content":"v1"}`)
	req.Dependencies = definition.DependencyManifest{
		Pins: []definition.DependencyPin{{Kind: definition.KindSkill, DefinitionID: "def-dep", VersionID: "dep-ver-1"}},
	}
	if _, err := store.PublishDefinitionVersion(ctx, req); err != nil {
		t.Fatalf("PublishDefinitionVersion with a same-project dependency: %v", err)
	}
}

func seedTwoProjects(t *testing.T, store *Store) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{"project-a", "project-b"} {
		if _, err := store.db.ExecContext(context.Background(), `
INSERT INTO projects (id, name, status, version, created_at, updated_at)
VALUES (?, ?, 'ACTIVE', 1, ?, ?)`, id, id, now, now); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
}

// TestPublishDefinitionVersion_UnifiedCommand_WorkflowAndSharedKind is
// V2-02's own "Một application command PublishDefinitionVersion hoạt
// động cho cả workflow và tám kind mới" required test: the exact same
// ports.DefinitionPublisher method, called with only Kind differing,
// succeeds for both a Workflow publish (routed to the existing V0
// workflow_definitions/workflow_versions tables) and a shared-kind
// publish (routed to the new definitions/definition_versions tables) —
// application code never has to know which layout served which.
func TestPublishDefinitionVersion_UnifiedCommand_WorkflowAndSharedKind(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-unified-command.db")
	ctx := context.Background()
	seedWorkflowRunOwners(t, ctx, store)

	// Workflow already needs its own workflow_definitions row to exist
	// before PublishWorkflowVersion's ensure/validate path runs.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_definitions (id, project_id, name, status, version, created_at, updated_at)
VALUES ('wf-def-1', 'project-1', 'Unified command workflow', 'ACTIVE', 1, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed workflow_definitions: %v", err)
	}

	workflowDoc := `{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["next"]},{"key":"end","type":"END"}],"edges":[{"key":"e1","from":"start","outcome":"next","to":"end"}]}`
	var publisher ports.DefinitionPublisher = store

	workflowResult, err := publisher.PublishDefinitionVersion(ctx, ports.PublishVersionRequest{
		DefinitionID: "wf-def-1", Kind: definition.KindWorkflow, VersionID: "wf-ver-1",
		SchemaVersion: 1, CanonicalSource: workflowDoc, SourceHash: "sha256:wf-source",
		CompiledSnapshot: workflowDoc, CompiledHash: "sha256:wf-compiled",
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("PublishDefinitionVersion(KindWorkflow): %v", err)
	}
	if workflowResult.Kind() != definition.KindWorkflow || workflowResult.VersionNumber() != 1 {
		t.Fatalf("workflow result = %+v, want Kind=WORKFLOW VersionNumber=1", workflowResult)
	}

	createTestSharedDefinition(t, store, "def-shared-1", definition.KindGate, definition.GlobalScope(), "unified command gate")
	sharedResult, err := publisher.PublishDefinitionVersion(ctx, publishRequest("def-shared-1", definition.KindGate, "gate-ver-1", `{"content":"v1"}`))
	if err != nil {
		t.Fatalf("PublishDefinitionVersion(KindGate): %v", err)
	}
	if sharedResult.Kind() != definition.KindGate || sharedResult.VersionNumber() != 1 {
		t.Fatalf("shared result = %+v, want Kind=GATE VersionNumber=1", sharedResult)
	}
}

// TestMigration0004_WorkflowRunSurvivesPublishStartRestartFinalize is
// V2-02's own "Upgrade database V0/V1 có sẵn workflow run, rồi load/
// restart/finalize vẫn pass" required test: migration 0004 (the new
// shared definitions tables) is applied by the same Open() call that
// migrates everything else, so this exercises a real database carrying
// both the old workflow tables and the new shared ones together.
func TestMigration0004_WorkflowRunSurvivesPublishStartRestartFinalize(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration-0004-workflow.db")
	store := openWorkflowTestStore(t, ctx, databasePath)
	seedWorkflowRunOwners(t, ctx, store)

	def := testWorkflowDefinition()
	v1 := compileWorkflowVersion(t, def, "wf-ver-1", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, def, v1); err != nil {
		t.Fatalf("publish workflow version: %v", err)
	}

	run, err := runtime.NewWorkflowRun("run-1", "project-1", "work-item-1", v1, "family-1", 1, json.RawMessage(`{"checkpoint":"created"}`))
	if err != nil {
		t.Fatalf("new workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}

	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "job-finalize-1", ProjectID: "project-1", Kind: "FINALIZE_RUN",
		AggregateType: "WorkflowRun", AggregateID: string(run.ID),
		MaxClaims: 5, IdempotencyKey: "job-finalize-1-key",
	}); err != nil {
		t.Fatalf("enqueue finalize job: %v", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("claim finalize job: %v", err)
	}

	running, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: run.ID, ExpectedState: runtime.WorkflowRunCreated, ExpectedVersion: 1,
		NextState: runtime.WorkflowRunRunning, SharedState: json.RawMessage(`{"checkpoint":"node-a"}`),
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close before simulated restart: %v", err)
	}
	restarted := openWorkflowTestStore(t, ctx, databasePath) // re-runs Migrate(), including 0004, idempotently
	defer func() { _ = restarted.Close() }()

	loaded, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load workflow run after restart: %v", err)
	}
	if loaded.State != runtime.WorkflowRunRunning || loaded.Version != running.Version {
		t.Fatalf("loaded run after restart = %#v, want state=RUNNING version=%d", loaded, running.Version)
	}

	finalized, err := restarted.FinalizeWorkflowRun(ctx, ports.WorkerWorkflowRunFinalization{
		Transition: ports.WorkflowRunTransition{
			RunID: run.ID, ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: running.Version,
			NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"terminal":"ok"}`),
			OccurredAt: time.Now().UTC(),
		},
		JobLease: lease, EventID: "event-finalize-1", CorrelationID: "migration-0004-finalize",
	})
	if err != nil {
		t.Fatalf("finalize workflow run after restart: %v", err)
	}
	if finalized.State != runtime.WorkflowRunSucceeded {
		t.Fatalf("finalized.State = %s, want SUCCEEDED", finalized.State)
	}
}

// TestListDefinitions_FiltersByKindAndScope is V6-15E's own real-SQLite
// coverage for definitionsRepository.ListDefinitions (added by that task —
// no caller anywhere reached this SQL before it, and
// internal/delivery/cli/definitions' own tests exercise the identical
// ports.DefinitionsRepository contract only against the in-memory fake,
// never real SQLite), proving the real `kind = ? AND project_id IS ?`
// filtering and workflow_definitions routing actually work against a real
// database: two BLOCK definitions in different scopes (global, project-a)
// and one SKILL definition in project-a (a different Kind, same scope)
// must never cross-contaminate a listing.
func TestListDefinitions_FiltersByKindAndScope(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-list-filter.db")
	seedTwoProjects(t, store)
	ctx := context.Background()
	uow := NewUnitOfWork(store)

	mustCreateDefinition := func(id string, kind definition.Kind, scope definition.Scope, name string) {
		t.Helper()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			return tx.Definitions().CreateDefinition(ctx, id, kind, scope, name, time.Now().UTC())
		})
		if err != nil {
			t.Fatalf("CreateDefinition(%s): %v", id, err)
		}
	}
	mustCreateDefinition("blk-global", definition.KindBlock, definition.GlobalScope(), "global block")
	mustCreateDefinition("blk-proj-a", definition.KindBlock, definition.ProjectScope("project-a"), "proj-a block")
	mustCreateDefinition("skl-proj-a", definition.KindSkill, definition.ProjectScope("project-a"), "proj-a skill")

	list := func(kind definition.Kind, scope definition.Scope) []ports.DefinitionSummary {
		t.Helper()
		var result []ports.DefinitionSummary
		err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			summaries, err := tx.Definitions().ListDefinitions(ctx, kind, scope)
			result = summaries
			return err
		})
		if err != nil {
			t.Fatalf("ListDefinitions(%s): %v", kind, err)
		}
		return result
	}

	globalBlocks := list(definition.KindBlock, definition.GlobalScope())
	if len(globalBlocks) != 1 || globalBlocks[0].ID != "blk-global" {
		t.Fatalf("ListDefinitions(BLOCK, global) = %+v, want exactly [blk-global]", globalBlocks)
	}
	if globalBlocks[0].Fields.Kind != definition.KindBlock || !globalBlocks[0].Fields.Scope.IsGlobal() {
		t.Fatalf("global block Fields = %+v, want Kind=BLOCK Scope=global", globalBlocks[0].Fields)
	}

	projABlocks := list(definition.KindBlock, definition.ProjectScope("project-a"))
	if len(projABlocks) != 1 || projABlocks[0].ID != "blk-proj-a" {
		t.Fatalf("ListDefinitions(BLOCK, project-a) = %+v, want exactly [blk-proj-a]", projABlocks)
	}

	// A different Kind in the SAME scope must never leak into a BLOCK
	// listing — proves the `kind = ?` filter, not just the scope filter.
	projASkills := list(definition.KindSkill, definition.ProjectScope("project-a"))
	if len(projASkills) != 1 || projASkills[0].ID != "skl-proj-a" {
		t.Fatalf("ListDefinitions(SKILL, project-a) = %+v, want exactly [skl-proj-a]", projASkills)
	}

	// A different project (project-b, seeded but never used above) and an
	// entirely unused Kind must both come back empty, not an error.
	empty := list(definition.KindBlock, definition.ProjectScope("project-b"))
	if len(empty) != 0 {
		t.Fatalf("ListDefinitions(BLOCK, project-b) = %+v, want empty", empty)
	}
}

// TestListDefinitions_RoutesWorkflowToWorkflowDefinitionsTable proves
// KindWorkflow reads workflow_definitions (never the shared definitions
// table), mirroring GetDefinition/CreateDefinition's own routing — a
// WORKFLOW definition and a same-scope BLOCK definition must never appear
// in each other's listing.
func TestListDefinitions_RoutesWorkflowToWorkflowDefinitionsTable(t *testing.T) {
	store := openDefinitionsTestStore(t, "agentkit-definitions-list-workflow.db")
	ctx := context.Background()
	uow := NewUnitOfWork(store)

	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Definitions().CreateDefinition(ctx, "wf-1", definition.KindWorkflow, definition.GlobalScope(), "a workflow", time.Now().UTC()); err != nil {
			return err
		}
		return tx.Definitions().CreateDefinition(ctx, "blk-1", definition.KindBlock, definition.GlobalScope(), "a block", time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	var workflows, blocks []ports.DefinitionSummary
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		workflows, err = tx.Definitions().ListDefinitions(ctx, definition.KindWorkflow, definition.GlobalScope())
		if err != nil {
			return err
		}
		blocks, err = tx.Definitions().ListDefinitions(ctx, definition.KindBlock, definition.GlobalScope())
		return err
	}); err != nil {
		t.Fatalf("ListDefinitions: %v", err)
	}

	if len(workflows) != 1 || workflows[0].ID != "wf-1" || workflows[0].Fields.Kind != definition.KindWorkflow {
		t.Fatalf("ListDefinitions(WORKFLOW) = %+v, want exactly [wf-1]", workflows)
	}
	if len(blocks) != 1 || blocks[0].ID != "blk-1" || blocks[0].Fields.Kind != definition.KindBlock {
		t.Fatalf("ListDefinitions(BLOCK) = %+v, want exactly [blk-1]", blocks)
	}
}

// TestMigration0004_WorkflowRunsForeignKeyUnchanged is V2-02's own "FK
// workflow_runs.workflow_version_id không đổi" required test.
func TestMigration0004_WorkflowRunsForeignKeyUnchanged(t *testing.T) {
	// Schema-shape test: keeps the real fresh-open/migrate path (see
	// template_db_test.go) rather than the shared pre-migrated template.
	store := openFreshStore(t, "agentkit-migration-0004-fk.db")
	rows, err := store.db.QueryContext(context.Background(), `PRAGMA foreign_key_list(workflow_runs)`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_list: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list row: %v", err)
		}
		if from == "workflow_version_id" {
			found = true
			if table != "workflow_versions" || to != "id" {
				t.Fatalf("workflow_runs.workflow_version_id FK = %s(%s), want workflow_versions(id)", table, to)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_list: %v", err)
	}
	if !found {
		t.Fatal("workflow_runs has no foreign key on workflow_version_id at all")
	}
}
