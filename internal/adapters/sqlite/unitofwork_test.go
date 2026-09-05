package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestUnitOfWork_WithSerializedWrite_CommitsOnSuccess(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-uow-commit.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	uow := NewUnitOfWork(store)
	var sawTx ports.Tx
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		sawTx = tx
		adapter, ok := tx.(*txAdapter)
		if !ok {
			t.Fatalf("Tx passed to fn = %T, want *txAdapter", tx)
		}
		_, err := adapter.tx.ExecContext(ctx, `INSERT INTO probe DEFAULT VALUES`)
		return err
	})
	if err != nil {
		t.Fatalf("WithSerializedWrite: %v", err)
	}
	if sawTx == nil {
		t.Fatal("fn was never called with a Tx")
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestUnitOfWork_WithSerializedWrite_RollsBackOnFnError(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-uow-rollback.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	uow := NewUnitOfWork(store)
	sentinel := errors.New("fn refused to commit")
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		adapter := tx.(*txAdapter)
		if _, err := adapter.tx.ExecContext(ctx, `INSERT INTO probe DEFAULT VALUES`); err != nil {
			return err
		}
		return sentinel
	})
	if err == nil {
		t.Fatal("WithSerializedWrite should propagate fn's error")
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (fn's error must roll back its own insert)", count)
	}
}

func TestUnitOfWork_WithReadOnly_DoesNotPersistWrites(t *testing.T) {
	// WithReadOnly does not mechanically forbid a write (SQLite has no
	// per-transaction read-only mode distinct from this connection pool's
	// uniform DSN), but it must still commit/rollback correctly like any
	// other transaction — this proves the read-only path is wired to a
	// real, working transaction, not a no-op.
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-uow-readonly.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var migrationCount int
	err = NewUnitOfWork(store).WithReadOnly(ctx, func(tx ports.Tx) error {
		adapter := tx.(*txAdapter)
		return adapter.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount)
	})
	if err != nil {
		t.Fatalf("WithReadOnly: %v", err)
	}
	if migrationCount != 18 {
		t.Fatalf("migrationCount = %d, want 18", migrationCount)
	}
}

func TestUnitOfWork_TxAccessorsReturnNonNil(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-uow-accessors.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	err = NewUnitOfWork(store).WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if tx.Catalog() == nil {
			t.Error("Catalog() returned nil")
		}
		if tx.Work() == nil {
			t.Error("Work() returned nil")
		}
		if tx.Definitions() == nil {
			t.Error("Definitions() returned nil")
		}
		if tx.Runtime() == nil {
			t.Error("Runtime() returned nil")
		}
		if tx.Jobs() == nil {
			t.Error("Jobs() returned nil")
		}
		if tx.Events() == nil {
			t.Error("Events() returned nil")
		}
		if tx.Receipts() == nil {
			t.Error("Receipts() returned nil")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithSerializedWrite: %v", err)
	}
}

func TestQueryStore_Ping(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-querystore.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := NewQueryStore(store).Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestQueryStore_PingFailsAfterClose(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-querystore-closed.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := NewQueryStore(store).Ping(ctx); err == nil {
		t.Fatal("Ping on a closed store should fail")
	}
}
