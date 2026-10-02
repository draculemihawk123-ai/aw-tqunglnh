package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
)

// TestMigration44_UpgradeWithRealData_LegacySnapshotLoadsAsV1AndNewOnesRecordV2
// proves migration 0044 (V9-03, ADR-032) on a database that already holds a
// context snapshot written before it: the column is added nullable, the legacy
// row loads with no recorded version (so it renders as instruction schema v1)
// and still passes the manifest-hash tamper check byte for byte, a snapshot
// created afterwards round-trips its version 2, the CHECK admits only NULL and
// 2, and flipping a stored row's version is caught as tampering instead of
// silently changing the instruction its refs render to.
func TestMigration44_UpgradeWithRealData_LegacySnapshotLoadsAsV1AndNewOnesRecordV2(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration44-upgrade.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 43)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	for _, attemptID := range []string{"attempt-legacy", "attempt-new", "attempt-bad"} {
		if err := SeedFixtureExecutionAttempt(ctx, store, projectID, familyID, workItemID, attemptID); err != nil {
			t.Fatalf("SeedFixtureExecutionAttempt(%s): %v", attemptID, err)
		}
	}

	// The row exactly as a build from before this migration wrote it: no
	// version column exists yet, and the hash is the pre-V9-03 one.
	legacy := newTestSnapshot(t, "snap-legacy", projectID, workItemID, "attempt-legacy")
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO attempt_context_snapshots (id, project_id, work_item_id, attempt_id, message_refs_json, resource_refs_json, evidence_refs_json, revision_set_json, manifest_hash, created_at)
VALUES ('snap-legacy', ?, ?, 'attempt-legacy', '[{"MessageID":"msg-1"}]', '[{"ResourceKey":"res-1","ContentHash":"hash-1"}]', '[{"EvidenceID":"evidence-1"}]', ?, ?, ?)`,
		projectID, workItemID,
		`[{"RepositoryID":"repo-1","VCSObjectID":"abc123","WorkspaceGeneration":1}]`, legacy.ManifestHash, formatWorkflowTime(legacy.CreatedAt),
	); err != nil {
		t.Fatalf("seed a pre-migration snapshot row: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (apply 0044): %v", err)
	}

	var columnType string
	var notNull int
	var defaultValue sql.NullString
	if err := store.db.QueryRowContext(ctx,
		`SELECT type, "notnull", dflt_value FROM pragma_table_info('attempt_context_snapshots') WHERE name = 'instruction_schema_version'`,
	).Scan(&columnType, &notNull, &defaultValue); err != nil {
		t.Fatalf("read instruction_schema_version column info: %v", err)
	}
	if !strings.EqualFold(columnType, "INTEGER") || notNull != 0 || defaultValue.Valid {
		t.Fatalf("instruction_schema_version = type %q notnull %d default %v, want a nullable INTEGER with no default (NULL means never recorded)", columnType, notNull, defaultValue)
	}
	var raw sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `SELECT instruction_schema_version FROM attempt_context_snapshots WHERE id = 'snap-legacy'`).Scan(&raw); err != nil {
		t.Fatalf("read legacy instruction_schema_version: %v", err)
	}
	if raw.Valid {
		t.Fatalf("legacy snapshot instruction_schema_version = %d, want NULL", raw.Int64)
	}

	uow := NewUnitOfWork(store)
	load := func(id string) (contextsnapshot.Snapshot, error) {
		var snapshot contextsnapshot.Snapshot
		err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			snapshot, err = tx.ContextSnapshots().GetSnapshot(ctx, id)
			return err
		})
		return snapshot, err
	}

	loaded, err := load("snap-legacy")
	if err != nil {
		t.Fatalf("GetSnapshot(legacy): %v", err)
	}
	if loaded.InstructionSchemaVersion != 0 || loaded.InstructionSchema() != contextsnapshot.InstructionSchemaV1 {
		t.Fatalf("legacy snapshot version = %d / effective %d, want none recorded and v1", loaded.InstructionSchemaVersion, loaded.InstructionSchema())
	}
	if loaded.ManifestHash != legacy.ManifestHash {
		t.Fatalf("legacy snapshot hash = %s, want the stored, unchanged %s", loaded.ManifestHash, legacy.ManifestHash)
	}

	// A snapshot created after the migration records and round-trips 2.
	fresh, err := contextsnapshot.NewSnapshot(
		"snap-new", projectID, workItemID, "attempt-new",
		legacy.MessageRefs, legacy.ResourceRefs, legacy.EvidenceRefs, legacy.Revisions, time.Now().UTC(),
		contextsnapshot.WithInstructionSchemaVersion(contextsnapshot.InstructionSchemaV2),
	)
	if err != nil {
		t.Fatalf("NewSnapshot(v2): %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.ContextSnapshots().CreateSnapshot(ctx, fresh)
		return err
	}); err != nil {
		t.Fatalf("CreateSnapshot(v2): %v", err)
	}
	reloaded, err := load("snap-new")
	if err != nil {
		t.Fatalf("GetSnapshot(new): %v", err)
	}
	if reloaded.InstructionSchemaVersion != contextsnapshot.InstructionSchemaV2 || reloaded.ManifestHash != fresh.ManifestHash {
		t.Fatalf("reloaded snapshot version %d hash %s, want version 2 hash %s", reloaded.InstructionSchemaVersion, reloaded.ManifestHash, fresh.ManifestHash)
	}
	if fresh.ManifestHash == legacy.ManifestHash {
		t.Fatal("a v2 snapshot hashes like the v1 snapshot of the same refs")
	}

	// The CHECK: only NULL and 2 are storable.
	for _, version := range []int{1, 3} {
		if _, err := store.db.ExecContext(ctx, `UPDATE attempt_context_snapshots SET instruction_schema_version = ? WHERE id = 'snap-new'`, version); err == nil {
			t.Fatalf("the CHECK accepted instruction_schema_version %d", version)
		}
	}

	// Tampering with the version of a stored row is caught on load: it would
	// otherwise change the instruction the same refs render to unnoticed.
	if _, err := store.db.ExecContext(ctx, `UPDATE attempt_context_snapshots SET instruction_schema_version = NULL WHERE id = 'snap-new'`); err != nil {
		t.Fatalf("clear the version of the v2 row: %v", err)
	}
	if _, err := load("snap-new"); !errors.Is(err, ports.ErrImmutableVersionConflict) {
		t.Fatalf("GetSnapshot after dropping the version = %v, want ErrImmutableVersionConflict (hash mismatch)", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE attempt_context_snapshots SET instruction_schema_version = 2 WHERE id = 'snap-legacy'`); err != nil {
		t.Fatalf("set a version on the legacy row: %v", err)
	}
	if _, err := load("snap-legacy"); !errors.Is(err, ports.ErrImmutableVersionConflict) {
		t.Fatalf("GetSnapshot after adding a version = %v, want ErrImmutableVersionConflict (hash mismatch)", err)
	}
}

