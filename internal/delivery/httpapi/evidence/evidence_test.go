package evidence_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

type evidenceListItem struct {
	EvidenceID         string   `json:"evidenceId"`
	ProjectID          string   `json:"projectId"`
	WorkItemID         string   `json:"workItemId"`
	RunID              string   `json:"runId"`
	NodeRunID          string   `json:"nodeRunId"`
	AttemptID          string   `json:"attemptId"`
	Kind               string   `json:"kind"`
	Verdict            string   `json:"verdict"`
	ArtifactReferences []string `json:"artifactReferences"`
	RevisionSetHash    string   `json:"revisionSetHash"`
	PolicyVersion      string   `json:"policyVersion"`
}

type evidenceListResponse struct {
	Items []evidenceListItem `json:"items"`
}

// TestGetEvidence_HappyPath_ReturnsVerifyMetadata proves getEvidence returns
// the "verify metadata" a caller needs (ArtifactReferences/RevisionSetHash/
// PolicyVersion/lineage), reloaded from a REAL Evidence row this test seeds
// via the real tx.Runtime().CreateEvidence.
func TestGetEvidence_HappyPath_ReturnsVerifyMetadata(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "gate output")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "gate-criterion-x", "PASS", []string{string(a.ID)})

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/evidence/"+string(evidence.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var detail evidenceListItem
	decodeInto(t, resp, &detail)
	if detail.EvidenceID != string(evidence.ID) || detail.ProjectID != "project-1" || detail.WorkItemID != root.WorkItemID {
		t.Fatalf("detail identities = %+v", detail)
	}
	if detail.RunID != attempt.RunID || detail.NodeRunID != attempt.NodeRunID || detail.AttemptID != attempt.AttemptID {
		t.Fatalf("detail lineage = %+v, want run=%s nodeRun=%s attempt=%s", detail, attempt.RunID, attempt.NodeRunID, attempt.AttemptID)
	}
	if detail.Kind != "gate-criterion-x" || detail.Verdict != "PASS" {
		t.Fatalf("detail kind/verdict = %+v", detail)
	}
	if len(detail.ArtifactReferences) != 1 || detail.ArtifactReferences[0] != string(a.ID) {
		t.Fatalf("detail.ArtifactReferences = %+v, want exactly [%s]", detail.ArtifactReferences, a.ID)
	}
	if detail.RevisionSetHash == "" {
		t.Fatal("detail.RevisionSetHash is empty, want a real content hash")
	}
	if detail.PolicyVersion != "policy-v1" {
		t.Fatalf("detail.PolicyVersion = %q, want policy-v1", detail.PolicyVersion)
	}
}

// TestListEvidence_FiltersByRunAndKind proves the "list evidence theo
// WorkItem/Run/criterion" line: an unfiltered list returns every Evidence
// row for the WorkItem, and `runId`/`kind` each narrow it correctly.
func TestListEvidence_FiltersByRunAndKind(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	attemptA := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "a")
	attemptB := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "b")
	artifactA := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "output a")
	artifactB := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "output b")
	evA := env.seedEvidence(t, "project-1", root.WorkItemID, attemptA, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(artifactA.ID)})
	evB := env.seedEvidence(t, "project-1", root.WorkItemID, attemptB, "gate-criterion-x", "PASS", []string{string(artifactB.ID)})

	base := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence"

	all := env.do(t, http.MethodGet, base, nil)
	var allList evidenceListResponse
	decodeInto(t, all, &allList)
	if len(allList.Items) != 2 {
		t.Fatalf("unfiltered items = %d, want 2: %+v", len(allList.Items), allList.Items)
	}

	byRun := env.do(t, http.MethodGet, base+"?runId="+url.QueryEscape(attemptA.RunID), nil)
	var byRunList evidenceListResponse
	decodeInto(t, byRun, &byRunList)
	if len(byRunList.Items) != 1 || byRunList.Items[0].EvidenceID != string(evA.ID) {
		t.Fatalf("filtered by runId = %+v, want exactly [%s]", byRunList.Items, evA.ID)
	}

	byKind := env.do(t, http.MethodGet, base+"?kind=gate-criterion-x", nil)
	var byKindList evidenceListResponse
	decodeInto(t, byKind, &byKindList)
	if len(byKindList.Items) != 1 || byKindList.Items[0].EvidenceID != string(evB.ID) {
		t.Fatalf("filtered by kind = %+v, want exactly [%s]", byKindList.Items, evB.ID)
	}
}

func TestListEvidence_EmptyWorkItem_ReturnsEmptyItems(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/evidence", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var list evidenceListResponse
	decodeInto(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("items = %+v, want empty", list.Items)
	}
}

func TestListEvidence_UnknownWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/does-not-exist/evidence", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetEvidence_UnknownEvidenceID_ReturnsHiddenNotFound proves a
// plausible-looking but nonexistent evidenceId — including one that looks
// like a path-traversal attempt — is safely rejected: GetEvidence only
// ever uses evidenceId as an opaque database lookup key, never a
// filesystem path, so there is nothing for a "../../" value to traverse
// into; it simply does not match any row.
func TestGetEvidence_UnknownEvidenceID_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	for _, evidenceID := range []string{"does-not-exist", "../../../../etc/passwd", "..%2f..%2fsecret"} {
		resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/evidence/"+url.PathEscape(evidenceID), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("evidenceID=%q: status = %d, want 404", evidenceID, resp.StatusCode)
		}
	}
}

// TestGetEvidence_BelongsToAnotherWorkItem_ReturnsHiddenNotFound is this
// task's own "cross-project/guessed ID" Verify bullet: a real Evidence row,
// just attached to a DIFFERENT WorkItem than the path claims, must be
// indistinguishable from a genuinely nonexistent one (404, never 403 —
// V6-02A's own leakage-normalization policy).
func TestGetEvidence_BelongsToAnotherWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")

	attempt := seedExecutionAttempt(t, env.uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "belongs to A")
	evidence := env.seedEvidence(t, "project-1", rootA.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/evidence/"+string(evidence.ID), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (leakage-normalized hidden)", resp.StatusCode)
	}
}

// TestGetEvidence_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound is
// the sibling cross-PROJECT case: a real WorkItem, just not one belonging
// to the project named in the path.
func TestGetEvidence_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-2", "repo-b")
	other := env.seedRootWorkItem(t, "project-2", "repo-b", "2")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+other.WorkItemID+"/evidence", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
