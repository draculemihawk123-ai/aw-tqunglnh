package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

// TestRunWorkItemList_ReturnsOnlyCallersProjectWorkItems is this task's own
// "subset" Verify bullet: `work-item list --project-id P1` must return only
// WorkItems genuinely stored under P1, never one from a second, unrelated
// project — even when that second project's own WorkItem was created in the
// very same UnitOfWork.
func TestRunWorkItemList_ReturnsOnlyCallersProjectWorkItems(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	rootA := rootWorkItemFixture(t, u, deps.IDs, "project-a", "repo-a")
	rootB := rootWorkItemFixture(t, u, deps.IDs, "project-b", "repo-b")

	var stdout bytes.Buffer
	if err := cliworkitem.RunWorkItemList(context.Background(), deps, []string{"--project-id", "project-a"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunWorkItemList() error = %v", err)
	}
	var view struct {
		Items []struct {
			WorkItemID string `json:"workItemId"`
			ProjectID  string `json:"projectId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(view.Items) != 1 {
		t.Fatalf("items = %+v, want exactly 1 (never leaking project-b's own root %s)", view.Items, rootB.WorkItemID)
	}
	if view.Items[0].WorkItemID != rootA.WorkItemID || view.Items[0].ProjectID != "project-a" {
		t.Fatalf("item = %+v, want the project-a root %s", view.Items[0], rootA.WorkItemID)
	}
}

func TestRunWorkItemList_MissingProjectID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemList(context.Background(), deps, nil, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemList() with no --project-id returned %v, want a cli.UsageError", err)
	}
}
