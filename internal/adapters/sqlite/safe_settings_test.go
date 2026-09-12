package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

func openSafeSettingsTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func validDesired() safesettings.SafeSettings {
	return safesettings.SafeSettings{
		ManagedWorkspaceRoot:   "data/workspaces",
		ManagedArtifactRoot:    "data/artifacts",
		EvidenceRetention:      7 * 24 * time.Hour,
		ProcessOutputLimit:     1 << 20,
		ProviderExecutablePath: "C:/tools/claude/claude.exe",
		ProviderDefaultModel:   "claude-sonnet-4-5",
		ProviderCredentialRef:  "keychain:claude-api-key",
	}
}

// TestSafeSettingsRepository_Get_SeededByMigration is this task's own
// "migration applies cleanly" Verify scenario, from the repository's own
// vantage point: a fresh database (migration 0036 having just run) already
// has the singleton row at Version 1 with the zero-value desired document —
// never ports.ErrPersistenceNotFound.
func TestSafeSettingsRepository_Get_SeededByMigration(t *testing.T) {
	store := openSafeSettingsTestStore(t, "safe-settings-seed.db")
	uow := NewUnitOfWork(store)

	var record ports.SafeSettingsRecord
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		record, err = tx.SafeSettings().Get(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("Get (seeded row): %v", err)
	}
	if record.Version != 1 {
		t.Fatalf("record.Version = %d, want 1", record.Version)
	}
	if !record.Desired.IsZero() {
		t.Fatalf("record.Desired = %+v, want IsZero()", record.Desired)
	}
	if record.UpdatedBy != "" {
		t.Fatalf("record.UpdatedBy = %q, want empty for the migration-seeded row", record.UpdatedBy)
	}
}

// TestSafeSettingsRepository_Update_Succeeds proves a well-formed CAS
// update replaces the full desired document and bumps version by exactly
// one.
func TestSafeSettingsRepository_Update_Succeeds(t *testing.T) {
	store := openSafeSettingsTestStore(t, "safe-settings-update.db")
	uow := NewUnitOfWork(store)

	desired := validDesired()
	occurredAt := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	var updated ports.SafeSettingsRecord
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		var err error
		updated, err = tx.SafeSettings().Update(context.Background(), ports.UpdateSafeSettingsRequest{
			Desired: desired, ExpectedVersion: 1, UpdatedBy: "actor-1", OccurredAt: occurredAt,
		})
		return err
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated.Version = %d, want 2", updated.Version)
	}
	if updated.Desired != desired {
		t.Fatalf("updated.Desired = %+v, want %+v", updated.Desired, desired)
	}
	if updated.UpdatedBy != "actor-1" {
		t.Fatalf("updated.UpdatedBy = %q, want actor-1", updated.UpdatedBy)
	}
	if !updated.UpdatedAt.Equal(occurredAt) {
		t.Fatalf("updated.UpdatedAt = %v, want %v", updated.UpdatedAt, occurredAt)
	}

	// Persisted across a fresh read.
	var reloaded ports.SafeSettingsRecord
	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		reloaded, err = tx.SafeSettings().Get(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("Get (reload): %v", err)
	}
	if reloaded.Version != 2 || reloaded.Desired != desired {
		t.Fatalf("reloaded = %+v, want version 2 with desired %+v", reloaded, desired)
	}
}

// TestSafeSettingsRepository_Update_StaleVersionConflict is this task's own
// "concurrency" Verify scenario: two updates both observing Version 1 —
// only the first may win, the second must fail
// ports.ErrOptimisticConflict, and the stored row must reflect only the
// first writer's own document, never a merge or a silent second overwrite.
func TestSafeSettingsRepository_Update_StaleVersionConflict(t *testing.T) {
	store := openSafeSettingsTestStore(t, "safe-settings-conflict.db")
	uow := NewUnitOfWork(store)

	first := validDesired()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.SafeSettings().Update(context.Background(), ports.UpdateSafeSettingsRequest{
			Desired: first, ExpectedVersion: 1, UpdatedBy: "actor-1", OccurredAt: time.Now().UTC(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("first Update: %v", err)
	}

	second := validDesired()
	second.ProviderDefaultModel = "claude-opus-4-1"
	err = uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.SafeSettings().Update(context.Background(), ports.UpdateSafeSettingsRequest{
			Desired: second, ExpectedVersion: 1, UpdatedBy: "actor-2", OccurredAt: time.Now().UTC(),
		})
		return err
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("second Update err = %v, want ports.ErrOptimisticConflict", err)
	}

	var current ports.SafeSettingsRecord
	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		current, err = tx.SafeSettings().Get(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("Get (after conflict): %v", err)
	}
	if current.Version != 2 || current.Desired != first {
		t.Fatalf("current = %+v after rejected conflicting update, want version 2 with the FIRST writer's document %+v", current, first)
	}
}

// TestSafeSettingsRepository_Get_CorruptRowFailsTyped is this task's own
// "corrupt persisted settings fail readiness/Doctor typed" Verify scenario,
// at the repository layer. There is no real production code path that ever
// writes malformed JSON into desired_json (Update always marshals a real
// safesettings.SafeSettings), so this test corrupts the row directly via
// raw SQL — the only way to simulate the out-of-band corruption (disk bit
// rot, a manual DB edit) this check exists to catch — never to fabricate a
// verified command outcome.
func TestSafeSettingsRepository_Get_CorruptRowFailsTyped(t *testing.T) {
	store := openSafeSettingsTestStore(t, "safe-settings-corrupt.db")
	// store.db is this package's own unexported *sql.DB — reached directly
	// here only because this is a same-package (internal) test file; no
	// production code path ever writes malformed JSON this way.
	if _, err := store.db.Exec(`UPDATE safe_settings SET desired_json = ? WHERE id = 'singleton'`, `{not-valid-json`); err != nil {
		t.Fatalf("corrupt fixture row: %v", err)
	}

	uow := NewUnitOfWork(store)
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		_, err := tx.SafeSettings().Get(context.Background())
		return err
	})
	if !errors.Is(err, ports.ErrSafeSettingsCorrupt) {
		t.Fatalf("Get (corrupt row) err = %v, want ports.ErrSafeSettingsCorrupt", err)
	}
}
