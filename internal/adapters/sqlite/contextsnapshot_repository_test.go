package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func testRevisions(t *testing.T) workspace.RevisionSet {
	t.Helper()
	set, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-1", VCSObjectID: "abc123", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	return set
}

func newTestSnapshot(t *testing.T, id, projectID, workItemID, attemptID string) contextsnapshot.Snapshot {
	t.Helper()
	snap, err := contextsnapshot.NewSnapshot(
		contextsnapshot.ID(id), project.ProjectID(projectID), work.WorkItemID(workItemID), contextsnapshot.AttemptID(attemptID),
		[]contextsnapshot.MessageRef{{MessageID: "msg-1"}},
		[]contextsnapshot.ResourceRef{{ResourceKey: "res-1", ContentHash: "hash-1"}},
		[]contextsnapshot.EvidenceRef{{EvidenceID: "evidence-1"}},
		testRevisions(t), time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snap
}

func TestContextSnapshotRepository_CreateSnapshot_GetSnapshot_RoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-roundtrip.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	snap := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "attempt-1")

	var created contextsnapshot.Snapshot
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, snap)
		return err
	})
	if created.ManifestHash != snap.ManifestHash {
		t.Fatalf("created.ManifestHash = %q, want %q", created.ManifestHash, snap.ManifestHash)
	}

	var loaded contextsnapshot.Snapshot
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = contextSnapshotRepository{tx: tx}.GetSnapshot(ctx, "snap-1")
		return err
	})
	if loaded.ManifestHash != snap.ManifestHash || len(loaded.MessageRefs) != 1 || len(loaded.ResourceRefs) != 1 {
		t.Fatalf("loaded = %+v, want a full round trip of %+v", loaded, snap)
	}
}

func TestContextSnapshotRepository_GetSnapshotByAttemptID(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-by-attempt.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	snap := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "attempt-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, snap)
		return err
	})

	var loaded contextsnapshot.Snapshot
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = contextSnapshotRepository{tx: tx}.GetSnapshotByAttemptID(ctx, "attempt-1")
		return err
	})
	if string(loaded.ID) != "snap-1" {
		t.Fatalf("GetSnapshotByAttemptID = %+v, want snap-1", loaded)
	}
}

func TestContextSnapshotRepository_CreateSnapshot_AttemptNotFound(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-attempt-notfound.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	snap := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "missing-attempt")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, snap)
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestContextSnapshotRepository_CreateSnapshot_DuplicateID_ReturnsExistingRow(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-duplicate.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	first := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "attempt-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, first)
		return err
	})

	var result contextsnapshot.Snapshot
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		result, err = contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, first)
		return err
	})
	if result.ManifestHash != first.ManifestHash {
		t.Fatalf("duplicate create returned %+v, want the original %+v", result, first)
	}
}

func TestContextSnapshotRepository_TamperDetected_ManifestHashMismatch(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-tamper.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	snap := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "attempt-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.CreateSnapshot(ctx, snap)
		return err
	})

	// Directly corrupt the stored manifest_hash, bypassing the repository
	// entirely — the only way a real tamper/corruption could happen.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE attempt_context_snapshots SET manifest_hash = 'sha256:tampered' WHERE id = ?`, "snap-1")
		return err
	})

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.GetSnapshot(ctx, "snap-1")
		if !errors.Is(err, ports.ErrImmutableVersionConflict) {
			t.Fatalf("err = %v, want ports.ErrImmutableVersionConflict", err)
		}
		return nil
	})
}

func TestContextSnapshotRepository_GetSnapshot_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-notfound.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := contextSnapshotRepository{tx: tx}.GetSnapshot(ctx, "missing")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// V9-10: the instruction files a provider CLI loads by itself round-trip through
// attempt_context_snapshots.repository_instruction_files_json, take part in the
// tamper check, and a snapshot without any stores NULL.
func TestContextSnapshotRepository_RepositoryInstructionFilesRoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-instruction-files.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	snap, err := contextsnapshot.NewSnapshot(
		"snap-files", project.ProjectID("project-1"), work.WorkItemID("work-item-1"), contextsnapshot.AttemptID("attempt-1"),
		nil, nil, nil, testRevisions(t), time.Now().UTC(),
		contextsnapshot.WithRepositoryInstructionFiles([]contextsnapshot.InstructionFileRef{
			{RepositoryID: "repo-1", Path: "CLAUDE.md", SHA256: "sha256:abc", SizeBytes: 20000, WarnLimitBytes: 16384},
		}),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := contextSnapshotRepository{tx: tx}
		if _, err := repo.CreateSnapshot(ctx, snap); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		loaded, err := repo.GetSnapshot(ctx, "snap-files")
		if err != nil {
			t.Fatalf("GetSnapshot: %v", err)
		}
		if len(loaded.RepositoryInstructionFiles) != 1 || loaded.RepositoryInstructionFiles[0] != snap.RepositoryInstructionFiles[0] || loaded.ManifestHash != snap.ManifestHash {
			t.Fatalf("loaded = %+v hash %s, want the pinned file and %s", loaded.RepositoryInstructionFiles, loaded.ManifestHash, snap.ManifestHash)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attempt_context_snapshots SET repository_instruction_files_json = NULL WHERE id = 'snap-files'`); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		if _, err := repo.GetSnapshot(ctx, "snap-files"); err == nil {
			t.Fatal("GetSnapshot accepted a snapshot whose pinned instruction files were removed")
		}
		return nil
	})
}
