package work_test

// V6-04B's real-sqlite proof: CreateRootWorkItem/CreateChildWorkItem carry an
// optional readiness contract end to end — command -> domain value -> real
// sqlite columns -> reload -> authoritative detail -> readiness -> READY.
// Every assertion reads back through the real repository/query accessors
// against a real database, never a fake and never a hand-built WorkItem row
// (the pre-V6-04B fixtures had to build a contracted WorkItem by hand because
// no command could; these tests are the ones that no longer need to).

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// publishWorkflowVersionSQLite publishes a minimal, real WorkflowVersion so a
// contract's optional WorkflowVersionID has something genuine to resolve to
// (the work_items.workflow_version_id column is a real foreign key).
func publishWorkflowVersionSQLite(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string) {
	t.Helper()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1,
		Document: workflow.WorkflowDocument{
			SchemaVersion: "1",
			Nodes: []workflow.Node{
				{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
				{Key: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
		},
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	ctx := context.Background()
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, candidate)
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
}

// fullContractRequest is a contract that sets every field the request can
// carry, including one descriptive-only criterion (no VerificationRef) beside
// an executable one.
func fullContractRequest(workflowVersionID string) *work.WorkItemContractRequest {
	return &work.WorkItemContractRequest{
		SchemaVersion: 1, Behavior: "Users can export a report as CSV",
		AcceptanceCriteria: []work.AcceptanceCriterionRequest{
			{Description: "the CSV has a header row", VerificationRef: "go test ./report/..."},
			{Description: "a human reviews the layout"},
		},
		VerificationSpec: "run the report package tests, then export a sample by hand",
		RiskLevel:        "LOW", Exclusions: []string{"PDF export", "scheduled exports"},
		WorkflowVersionID: workflowVersionID,
	}
}

// assertStoredContract fails unless item's stored contract fields equal want
// exactly — every one of the seven fields, compared individually so a failure
// names which column was dropped or mangled.
func assertStoredContract(t *testing.T, label string, item workdomain.WorkItem, want *work.WorkItemContractRequest) {
	t.Helper()
	if item.SchemaVersion != want.SchemaVersion {
		t.Errorf("%s: SchemaVersion = %d, want %d", label, item.SchemaVersion, want.SchemaVersion)
	}
	if item.Behavior != want.Behavior {
		t.Errorf("%s: Behavior = %q, want %q", label, item.Behavior, want.Behavior)
	}
	var wantCriteria []workdomain.AcceptanceCriterion // nil when none given: an unset list reads back nil
	for _, c := range want.AcceptanceCriteria {
		wantCriteria = append(wantCriteria, workdomain.AcceptanceCriterion{Description: c.Description, VerificationRef: c.VerificationRef})
	}
	if !reflect.DeepEqual(item.AcceptanceCriteria, wantCriteria) {
		t.Errorf("%s: AcceptanceCriteria = %+v, want %+v", label, item.AcceptanceCriteria, wantCriteria)
	}
	if item.VerificationSpec != want.VerificationSpec {
		t.Errorf("%s: VerificationSpec = %q, want %q", label, item.VerificationSpec, want.VerificationSpec)
	}
	if string(item.RiskLevel) != want.RiskLevel {
		t.Errorf("%s: RiskLevel = %q, want %q", label, item.RiskLevel, want.RiskLevel)
	}
	if !reflect.DeepEqual(item.Exclusions, want.Exclusions) {
		t.Errorf("%s: Exclusions = %v, want %v", label, item.Exclusions, want.Exclusions)
	}
	switch {
	case want.WorkflowVersionID == "" && item.WorkflowVersionID != nil:
		t.Errorf("%s: WorkflowVersionID = %q, want unset", label, *item.WorkflowVersionID)
	case want.WorkflowVersionID != "" && (item.WorkflowVersionID == nil || string(*item.WorkflowVersionID) != want.WorkflowVersionID):
		t.Errorf("%s: WorkflowVersionID = %v, want %q", label, item.WorkflowVersionID, want.WorkflowVersionID)
	}
}

func assertEmptyStoredContract(t *testing.T, label string, item workdomain.WorkItem) {
	t.Helper()
	if item.SchemaVersion != 0 || item.Behavior != "" || len(item.AcceptanceCriteria) != 0 || item.VerificationSpec != "" ||
		item.RiskLevel != "" || len(item.Exclusions) != 0 || item.WorkflowVersionID != nil {
		t.Errorf("%s: stored contract = %+v, want entirely empty", label, item)
	}
}

func createCmd(commandType, idempotencyKey, requestHash string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: requestHash,
	}
}

// TestCreateRootWorkItem_WithContract_PersistsEveryFieldAndBecomesReadySQLite
// is the gap-closing proof V6-14 asked for: a root created WITH a contract
// stores all seven contract fields, the authoritative detail and the readiness
// explanation both reflect what was stored, and MarkWorkItemReady then
// transitions it BACKLOG -> READY — with no hand-built WorkItem anywhere.
func TestCreateRootWorkItem_WithContract_PersistsEveryFieldAndBecomesReadySQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-root-contract.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	publishWorkflowVersionSQLite(t, uow, "project-1", "wf-def-1", "wf-v-1")

	contract := fullContractRequest("wf-v-1")
	req := baseRequest("project-1", grant("repo-a"))
	req.Contract = contract
	result, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-root-contract", "hash-root-contract"), req)
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	if result.Status != string(workdomain.WorkItemBacklog) {
		t.Fatalf("result.Status = %q, want BACKLOG (a contract never moves a WorkItem anywhere)", result.Status)
	}

	stored := loadWorkItemSQLite(t, uow, result.WorkItemID)
	assertStoredContract(t, "reloaded root", stored, contract)
	if stored.Status != workdomain.WorkItemBacklog || stored.Version != 1 {
		t.Fatalf("stored root = %s@%d, want BACKLOG@1", stored.Status, stored.Version)
	}

	detail, err := work.GetWorkItem(ctx, uow, ports.ProjectScope("project-1"), result.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	wantView := &work.WorkItemContractView{
		SchemaVersion: 1, Behavior: contract.Behavior,
		AcceptanceCriteria: []work.AcceptanceCriterionView{
			{Description: "the CSV has a header row", VerificationRef: "go test ./report/..."},
			{Description: "a human reviews the layout"},
		},
		VerificationSpec: contract.VerificationSpec, RiskLevel: "LOW", Exclusions: []string{"PDF export", "scheduled exports"},
		WorkflowVersionID: "wf-v-1",
	}
	if !reflect.DeepEqual(detail.Contract, wantView) {
		t.Fatalf("detail.Contract = %+v, want %+v", detail.Contract, wantView)
	}
	if detail.WorkflowVersionID != "wf-v-1" {
		t.Fatalf("detail.WorkflowVersionID = %q, want the pre-existing top-level field to keep reporting wf-v-1", detail.WorkflowVersionID)
	}

	readiness, err := work.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope("project-1"), result.WorkItemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness: %v", err)
	}
	if !readiness.Ready || len(readiness.Problems) != 0 {
		t.Fatalf("readiness = %+v, want Ready with no problems", readiness)
	}

	ready, err := work.MarkWorkItemReady(ctx, uow, markReadyCmd("idem-mark-root-contract", "hash-mark-root-contract", "project-1"),
		work.MarkWorkItemReadyRequest{WorkItemID: result.WorkItemID})
	if err != nil {
		t.Fatalf("MarkWorkItemReady on a contract-bearing root: %v", err)
	}
	if ready.Status != string(workdomain.WorkItemReady) || ready.Version != 2 {
		t.Fatalf("mark-ready result = %+v, want READY@2", ready)
	}
}

