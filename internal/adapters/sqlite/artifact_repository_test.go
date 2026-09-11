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

// newTestArtifactWithLocator mirrors newTestArtifact but lets the caller
// pin an explicit, shared Locator — needed to exercise
// ListArtifactsByLocator's own "same Locator, multiple rows, even across
// Projects" scenario, which newTestArtifact's own ID-derived Locator can't
// produce.
func newTestArtifactWithLocator(t *testing.T, id, projectID, locator string, class artifact.RetentionClass, state artifact.AttachState, createdAt time.Time) artifact.Artifact {
	t.Helper()
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID(projectID), locator, locator, 128, "text/plain",
		redact.Public, false, class, state, false, artifact.ComputeExpiresAt(class, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct test artifact: %v", err)
	}
	return a
}

// TestArtifactRepository_ListArtifactsByLocator_AcrossProjects is V5-14's
// own group-eligibility/refcount read: the same Locator can back rows in
// DIFFERENT Projects (0027_artifacts.sql's own "content_hash is
// deliberately NOT unique"), and this query must return every one of them,
// never scoped to a single Project, ordered by ID.
func TestArtifactRepository_ListArtifactsByLocator_AcrossProjects(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-list-by-locator.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedProject(t, store, "project-2")

	createdAt := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	const sharedLocator = "sha256:c0ffee0000000000000000000000000000000000000000000000000000000000"
	sameLocatorA := newTestArtifactWithLocator(t, "shared-a", "project-1", sharedLocator, artifact.RetentionRawOutputTemp, artifact.Orphan, createdAt)
	sameLocatorB := newTestArtifactWithLocator(t, "shared-b", "project-2", sharedLocator, artifact.RetentionCanonicalContext, artifact.Attached, createdAt)
	unrelated := newTestArtifact(t, "unrelated", "project-1", artifact.RetentionRawOutputTemp, artifact.Orphan, createdAt)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := artifactRepository{tx: tx}
		for _, a := range []artifact.Artifact{sameLocatorA, sameLocatorB, unrelated} {
			if _, err := repo.InsertArtifact(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})

	var got []artifact.Artifact
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		got, err = artifactRepository{tx: tx}.ListArtifactsByLocator(ctx, sharedLocator)
		return err
	})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (got %+v)", len(got), got)
	}
	if got[0].ID != "shared-a" || got[1].ID != "shared-b" {
		t.Fatalf("got order = [%s, %s], want [shared-a, shared-b]", got[0].ID, got[1].ID)
	}
	if got[0].ProjectID == got[1].ProjectID {
		t.Fatal("expected the two shared-locator rows to belong to different Projects")
	}
}

// TestArtifactRepository_ClaimArtifactLocatorForPurge_DuplicateRejected is
// this task's own TOCTOU-fence proof at the storage layer: a second claim
// against an already-claimed Locator is ErrPersistenceAlreadyExists, never
// silently accepted.
func TestArtifactRepository_ClaimArtifactLocatorForPurge_DuplicateRejected(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-claim-duplicate.db")
	ctx := context.Background()
	claimedAt := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ClaimArtifactLocatorForPurge(ctx, "sha256:aaaa", "sweep-1", claimedAt)
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		err := artifactRepository{tx: tx}.ClaimArtifactLocatorForPurge(ctx, "sha256:aaaa", "sweep-2", claimedAt)
		if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
			t.Fatalf("second claim error = %v, want ErrPersistenceAlreadyExists", err)
		}
		return nil
	})
}

// TestArtifactRepository_ReleaseArtifactLocatorClaim_IdempotentAndUnblocks
// proves both halves of the claim's own lifecycle: releasing an unclaimed
// Locator is a safe no-op, and releasing an actually-claimed one really
// clears it (a fresh claim against the same Locator succeeds afterward).
func TestArtifactRepository_ReleaseArtifactLocatorClaim_IdempotentAndUnblocks(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-claim-release.db")
	ctx := context.Background()
	claimedAt := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ReleaseArtifactLocatorClaim(ctx, "sha256:never-claimed")
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ClaimArtifactLocatorForPurge(ctx, "sha256:bbbb", "sweep-1", claimedAt)
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ReleaseArtifactLocatorClaim(ctx, "sha256:bbbb")
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ClaimArtifactLocatorForPurge(ctx, "sha256:bbbb", "sweep-2", claimedAt)
	})
}

