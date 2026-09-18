package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

func TestRunWorkItemShow_ReturnsDetail(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout bytes.Buffer
	if err := cliworkitem.RunWorkItemShow(context.Background(), deps, []string{"--project-id", "project-1", root.WorkItemID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunWorkItemShow() error = %v", err)
	}
	var detail struct {
		WorkItemID string `json:"workItemId"`
		ProjectID  string `json:"projectId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if detail.WorkItemID != root.WorkItemID || detail.ProjectID != "project-1" || detail.Status != "BACKLOG" {
		t.Fatalf("detail = %+v, want WorkItemID=%s ProjectID=project-1 Status=BACKLOG", detail, root.WorkItemID)
	}
}

// TestRunWorkItemShow_CrossProject_ReturnsError proves a WorkItem genuinely
// belonging to a different real project is never leaked back just because
// the caller happened to guess its ID.
func TestRunWorkItemShow_CrossProject_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-a", "repo-a")
	mustCreateProject(t, u, "project-b")

	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemShow(context.Background(), deps, []string{"--project-id", "project-b", root.WorkItemID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemShow() across projects returned nil error, want ErrScopeMismatch-shaped rejection")
	}
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("error = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestRunWorkItemShow_MissingProjectID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemShow(context.Background(), deps, []string{"some-id"}, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemShow() with no --project-id returned %v, want a cli.UsageError", err)
	}
}
