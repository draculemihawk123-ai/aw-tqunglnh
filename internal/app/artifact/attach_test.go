package artifact_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

func TestPrepareAttachment_RawOutputTemp_DurableAndHashVerifiedBeforeReturn(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	ids := idsource.NewSequential("art")
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFixed(createdAt)

	rec, err := appartifact.PrepareAttachment(context.Background(), store, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      "project-1",
		Body:           strings.NewReader("stdout from a command"),
		ContentType:    "text/plain",
		Sensitivity:    redact.Public,
		Redacted:       false,
		RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment returned error: %v", err)
	}

	if rec.ID != "art-1" {
		t.Fatalf("ID = %q, want art-1", rec.ID)
	}
	if rec.AttachState != artifact.Attached {
		t.Fatalf("AttachState = %q, want Attached", rec.AttachState)
	}
	if rec.ExpiresAt == nil || !rec.ExpiresAt.Equal(createdAt.Add(7*24*time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want %v", rec.ExpiresAt, createdAt.Add(7*24*time.Hour))
	}

	// The Done-when bar this function exists to satisfy: by the time
	// PrepareAttachment returns, the content is ALREADY durable and
	// hash-verified in the store — never something a caller has to trust
	// blindly before its own InsertArtifact call.
	if err := store.Verify(context.Background(), ports.ArtifactRef{
		Locator: rec.Locator, SHA256: rec.ContentHash, Size: rec.Size, ContentType: rec.MediaType,
	}); err != nil {
		t.Fatalf("store.Verify on the returned Artifact's own Locator/hash failed: %v", err)
	}
}

func TestPrepareAttachment_CanonicalContext_NoExpiry(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	ids := idsource.NewSequential("art")
	clk := clock.NewFixed(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))

	rec, err := appartifact.PrepareAttachment(context.Background(), store, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      "project-1",
		Body:           strings.NewReader("canonical message content"),
		ContentType:    "text/plain",
		Sensitivity:    redact.Sensitive,
		Redacted:       true,
		RetentionClass: artifact.RetentionCanonicalContext,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment returned error: %v", err)
	}
	if rec.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil for RetentionCanonicalContext", rec.ExpiresAt)
	}
	if !rec.Redacted || rec.Sensitivity != redact.Sensitive {
		t.Fatalf("Redacted/Sensitivity not preserved from the request: %+v", rec)
	}
}

func TestPrepareAttachment_NilBody_NeverReachesDatabaseConstruction(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	ids := idsource.NewSequential("art")
	clk := clock.NewFixed(time.Now())

	_, err = appartifact.PrepareAttachment(context.Background(), store, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      "project-1",
		Body:           nil,
		ContentType:    "text/plain",
		RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err == nil {
		t.Fatal("expected an error for a nil body, got nil")
	}
}
