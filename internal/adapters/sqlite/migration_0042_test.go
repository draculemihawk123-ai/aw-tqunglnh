package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// TestMigration42_UpgradeWithRealData_LegacyAttemptHasNoInputTreesAndRecordsSetOnce
// proves migration 0042 (V9-01, ADR-030) on a database that already holds an
// execution attempt created before it: the column is added nullable, the
// legacy row loads with no InputTrees (consumers fall back to the pre-V9-01
// rule for it), the first RecordAttemptInputTrees applies and round-trips,
// a second one does not overwrite, and neither bumps the attempt's version.
func TestMigration42_UpgradeWithRealData_LegacyAttemptHasNoInputTreesAndRecordsSetOnce(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration42-upgrade.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 41)

	const projectID, familyID, workItemID, attemptID = "p1", "f1", "wi1", "attempt-legacy"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, projectID, familyID, workItemID, attemptID); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (apply 0042): %v", err)
	}

	var columnType string
	var notNull int
	if err := store.db.QueryRowContext(ctx,
		`SELECT type, "notnull" FROM pragma_table_info('execution_attempts') WHERE name = 'input_trees_json'`,
	).Scan(&columnType, &notNull); err != nil {
		t.Fatalf("read input_trees_json column info: %v", err)
	}
	if !strings.EqualFold(columnType, "TEXT") || notNull != 0 {
		t.Fatalf("input_trees_json = type %q notnull %d, want a nullable TEXT column (NULL means never recorded)", columnType, notNull)
	}
	var raw sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT input_trees_json FROM execution_attempts WHERE id = ?`, attemptID).Scan(&raw); err != nil {
		t.Fatalf("read legacy input_trees_json: %v", err)
	}
	if raw.Valid {
		t.Fatalf("legacy attempt input_trees_json = %q, want NULL", raw.String)
	}

	uow := NewUnitOfWork(store)
	load := func() runtime.ExecutionAttempt {
		var attempt runtime.ExecutionAttempt
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
			return err
		}); err != nil {
			t.Fatalf("GetExecutionAttempt: %v", err)
		}
		return attempt
	}
	record := func(trees map[project.RepositoryID]string) bool {
		var applied bool
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			var err error
			applied, err = tx.Runtime().RecordAttemptInputTrees(ctx, attemptID, trees)
			return err
		}); err != nil {
			t.Fatalf("RecordAttemptInputTrees: %v", err)
		}
		return applied
	}

	legacy := load()
	if len(legacy.InputTrees) != 0 {
		t.Fatalf("legacy attempt InputTrees = %v, want none", legacy.InputTrees)
	}
	first := map[project.RepositoryID]string{"repo-a": strings.Repeat("1f", 20), "repo-b": strings.Repeat("2e", 20)}
	if !record(first) {
		t.Fatal("first record was not applied")
	}
	if record(map[project.RepositoryID]string{"repo-a": strings.Repeat("99", 20)}) {
		t.Fatal("second record was applied: a recorded InputTree must never be overwritten")
	}
	recorded := load()
	if len(recorded.InputTrees) != 2 || recorded.InputTrees["repo-a"] != first["repo-a"] || recorded.InputTrees["repo-b"] != first["repo-b"] {
		t.Fatalf("InputTrees = %v, want exactly %v", recorded.InputTrees, first)
	}
	if recorded.Version != legacy.Version {
		t.Fatalf("attempt version %d -> %d: recording must not bump it", legacy.Version, recorded.Version)
	}

	// A successor created WITH InputTrees (retry/recovery inheritance)
	// round-trips through createExecutionAttemptTx.
	successor, err := runtime.NewExecutionAttempt("attempt-successor", legacy.NodeRunID, 2, legacy.ExecutionProfileHash, legacy.ProviderKey, legacy.InputRevisionSet)
	if err != nil {
		t.Fatalf("NewExecutionAttempt: %v", err)
	}
	successor.InputTrees = recorded.InheritedInputTrees()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateExecutionAttempt(ctx, successor)
		return err
	}); err != nil {
		t.Fatalf("CreateExecutionAttempt(successor): %v", err)
	}
	var reloaded runtime.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		reloaded, err = tx.Runtime().GetExecutionAttempt(ctx, "attempt-successor")
		return err
	}); err != nil {
		t.Fatalf("GetExecutionAttempt(successor): %v", err)
	}
	if len(reloaded.InputTrees) != 2 || reloaded.InputTrees["repo-a"] != first["repo-a"] {
		t.Fatalf("successor InputTrees = %v, want the inherited %v", reloaded.InputTrees, first)
	}
}