// TestArtifactRepository_InsertArtifact_ClaimedLocator_Rejected is this
// task's own other TOCTOU fence half: InsertArtifact must refuse a fresh
// row against a Locator currently claimed for purge, so a new Put of
// byte-identical content can never start depending on bytes a sweep is
// mid-way through deleting.
func TestArtifactRepository_InsertArtifact_ClaimedLocator_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-insert-claimed-locator.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	claimedAt := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	const locator = "sha256:cccc"

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return artifactRepository{tx: tx}.ClaimArtifactLocatorForPurge(ctx, locator, "sweep-1", claimedAt)
	})

	fresh := newTestArtifactWithLocator(t, "fresh-insert", "project-1", locator, artifact.RetentionRawOutputTemp, artifact.Orphan, claimedAt)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.InsertArtifact(ctx, fresh)
		if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
			t.Fatalf("InsertArtifact against a claimed locator error = %v, want ErrPersistenceAlreadyExists", err)
		}
		return nil
	})
}

// TestArtifactRepository_ArtifactSweepState_SeededDryRunThenAdvances proves
// migration 35's own seeded singleton row ({Generation:0, DryRun:true,
// Version:1}), that AdvanceArtifactSweepGeneration bumps Generation/Version
// by exactly one and rejects a stale caller, and that SetArtifactSweepDryRun
// flips DryRun independent of Generation.
func TestArtifactRepository_ArtifactSweepState_SeededDryRunThenAdvances(t *testing.T) {
	store := openCatalogTestStore(t, "artifacts-sweep-state.db")
	ctx := context.Background()

	var seeded ports.ArtifactSweepState
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		seeded, err = artifactRepository{tx: tx}.GetArtifactSweepState(ctx)
		return err
	})
	if seeded.Generation != 0 || !seeded.DryRun || seeded.Version != 1 {
		t.Fatalf("seeded state = %+v, want {Generation:0 DryRun:true Version:1}", seeded)
	}

	var advanced ports.ArtifactSweepState
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		advanced, err = artifactRepository{tx: tx}.AdvanceArtifactSweepGeneration(ctx, ports.AdvanceArtifactSweepGenerationRequest{
			ExpectedGeneration: 0, ExpectedVersion: 1,
		})
		return err
	})
	if advanced.Generation != 1 || advanced.Version != 2 || !advanced.DryRun {
		t.Fatalf("advanced state = %+v, want {Generation:1 DryRun:true Version:2}", advanced)
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.AdvanceArtifactSweepGeneration(ctx, ports.AdvanceArtifactSweepGenerationRequest{
			ExpectedGeneration: 0, ExpectedVersion: 1,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("stale AdvanceArtifactSweepGeneration error = %v, want ErrOptimisticConflict", err)
		}
		return nil
	})

	var realRun ports.ArtifactSweepState
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		realRun, err = artifactRepository{tx: tx}.SetArtifactSweepDryRun(ctx, ports.SetArtifactSweepDryRunRequest{
			DryRun: false, ExpectedVersion: 2,
		})
		return err
	})
	if realRun.DryRun || realRun.Version != 3 || realRun.Generation != 1 {
		t.Fatalf("state after SetArtifactSweepDryRun = %+v, want {Generation:1 DryRun:false Version:3}", realRun)
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := artifactRepository{tx: tx}.SetArtifactSweepDryRun(ctx, ports.SetArtifactSweepDryRunRequest{
			DryRun: true, ExpectedVersion: 2,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("stale SetArtifactSweepDryRun error = %v, want ErrOptimisticConflict", err)
		}
		return nil
	})
}
