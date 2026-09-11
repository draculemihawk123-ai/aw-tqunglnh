package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// seedReleaseSetProject creates a project and one repositories row per
// repoID — repositories are project-scoped, not family-scoped, so a
// project shared by more than one task family (seedTaskFamily below) must
// only ever register each repository once.
func seedReleaseSetProject(t *testing.T, store *Store, projectID string, repoIDs ...string) {
	t.Helper()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		if _, err := (catalogRepository{tx: tx}).CreateProject(context.Background(), ports.CreateProjectRequest{ID: projectID, Name: "demo"}); err != nil {
			return err
		}
		for _, repoID := range repoIDs {
			if _, err := (catalogRepository{tx: tx}).RegisterRepository(context.Background(), ports.RegisterRepositoryRequest{
				ID: repoID, ProjectID: projectID, Name: repoID, RemoteLocator: "https://example.invalid/" + repoID, DefaultRef: "main",
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// seedTaskFamily creates a task family under an already-seeded project
// (task_families.root_work_item_id has no foreign key, unlike project_id,
// see createTaskFamilyTx) — CreateReleaseSet's own family-existence check
// requires this row.
func seedTaskFamily(t *testing.T, store *Store, projectID, familyID string) {
	t.Helper()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		family, err := work.NewTaskFamily(work.TaskFamilyID(familyID), mustRootWorkItem(t, projectID, familyID))
		if err != nil {
			return err
		}
		_, err = (workRepository{tx: tx}).CreateTaskFamily(context.Background(), family)
		return err
	})
}

// seedReleaseSetFamily is seedReleaseSetProject+seedTaskFamily for the
// common single-family-per-project fixture every test but the ordering
// test below needs.
func seedReleaseSetFamily(t *testing.T, store *Store, projectID, familyID string, repoIDs ...string) {
	t.Helper()
	seedReleaseSetProject(t, store, projectID, repoIDs...)
	seedTaskFamily(t, store, projectID, familyID)
}

func mustRootWorkItem(t *testing.T, projectID, familyID string) work.WorkItem {
	t.Helper()
	// mustRootWorkItem's own result is never persisted — task_families has
	// no foreign key on root_work_item_id (see createTaskFamilyTx) — it
	// only exists so work.NewTaskFamily has a real ROOT WorkItem to derive
	// TaskFamily.ProjectID from.
	root, err := work.NewRootWorkItem(work.WorkItemID("root-"+familyID), project.ProjectID(projectID), work.TaskFamilyID(familyID), "Root")
	if err != nil {
		t.Fatalf("NewRootWorkItem: %v", err)
	}
	return root
}

func mustReleaseSet(t *testing.T, id, projectID, familyID string, entries []work.RepositoryRelease, createdAt time.Time) work.ReleaseSet {
	t.Helper()
	releaseSet, err := work.NewReleaseSet(work.ReleaseSetID(id), project.ProjectID(projectID), work.TaskFamilyID(familyID), entries, createdAt)
	if err != nil {
		t.Fatalf("NewReleaseSet: %v", err)
	}
	return releaseSet
}

func TestReleaseSetRepository_CreateAndGet_RoundTrips(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-create-get.db")
	seedReleaseSetFamily(t, store, "p1", "family-1", "repo-a", "repo-b")
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	releaseSet := mustReleaseSet(t, "release-1", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-b", BaseVCSObjectID: "base-b", ResultVCSObjectID: "result-b", Verdict: gate.VerdictFail},
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, createdAt)

	var stored work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		stored, err = (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})
	if stored.State != work.ReleaseSetCreated || stored.Version != 1 {
		t.Fatalf("stored = %+v, want CREATED/version 1", stored)
	}

	var loaded work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = (workRepository{tx: tx}).GetReleaseSet(context.Background(), "release-1")
		return err
	})
	if loaded.ContentHash() != releaseSet.ContentHash() {
		t.Fatalf("loaded content hash = %q, want %q", loaded.ContentHash(), releaseSet.ContentHash())
	}
	entries := loaded.Entries()
	if len(entries) != 2 || entries[0].RepositoryID != "repo-a" || entries[1].RepositoryID != "repo-b" {
		t.Fatalf("loaded entries = %+v, want sorted repo-a, repo-b", entries)
	}
	if !loaded.CreatedAt.Equal(createdAt) {
		t.Fatalf("loaded.CreatedAt = %v, want %v", loaded.CreatedAt, createdAt)
	}
}

func TestReleaseSetRepository_CreateReleaseSet_IsIdempotentByID(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-idempotent.db")
	seedReleaseSetFamily(t, store, "p1", "family-1", "repo-a")
	releaseSet := mustReleaseSet(t, "release-1", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Now())

	var first, second work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		first, err = (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		second, err = (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})
	if first.ContentHash() != second.ContentHash() || first.Version != second.Version {
		t.Fatalf("duplicate create returned a different row: first=%+v second=%+v", first, second)
	}
}

