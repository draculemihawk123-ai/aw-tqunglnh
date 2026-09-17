package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

func TestRunContextSnapshotShow_HappyPath_NeverExposesLocator(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	snapshot := env.seedContextSnapshot(t, "project-1", root.WorkItemID, attempt.AttemptID, "1")

	var stdout, stderr bytes.Buffer
	err := clievidence.RunContextSnapshotShow(context.Background(), env.deps, []string{
		"--project-id", "project-1", root.WorkItemID, string(snapshot.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunContextSnapshotShow: %v, stderr=%s", err, stderr.String())
	}

	raw := stdout.Bytes()
	if strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("response leaks a locator-shaped field: %s", raw)
	}

	var result struct {
		SnapshotID   string `json:"snapshotId"`
		WorkItemID   string `json:"workItemId"`
		ResourceRefs []struct {
			ResourceKey string `json:"resourceKey"`
			ContentHash string `json:"contentHash"`
		} `json:"resourceRefs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, raw)
	}
	if result.SnapshotID != string(snapshot.ID) || result.WorkItemID != root.WorkItemID {
		t.Fatalf("result identities = %+v", result)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0].ResourceKey != "resource-key-1" {
		t.Fatalf("result.ResourceRefs = %+v", result.ResourceRefs)
	}
}

func TestRunContextSnapshotShow_RequiresProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	snapshot := env.seedContextSnapshot(t, "project-1", root.WorkItemID, attempt.AttemptID, "1")

	var stdout, stderr bytes.Buffer
	err := clievidence.RunContextSnapshotShow(context.Background(), env.deps, []string{
		root.WorkItemID, string(snapshot.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunContextSnapshotShow with no --project-id succeeded, want a usage error")
	}
}

func TestRunContextSnapshotShow_SnapshotBelongsToAnotherWorkItem_ReturnsError(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "1")
	snapshot := env.seedContextSnapshot(t, "project-1", rootA.WorkItemID, attempt.AttemptID, "1")

	var stdout, stderr bytes.Buffer
	err := clievidence.RunContextSnapshotShow(context.Background(), env.deps, []string{
		"--project-id", "project-1", rootB.WorkItemID, string(snapshot.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunContextSnapshotShow across a scope mismatch succeeded, want an error")
	}
}