// TestCreateChildWorkItem_WithContract_PersistsEveryFieldIndependentOfParentSQLite
// proves the child half: a child's contract is its own — stored in full on the
// child, never leaking onto the parent, and never inherited by a sibling
// created without one.
func TestCreateChildWorkItem_WithContract_PersistsEveryFieldIndependentOfParentSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-child-contract.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	publishWorkflowVersionSQLite(t, uow, "project-1", "wf-def-1", "wf-v-1")

	parentContract := &work.WorkItemContractRequest{SchemaVersion: 2, Behavior: "the parent's own behavior", RiskLevel: "HIGH"}
	rootReq := baseRequest("project-1", grant("repo-a"))
	rootReq.Contract = parentContract
	root, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-root", "hash-root"), rootReq)
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	childContract := fullContractRequest("wf-v-1")
	child, err := work.CreateChildWorkItem(ctx, uow, ids, createCmd("CreateChildWorkItem", "idem-child-contract", "hash-child-contract"), work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Child with a contract", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{childGrant("repo-a", workdomain.RepositoryWrite, []string{"services/api/handler"})},
		Contract:       childContract,
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem (with contract): %v", err)
	}
	bare, err := work.CreateChildWorkItem(ctx, uow, ids, createCmd("CreateChildWorkItem", "idem-child-bare", "hash-child-bare"), work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Child without a contract", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{childGrant("repo-a", workdomain.RepositoryWrite, []string{"services/api/handler"})},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem (without contract): %v", err)
	}

	assertStoredContract(t, "child with contract", loadWorkItemSQLite(t, uow, child.WorkItemID), childContract)
	assertEmptyStoredContract(t, "child without contract (must not inherit the parent's)", loadWorkItemSQLite(t, uow, bare.WorkItemID))
	assertStoredContract(t, "parent after children were created", loadWorkItemSQLite(t, uow, root.WorkItemID), parentContract)

	readiness, err := work.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope("project-1"), child.WorkItemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness(child): %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("child readiness = %+v, want Ready", readiness)
	}
	bareReadiness, err := work.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope("project-1"), bare.WorkItemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness(bare child): %v", err)
	}
	if bareReadiness.Ready {
		t.Fatalf("bare child readiness = %+v, want not Ready", bareReadiness)
	}
}

