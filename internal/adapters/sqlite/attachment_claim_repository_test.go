package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func newTestAttachmentClaim(uploadID, declaredSHA256 string, claimedAt time.Time) ports.AttachmentPrepareClaim {
	return ports.AttachmentPrepareClaim{
		UploadID: uploadID, ProjectID: "project-1", WorkItemID: "work-item-1",
		Actor: "actor-1", IdempotencyKey: "idem-1", Role: "USER", ContentType: "image/png",
		Sensitivity: "PUBLIC", RetentionClass: "CANONICAL_CONTEXT", DeclaredSHA256: declaredSHA256,
		ClaimOwner: "owner-1", ClaimedAt: claimedAt,
	}
}

func TestAttachmentClaimRepository_ClaimAttachmentUpload_IdempotentInsertOrReturnExisting(t *testing.T) {
	store := openCatalogTestStore(t, "attachment-claims-insert.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	claimedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	var first, second ports.AttachmentPrepareClaim
	var firstCreated, secondCreated bool
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		first, firstCreated, err = attachmentClaimRepository{tx: tx}.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-1", "aaaa", claimedAt))
		return err
	})
	if !firstCreated {
		t.Fatalf("expected first ClaimAttachmentUpload to create a new row")
	}
	if first.State != ports.AttachmentClaimSpooling || first.Version != 1 {
		t.Fatalf("first = %+v, want State=SPOOLING Version=1", first)
	}

	// A second claim attempt for the SAME UploadID (even with a different
	// DeclaredSHA256 in the request — the caller's own job to compare, not
	// this method's) never errors and never inserts a second row: it
	// returns the ALREADY-stored row unchanged.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		second, secondCreated, err = attachmentClaimRepository{tx: tx}.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-1", "bbbb", claimedAt))
		return err
	})
	if secondCreated {
		t.Fatalf("expected second ClaimAttachmentUpload to find the existing row, not create one")
	}
	if second.DeclaredSHA256 != "aaaa" {
		t.Fatalf("second.DeclaredSHA256 = %q, want the ORIGINAL claim's own %q (never silently overwritten)", second.DeclaredSHA256, "aaaa")
	}
	if second != first {
		t.Fatalf("second = %+v, want identical to first = %+v", second, first)
	}
}

func TestAttachmentClaimRepository_RecordAttachmentBlobReady_CASSuccessAndConflict(t *testing.T) {
	store := openCatalogTestStore(t, "attachment-claims-blobready.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	claimedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, _, err := attachmentClaimRepository{tx: tx}.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-2", "cccc", claimedAt))
		return err
	})

	var updated ports.AttachmentPrepareClaim
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		updated, err = attachmentClaimRepository{tx: tx}.RecordAttachmentBlobReady(ctx, ports.RecordAttachmentBlobReadyRequest{
			UploadID: "upload-2", ExpectedVersion: 1, Locator: "sha256:deadbeef", ActualSHA256: "sha256:deadbeef",
			Size: 42, UpdatedAt: claimedAt,
		})
		return err
	})
	if updated.State != ports.AttachmentClaimBlobReady || updated.Version != 2 {
		t.Fatalf("updated = %+v, want State=BLOB_READY Version=2", updated)
	}
	if updated.Locator != "sha256:deadbeef" || updated.ActualSHA256 != "sha256:deadbeef" || updated.Size != 42 {
		t.Fatalf("updated blob fields = %+v, want locator/hash/size populated", updated)
	}

	// A stale CAS (wrong ExpectedVersion, OR the row is no longer SPOOLING)
	// is ErrOptimisticConflict, never a silent overwrite.
	var casErr error
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := attachmentClaimRepository{tx: tx}.RecordAttachmentBlobReady(ctx, ports.RecordAttachmentBlobReadyRequest{
			UploadID: "upload-2", ExpectedVersion: 1, Locator: "sha256:deadbeef", ActualSHA256: "sha256:deadbeef",
			Size: 42, UpdatedAt: claimedAt,
		})
		casErr = err
		return nil
	})
	if !errors.Is(casErr, ports.ErrOptimisticConflict) {
		t.Fatalf("casErr = %v, want ErrOptimisticConflict", casErr)
	}
}

