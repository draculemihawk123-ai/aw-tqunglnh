package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

// TestRunArtifactGet_Dash_ExactBytesToStdout is this task's own "exact
// bytes" and "binary stdout" Verify bullets combined: --output - streams
// the artifact's real, stored content byte-for-byte to stdout, and nothing
// else (no JSON wrapper) reaches that same writer.
func TestRunArtifactGet_Dash_ExactBytesToStdout(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const content = "0123456789 exact byte round-trip"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), content)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--output", "-", root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunArtifactGet: %v, stderr=%s", err, stderr.String())
	}
	if stdout.String() != content {
		t.Fatalf("stdout = %q, want exactly %q", stdout.String(), content)
	}
	// Metadata goes to stderr in the dash branch, never mixed into stdout.
	if !strings.Contains(stderr.String(), string(a.ID)) {
		t.Fatalf("stderr = %q, want it to mention the artifact metadata", stderr.String())
	}
}

// TestRunArtifactGet_Dash_GenuineBinaryContent proves non-UTF8 bytes
// survive the --output - path with no text-mode corruption.
func TestRunArtifactGet_Dash_GenuineBinaryContent(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	binary := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x0A, 0x0D, 0x00, 'h', 'i', 0x80, 0x81, 0x7F}
	a := env.seedArtifactBinary(t, "project-1", "application/octet-stream", binary)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--output", "-", root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunArtifactGet: %v, stderr=%s", err, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), binary) {
		t.Fatalf("stdout = %v, want exactly %v (byte-for-byte, no text-mode corruption)", stdout.Bytes(), binary)
	}
}

