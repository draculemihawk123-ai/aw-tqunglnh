package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

func TestRunEvidenceVerify_AllArtifactsVerified_PassesCleanly(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a1 := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "artifact one content")
	a2 := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "artifact two content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a1.ID), string(a2.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceVerify(context.Background(), env.deps, []string{
		"--project-id", "project-1", root.WorkItemID, string(evidence.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunEvidenceVerify: %v, stderr=%s", err, stderr.String())
	}

	var result clievidence.VerifyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode VerifyResult: %v\nstdout=%s", err, stdout.String())
	}
	if result.Verdict != "VERIFIED" {
		t.Fatalf("Verdict = %q, want VERIFIED", result.Verdict)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("len(Artifacts) = %d, want 2", len(result.Artifacts))
	}
	for _, a := range result.Artifacts {
		if !a.Verified {
			t.Errorf("artifact %s: Verified = false, want true", a.ArtifactID)
		}
		if a.Reason != "" {
			t.Errorf("artifact %s: Reason = %q, want empty on a verified artifact", a.ArtifactID, a.Reason)
		}
	}
}

// TestRunEvidenceVerify_TamperedArtifact_ReportsFailedNotSilentPass is this
// task's own "tamper" Verify bullet applied to `evidence verify`: a real
// on-disk byte corruption (never a DB row edit) is caught by
// ArtifactStore.Verify and reported as a failed/tampered entry — never
// silently passing — and the command itself returns a non-nil error
// (mapping to cli.ExitFailure) even though the full JSON result was still
// written to stdout first.
func TestRunEvidenceVerify_TamperedArtifact_ReportsFailedNotSilentPass(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	good := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "untampered content")
	bad := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "the real, untampered gate output")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(good.ID), string(bad.ID)})

	objectPath := env.artifactObjectPath(t, bad.Locator)
	if err := os.WriteFile(objectPath, []byte("corrupted for real — not the recorded hash's own content"), 0o600); err != nil {
		t.Fatalf("tamper artifact content at %s: %v", objectPath, err)
	}

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceVerify(context.Background(), env.deps, []string{
		"--project-id", "project-1", root.WorkItemID, string(evidence.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunEvidenceVerify over a tampered artifact succeeded, want a non-nil error")
	}

	var result clievidence.VerifyResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("decode VerifyResult (must still be written to stdout despite the error): %v\nstdout=%s", decodeErr, stdout.String())
	}
	if result.Verdict != "TAMPERED" {
		t.Fatalf("Verdict = %q, want TAMPERED", result.Verdict)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("len(Artifacts) = %d, want 2", len(result.Artifacts))
	}
	byID := map[string]clievidence.ArtifactVerification{}
	for _, a := range result.Artifacts {
		byID[a.ArtifactID] = a
	}
	if !byID[string(good.ID)].Verified {
		t.Errorf("good artifact %s reported as not verified", good.ID)
	}
	if byID[string(bad.ID)].Verified {
		t.Errorf("tampered artifact %s reported as verified — must never silently pass", bad.ID)
	}
	if byID[string(bad.ID)].Reason == "" {
		t.Error("tampered artifact has an empty Reason, want a non-empty failure reason")
	}
	if strings.Contains(strings.ToLower(byID[string(bad.ID)].Reason), "locator") {
		t.Errorf("tampered artifact Reason leaks a locator-shaped value: %q", byID[string(bad.ID)].Reason)
	}
	raw := stdout.String()
	if strings.Contains(raw, "corrupted for real") {
		t.Fatalf("stdout leaked the corrupted content itself: %s", raw)
	}
	if strings.Contains(strings.ToLower(raw), "locator") {
		t.Fatalf("stdout leaks a locator-shaped field: %s", raw)
	}
}

func TestRunEvidenceVerify_RequiresProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunEvidenceVerify(context.Background(), env.deps, []string{
		root.WorkItemID, string(evidence.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunEvidenceVerify with no --project-id succeeded, want a usage error")
	}
}