func TestAttachmentClaimRepository_TakeOverAttachmentClaim_CASSuccessAndConflict(t *testing.T) {
	store := openCatalogTestStore(t, "attachment-claims-takeover.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	claimedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		claim := newTestAttachmentClaim("upload-3", "dddd", claimedAt)
		_, _, err := attachmentClaimRepository{tx: tx}.ClaimAttachmentUpload(ctx, claim)
		return err
	})

	takenOverAt := claimedAt.Add(5 * time.Minute)
	var taken ports.AttachmentPrepareClaim
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		taken, err = attachmentClaimRepository{tx: tx}.TakeOverAttachmentClaim(ctx, ports.TakeOverAttachmentClaimRequest{
			UploadID: "upload-3", ExpectedVersion: 1, NewClaimOwner: "owner-2", ClaimedAt: takenOverAt,
		})
		return err
	})
	if taken.ClaimOwner != "owner-2" || !taken.ClaimedAt.Equal(takenOverAt) || taken.Version != 2 {
		t.Fatalf("taken = %+v, want ClaimOwner=owner-2 ClaimedAt=%v Version=2", taken, takenOverAt)
	}
	// State/DeclaredSHA256 are untouched by a take-over.
	if taken.State != ports.AttachmentClaimSpooling || taken.DeclaredSHA256 != "dddd" {
		t.Fatalf("taken = %+v, want State/DeclaredSHA256 unchanged", taken)
	}

	var casErr error
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := attachmentClaimRepository{tx: tx}.TakeOverAttachmentClaim(ctx, ports.TakeOverAttachmentClaimRequest{
			UploadID: "upload-3", ExpectedVersion: 1, NewClaimOwner: "owner-3", ClaimedAt: takenOverAt,
		})
		casErr = err
		return nil
	})
	if !errors.Is(casErr, ports.ErrOptimisticConflict) {
		t.Fatalf("casErr = %v, want ErrOptimisticConflict", casErr)
	}
}

func TestAttachmentClaimRepository_ReleaseAttachmentClaim_IdempotentDelete(t *testing.T) {
	store := openCatalogTestStore(t, "attachment-claims-release.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	claimedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, _, err := attachmentClaimRepository{tx: tx}.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-4", "eeee", claimedAt))
		return err
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return attachmentClaimRepository{tx: tx}.ReleaseAttachmentClaim(ctx, "upload-4")
	})
	var getErr error
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := attachmentClaimRepository{tx: tx}.GetAttachmentClaim(ctx, "upload-4")
		getErr = err
		return nil
	})
	if !errors.Is(getErr, ports.ErrPersistenceNotFound) {
		t.Fatalf("getErr = %v, want ErrPersistenceNotFound", getErr)
	}

	// Releasing an already-released (or never-claimed) UploadID is a no-op,
	// never an error.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return attachmentClaimRepository{tx: tx}.ReleaseAttachmentClaim(ctx, "upload-4")
	})
}

func TestAttachmentClaimRepository_ListStaleAttachmentClaims_OrderedByClaimedAt(t *testing.T) {
	store := openCatalogTestStore(t, "attachment-claims-liststale.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	base := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := attachmentClaimRepository{tx: tx}
		if _, _, err := repo.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-old", "1111", base)); err != nil {
			return err
		}
		if _, _, err := repo.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-mid", "2222", base.Add(time.Hour))); err != nil {
			return err
		}
		_, _, err := repo.ClaimAttachmentUpload(ctx, newTestAttachmentClaim("upload-new", "3333", base.Add(2*time.Hour)))
		return err
	})

	var stale []ports.AttachmentPrepareClaim
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		stale, err = attachmentClaimRepository{tx: tx}.ListStaleAttachmentClaims(ctx, base.Add(time.Hour))
		return err
	})
	if len(stale) != 2 {
		t.Fatalf("len(stale) = %d, want 2 (upload-old, upload-mid)", len(stale))
	}
	if stale[0].UploadID != "upload-old" || stale[1].UploadID != "upload-mid" {
		t.Fatalf("stale order = [%s, %s], want [upload-old, upload-mid]", stale[0].UploadID, stale[1].UploadID)
	}
}