// TestCreateWorkItem_WithoutContract_StaysExactlyAsBeforeSQLite is the
// backward-compatibility pin: omitting the contract — or passing an entirely
// empty one — leaves every contract column unset, the authoritative detail
// carries no "contract" key at all, and readiness reports the same
// completeness gaps it always did.
func TestCreateWorkItem_WithoutContract_StaysExactlyAsBeforeSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-no-contract.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")

	omitted, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-omitted", "hash-omitted"), baseRequest("project-1", grant("repo-a")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem (omitted): %v", err)
	}
	emptyReq := baseRequest("project-1", grant("repo-a"))
	emptyReq.Contract = &work.WorkItemContractRequest{}
	empty, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-empty", "hash-empty"), emptyReq)
	if err != nil {
		t.Fatalf("CreateRootWorkItem (empty contract): %v", err)
	}

	for label, id := range map[string]string{"omitted": omitted.WorkItemID, "empty": empty.WorkItemID} {
		assertEmptyStoredContract(t, label, loadWorkItemSQLite(t, uow, id))

		detail, err := work.GetWorkItem(ctx, uow, ports.ProjectScope("project-1"), id)
		if err != nil {
			t.Fatalf("GetWorkItem(%s): %v", label, err)
		}
		if detail.Contract != nil {
			t.Errorf("%s: detail.Contract = %+v, want nil", label, detail.Contract)
		}
		wire, err := json.Marshal(detail)
		if err != nil {
			t.Fatalf("marshal detail: %v", err)
		}
		if strings.Contains(string(wire), `"contract"`) {
			t.Errorf("%s: detail JSON = %s, want no \"contract\" key for a WorkItem without one", label, wire)
		}

		readiness, err := work.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope("project-1"), id)
		if err != nil {
			t.Fatalf("ExplainWorkItemReadiness(%s): %v", label, err)
		}
		if readiness.Ready || !strings.Contains(strings.Join(readiness.Problems, "; "), "behavior is required") {
			t.Errorf("%s: readiness = %+v, want not Ready with the usual completeness problems", label, readiness)
		}
	}
}

// TestCreateRootWorkItem_ContractSameKeyReplaysAndStoresOnceSQLite: a retry of
// a contract-bearing create replays the stored result without writing a second
// row, and the contract is stored exactly once; the same key under a different
// RequestHash is a receipt conflict that leaves the first contract untouched.
// (The RequestHash itself is computed by the delivery layer — HTTP and CLI
// each have a test proving the contract participates in it.)
func TestCreateRootWorkItem_ContractSameKeyReplaysAndStoresOnceSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-contract-replay.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")

	first := fullContractRequest("")
	req := baseRequest("project-1", grant("repo-a"))
	req.Contract = first
	cmd := createCmd("CreateRootWorkItem", "idem-replay", "hash-replay-1")
	created, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateRootWorkItem: %v", err)
	}
	replayed, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("replayed CreateRootWorkItem: %v", err)
	}
	if replayed.WorkItemID != created.WorkItemID {
		t.Fatalf("replay WorkItemID = %s, want the original %s", replayed.WorkItemID, created.WorkItemID)
	}
	assertCount(t, "work_items after replay", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertStoredContract(t, "after replay", loadWorkItemSQLite(t, uow, created.WorkItemID), first)

	other := fullContractRequest("")
	other.Behavior = "a different behavior"
	otherReq := baseRequest("project-1", grant("repo-a"))
	otherReq.Contract = other
	otherCmd := createCmd("CreateRootWorkItem", "idem-replay", "hash-replay-2")
	if _, err := work.CreateRootWorkItem(ctx, uow, ids, otherCmd, otherReq); !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("same key, different hash: err = %v, want ports.ErrReceiptConflict", err)
	}
	assertCount(t, "work_items after conflict", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertStoredContract(t, "after conflict", loadWorkItemSQLite(t, uow, created.WorkItemID), first)
}

