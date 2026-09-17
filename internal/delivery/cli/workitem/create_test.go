package workitem_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

func TestRunWorkItemCreate_CreatesRootWorkItem(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	body := `{"title":"Build the thing","initialScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"root task"}]}`
	args := []string{"--project-id", "project-1", "--idempotency-key", "key-create-1"}
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemCreate() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeCreateRootResult(t, &stdout)
	if result.ProjectID != "project-1" || result.Status != "BACKLOG" {
		t.Fatalf("result = %+v, want ProjectID=project-1 Status=BACKLOG", result)
	}
	if result.WorkItemID == "" || result.FamilyID == "" || result.WorkspaceSetID == "" {
		t.Fatalf("result = %+v, want non-empty WorkItemID/FamilyID/WorkspaceSetID", result)
	}
	if len(result.ProvisionedRepositories) != 1 || result.ProvisionedRepositories[0].RepositoryID != "repo-1" {
		t.Fatalf("result.ProvisionedRepositories = %+v, want exactly one entry for repo-1", result.ProvisionedRepositories)
	}
}

// TestRunWorkItemCreate_ReplaySameIdempotencyKey_ReturnsIdenticalResult is
// this task's own "replay" Verify bullet applied to create: a second call
// with the identical --idempotency-key must replay the first call's exact
// result, never create a second WorkItem/TaskFamily/WorkspaceSet.
func TestRunWorkItemCreate_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Build the thing","initialScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"root task"}]}`
	args := []string{"--project-id", "project-1", "--idempotency-key", "key-create-replay"}

	var first bytes.Buffer
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunWorkItemCreate() error = %v", err)
	}
	firstResult := decodeCreateRootResult(t, &first)
	if replayedField(t, first.String()) {
		t.Fatal("first call reported Replayed = true, want false")
	}

	var second bytes.Buffer
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunWorkItemCreate() error = %v", err)
	}
	secondResult := decodeCreateRootResult(t, &second)
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	if secondResult.WorkItemID != firstResult.WorkItemID || secondResult.FamilyID != firstResult.FamilyID {
		t.Fatalf("replay result = %+v, want the exact original %+v", secondResult, firstResult)
	}

	items, err := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")
	if err != nil {
		t.Fatalf("ListWorkItemsByProject: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("work items in project after replay = %d, want exactly 1 (the replay must never create a second one)", len(items))
	}
}

func TestRunWorkItemCreate_MissingTitle_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"","initialScope":[{"repositoryId":"repo-1","access":"WRITE","reason":"root task"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1"}
	err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemCreate() with a blank title returned %v, want a cli.UsageError", err)
	}
}

func TestRunWorkItemCreate_InvalidScopeAccess_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Build the thing","initialScope":[{"repositoryId":"repo-1","access":"DELETE","reason":"root task"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1"}
	err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemCreate() with access=DELETE returned %v, want a cli.UsageError", err)
	}
}

func TestRunWorkItemCreate_MissingProjectID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	body := `{"title":"Build the thing","initialScope":[{"repositoryId":"repo-1","access":"WRITE","reason":"root task"}]}`
	err := cliworkitem.RunWorkItemCreate(context.Background(), deps, nil, strings.NewReader(body), &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemCreate() with no --project-id returned %v, want a cli.UsageError", err)
	}
}
