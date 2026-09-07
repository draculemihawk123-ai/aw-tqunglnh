package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
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

func newTestArtifact(t *testing.T, id, projectID string, class artifact.RetentionClass, state artifact.AttachState, createdAt time.Time) artifact.Artifact {
	t.Helper()
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID(projectID), "sha256:"+id, "sha256:"+id, 128, "text/plain",
		redact.Public, false, class, state, false, artifact.ComputeExpiresAt(class, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct test artifact: %v", err)
	}
	return a
}

func TestArtifactRepository_InsertArtifact_GetArtifact_RoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-roundtrip.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	want := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Attached, createdAt)

	var inserted, loaded artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		inserted, err = artifactRepository{tx: tx}.InsertArtifact(ctx, want)
		return err
	})
	if !artifactsEqual(inserted, want) {
		t.Fatalf("inserted = %+v, want %+v", inserted, want)
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = artifactRepository{tx: tx}.GetArtifact(ctx, "artifact-1")
		return err
	})
	if !artifactsEqual(loaded, want) {
		t.Fatalf("loaded = %+v, want %+v", loaded, want)
	}
}

func TestArtifactRepository_InsertArtifact_ProjectNotFound(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-project-notfound.db")
	ctx := context.Background()

	a := newTestArtifact(t, "artifact-1", "missing-project", artifact.RetentionCanonicalContext, artifact.Attached, time.Now().UTC())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, a)
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestArtifactRepository_InsertArtifact_DuplicateID_ReturnsExistingRow(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-duplicate-id.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	createdAt := time.Now().UTC()
	first := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Attached, createdAt)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, first)
		return err
	})

	// A second insert with the same ID but different content must return
	// the ALREADY-stored row rather than erroring or overwriting it — the
	// same idempotent-by-ID discipline createWorkItemBlockerTx establishes.
	different := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionCanonicalContext, artifact.Orphan, createdAt.Add(time.Hour))
	var result artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		result, err = artifactRepository{tx: tx}.InsertArtifact(ctx, different)
		return err
	})
	if !artifactsEqual(result, first) {
		t.Fatalf("duplicate insert returned %+v, want the original %+v", result, first)
	}
}

func TestArtifactRepository_GetArtifact_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-get-notfound.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.GetArtifact(ctx, "missing")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestArtifactRepository_TransitionArtifactAttachState_OrphanToAttached(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-transition-success.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	a := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, time.Now().UTC())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, a)
		return err
	})

	var transitioned artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		transitioned, err = artifactRepository{tx: tx}.TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
			ArtifactID: "artifact-1", ExpectedState: artifact.Orphan, ExpectedVersion: 1, NextState: artifact.Attached,
		})
		return err
	})
	if transitioned.AttachState != artifact.Attached || transitioned.Version != 2 {
		t.Fatalf("transitioned = %+v, want Attached@2", transitioned)
	}
}

func TestArtifactRepository_TransitionArtifactAttachState_StaleVersionConflict(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-transition-conflict.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	a := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, time.Now().UTC())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, a)
		return err
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
			ArtifactID: "artifact-1", ExpectedState: artifact.Orphan, ExpectedVersion: 99, NextState: artifact.Attached,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
		}
		return nil
	})
}

func TestArtifactRepository_TransitionArtifactAttachState_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-transition-notfound.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
			ArtifactID: "missing", ExpectedState: artifact.Orphan, ExpectedVersion: 1, NextState: artifact.Attached,
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestArtifactRepository_SetArtifactHold_TogglesIndependentOfAttachState(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-hold.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	a := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Attached, time.Now().UTC())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, a)
		return err
	})

	var held artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		held, err = artifactRepository{tx: tx}.SetArtifactHold(ctx, ports.SetArtifactHoldRequest{
			ArtifactID: "artifact-1", ExpectedVersion: 1, Hold: true,
		})
		return err
	})
	if !held.Hold || held.AttachState != artifact.Attached || held.Version != 2 {
		t.Fatalf("held = %+v, want Hold=true/Attached/version 2", held)
	}

	var released artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		released, err = artifactRepository{tx: tx}.SetArtifactHold(ctx, ports.SetArtifactHoldRequest{
			ArtifactID: "artifact-1", ExpectedVersion: 2, Hold: false,
		})
		return err
	})
	if released.Hold || released.Version != 3 {
		t.Fatalf("released = %+v, want Hold=false/version 3", released)
	}
}

func TestArtifactRepository_SetArtifactHold_StaleVersionConflict(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-hold-conflict.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	a := newTestArtifact(t, "artifact-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Attached, time.Now().UTC())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, a)
		return err
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.SetArtifactHold(ctx, ports.SetArtifactHoldRequest{
			ArtifactID: "artifact-1", ExpectedVersion: 99, Hold: true,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
		}
		return nil
	})
}

func TestArtifactRepository_ListOrphanedArtifacts_FiltersAndOrders(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-list-orphaned.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	older := newTestArtifact(t, "orphan-older", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, base)
	newer := newTestArtifact(t, "orphan-newer", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, base.Add(time.Hour))
	tooNew := newTestArtifact(t, "orphan-too-new", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, base.Add(48*time.Hour))
	attached := newTestArtifact(t, "attached-1", "project-1", artifact.RetentionRawOutputTemp, artifact.Attached, base)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := artifactRepository{tx: tx}
		for _, a := range []artifact.Artifact{older, newer, tooNew, attached} {
			if _, err := repo.InsertArtifact(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})

	var orphaned []artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		orphaned, err = artifactRepository{tx: tx}.ListOrphanedArtifacts(ctx, base.Add(time.Hour))
		return err
	})
	if len(orphaned) != 2 {
		t.Fatalf("len(orphaned) = %d, want 2 (got %+v)", len(orphaned), orphaned)
	}
	if orphaned[0].ID != "orphan-older" || orphaned[1].ID != "orphan-newer" {
		t.Fatalf("orphaned order = [%s, %s], want [orphan-older, orphan-newer]", orphaned[0].ID, orphaned[1].ID)
	}
}
