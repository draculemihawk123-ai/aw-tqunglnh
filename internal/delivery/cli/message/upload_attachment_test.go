package message_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestRunMessageUploadAttachment_LocalDigest_HappyPath(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	content := []byte("attachment bytes, locally digested")
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", "--content-type", "image/png", workItemID}

	if err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageUploadAttachment() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                         `json:"idempotencyKey"`
		Replayed       bool                           `json:"replayed"`
		Result         appmessage.AppendMessageResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}
	if envelope.Result.MessageID == "" || envelope.Result.ContentArtifactID == "" {
		t.Fatalf("result missing IDs: %+v", envelope.Result)
	}

	// The diagnostic line on stderr must show the SAME digest this leaf
	// computed locally (crypto/sha256 over the exact bytes given).
	wantDigest := sha256Hex(content)
	if !strings.Contains(stderr.String(), wantDigest) {
		t.Fatalf("stderr = %q, want it to contain the locally computed digest %q", stderr.String(), wantDigest)
	}

	// Verify the stored content round-trips byte for byte through the real
	// ArtifactStore (never trusted blindly).
	var locator, hash string
	if err := deps.UnitOfWork.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		row, err := tx.Artifacts().GetArtifact(context.Background(), envelope.Result.ContentArtifactID)
		if err != nil {
			return err
		}
		locator, hash = row.Locator, row.ContentHash
		return nil
	}); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	reader, err := deps.ArtifactStore.Open(context.Background(), ports.ArtifactRef{Locator: locator, SHA256: hash, Size: int64(len(content))})
	if err != nil {
		t.Fatalf("ArtifactStore.Open: %v", err)
	}
	defer reader.Close()
	got := make([]byte, len(content))
	if _, err := reader.Read(got); err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("stored content = %q, want %q", got, content)
	}
}

// TestRunMessageUploadAttachment_Replay_NeverAppendsTwice is the "Replay"
// Verify bullet applied to attachments: the exact same --idempotency-key
// resubmitted (with the identical content) must return the identical stored
// result and report Replayed=true, never create a second Message/Artifact.
func TestRunMessageUploadAttachment_Replay_NeverAppendsTwice(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	content := []byte("replay me")
	args := []string{"--project-id", projectID, "--role", "USER", "--idempotency-key", "attach-key-1", workItemID}

	var first bytes.Buffer
	if err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunMessageUploadAttachment() error = %v", err)
	}
	var second bytes.Buffer
	if err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunMessageUploadAttachment() error = %v", err)
	}

	var firstEnvelope, secondEnvelope struct {
		Replayed bool                           `json:"replayed"`
		Result   appmessage.AppendMessageResult `json:"result"`
	}
	if err := json.Unmarshal(first.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first stdout: %v", err)
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout: %v", err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second RunMessageUploadAttachment() with the identical idempotency key reported Replayed = false, want true")
	}
	if secondEnvelope.Result.MessageID != firstEnvelope.Result.MessageID {
		t.Fatalf("replay MessageID = %s, want the exact original %s", secondEnvelope.Result.MessageID, firstEnvelope.Result.MessageID)
	}
	if secondEnvelope.Result.ContentArtifactID != firstEnvelope.Result.ContentArtifactID {
		t.Fatalf("replay ContentArtifactID = %s, want the exact original %s", secondEnvelope.Result.ContentArtifactID, firstEnvelope.Result.ContentArtifactID)
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want exactly 1 (no duplicate append from replay)", len(msgs))
	}
}

// TestRunMessageUploadAttachment_ExplicitSha256Override_Matches proves the
// --sha256 override path (as opposed to local computation) succeeds when
// the operator-declared digest DOES match the actual bytes — the
// "digest computed elsewhere" half of this leaf's own dual digest design.
func TestRunMessageUploadAttachment_ExplicitSha256Override_Matches(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	content := []byte("digest computed on a different machine")
	digest := sha256Hex(content)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", "--sha256", digest, workItemID}

	if err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageUploadAttachment() error = %v, stderr = %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "operator-declared") {
		t.Fatalf("stderr = %q, want it to note the digest came from the operator, not local computation", stderr.String())
	}
}

// TestRunMessageUploadAttachment_TamperedSha256_IsRejected is the "Tamper"
// Verify bullet: an explicit --sha256 that does NOT match the actual
// uploaded bytes must be rejected with appmessage.ErrAttachmentDigestMismatch,
// never silently accepted, and must never create a Message row.
func TestRunMessageUploadAttachment_TamperedSha256_IsRejected(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	content := []byte("the real bytes")
	wrongDigest := sha256Hex([]byte("not the real bytes at all"))
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", "--sha256", wrongDigest, workItemID}

	err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunMessageUploadAttachment(tampered --sha256) error = nil, want ErrAttachmentDigestMismatch")
	}
	if !errors.Is(err, appmessage.ErrAttachmentDigestMismatch) {
		t.Fatalf("RunMessageUploadAttachment(tampered --sha256) error = %v, want errors.Is(..., appmessage.ErrAttachmentDigestMismatch)", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to on a rejected tampered digest: %q", stdout.String())
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0 (a tampered/mismatched digest must never be silently accepted)", len(msgs))
	}
}

// TestRunMessageUploadAttachment_ExceedsMaxAttachmentSize_IsRejectedCleanly
// is the "Size" Verify bullet applied to attachments: an input exceeding
// appmessage.MaxAttachmentSize must be rejected outright by the leaf's own
// bounded read, never silently truncated.
func TestRunMessageUploadAttachment_ExceedsMaxAttachmentSize_IsRejectedCleanly(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	oversized := bytes.Repeat([]byte("a"), appmessage.MaxAttachmentSize+1)

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}
	err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(oversized), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunMessageUploadAttachment(oversized content) error = nil, want a usage error")
	}
	if !isUsageError(err) {
		t.Fatalf("RunMessageUploadAttachment(oversized content) error = %v, want a cli.UsageError wrapping cli.ErrInputTooLarge", err)
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0 (rejected oversized input must never be silently truncated and stored)", len(msgs))
	}
}

func TestRunMessageUploadAttachment_MissingRole_IsUsageError(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, workItemID}
	err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader([]byte("x")), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunMessageUploadAttachment(no --role) error = %v, want a cli.UsageError", err)
	}
}

// TestRunMessageUploadAttachment_StdoutStderrSeparation is the "Stdout/
// stderr separation" Verify bullet: the command's own RESULT (JSON) goes to
// stdout, and ONLY stdout — the digest-computation diagnostic goes to
// stderr, never mixed into the same stream a JSON-consuming caller parses.
func TestRunMessageUploadAttachment_StdoutStderrSeparation(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	content := []byte("separated attachment")
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}
	if err := climessage.RunMessageUploadAttachment(context.Background(), deps, args, bytes.NewReader(content), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageUploadAttachment() error = %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("decode the one JSON document stdout must carry: %v", err)
	}
	var second json.RawMessage
	if err := decoder.Decode(&second); err == nil {
		t.Fatalf("stdout carried more than one JSON document: extra=%s", second)
	}

	if stderr.Len() == 0 {
		t.Fatal("stderr got nothing written to it — expected the digest diagnostic line")
	}
	if strings.Contains(stdout.String(), "sha256 digest") {
		t.Fatalf("stdout contains diagnostic text that belongs only on stderr: %q", stdout.String())
	}
}
