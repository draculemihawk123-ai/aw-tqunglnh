package evidence_test

import (
	"net/http"
	"testing"
)

type contextSnapshotDetailView struct {
	SnapshotID   string `json:"snapshotId"`
	ProjectID    string `json:"projectId"`
	WorkItemID   string `json:"workItemId"`
	AttemptID    string `json:"attemptId"`
	ManifestHash string `json:"manifestHash"`
	ResourceRefs []struct {
		ResourceKey string `json:"resourceKey"`
	} `json:"resourceRefs"`
}

// TestGetContextSnapshot_HappyPath_ResolvesStandaloneById proves this
// task's own standalone "ContextSnapshot detail" route — reachable by
// snapshotId alone, unlike V6-07's own message-scoped
// getMessageContextSnapshot — resolves a real, directly-seeded V5-04
// ContextSnapshot end to end through a real HTTP round trip.
func TestGetContextSnapshot_HappyPath_ResolvesStandaloneById(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	snapshot := env.seedContextSnapshot(t, "project-1", root.WorkItemID, attempt.AttemptID, "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/context-snapshots/"+string(snapshot.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var detail contextSnapshotDetailView
	decodeInto(t, resp, &detail)
	if detail.SnapshotID != string(snapshot.ID) || detail.ProjectID != "project-1" || detail.WorkItemID != root.WorkItemID {
		t.Fatalf("detail identities = %+v", detail)
	}
	if detail.AttemptID != attempt.AttemptID || detail.ManifestHash != snapshot.ManifestHash {
		t.Fatalf("detail = %+v, want AttemptID=%s ManifestHash=%s", detail, attempt.AttemptID, snapshot.ManifestHash)
	}
	if len(detail.ResourceRefs) != 1 || detail.ResourceRefs[0].ResourceKey != "resource-key-1" {
		t.Fatalf("detail.ResourceRefs = %+v, want exactly [resource-key-1]", detail.ResourceRefs)
	}
}

func TestGetContextSnapshot_UnknownID_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/context-snapshots/does-not-exist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetContextSnapshot_BelongsToAnotherWorkItem_ReturnsHiddenNotFound is
// this task's own "cross-project/guessed ID" Verify bullet applied to
// ContextSnapshot: a real Snapshot, just bound to a DIFFERENT WorkItem than
// the path claims, must be indistinguishable from a genuinely unknown one.
func TestGetContextSnapshot_BelongsToAnotherWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "1")
	snapshot := env.seedContextSnapshot(t, "project-1", rootA.WorkItemID, attempt.AttemptID, "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/context-snapshots/"+string(snapshot.ID), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (leakage-normalized hidden)", resp.StatusCode)
	}
}

func TestGetContextSnapshot_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-2", "repo-b")
	other := env.seedRootWorkItem(t, "project-2", "repo-b", "2")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+other.WorkItemID+"/context-snapshots/whatever", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