// TestRunArtifactGet_RealFile_WritesExactBytesAndEmitsSummary is this
// task's own "exact bytes" bullet for the file-target branch, plus proving
// the JSON summary that then goes to stdout carries real, correct metadata
// and no locator.
func TestRunArtifactGet_RealFile_WritesExactBytesAndEmitsSummary(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const content = "written to a real file, byte for byte"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), content)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	outputPath := filepath.Join(t.TempDir(), "downloaded.txt")
	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--output", outputPath, root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunArtifactGet: %v, stderr=%s", err, stderr.String())
	}

	got, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read output file: %v", readErr)
	}
	if string(got) != content {
		t.Fatalf("file content = %q, want %q", got, content)
	}

	raw := stdout.Bytes()
	if strings.Contains(strings.ToLower(string(raw)), "locator") {
		t.Fatalf("summary leaks a locator-shaped field: %s", raw)
	}
	var summary struct {
		ArtifactID  string `json:"artifactId"`
		ContentHash string `json:"contentHash"`
		Size        int64  `json:"size"`
		MediaType   string `json:"mediaType"`
		Sensitivity string `json:"sensitivity"`
		Redacted    bool   `json:"redacted"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("decode summary: %v\nstdout=%s", err, raw)
	}
	if summary.ArtifactID != string(a.ID) || summary.ContentHash != a.ContentHash || summary.Size != int64(len(content)) {
		t.Fatalf("summary = %+v, want ArtifactID=%s ContentHash=%s Size=%d", summary, a.ID, a.ContentHash, len(content))
	}
	if summary.MediaType != "text/plain" || summary.Sensitivity != "PUBLIC" || summary.Redacted {
		t.Fatalf("summary sensitivity fields = %+v", summary)
	}
}

// TestRunArtifactGet_TamperedContent_NoPartialFileNoBytesOnStdout is this
// task's own "tamper" Verify bullet: a real on-disk byte corruption is
// caught by ArtifactStore.Verify BEFORE Open/WriteBinaryOutput ever run —
// no partial file is created at a real --output path, and no bytes reach
// stdout for --output -.
func TestRunArtifactGet_TamperedContent_NoPartialFileNoBytesOnStdout(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "the real, untampered gate output")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	objectPath := env.artifactObjectPath(t, a.Locator)
	if err := os.WriteFile(objectPath, []byte("corrupted for real — not the recorded hash's own content"), 0o600); err != nil {
		t.Fatalf("tamper artifact content at %s: %v", objectPath, err)
	}

	t.Run("real file target: no partial file written", func(t *testing.T) {
		outputPath := filepath.Join(t.TempDir(), "downloaded.txt")
		var stdout, stderr bytes.Buffer
		err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
			"--project-id", "project-1", "--output", outputPath, root.WorkItemID, string(evidence.ID), string(a.ID),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("RunArtifactGet over tampered content succeeded, want an error")
		}
		if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
			t.Fatalf("a partial/corrupted file was created at %s despite the tamper being caught before Open", outputPath)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout got written to on a failed (tampered) get: %q", stdout.String())
		}
	})

	t.Run("stdout target: no bytes streamed", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
			"--project-id", "project-1", "--output", "-", root.WorkItemID, string(evidence.ID), string(a.ID),
		}, &stdout, &stderr)
		if err == nil {
			t.Fatal("RunArtifactGet over tampered content succeeded, want an error")
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout got written to on a failed (tampered) get: %q", stdout.String())
		}
		if strings.Contains(stderr.String(), "corrupted for real") {
			t.Fatalf("stderr leaked the corrupted content: %q", stderr.String())
		}
	})
}

// TestRunArtifactGet_ArtifactNotReferencedByThisEvidence_ReturnsError is
// this task's own "refs" Verify bullet: artifactB is a real artifact in the
// same project, real stored content, but it is NOT one of evidenceA's own
// ArtifactReferences — requesting it through evidenceA must fail exactly
// like a genuinely unknown artifact ID would (ResolveEvidenceArtifactContent's
// own centralized guard), never silently served because it happens to be
// real.
func TestRunArtifactGet_ArtifactNotReferencedByThisEvidence_ReturnsError(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attemptA := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "a")
	attemptB := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "b")
	artifactA := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content a")
	artifactB := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content b")
	evidenceA := env.seedEvidence(t, "project-1", root.WorkItemID, attemptA, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(artifactA.ID)})
	_ = env.seedEvidence(t, "project-1", root.WorkItemID, attemptB, "gate-criterion-x", "PASS", []string{string(artifactB.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--output", "-", root.WorkItemID, string(evidenceA.ID), string(artifactB.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunArtifactGet for an artifact not referenced by this evidence succeeded, want an error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to on a rejected (unreferenced) artifact: %q", stdout.String())
	}
}

// TestRunArtifactGet_SecretSensitivity_SummarySurfacesSensitivityFlag is
// this task's own "secret redaction" bullet, read per this task's own
// brief: NOT a claim that this leaf re-redacts (the stored bytes are
// already the redacted form, per "redact BEFORE Put"), but a proof that the
// summary surfaces Sensitivity/Redacted so an operator can SEE it, and that
// the served content really is the already-redacted persisted bytes, never
// the original secret.
func TestRunArtifactGet_SecretSensitivity_SummarySurfacesSensitivityFlag(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const secret = "this looks harmless but is classified"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Secret, redact.NewMatcher(), secret)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	outputPath := filepath.Join(t.TempDir(), "secret.txt")
	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", "--output", outputPath, root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunArtifactGet: %v, stderr=%s", err, stderr.String())
	}

	got, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read output file: %v", readErr)
	}
	if string(got) != "[REDACTED]" {
		t.Fatalf("file content = %q, want [REDACTED] (the real persisted bytes, never the original secret)", got)
	}
	if strings.Contains(string(got), "classified") {
		t.Fatalf("file leaked the original secret content: %q", got)
	}

	var summary struct {
		Sensitivity string `json:"sensitivity"`
		Redacted    bool   `json:"redacted"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v\nstdout=%s", err, stdout.String())
	}
	if summary.Sensitivity != "SECRET" || !summary.Redacted {
		t.Fatalf("summary = %+v, want Sensitivity=SECRET Redacted=true", summary)
	}
}

func TestRunArtifactGet_RequiresOutput(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--project-id", "project-1", root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunArtifactGet with no --output succeeded, want a usage error")
	}
}

func TestRunArtifactGet_RequiresProjectID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	var stdout, stderr bytes.Buffer
	err := clievidence.RunArtifactGet(context.Background(), env.deps, []string{
		"--output", "-", root.WorkItemID, string(evidence.ID), string(a.ID),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunArtifactGet with no --project-id succeeded, want a usage error")
	}
}