// TestContextSnapshotRepository_InstructionSchemaVersion_RoundTrip and
// RewriteSnapshotAsPreV903ForTest: the repository stores NULL for a snapshot
// that records none and the version otherwise, and the test helper that
// produces a pre-V9-03 row leaves one that loads cleanly as v1.
func TestContextSnapshotRepository_InstructionSchemaVersion_RoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-schema-version.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	for _, attemptID := range []string{"attempt-v1", "attempt-v2"} {
		if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", attemptID); err != nil {
			t.Fatalf("SeedFixtureExecutionAttempt(%s): %v", attemptID, err)
		}
	}
	v1 := newTestSnapshot(t, "snap-v1", "project-1", "work-item-1", "attempt-v1")
	v2, err := contextsnapshot.NewSnapshot(
		"snap-v2", "project-1", "work-item-1", "attempt-v2",
		v1.MessageRefs, v1.ResourceRefs, v1.EvidenceRefs, v1.Revisions, time.Now().UTC(),
		contextsnapshot.WithInstructionSchemaVersion(contextsnapshot.InstructionSchemaV2),
	)
	if err != nil {
		t.Fatalf("NewSnapshot(v2): %v", err)
	}
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		if _, err := (contextSnapshotRepository{tx: tx}).CreateSnapshot(ctx, v1); err != nil {
			return err
		}
		_, err := (contextSnapshotRepository{tx: tx}).CreateSnapshot(ctx, v2)
		return err
	})

	stored := func(id string) sql.NullInt64 {
		t.Helper()
		var version sql.NullInt64
		if err := store.db.QueryRowContext(ctx, `SELECT instruction_schema_version FROM attempt_context_snapshots WHERE id = ?`, id).Scan(&version); err != nil {
			t.Fatalf("read stored version of %s: %v", id, err)
		}
		return version
	}
	if got := stored("snap-v1"); got.Valid {
		t.Fatalf("a snapshot that records no version is stored as %d, want NULL", got.Int64)
	}
	if got := stored("snap-v2"); !got.Valid || got.Int64 != 2 {
		t.Fatalf("a v2 snapshot is stored as %v, want 2", got)
	}

	// The helper turns the v2 row into a pre-V9-03 row.
	if err := RewriteSnapshotAsPreV903ForTest(ctx, store, "snap-v2"); err != nil {
		t.Fatalf("RewriteSnapshotAsPreV903ForTest: %v", err)
	}
	if got := stored("snap-v2"); got.Valid {
		t.Fatalf("rewritten row version = %d, want NULL", got.Int64)
	}
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		loaded, err := (contextSnapshotRepository{tx: tx}).GetSnapshot(ctx, "snap-v2")
		if err != nil {
			return err
		}
		if loaded.InstructionSchema() != contextsnapshot.InstructionSchemaV1 || loaded.ManifestHash != v1.ManifestHash {
			t.Errorf("rewritten snapshot = effective v%d hash %s, want v1 with the v1 hash %s", loaded.InstructionSchema(), loaded.ManifestHash, v1.ManifestHash)
		}
		return nil
	})
}
