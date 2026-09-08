package message_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// seedFakeExecutionAttempt inserts a minimal, real WorkflowRun->NodeRun->
// ExecutionAttempt chain directly via the low-level
// CreateWorkflowRun/CreateNodeRun/CreateExecutionAttempt Tx methods — no
// AgentProfile/Policy/scheduler machinery needed, since this test only
// needs a real Attempt that resolves to a specific WorkItem/Project, not a
// dispatchable one. workItemID must already exist (from setupFixture or a
// second root WorkItem created the same way).
func seedFakeExecutionAttempt(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, familyID, suffix string) string {
	t.Helper()
	ctx := context.Background()
	version, err := workflow.Compile(
		workflow.WorkflowDefinition{ID: workflow.WorkflowDefinitionID("wf-def-" + suffix), Status: workflow.DefinitionActive, Version: 1},
		workflow.PublishRequest{
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
		},
	)
	if err != nil {
		t.Fatalf("workflow.Compile: %v", err)
	}

	runID := runtimedomain.WorkflowRunID("wf-run-" + suffix)
	run, err := runtimedomain.NewWorkflowRun(
		runID, project.ProjectID(projectID), workdomain.WorkItemID(workItemID), version, workdomain.TaskFamilyID(familyID), 1, []byte(`{}`),
	)
	if err != nil {
		t.Fatalf("NewWorkflowRun: %v", err)
	}
	nodeRunID := runtimedomain.NodeRunID("node-run-" + suffix)
	nodeRun, err := runtimedomain.NewNodeRun(nodeRunID, runID, "agent", 1, 0, nil, "sha256:input-fixture", "sha256:profile-fixture")
	if err != nil {
		t.Fatalf("NewNodeRun: %v", err)
	}
	attemptID := "attempt-" + suffix
	attempt, err := runtimedomain.NewExecutionAttempt(
		runtimedomain.ExecutionAttemptID(attemptID), nodeRunID, 1, "sha256:profile-fixture", "fake-provider", nil,
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
		t.Fatalf("seed fixture execution attempt: %v", err)
	}
	return attemptID
}

// TestAppendMessage_AttemptBelongsToDifferentWorkItem_Rejected and its
// cross-project sibling below are the audit finding (2026-09-08) fix
// applied at the app layer via fake.UnitOfWork — the original V5-02 tests
// only ever exercised this at the sqlite layer (this file's own package
// doc note: "dựng một ExecutionAttempt thật qua fake cần cả chuỗi
// WorkflowRun→NodeRun→ExecutionAttempt, không có shortcut như sqlite's
// fixture helper" — seedFakeExecutionAttempt above is exactly that missing
// shortcut, built once this fix needed it for real).
func TestAppendMessage_AttemptBelongsToDifferentWorkItem_Rejected(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()

	secondRoot, err := work.CreateRootWorkItem(ctx, uow, ids, testCommand("idem-root-2", "hash-root-2"), work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "A different task",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/other"}, Reason: "a different task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem (second): %v", err)
	}

	attemptID := seedFakeExecutionAttempt(t, uow, "project-1", secondRoot.WorkItemID, secondRoot.FamilyID, "cross-workitem")

	clk := clock.NewFixed(time.Now())
	_, err = appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-msg-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, AttemptID: attemptID, Role: messagedomain.RoleAssistant,
		Content: []byte("output from the wrong work item's attempt"), ContentType: "text/plain",
	})
	if !errors.Is(err, ports.ErrCrossWorkItemReference) {
		t.Fatalf("err = %v, want ports.ErrCrossWorkItemReference", err)
	}
}

func TestAppendMessage_AttemptBelongsToDifferentProject_Rejected(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: "project-2", Name: "project-2"})
		return err
	}); err != nil {
		t.Fatalf("seed project-2: %v", err)
	}
	regCmd := testCommand("idem-repo-2", "hash-repo-2")
	regCmd.Type = "RegisterRepository"
	regCmd.Scope = ports.ProjectScope("project-2")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-2", ProjectID: "project-2", Name: "repo-2",
		RemoteLocator: "https://example.invalid/repo-2.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository (project-2): %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-2", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-2", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repository (project-2): %v", err)
	}
	otherRoot, err := work.CreateRootWorkItem(ctx, uow, ids, testCommand("idem-root-3", "hash-root-3"), work.CreateRootWorkItemRequest{
		ProjectID: "project-2", Title: "A task in another project",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/other"}, Reason: "a task in another project",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem (project-2): %v", err)
	}

	attemptID := seedFakeExecutionAttempt(t, uow, "project-2", otherRoot.WorkItemID, otherRoot.FamilyID, "cross-project")

	clk := clock.NewFixed(time.Now())
	_, err = appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-msg-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, AttemptID: attemptID, Role: messagedomain.RoleAssistant,
		Content: []byte("output from another project's attempt"), ContentType: "text/plain",
	})
	if !errors.Is(err, ports.ErrCrossWorkItemReference) {
		t.Fatalf("err = %v, want ports.ErrCrossWorkItemReference", err)
	}
}
