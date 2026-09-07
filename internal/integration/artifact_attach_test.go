package integration

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

// artifactsEqual compares two Artifact values field by field. A plain a ==
// b is wrong here: ExpiresAt is a *time.Time, so struct equality would
// compare pointer identity rather than the pointed-to instant — two
// independently constructed/loaded values never share a pointer even when
// they represent the identical time.
func artifactsEqual(a, b artifact.Artifact) bool {
	switch {
	case a.ExpiresAt == nil && b.ExpiresAt == nil:
	case a.ExpiresAt == nil || b.ExpiresAt == nil:
		return false
	case !a.ExpiresAt.Equal(*b.ExpiresAt):
		return false
	}
	a.ExpiresAt, b.ExpiresAt = nil, nil
	return a == b
}

// TestArtifactAttach_DurableAndSurvivesRestart is V5-01's own "attach" and
// "restart" Verify scenarios (docs/design/07-v5-execution-evidence.md):
// PrepareAttachment (internal/app/artifact) must make content durable and
// hash-verified in the real filesystem ArtifactStore BEFORE the Artifact
// row ever commits to the real sqlite database — and once committed, both
// the row and the underlying bytes must survive a full process restart
// (store closed and reopened against the same database/artifact root,
// mirroring foundation_test.go's own V1-12 restart contract).
func TestArtifactAttach_DurableAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "agentkit.db")
	artifactRoot := filepath.Join(root, "artifacts")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (clean start): %v", err)
	}
	if err := sqlite.SeedFixtureOwners(ctx, store, "proj-artifact", "fam-artifact", "wi-artifact"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	blobs, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	ids := idsource.NewSequential("artifact")
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFixed(createdAt)
	content := "V5-01 attach integration test content"

	prepared, err := appartifact.PrepareAttachment(ctx, blobs, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      "proj-artifact",
		Body:           strings.NewReader(content),
		ContentType:    "text/plain",
		Sensitivity:    redact.Public,
		RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment: %v", err)
	}

	// By the time PrepareAttachment returned, the content is ALREADY
	// durable and hash-verified — this is V5-01's own Done-when bar
	// ("evidence bắt buộc không commit trước artifact durable/hash
	// verified"), checked here BEFORE the row is ever inserted.
	if err := blobs.Verify(ctx, ports.ArtifactRef{Locator: prepared.Locator, SHA256: prepared.ContentHash, Size: prepared.Size}); err != nil {
		t.Fatalf("content must already be durable/verified before InsertArtifact: %v", err)
	}

	uow := sqlite.NewUnitOfWork(store)
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, prepared)
		return err
	})
	if err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}

	// --- restart: close and reopen against the same database/artifact root ---
	if err := store.Close(); err != nil {
		t.Fatalf("Close (simulated restart): %v", err)
	}
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (restart): %v", err)
	}
	defer store2.Close()

	var reloaded artifact.Artifact
	uow2 := sqlite.NewUnitOfWork(store2)
	err = uow2.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		reloaded, err = tx.Artifacts().GetArtifact(ctx, string(prepared.ID))
		return err
	})
	if err != nil {
		t.Fatalf("GetArtifact after restart: %v", err)
	}
	if !artifactsEqual(reloaded, prepared) {
		t.Fatalf("reloaded after restart = %+v, want %+v", reloaded, prepared)
	}

	blobsAfterRestart, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New (restart): %v", err)
	}
	reader, err := blobsAfterRestart.Open(ctx, ports.ArtifactRef{Locator: reloaded.Locator, SHA256: reloaded.ContentHash, Size: reloaded.Size})
	if err != nil {
		t.Fatalf("Open artifact content after restart: %v", err)
	}
	defer reader.Close()
	roundTripped, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read artifact content after restart: %v", err)
	}
	if string(roundTripped) != content {
		t.Fatalf("content after restart = %q, want %q", roundTripped, content)
	}
}

// TestArtifactAttach_TamperAfterAttachIsDetected is V5-01's own "tamper"
// Verify scenario: once an artifact is durably attached, corrupting its
// underlying bytes on disk (bypassing ArtifactStore entirely, the way a
// real filesystem-level tamper or bit-rot would) must be caught by
// re-verifying the content against the Artifact row's own recorded
// ContentHash/Size — the whole point of AK-ARCH-021/GC-INV-20's evidence
// provenance chain being hash-verified, not just "present".
func TestArtifactAttach_TamperAfterAttachIsDetected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "agentkit.db")
	artifactRoot := filepath.Join(root, "artifacts")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := sqlite.SeedFixtureOwners(ctx, store, "proj-artifact", "fam-artifact", "wi-artifact"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	blobs, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	ids := idsource.NewSequential("artifact")
	clk := clock.NewFixed(time.Now())
	prepared, err := appartifact.PrepareAttachment(ctx, blobs, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      "proj-artifact",
		Body:           strings.NewReader("content that will be tampered with after attach"),
		ContentType:    "text/plain",
		Sensitivity:    redact.Public,
		RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment: %v", err)
	}

	uow := sqlite.NewUnitOfWork(store)
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, prepared)
		return err
	})
	if err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}

	// Corrupt the underlying content-addressed file directly on disk,
	// bypassing ArtifactStore's own API entirely (which never overwrites
	// a finalized object) — the only way a real bit-rot/tamper scenario
	// could happen.
	objectsRoot := filepath.Join(artifactRoot, "objects")
	var tamperedPath string
	err = filepath.WalkDir(objectsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		tamperedPath = path
		return filepath.SkipAll
	})
	if err != nil {
		t.Fatalf("walk artifact objects directory: %v", err)
	}
	if tamperedPath == "" {
		t.Fatal("no stored artifact object file found to tamper with")
	}
	if err := os.WriteFile(tamperedPath, []byte("TAMPERED"), 0o600); err != nil {
		t.Fatalf("tamper with stored artifact content: %v", err)
	}

	// Re-verify using ONLY what the durable Artifact row itself recorded —
	// exactly what a future evidence consumer does before trusting it.
	err = blobs.Verify(ctx, ports.ArtifactRef{Locator: prepared.Locator, SHA256: prepared.ContentHash, Size: prepared.Size})
	if !errors.Is(err, artifactstore.ErrIntegrity) {
		t.Fatalf("Verify after tamper = %v, want artifactstore.ErrIntegrity", err)
	}
}
