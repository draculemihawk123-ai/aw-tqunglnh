package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// compileStubWorkflowVersion builds a real, minimally-valid
// workflow.WorkflowVersion via workflow.Compile — never published anywhere
// (this file's own tests never touch tx.Definitions() at all, since
// reconcileRunTerminalityTx's own document parameter is already the
// compiled document, not something it re-loads) — just enough for
// runtimedomain.NewWorkflowRun's own validation (ID()/ContentHash()/
// Dependencies() must be non-empty).
func compileStubWorkflowVersion(t *testing.T, document workflow.WorkflowDocument) workflow.WorkflowVersion {
	t.Helper()
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID("wf-def-stub"), Name: "stub", Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID("wf-v-stub"), VersionNumber: 1, Document: document,
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile stub workflow version: %v", err)
	}
	return version
}

// TestReconcileRunTerminalityTx_NoLiveBlockedEndOrFailure_TerminalPathInvalid
// is a white-box unit test (package runtime, not runtime_test — see
// event_schema_test.go's own identical precedent for why this file is
// internal) for reconcileRunTerminalityTx's own defensive, should-be-
// unreachable-via-any-real-flow-today fail-closed branch: no live NodeRun,
// no BLOCKED NodeRun, no END reached, and no FAILED NodeRun either.
// Nothing in this codebase currently produces this combination through the
// public API (SKIPPED always creates a live escalation-target NodeRun in
// the same transaction, and CANCELLED has no real producer yet — V4-12B's
// own scope) — this test constructs it directly against the fake
// RuntimeRepository (which needs no WorkItem/Repository cross-validation
// at all, unlike sqlite's own FK-enforced schema) to prove the reducer
// still fails closed rather than leaving the Run stuck RUNNING forever.
func TestReconcileRunTerminalityTx_NoLiveBlockedEndOrFailure_TerminalPathInvalid(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()

	document := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
	}
	version := compileStubWorkflowVersion(t, document)

	var run runtimedomain.WorkflowRun
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		r, err := runtimedomain.NewWorkflowRun(
			"run-1", project.ProjectID("project-1"), work.WorkItemID("work-item-1"),
			version, work.TaskFamilyID("family-1"), 1, nil,
		)
		if err != nil {
			return err
		}
		r.State = runtimedomain.WorkflowRunRunning
		run, err = tx.Runtime().CreateWorkflowRun(ctx, r)
		if err != nil {
			return err
		}

		startNodeRun, err := runtimedomain.NewNodeRun("node-run-start", run.ID, "start", 1, 0, nil, "sha256:x", "")
		if err != nil {
			return err
		}
		// Poked directly to CANCELLED — no real producer exists for this
		// state yet (V4-12B's own scope) — this is exactly the synthetic
		// "everything terminal, nothing live/blocked/end/failed"
		// combination this test exists to prove fails closed.
		startNodeRun.State = runtimedomain.NodeRunCancelled
		_, err = tx.Runtime().CreateNodeRun(ctx, startNodeRun)
		return err
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return reconcileRunTerminalityTx(ctx, tx, run, document, "corr-1", "job-1")
	}); err != nil {
		t.Fatalf("reconcileRunTerminalityTx: %v", err)
	}

	updated, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updated.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run state = %s, want FAILED", updated.State)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == RunFailedEventType && e.AggregateID == string(run.ID) {
			found = true
			if !strings.Contains(e.PayloadJSON, RunFailureReasonTerminalPathInvalid) {
				t.Fatalf("%s payload = %s, want it to contain reason %s", RunFailedEventType, e.PayloadJSON, RunFailureReasonTerminalPathInvalid)
			}
		}
	}
	if !found {
		t.Fatalf("no %s event found for run %s among %+v", RunFailedEventType, run.ID, events)
	}
}

// TestReconcileRunTerminalityTx_AlreadyDecided_NeverRefires proves the
// reducer's own top-level guard: a Run already outside RUNNING/WAITING
// (here, already VERIFYING) is left completely untouched — never
// re-decided, never a second event — regardless of what its own NodeRun
// state summary would otherwise suggest.
func TestReconcileRunTerminalityTx_AlreadyDecided_NeverRefires(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()

	document := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
	}
	version := compileStubWorkflowVersion(t, document)

	var run runtimedomain.WorkflowRun
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		r, err := runtimedomain.NewWorkflowRun(
			"run-2", project.ProjectID("project-1"), work.WorkItemID("work-item-1"),
			version, work.TaskFamilyID("family-1"), 1, nil,
		)
		if err != nil {
			return err
		}
		r.State = runtimedomain.WorkflowRunVerifying
		run, err = tx.Runtime().CreateWorkflowRun(ctx, r)
		return err
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return reconcileRunTerminalityTx(ctx, tx, run, document, "corr-1", "job-1")
	}); err != nil {
		t.Fatalf("reconcileRunTerminalityTx: %v", err)
	}

	updated, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updated.State != runtimedomain.WorkflowRunVerifying || updated.Version != run.Version {
		t.Fatalf("run = %+v, want unchanged VERIFYING@%d", updated, run.Version)
	}
	if events := uow.Snapshot.Events().(*fake.EventsRepository).Items(); len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}