// TestCreateWorkItem_InvalidContract_RejectedBeforeAnyWriteSQLite: a contract
// with a shape that can never be valid is rejected with the typed error
// naming every problem, before any row, job or receipt is written — for root
// and child alike.
func TestCreateWorkItem_InvalidContract_RejectedBeforeAnyWriteSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-invalid-contract.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)
	itemsBefore, err := store.CountWorkItems(ctx)
	if err != nil {
		t.Fatalf("CountWorkItems: %v", err)
	}

	bad := &work.WorkItemContractRequest{
		SchemaVersion:      -1,
		AcceptanceCriteria: []work.AcceptanceCriterionRequest{{Description: "fine"}, {Description: "   ", VerificationRef: "go test"}},
		Exclusions:         []string{"ok", ""},
		WorkflowVersionID:  "   ",
	}
	wantFields := []string{"schemaVersion", "acceptanceCriteria[1].description", "exclusions[1]", "workflowVersionId"}

	assertInvalid := func(label string, err error) {
		t.Helper()
		var invalid *work.InvalidWorkItemContractError
		if !errors.As(err, &invalid) {
			t.Fatalf("%s: err = %v, want *InvalidWorkItemContractError", label, err)
		}
		got := make([]string, 0, len(invalid.Problems))
		for _, p := range invalid.Problems {
			got = append(got, p.Field)
		}
		if !reflect.DeepEqual(got, wantFields) {
			t.Fatalf("%s: problem fields = %v, want %v (every problem reported at once)", label, got, wantFields)
		}
	}

	rootReq := baseRequest("project-1", grant("repo-a"))
	rootReq.Contract = bad
	_, err = work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-bad-root", "hash-bad-root"), rootReq)
	assertInvalid("root", err)
	_, err = work.CreateChildWorkItem(ctx, uow, ids, createCmd("CreateChildWorkItem", "idem-bad-child", "hash-bad-child"), work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Bad child", ParentJoinPolicy: "ALL", Contract: bad,
		EffectiveScope: []work.ScopeGrantRequest{childGrant("repo-a", workdomain.RepositoryWrite, []string{"services/api/handler"})},
	})
	assertInvalid("child", err)

	assertCount(t, "work_items unchanged", itemsBefore, func() (int, error) { return store.CountWorkItems(ctx) })
	for _, key := range []string{"idem-bad-root", "idem-bad-child"} {
		receipts, err := store.CountCommandReceiptsByIdempotencyKey(ctx, key)
		if err != nil {
			t.Fatalf("CountCommandReceiptsByIdempotencyKey(%s): %v", key, err)
		}
		if receipts != 0 {
			t.Errorf("receipts for %s = %d, want 0 (an invalid request must leave nothing behind)", key, receipts)
		}
	}
}

// TestCreateWorkItem_UnknownWorkflowVersion_RolledBackSQLite: a contract
// pinning a WorkflowVersion that does not exist is a typed, caller-fixable
// error (not the sqlite adapter's generic foreign-key failure) and leaves zero
// rows behind, for root and child alike.
func TestCreateWorkItem_UnknownWorkflowVersion_RolledBackSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-unknown-workflow-version.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")

	rootReq := baseRequest("project-1", grant("repo-a"))
	rootReq.Contract = &work.WorkItemContractRequest{SchemaVersion: 1, WorkflowVersionID: "wf-does-not-exist"}
	_, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-unknown-wf", "hash-unknown-wf"), rootReq)
	if !errors.Is(err, work.ErrUnknownWorkflowVersion) {
		t.Fatalf("root: err = %v, want work.ErrUnknownWorkflowVersion", err)
	}
	assertCount(t, "work_items", 0, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "task_families", 0, func() (int, error) { return store.CountTaskFamilies(ctx) })
	assertCount(t, "workspace_sets", 0, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "family_repository_scopes", 0, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })
	if n, err := store.CountDurableJobsByKind(ctx, work.WorkspaceProvisionJobKind); err != nil || n != 0 {
		t.Fatalf("WORKSPACE_PROVISION jobs = %d (err %v), want 0", n, err)
	}

	realRoot, err := work.CreateRootWorkItem(ctx, uow, ids, createCmd("CreateRootWorkItem", "idem-real-root", "hash-real-root"), baseRequest("project-1", grant("repo-a")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem (no contract): %v", err)
	}
	_, err = work.CreateChildWorkItem(ctx, uow, ids, createCmd("CreateChildWorkItem", "idem-unknown-wf-child", "hash-unknown-wf-child"), work.CreateChildWorkItemRequest{
		ParentWorkItemID: realRoot.WorkItemID, Title: "Child", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{childGrant("repo-a", workdomain.RepositoryWrite, []string{"services/api/handler"})},
		Contract:       &work.WorkItemContractRequest{WorkflowVersionID: "wf-does-not-exist"},
	})
	if !errors.Is(err, work.ErrUnknownWorkflowVersion) {
		t.Fatalf("child: err = %v, want work.ErrUnknownWorkflowVersion", err)
	}
	assertCount(t, "work_items (only the real root)", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	if n, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-unknown-wf-child"); err != nil || n != 0 {
		t.Fatalf("receipts for the failed child = %d (err %v), want 0", n, err)
	}
}
