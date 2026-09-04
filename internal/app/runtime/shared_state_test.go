package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// sharedStateDocument is start -> router(one outcome) -> end, declaring
// three shared-state fields exercising each of the three MergeRule
// semantics (HE-14-M04), all owned by "start" and writable by both "start"
// and "router" (except lockedNote, writable only by "start" — the fixture
// this file's own writer-not-allowed test needs).
func sharedStateDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "router", Type: workflow.NodeRouter, Outcomes: []string{"go"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-router", From: "start", Outcome: "next", To: "router"},
			{Key: "router-to-end", From: "router", Outcome: "go", To: "end"},
		},
		SharedState: []workflow.SharedStateField{
			{
				Name: "counter", Type: workflow.SharedStateTypeNumber, Owner: "start",
				Writers: []string{"start", "router"}, Readers: []string{"router", "end"},
				MergeRule: workflow.MergeRuleLastWriteWins,
			},
			{
				Name: "log", Type: workflow.SharedStateTypeArray, Owner: "start",
				Writers: []string{"start", "router"}, Readers: []string{"end"},
				MergeRule: workflow.MergeRuleAppend,
			},
			{
				Name: "lockedNote", Type: workflow.SharedStateTypeString, Owner: "start",
				Writers: []string{"start", "router"}, Readers: []string{"end"},
				MergeRule: workflow.MergeRuleRejectOnConflict,
			},
			{
				Name: "ownerOnlyNote", Type: workflow.SharedStateTypeString, Owner: "start",
				Writers: []string{"start"}, Readers: []string{"router"},
				MergeRule: workflow.MergeRuleLastWriteWins,
			},
		},
	}
}

func expectedStateHash(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return b
}

func TestAdvanceRun_SharedStatePatch_LastWriteWinsAppliesAndHashesNewState(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"counter": rawJSON(t, 1)},
	})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	if string(state["counter"]) != "1" {
		t.Fatalf("shared state counter = %s, want 1", state["counter"])
	}

	nextNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, result.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(next): %v", err)
	}
	if nextNodeRun.InputStateHash != expectedStateHash(t, run.SharedState) {
		t.Fatalf("downstream InputStateHash = %s, want hash of the post-patch shared state %s", nextNodeRun.InputStateHash, expectedStateHash(t, run.SharedState))
	}
}

func TestAdvanceRun_SharedStatePatch_AppendAccumulatesAcrossHops(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"log": rawJSON(t, []string{"from-start"})},
	})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: hop1.NextNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"log": rawJSON(t, []string{"from-router"})},
	}); err != nil {
		t.Fatalf("hop 2 AdvanceRun: %v", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	var log []string
	if err := json.Unmarshal(state["log"], &log); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if len(log) != 2 || log[0] != "from-start" || log[1] != "from-router" {
		t.Fatalf("log = %v, want [from-start from-router] (APPEND across both hops)", log)
	}
}

func TestAdvanceRun_SharedStatePatch_RejectOnConflict_SecondWriteRejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"lockedNote": rawJSON(t, "first")},
	})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}

	// router is a declared writer for lockedNote too, but the field
	// already has "first" from start's own write above — REJECT_ON_CONFLICT
	// must refuse this second write outright rather than silently
	// overwriting it.
	_, err = runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: hop1.NextNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"lockedNote": rawJSON(t, "second")},
	})
	if !errors.Is(err, runtime.ErrSharedStateMergeConflict) {
		t.Fatalf("err = %v, want ErrSharedStateMergeConflict", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	if string(state["lockedNote"]) != `"first"` {
		t.Fatalf("shared state lockedNote = %s, want unchanged \"first\" (rejected write must not overwrite it)", state["lockedNote"])
	}

	// The rejected hop must not have advanced router at all: it stays
	// RUNNING, un-routed, so a retry with a non-conflicting patch can still
	// succeed.
	routerNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop1.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(router): %v", err)
	}
	if routerNodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("router node run state = %s, want still RUNNING (rejected patch must not route away from it)", routerNodeRun.State)
	}
}

func TestAdvanceRun_SharedStatePatch_FieldNotDeclared_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	_, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"notDeclared": rawJSON(t, 1)},
	})
	if !errors.Is(err, runtime.ErrSharedStateFieldNotDeclared) {
		t.Fatalf("err = %v, want ErrSharedStateFieldNotDeclared", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.Version != 1 {
		t.Fatalf("run version = %d, want unchanged 1 (rejected patch must not touch shared state)", run.Version)
	}
}

func TestAdvanceRun_SharedStatePatch_WriterNotAllowed_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}

	// router is RUNNING now (auto-advanced) but is NOT in ownerOnlyNote's
	// own Writers list (["start"] only) — writing it from router must fail.
	_, err = runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: hop1.NextNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"ownerOnlyNote": rawJSON(t, "from-router")},
	})
	if !errors.Is(err, runtime.ErrSharedStateWriterNotAllowed) {
		t.Fatalf("err = %v, want ErrSharedStateWriterNotAllowed", err)
	}
}

func TestAdvanceRun_SharedStatePatch_TypeMismatch_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, sharedStateDocument())

	_, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startNodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"counter": rawJSON(t, "not-a-number")},
	})
	if !errors.Is(err, runtime.ErrSharedStateTypeMismatch) {
		t.Fatalf("err = %v, want ErrSharedStateTypeMismatch", err)
	}
}

func TestAdvanceRun_NodeRoutedEvent_AppendedInSameTransaction(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	var found *ports.DomainEvent
	for i := range events {
		if events[i].AggregateType == "NodeRun" && events[i].AggregateID == startNodeRunID && events[i].EventType == "NODE_ROUTED" {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("no NODE_ROUTED event for node run %s, events = %+v", startNodeRunID, events)
	}
	var payload struct {
		RunID           string `json:"runId"`
		NodeRunID       string `json:"nodeRunId"`
		NodeKey         string `json:"nodeKey"`
		SelectedOutcome string `json:"selectedOutcome"`
		NextNodeRunID   string `json:"nextNodeRunId"`
		NextNodeKey     string `json:"nextNodeKey"`
	}
	if err := json.Unmarshal([]byte(found.PayloadJSON), &payload); err != nil {
		t.Fatalf("unmarshal NODE_ROUTED payload: %v", err)
	}
	if payload.RunID != runID || payload.NodeRunID != startNodeRunID || payload.NodeKey != "start" ||
		payload.SelectedOutcome != "next" || payload.NextNodeRunID != result.NextNodeRunID || payload.NextNodeKey != "end" {
		t.Fatalf("NODE_ROUTED payload = %+v, want it to describe this exact hop", payload)
	}
}

func TestAdvanceRun_NoSharedStatePatch_DoesNotBumpWorkflowRunVersion(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID}); err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.Version != 1 {
		t.Fatalf("run version = %d, want unchanged 1 (no patch supplied, no shared-state write should happen)", run.Version)
	}
}
