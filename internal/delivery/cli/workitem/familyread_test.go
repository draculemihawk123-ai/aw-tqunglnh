package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

func createChildForTest(t *testing.T, deps cliworkitem.Dependencies, parentID, title, key string) workapp.CreateChildWorkItemResult {
	t.Helper()
	body := `{"title":"` + title + `","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"child task"}]}`
	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, []string{"--idempotency-key", key, parentID}, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("create child %s: %v, stderr=%s", title, err, stderr.String())
	}
	return decodeCreateChildResult(t, &stdout)
}

func TestRunWorkItemChildren_ListsDirectChildrenOnly(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	childA := createChildForTest(t, deps, root.WorkItemID, "Child A", "key-a")
	childB := createChildForTest(t, deps, root.WorkItemID, "Child B", "key-b")
	grandchild := createChildForTest(t, deps, childA.WorkItemID, "Grandchild", "key-g")

	list := func(parentID string) map[string]string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := cliworkitem.RunWorkItemChildren(context.Background(), deps, []string{"--project-id", "project-1", parentID}, &stdout, &stderr); err != nil {
			t.Fatalf("RunWorkItemChildren(%s): %v, stderr=%s", parentID, err, stderr.String())
		}
		var body struct {
			Items []workapp.WorkItemDetail `json:"items"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", stdout.String(), err)
		}
		titles := map[string]string{}
		for _, item := range body.Items {
			if item.ParentWorkItemID != parentID {
				t.Errorf("item %s has parent %q, want %s", item.WorkItemID, item.ParentWorkItemID, parentID)
			}
			titles[item.WorkItemID] = item.Title
		}
		return titles
	}

	got := list(root.WorkItemID)
	if len(got) != 2 || got[childA.WorkItemID] != "Child A" || got[childB.WorkItemID] != "Child B" {
		t.Fatalf("children of root = %v, want exactly Child A and Child B (never the grandchild %s)", got, grandchild.WorkItemID)
	}
	if got := list(childA.WorkItemID); len(got) != 1 || got[grandchild.WorkItemID] != "Grandchild" {
		t.Fatalf("children of Child A = %v, want only the grandchild", got)
	}
}

func TestRunWorkItemChildren_LeafAndForeignProject(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunWorkItemChildren(context.Background(), deps, []string{"--project-id", "project-1", root.WorkItemID}, &stdout, &stderr); err != nil {
		t.Fatalf("a work item with no children is not an error: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil || string(raw["items"]) != "[]" {
		t.Fatalf("result %s (err %v), want {\"items\":[]}", stdout.String(), err)
	}

	stdout.Reset()
	if err := cliworkitem.RunWorkItemChildren(context.Background(), deps, []string{"--project-id", "project-2", root.WorkItemID}, &stdout, &stderr); err == nil {
		t.Fatal("children must not be readable through another project")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a rejected read wrote %q to stdout", stdout.String())
	}
}

func TestRunTaskFamilyShow_ReportsTheFamilyAndItsScopeVersion(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunTaskFamilyShow(context.Background(), deps, []string{"--project-id", "project-1", root.FamilyID}, &stdout, &stderr); err != nil {
		t.Fatalf("RunTaskFamilyShow: %v, stderr=%s", err, stderr.String())
	}
	var detail workapp.TaskFamilyDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if detail.FamilyID != root.FamilyID || detail.ProjectID != "project-1" || detail.RootWorkItemID != root.WorkItemID || detail.ScopeVersion < 1 || detail.Status == "" {
		t.Fatalf("detail = %+v, want family %s of project-1 rooted at %s with a ScopeVersion >= 1", detail, root.FamilyID, root.WorkItemID)
	}

	stdout.Reset()
	if err := cliworkitem.RunTaskFamilyShow(context.Background(), deps, []string{"--project-id", "project-2", root.FamilyID}, &stdout, &stderr); err == nil {
		t.Fatal("a family must not be readable through another project")
	}
	if err := cliworkitem.RunTaskFamilyShow(context.Background(), deps, []string{"--project-id", "project-1", "no-such-family"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown family id must be an error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a rejected read wrote %q to stdout", stdout.String())
	}
}

func TestFamilyReads_UsageErrors(t *testing.T) {
	deps := newTestDeps(t)
	for name, args := range map[string][]string{
		"no project id": {"x1"},
		"no argument":   {"--project-id", "p1"},
		"blank id":      {"--project-id", "p1", " "},
		"two arguments": {"--project-id", "p1", "a", "b"},
	} {
		var stdout, stderr bytes.Buffer
		if err := cliworkitem.RunWorkItemChildren(context.Background(), deps, args, &stdout, &stderr); err == nil || !cli.IsUsageError(err) {
			t.Errorf("children %s: err = %v, want a usage error", name, err)
		}
		if err := cliworkitem.RunTaskFamilyShow(context.Background(), deps, args, &stdout, &stderr); err == nil || !cli.IsUsageError(err) {
			t.Errorf("task-family show %s: err = %v, want a usage error", name, err)
		}
	}
}
