package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

func TestRunEvidenceList_HappyPath_NeverExposesLocator(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "gate output content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "gate-criterion-x", "PASS", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceList(context.Background(), env.deps, []string{"--project-id", "project-1", root.WorkItemID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunEvidenceList: %v, stderr=%s", err, stderr.String())
	}

	raw := stdout.Bytes()
	if strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("response leaks a locator-shaped field: %s", raw)
	}

	var result struct {
		Items []struct {
			EvidenceID         string   `json:"evidenceId"`
			WorkItemID         string   `json:"workItemId"`
			Kind               string   `json:"kind"`
			Verdict            string   `json:"verdict"`
			ArtifactReferences []string `json:"artifactReferences"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, raw)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v, want exactly 1", result.Items)
	}
	item := result.Items[0]
	if item.EvidenceID != string(evidence.ID) || item.WorkItemID != root.WorkItemID {
		t.Fatalf("item identities = %+v", item)
	}
	if item.Kind != "gate-criterion-x" || item.Verdict != "PASS" {
		t.Fatalf("item kind/verdict = %+v", item)
	}
	if len(item.ArtifactReferences) != 1 || item.ArtifactReferences[0] != string(a.ID) {
		t.Fatalf("item.ArtifactReferences = %v, want [%s]", item.ArtifactReferences, a.ID)
	}
}

func TestRunEvidenceList_FiltersByRunIDAndKind(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attemptA := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "a")
	attemptB := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "b")
	artifactA := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content a")
	artifactB := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content b")
	env.seedEvidence(t, "project-1", root.WorkItemID, attemptA, "gate-criterion-x", "PASS", []string{string(artifactA.ID)})
	env.seedEvidence(t, "project-1", root.WorkItemID, attemptB, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(artifactB.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceList(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--kind", "COMMAND_EXECUTION", root.WorkItemID,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunEvidenceList: %v, stderr=%s", err, stderr.String())
	}
	var result struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Kind != "COMMAND_EXECUTION" {
		t.Fatalf("items = %+v, want exactly one COMMAND_EXECUTION entry", result.Items)
	}
}

func TestRunEvidenceList_RequiresProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceList(context.Background(), env.deps, []string{root.WorkItemID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunEvidenceList with no --project-id succeeded, want a usage error")
	}
}

func TestRunEvidenceList_WorkItemBelongsToAnotherProject_ReturnsError(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceList(context.Background(), env.deps, []string{
		"--project-id", "project-2", root.WorkItemID,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunEvidenceList across a scope mismatch succeeded, want an error")
	}
}