func TestReleaseSetRepository_CreateReleaseSet_MissingFamily(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-missing-family.db")
	releaseSet := mustReleaseSet(t, "release-1", "p1", "missing-family", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Now())

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestReleaseSetRepository_GetReleaseSet_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-get-notfound.db")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).GetReleaseSet(context.Background(), "missing")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestReleaseSetRepository_TransitionReleaseSetState_SealSucceeds(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-seal.db")
	seedReleaseSetFamily(t, store, "p1", "family-1", "repo-a")
	releaseSet := mustReleaseSet(t, "release-1", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Now())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})

	occurredAt := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	var sealed work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		sealed, err = (workRepository{tx: tx}).TransitionReleaseSetState(context.Background(), ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: "release-1", ExpectedState: work.ReleaseSetCreated, ExpectedVersion: 1,
			NextState: work.ReleaseSetSealed, OccurredAt: occurredAt,
		})
		return err
	})
	if sealed.State != work.ReleaseSetSealed || sealed.Version != 2 {
		t.Fatalf("sealed = %+v, want SEALED/version 2", sealed)
	}
	if sealed.SealedAt == nil || !sealed.SealedAt.Equal(occurredAt) {
		t.Fatalf("sealed.SealedAt = %v, want %v", sealed.SealedAt, occurredAt)
	}
	if sealed.AbandonedAt != nil {
		t.Fatalf("sealed.AbandonedAt = %v, want nil", sealed.AbandonedAt)
	}
}

func TestReleaseSetRepository_TransitionReleaseSetState_AbandonSucceeds(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-abandon.db")
	seedReleaseSetFamily(t, store, "p1", "family-1", "repo-a")
	releaseSet := mustReleaseSet(t, "release-1", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictFail},
	}, time.Now())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})

	occurredAt := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	var abandoned work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		abandoned, err = (workRepository{tx: tx}).TransitionReleaseSetState(context.Background(), ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: "release-1", ExpectedState: work.ReleaseSetCreated, ExpectedVersion: 1,
			NextState: work.ReleaseSetAbandoned, OccurredAt: occurredAt,
		})
		return err
	})
	if abandoned.State != work.ReleaseSetAbandoned || abandoned.Version != 2 {
		t.Fatalf("abandoned = %+v, want ABANDONED/version 2", abandoned)
	}
	if abandoned.AbandonedAt == nil || !abandoned.AbandonedAt.Equal(occurredAt) {
		t.Fatalf("abandoned.AbandonedAt = %v, want %v", abandoned.AbandonedAt, occurredAt)
	}
}

// TestReleaseSetRepository_TransitionReleaseSetState_StaleVersion_Conflict
// is this task's own "stale revision" Verify-line scenario: a caller whose
// observed Version no longer matches storage — because another transition
// already landed — must be rejected, never silently reapplied or
// double-applied.
func TestReleaseSetRepository_TransitionReleaseSetState_StaleVersion_Conflict(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-stale-version.db")
	seedReleaseSetFamily(t, store, "p1", "family-1", "repo-a")
	releaseSet := mustReleaseSet(t, "release-1", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Now())
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).CreateReleaseSet(context.Background(), releaseSet)
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).TransitionReleaseSetState(context.Background(), ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: "release-1", ExpectedState: work.ReleaseSetCreated, ExpectedVersion: 1,
			NextState: work.ReleaseSetSealed, OccurredAt: time.Now(),
		})
		return err
	})

	// Duplicate/stale attempt: still claims ExpectedVersion 1 (and
	// ExpectedState CREATED), but the row is already SEALED@2.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).TransitionReleaseSetState(context.Background(), ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: "release-1", ExpectedState: work.ReleaseSetCreated, ExpectedVersion: 1,
			NextState: work.ReleaseSetAbandoned, OccurredAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
		}
		return nil
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		current, err := (workRepository{tx: tx}).GetReleaseSet(context.Background(), "release-1")
		if err != nil {
			return err
		}
		if current.State != work.ReleaseSetSealed {
			t.Fatalf("current.State = %s after rejected duplicate transition, want unchanged SEALED", current.State)
		}
		return nil
	})
}

func TestReleaseSetRepository_TransitionReleaseSetState_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-transition-notfound.db")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (workRepository{tx: tx}).TransitionReleaseSetState(context.Background(), ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: "missing", ExpectedState: work.ReleaseSetCreated, ExpectedVersion: 1,
			NextState: work.ReleaseSetSealed, OccurredAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestReleaseSetRepository_ListReleaseSetsForFamily_OrderedByCreatedAtThenID(t *testing.T) {
	store := openCatalogTestStore(t, "release-set-list.db")
	seedReleaseSetProject(t, store, "p1", "repo-a")
	seedTaskFamily(t, store, "p1", "family-1")
	seedTaskFamily(t, store, "p1", "family-2")

	older := mustReleaseSet(t, "release-b", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := mustReleaseSet(t, "release-a", "p1", "family-1", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictFail},
	}, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	otherFamily := mustReleaseSet(t, "release-c", "p1", "family-2", []work.RepositoryRelease{
		{RepositoryID: "repo-a", BaseVCSObjectID: "base-a", ResultVCSObjectID: "result-a", Verdict: gate.VerdictPass},
	}, time.Now())

	for _, rs := range []work.ReleaseSet{older, newer, otherFamily} {
		rs := rs
		withCatalogTx(t, store, func(tx *sql.Tx) error {
			_, err := (workRepository{tx: tx}).CreateReleaseSet(context.Background(), rs)
			return err
		})
	}

	var listed []work.ReleaseSet
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		listed, err = (workRepository{tx: tx}).ListReleaseSetsForFamily(context.Background(), "family-1")
		return err
	})
	if len(listed) != 2 {
		t.Fatalf("listed = %d release sets, want 2 (family-2's own release set must not leak in)", len(listed))
	}
	if listed[0].ID != "release-b" || listed[1].ID != "release-a" {
		t.Fatalf("listed order = [%s, %s], want [release-b, release-a] (ordered by CreatedAt)", listed[0].ID, listed[1].ID)
	}
}
