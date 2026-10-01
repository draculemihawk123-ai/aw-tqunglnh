package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

func TestRunEvidenceShow_HappyPath_NeverExposesLocator(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "gate output content")
	ev := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "gate-criterion-x", "PASS", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceShow(context.Background(), env.deps, []string{"--project-id", "project-1", root.WorkItemID, string(ev.ID)}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunEvidenceShow: %v, stderr=%s", err, stderr.String())
	}
	raw := stdout.Bytes()
	if strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("response leaks a locator-shaped field: %s", raw)
	}
	var result struct {
		EvidenceID         string   `json:"evidenceId"`
		WorkItemID         string   `json:"workItemId"`
		Kind               string   `json:"kind"`
		Verdict            string   `json:"verdict"`
		ArtifactReferences []string `json:"artifactReferences"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, raw)
	}
	if result.EvidenceID != string(ev.ID) || result.WorkItemID != root.WorkItemID || result.Kind != "gate-criterion-x" || result.Verdict != "PASS" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.ArtifactReferences) != 1 || result.ArtifactReferences[0] != string(a.ID) {
		t.Fatalf("artifactReferences = %v, want [%s]", result.ArtifactReferences, a.ID)
	}
}

func TestRunEvidenceShow_UsageAndNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	for name, args := range map[string][]string{
		"no project id":  {root.WorkItemID, "evidence-1"},
		"missing arg":    {"--project-id", "project-1", root.WorkItemID},
		"blank evidence": {"--project-id", "project-1", root.WorkItemID, " "},
	} {
		var stdout, stderr bytes.Buffer
		err := clievidence.RunEvidenceShow(context.Background(), env.deps, args, &stdout, &stderr)
		var usage cli.UsageError
		if err == nil || !asUsage(err, &usage) {
			t.Errorf("%s: err = %v, want a cli.UsageError", name, err)
		}
	}

	var stdout, stderr bytes.Buffer
	if err := clievidence.RunEvidenceShow(context.Background(), env.deps, []string{"--project-id", "project-1", root.WorkItemID, "no-such-evidence"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown evidence id must be reported, not an empty success")
	}
}

func TestRunArtifactList_ListsTheEvidencesArtifactsWithoutLocators(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a1 := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "first artifact")
	a2 := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "second artifact")
	ev := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a1.ID), string(a2.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactList(context.Background(), env.deps, []string{"--project-id", "project-1", root.WorkItemID, string(ev.ID)}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunArtifactList: %v, stderr=%s", err, stderr.String())
	}
	raw := stdout.Bytes()
	if strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("response leaks a locator-shaped field: %s", raw)
	}
	var result struct {
		Items []struct {
			ArtifactID string `json:"artifactId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, raw)
	}
	got := map[string]bool{}
	for _, item := range result.Items {
		got[item.ArtifactID] = true
	}
	if len(result.Items) != 2 || !got[string(a1.ID)] || !got[string(a2.ID)] {
		t.Fatalf("items = %+v, want exactly the two artifacts %s and %s", result.Items, a1.ID, a2.ID)
	}
}

func TestRunArtifactList_UsageErrors(t *testing.T) {
	env := newTestEnv(t)
	for name, args := range map[string][]string{
		"no project id": {"wi", "ev"},
		"missing arg":   {"--project-id", "project-1", "wi"},
	} {
		var stdout, stderr bytes.Buffer
		err := clievidence.RunArtifactList(context.Background(), env.deps, args, &stdout, &stderr)
		var usage cli.UsageError
		if err == nil || !asUsage(err, &usage) {
			t.Errorf("%s: err = %v, want a cli.UsageError", name, err)
		}
	}
}

func asUsage(err error, target *cli.UsageError) bool {
	u, ok := err.(cli.UsageError)
	if ok {
		*target = u
	}
	return ok
}
