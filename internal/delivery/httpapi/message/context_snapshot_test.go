package message_test

// Fixture-heavy coverage for getMessageContextSnapshot's own "context
// refs" Verify bullet: a real V5-04 ContextSnapshot, bound to a real
// (minimal, non-dispatchable) ExecutionAttempt, resolved end to end
// through a real HTTP round trip.

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// seedFakeExecutionAttempt mirrors internal/app/message/attempt_linkage_test.go's
// own seedFakeExecutionAttempt (a minimal WorkflowRun->NodeRun->
// ExecutionAttempt chain via the low-level CreateWorkflowRun/CreateNodeRun/
// CreateExecutionAttempt Tx methods — no AgentProfile/Policy/scheduler
// machinery needed, since these tests only need a real Attempt that
// resolves to a specific WorkItem/Project, not a dispatchable one), extended
// with a REAL tx.Definitions().PublishWorkflowVersion call
// (internal/app/runtime/advance_test.go's own publishWorkflowVersionDocument
// idiom) that attempt_linkage_test.go's own fake.UnitOfWork twin does not
// need: a real *sqlite.Store enforces workflow_runs.workflow_version_id's
// own FOREIGN KEY into workflow_versions(id), so this fixture must publish a
// real version row first, not merely compile one in memory.
func seedFakeExecutionAttempt(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, familyID, suffix string) string {
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

// seedContextSnapshot builds a real V5-04 ContextSnapshot bound to
// attemptID and persists it via the real
// ports.ContextSnapshotRepository.CreateSnapshot — messageID (already
// appended and durable) is the one MessageRef this fixture pins, matching
// a real scheduling flow's own "the snapshot's own MessageRefs are the
// messages actually rendered into that attempt's context" shape.
func seedContextSnapshot(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, attemptID, messageID, suffix string) contextsnapshot.Snapshot {
	t.Helper()
	ctx := context.Background()
	revisions, err := workspace.NewRevisionSet(nil)
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	snapshot, err := contextsnapshot.NewSnapshot(
		contextsnapshot.ID("snapshot-"+suffix), project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		contextsnapshot.AttemptID(attemptID),
		[]contextsnapshot.MessageRef{{MessageID: messageID}},
		[]contextsnapshot.ResourceRef{{OwnerVersionID: "layer-version-1", ResourceKey: "resource-key-1", ContentHash: "sha256:resource-fixture"}},
		nil, revisions, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.ContextSnapshots().CreateSnapshot(ctx, snapshot)
		return err
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	return snapshot
}

func TestGetMessageContextSnapshot_ResolvesWhenPresent(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	attemptID := seedFakeExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("ASSISTANT", "response produced during this attempt", "text/plain", "", attemptID))
	if appendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appendResp.Body)
		t.Fatalf("append status = %d, want 201, body=%s", appendResp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	snapshot := seedContextSnapshot(t, env.uow, "project-1", root.WorkItemID, attemptID, result.MessageID, "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/"+result.MessageID+"/context-snapshot", "", nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	var detail struct {
		SnapshotID   string `json:"snapshotId"`
		AttemptID    string `json:"attemptId"`
		ManifestHash string `json:"manifestHash"`
		MessageRefs  []struct {
			MessageID string `json:"messageId"`
		} `json:"messageRefs"`
		ResourceRefs []struct {
			ResourceKey string `json:"resourceKey"`
		} `json:"resourceRefs"`
	}
	decodeInto(t, resp, &detail)
	if detail.SnapshotID != string(snapshot.ID) || detail.AttemptID != attemptID || detail.ManifestHash != snapshot.ManifestHash {
		t.Fatalf("detail = %+v, want SnapshotID=%s AttemptID=%s ManifestHash=%s", detail, snapshot.ID, attemptID, snapshot.ManifestHash)
	}
	if len(detail.MessageRefs) != 1 || detail.MessageRefs[0].MessageID != result.MessageID {
		t.Fatalf("detail.MessageRefs = %+v, want exactly [%s]", detail.MessageRefs, result.MessageID)
	}
	if len(detail.ResourceRefs) != 1 || detail.ResourceRefs[0].ResourceKey != "resource-key-1" {
		t.Fatalf("detail.ResourceRefs = %+v, want exactly [resource-key-1]", detail.ResourceRefs)
	}
}

// TestGetMessageContextSnapshot_AttemptWithoutSnapshotYet_Returns404 is the
// "behaves sanely when absent" half of this task's own "context refs"
// Verify bullet, for the case where the Message DOES carry a real
// AttemptID but that Attempt has not (yet) produced a bound Snapshot — a
// legitimate in-flight state (the request-assembly step that creates the
// Snapshot has simply not run yet), not a leakage-sensitive one.
func TestGetMessageContextSnapshot_AttemptWithoutSnapshotYet_Returns404(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	attemptID := seedFakeExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("ASSISTANT", "attempt exists but no snapshot yet", "text/plain", "", attemptID))
	if appendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appendResp.Body)
		t.Fatalf("append status = %d, want 201, body=%s", appendResp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/"+result.MessageID+"/context-snapshot", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, body)
	}
}
